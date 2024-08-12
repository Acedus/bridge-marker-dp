package main

import (
	goflag "flag"
	"sync"
	"time"

	"github.com/Acedus/bridge-marker-dp/pkg/plugin"
	flag "github.com/spf13/pflag"
	"kubevirt.io/client-go/log"
)

const (
	// THe Linux kernel has a default hardcoded parameter BR_PORT_BITS = 10.
	// This means that the maximum number of ports allowed on a bridge is 1024 (2^10 = 1024).
	maxDevices = 1024
)

var defaultBackoffTime = []time.Duration{1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}

type bridgeMarkerApp struct {
	startedPluginMutex sync.Mutex
	maxDevices         int
	backoff            []time.Duration
	stop               chan struct{}
}

func (app *bridgeMarkerApp) InitFlags() {
	flag.CommandLine.AddGoFlag(goflag.CommandLine.Lookup("v"))
}

func (app *bridgeMarkerApp) AddFlags() {
	app.InitFlags()
	flag.IntVar(&app.maxDevices, "max-devices", maxDevices,
		"The maximum number of connected devices to the bridge")
}

func (app *bridgeMarkerApp) Run() {
	logger := log.DefaultLogger()
	bridgeDevices, err := plugin.GetBridgeDevicePlugins(app.maxDevices)
	if err != nil {
		logger.Errorf("bridge-marker couldn't start: %v", err)
		panic(err)
	}

	if len(bridgeDevices) == 0 {
		logger.Warning("no bridge devices found on node.")
	}

	bridgeDeviceController := plugin.NewBridgeDeviceController(bridgeDevices)

	go bridgeDeviceController.Run(app.stop)

	<-app.stop
}

func main() {
	app := &bridgeMarkerApp{
		stop:    make(chan struct{}),
		backoff: defaultBackoffTime,
	}
	app.AddFlags()

	flag.Parse()

	app.Run()
}
