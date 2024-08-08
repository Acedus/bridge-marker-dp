// Copyright 2019 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	goflag "flag"
	"sync"
	"time"

	"github.com/Acedus/bridge-marker-dp/internal/plugin"
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
	maxDevices          int
	backoff             []time.Duration
	stop                chan struct{}
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

	bridgeDeviceController := plugin.NewBridgeDeviceController(bridgeDevices)
	
	go bridgeDeviceController.Run(app.stop)

	<- app.stop
}

func main() {
	app := &bridgeMarkerApp{
		stop: make(chan struct{}),
		backoff: defaultBackoffTime,
	}
	app.AddFlags()

	flag.Parse()

	app.Run()
}
