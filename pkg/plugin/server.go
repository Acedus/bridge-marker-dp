package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"kubevirt.io/client-go/log"
)

const (
	DeviceNamespace   = "bridge.network.kubevirt.io"
	connectionTimeout = 5 * time.Second
)

type Device interface {
	Start(stop <-chan struct{}) error
	ListAndWatch(*pluginapi.Empty, pluginapi.DevicePlugin_ListAndWatchServer) error
	PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error)
	GetPreferredAllocation(context.Context, *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error)
	Allocate(context.Context, *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error)
	GetDeviceName() string
	GetInitialized() bool
}

type BridgeDevicePlugin struct {
	devs         []*pluginapi.Device
	server       *grpc.Server
	socketPath   string
	stop         <-chan struct{}
	health       chan deviceHealth
	deviceName   string
	resourceName string
	done         chan struct{}
	initialized  bool
	lock         *sync.Mutex
	deregistered chan struct{}
}

func NewBridgeDevicePlugin(deviceName string, maxDevices int) *BridgeDevicePlugin {
	serverSock := SocketPath(deviceName)
	dpi := &BridgeDevicePlugin{
		devs:         []*pluginapi.Device{},
		socketPath:   serverSock,
		health:       make(chan deviceHealth),
		deviceName:   deviceName,
		resourceName: fmt.Sprintf("%s/%s", DeviceNamespace, deviceName),
		initialized:  false,
		lock:         &sync.Mutex{},
	}

	for i := 0; i < maxDevices; i++ {
		deviceId := dpi.deviceName + strconv.Itoa(i)
		dpi.devs = append(dpi.devs, &pluginapi.Device{
			ID:     deviceId,
			Health: pluginapi.Healthy,
		})
	}

	return dpi
}

func (dpi *BridgeDevicePlugin) GetDeviceName() string {
	return dpi.deviceName
}

// Start starts the device plugin
func (dpi *BridgeDevicePlugin) Start(stop <-chan struct{}) (err error) {
	logger := log.DefaultLogger()
	dpi.stop = stop
	dpi.done = make(chan struct{})
	dpi.deregistered = make(chan struct{})

	err = dpi.cleanup()
	if err != nil {
		return err
	}

	sock, err := net.Listen("unix", dpi.socketPath)
	if err != nil {
		return fmt.Errorf("error creating GRPC server socket: %v", err)
	}

	dpi.server = grpc.NewServer([]grpc.ServerOption{}...)
	defer dpi.stopDevicePlugin()

	pluginapi.RegisterDevicePluginServer(dpi.server, dpi)

	errChan := make(chan error, 2)

	go func() {
		errChan <- dpi.server.Serve(sock)
	}()

	err = waitForGRPCServer(dpi.socketPath, connectionTimeout)
	if err != nil {
		return fmt.Errorf("error starting the GRPC server: %v", err)
	}

	err = dpi.register()
	if err != nil {
		return fmt.Errorf("error registering with device plugin manager: %v", err)
	}

	go func() {
		errChan <- dpi.healthCheck()
	}()

	dpi.setInitialized(true)
	logger.Infof("%s device plugin started", dpi.deviceName)
	err = <-errChan

	return err
}

// Stop stops the gRPC server
func (dpi *BridgeDevicePlugin) stopDevicePlugin() error {
	defer func() {
		if !IsChanClosed(dpi.done) {
			close(dpi.done)
		}
	}()

	// Give the device plugin one second to properly deregister
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	select {
	case <-dpi.deregistered:
	case <-ticker.C:
	}
	dpi.server.Stop()
	dpi.setInitialized(false)
	return dpi.cleanup()
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (dpi *BridgeDevicePlugin) register() error {
	conn, err := gRPCConnect(pluginapi.KubeletSocket, connectionTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	reqt := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     path.Base(dpi.socketPath),
		ResourceName: dpi.resourceName,
	}

	_, err = client.Register(context.Background(), reqt)
	if err != nil {
		return err
	}
	return nil
}

func (dpi *BridgeDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	s.Send(&pluginapi.ListAndWatchResponse{Devices: dpi.devs})

	done := false
	for {
		select {
		case devHealth := <-dpi.health:
			// There's only one shared bridge device
			// so update each plugin device to reflect overall device health
			for _, dev := range dpi.devs {
				dev.Health = devHealth.Health
			}
			s.Send(&pluginapi.ListAndWatchResponse{Devices: dpi.devs})
		case <-dpi.stop:
			done = true
		case <-dpi.done:
			done = true
		}
		if done {
			break
		}
	}
	// Send empty list to increase the chance that the kubelet acts fast on stopped device plugins
	// There exists no explicit way to deregister devices
	emptyList := []*pluginapi.Device{}
	if err := s.Send(&pluginapi.ListAndWatchResponse{Devices: emptyList}); err != nil {
		log.DefaultLogger().Reason(err).Infof("%s device plugin failed to deregister", dpi.deviceName)
	}
	close(dpi.deregistered)
	return nil
}

func (dpi *BridgeDevicePlugin) Allocate(ctx context.Context, r *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	log.DefaultLogger().Infof("Bridge Allocate: resourceName: %s", dpi.deviceName)
	log.DefaultLogger().Infof("Bridge Allocate: request: %v", r.ContainerRequests)

	res := pluginapi.AllocateResponse{}
	containerResponse := new(pluginapi.ContainerAllocateResponse)

	// No DeviceSpec needed if no device mounts are required
	res.ContainerResponses = []*pluginapi.ContainerAllocateResponse{containerResponse}

	return &res, nil
}

func (dpi *BridgeDevicePlugin) cleanup() error {
	if err := os.Remove(dpi.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

func (dpi *BridgeDevicePlugin) GetDevicePluginOptions(_ context.Context, _ *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	options := &pluginapi.DevicePluginOptions{
		PreStartRequired:                false,
		GetPreferredAllocationAvailable: false,
	}
	return options, nil
}

func (dpi *BridgeDevicePlugin) PreStartContainer(_ context.Context, _ *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	res := &pluginapi.PreStartContainerResponse{}
	return res, nil
}

func (dpi *BridgeDevicePlugin) GetPreferredAllocation(ctx context.Context, _ *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	res := &pluginapi.PreferredAllocationResponse{}
	return res, nil
}

func (dpi *BridgeDevicePlugin) healthCheck() error {
	logger := log.DefaultLogger()
	// Open a netlink handle
	nlHandle, err := netlink.NewHandle()
	if err != nil {
		return fmt.Errorf("failed to create netlink handle: %v", err)
	}
	defer nlHandle.Delete()

	// Subscribe to link updates
	updates := make(chan netlink.LinkUpdate)
	if err := netlink.LinkSubscribe(updates, dpi.stop); err != nil {
		return fmt.Errorf("failed to subscribe to link updates: %v", err)
	}

	// Set up fsnotify for the socket file
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %v", err)
	}
	defer watcher.Close()

	socketDir := filepath.Dir(dpi.socketPath)
	if err := watcher.Add(socketDir); err != nil {
		return fmt.Errorf("failed to add socket directory to watcher: %v", err)
	}

	_, err = os.Stat(dpi.socketPath)
	if _, err := os.Stat(dpi.socketPath); err != nil {
		return fmt.Errorf("failed to stat the device-plugin socket: %v", err)
	}

	// Initial bridge check
	link, err := netlink.LinkByName(dpi.deviceName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			logger.Warningf("bridge '%s' is not present, the device plugin can't expose it: %v", dpi.deviceName, err)
			dpi.health <- deviceHealth{Health: pluginapi.Unhealthy}
		} else {
			return fmt.Errorf("could not check the bridge: %v", err)
		}
	} else {
		logger.Infof("bridge '%s' is present.", dpi.deviceName)
		if link.Attrs().OperState == netlink.OperUp {
			logger.Infof("monitored bridge %s is up", dpi.deviceName)
			dpi.health <- deviceHealth{Health: pluginapi.Healthy}
		} else {
			logger.Infof("monitored bridge %s is down", dpi.deviceName)
			dpi.health <- deviceHealth{Health: pluginapi.Unhealthy}
		}
	}

	for {
		select {
		case <-dpi.stop:
			return nil
		case update := <-updates:
			if update.Attrs().Name == dpi.deviceName {
				if update.Link.Attrs().OperState == netlink.OperUp {
					logger.Infof("monitored bridge %s is up", dpi.deviceName)
					dpi.health <- deviceHealth{Health: pluginapi.Healthy}
				} else {
					logger.Infof("monitored bridge %s is down", dpi.deviceName)
					dpi.health <- deviceHealth{Health: pluginapi.Unhealthy}
				}
			}
		case event := <-watcher.Events:
			if event.Name == dpi.socketPath && event.Op&fsnotify.Remove == fsnotify.Remove {
				logger.Infof("device socket file for device %s was removed, kubelet probably restarted.", dpi.deviceName)
				return nil
			}
		case err := <-watcher.Errors:
			logger.Errorf("Error watching socket file: %v", err)
		}
	}
}

func (dpi *BridgeDevicePlugin) GetInitialized() bool {
	dpi.lock.Lock()
	defer dpi.lock.Unlock()
	return dpi.initialized
}

func (dpi *BridgeDevicePlugin) setInitialized(initialized bool) {
	dpi.lock.Lock()
	defer dpi.lock.Unlock()
	dpi.initialized = initialized
}
