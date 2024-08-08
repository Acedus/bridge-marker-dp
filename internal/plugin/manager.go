/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2018 Red Hat, Inc.
 *
 */

package plugin

import (
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"

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
	backoff             []time.Duration
	stop                chan struct{}
}

func NewBridgeDeviceController(
	permanentPlugins []Device,
) *BridgeDeviceController {

	permanentPluginsMap := make(map[string]Device, len(permanentPlugins))
	for i := range permanentPlugins {
		permanentPluginsMap[permanentPlugins[i].GetDeviceName()] = permanentPlugins[i]
	}

	controller := &BridgeDeviceController{
		permanentPlugins: permanentPluginsMap,
		startedPlugins:   map[string]controlledDevice{},
		backoff:          defaultBackoffTime,
	}

	return controller
}

func (c *BridgeDeviceController) NodeHasDevice(devicePath string) bool {
	_, err := os.Stat(devicePath)
	// Since this is a boolean question, any error means "no"
	return (err == nil)
}

func removeSelectorSpaces(selectorName string) string {
	// The name usually contain spaces which should be replaced with _
	// Such as GRID T4-1Q
	typeNameStr := strings.Replace(string(selectorName), " ", "_", -1)
	typeNameStr = strings.TrimSpace(typeNameStr)
	return typeNameStr

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
	func() {
		c.startedPluginsMutex.Lock()
		defer c.startedPluginsMutex.Unlock()
		for name, dev := range c.permanentPlugins {
			c.startDevice(name, dev)
		}
	}()

	// keep running until stop
	<-stop

	// stop all device plugins
	func() {
		c.startedPluginsMutex.Lock()
		defer c.startedPluginsMutex.Unlock()
		for name := range c.startedPlugins {
			c.stopDevice(name)
		}
	}()
	logger.Info("Shutting down device plugin controller")
	return nil
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
