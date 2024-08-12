package plugin

import (
	"math"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"kubevirt.io/client-go/log"
)

var defaultBackoffTime = []time.Duration{1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}

type controlledDevice struct {
	devicePlugin Device
	started      bool
	stopChan     chan struct{}
	backoff      []time.Duration
}

func (c *controlledDevice) Start() {
	if c.started {
		return
	}

	stop := make(chan struct{})

	logger := log.DefaultLogger()
	dev := c.devicePlugin
	deviceName := dev.GetDeviceName()
	logger.Infof("Starting a device plugin for device: %s", deviceName)
	retries := 0

	backoff := c.backoff
	if backoff == nil {
		backoff = defaultBackoffTime
	}

	go func() {
		for {
			err := dev.Start(stop)
			if err != nil {
				logger.Reason(err).Errorf("Error starting %s device plugin", deviceName)
				retries = int(math.Min(float64(retries+1), float64(len(backoff)-1)))
			} else {
				retries = 0
			}

			select {
			case <-stop:
				// Ok we don't want to re-register
				return
			case <-time.After(backoff[retries]):
				// Wait a little and re-register
				continue
			}
		}
	}()

	c.stopChan = stop
	c.started = true
}

func (c *controlledDevice) Stop() {
	if !c.started {
		return
	}
	close(c.stopChan)

	c.stopChan = nil
	c.started = false
}

func (c *controlledDevice) GetName() string {
	return c.devicePlugin.GetDeviceName()
}

func GetBridgeDevicePlugins(maxDevices int) ([]Device, error) {
	ret := make([]Device, 0)
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if bridge, ok := link.(*netlink.Bridge); ok {
			ret = append(ret, NewBridgeDevicePlugin(bridge.Name, maxDevices))
		}
	}
	return ret, nil
}

type BridgeDeviceControllerInterface interface {
	Initialized() bool
	RefreshMediatedDeviceTypes()
}

type BridgeDeviceController struct {
	permanentPlugins    map[string]Device
	startedPlugins      map[string]controlledDevice
	startedPluginsMutex sync.Mutex
	newPlugins	    chan Device
	maxDevices	    int
	backoff             []time.Duration
	refreshInterval     time.Duration
	stop                chan struct{}
}

func NewBridgeDeviceController(
	permanentPlugins []Device,
	maxDevices int,
) *BridgeDeviceController {

	permanentPluginsMap := make(map[string]Device, len(permanentPlugins))
	for i := range permanentPlugins {
		permanentPluginsMap[permanentPlugins[i].GetDeviceName()] = permanentPlugins[i]
	}

	controller := &BridgeDeviceController{
		permanentPlugins: permanentPluginsMap,
		startedPlugins:   map[string]controlledDevice{},
		newPlugins:	  make(chan Device),
		backoff:          defaultBackoffTime,
		maxDevices: maxDevices,
	}

	return controller
}

func (c *BridgeDeviceController) startDevice(resourceName string, dev Device) {
	c.stopDevice(resourceName)
	controlledDev := controlledDevice{
		devicePlugin: dev,
		backoff:      c.backoff,
	}
	controlledDev.Start()
	c.startedPlugins[resourceName] = controlledDev
	
}

func (c *BridgeDeviceController) stopDevice(resourceName string) {
	dev, exists := c.startedPlugins[resourceName]
	if exists {
		dev.Stop()
		delete(c.startedPlugins, resourceName)
	}
}

func (c *BridgeDeviceController) Run(stop chan struct{}) error {
	logger := log.DefaultLogger()

	// start the permanent DevicePlugins
	c.startPermanentPlugins()

	// Scan for new devices and adds them as they become available
	go c.ScanForNewDevices(stop)

	for {
		select {
		case device := <-c.newPlugins:
			c.startNewPlugin(device)
		// keep running until stop
		case <-stop:
			logger.Info("Shutting down device plugin controller")
			c.stopAllPlugins()
			return nil
		}
	}

}

func (c *BridgeDeviceController) startPermanentPlugins() {
	c.startedPluginsMutex.Lock()
	defer c.startedPluginsMutex.Unlock()
	for name, dev := range c.permanentPlugins {
		c.startDevice(name, dev)
	}
}

func (c *BridgeDeviceController) stopAllPlugins() {
	c.startedPluginsMutex.Lock()
	defer c.startedPluginsMutex.Unlock()
	for name := range c.startedPlugins {
		c.stopDevice(name)
	}
}

func (c *BridgeDeviceController) startNewPlugin(device Device) {
	c.startedPluginsMutex.Lock()
	defer c.startedPluginsMutex.Unlock()
	c.startDevice(device.GetDeviceName(), device)
}

func (c *BridgeDeviceController) Initialized() bool {
	c.startedPluginsMutex.Lock()
	defer c.startedPluginsMutex.Unlock()
	for _, dev := range c.startedPlugins {
		if !dev.devicePlugin.GetInitialized() {
			return false
		}
	}

	return true
}

func (c *BridgeDeviceController) ScanForNewDevices(stop chan struct{}) {
	defer close(c.newPlugins)
	logger := log.DefaultLogger()
	updates := make(chan netlink.LinkUpdate) 
	if err := netlink.LinkSubscribe(updates, stop); err != nil {
		logger.Reason(err).Criticalf("Could not subscribe to link updates, stopping device plugin.")
		close(stop)
		return
	}

	for {
		select {
		case update := <-updates:
			link := update.Link
			if bridge, ok := link.(*netlink.Bridge); ok && update.Header.Type == unix.RTM_NEWLINK {
				c.newPlugins <- NewBridgeDevicePlugin(bridge.Name, c.maxDevices)
			}
		case <-stop:
			logger.Info("Stop scanning for new devices due to stop signal")
			return
		}
	}
}
