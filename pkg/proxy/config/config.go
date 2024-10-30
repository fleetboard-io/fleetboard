/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package config

import (
	"fmt"
	"time"

	discovery "k8s.io/api/discovery/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	discoveryinformers "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsinformer "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions/apis/v1alpha1"
)

// ServiceImportHandler is an abstract interface of objects which receive
// notifications about service object changes.
type ServiceImportHandler interface {
	// OnServiceImportAdd is called whenever creation of new service object
	// is observed.
	OnServiceImportAdd(serviceImport *v1alpha1.ServiceImport)
	// OnServiceImportUpdate is called whenever modification of an existing
	// service object is observed.
	OnServiceImportUpdate(oldServiceImport, serviceImport *v1alpha1.ServiceImport)
	// OnServiceImportDelete is called whenever deletion of an existing service
	// object is observed.
	OnServiceImportDelete(serviceImport *v1alpha1.ServiceImport)
	// OnServiceImportSynced is called once all the initial event handlers were
	// called and the state is fully propagated to local cache.
	OnServiceImportSynced()
}

// EndpointSliceHandler is an abstract interface of objects which receive
// notifications about endpoint slice object changes.
type EndpointSliceHandler interface {
	// OnEndpointSliceAdd is called whenever creation of new endpoint slice
	// object is observed.
	OnEndpointSliceAdd(endpointSlice *discovery.EndpointSlice)
	// OnEndpointSliceUpdate is called whenever modification of an existing
	// endpoint slice object is observed.
	OnEndpointSliceUpdate(oldEndpointSlice, newEndpointSlice *discovery.EndpointSlice)
	// OnEndpointSliceDelete is called whenever deletion of an existing
	// endpoint slice object is observed.
	OnEndpointSliceDelete(endpointSlice *discovery.EndpointSlice)
	// OnEndpointSlicesSynced is called once all the initial event handlers were
	// called and the state is fully propagated to local cache.
	OnEndpointSlicesSynced()
}

// EndpointSliceConfig tracks a set of endpoints configurations.
type EndpointSliceConfig struct {
	listerSynced  cache.InformerSynced
	eventHandlers []EndpointSliceHandler
}

// NewEndpointSliceConfig creates a new EndpointSliceConfig.
func NewEndpointSliceConfig(endpointSliceInformer discoveryinformers.EndpointSliceInformer, resyncPeriod time.Duration) *EndpointSliceConfig {
	result := &EndpointSliceConfig{}

	handlerRegistration, err := endpointSliceInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    result.handleAddEndpointSlice,
			UpdateFunc: result.handleUpdateEndpointSlice,
			DeleteFunc: result.handleDeleteEndpointSlice,
		},
		resyncPeriod,
	)
	if err != nil {
		klog.Fatalf("Create EndpointSlice Informer failed: %v", err)
	}

	result.listerSynced = handlerRegistration.HasSynced

	return result
}

// RegisterEventHandler registers a handler which is called on every endpoint slice change.
func (c *EndpointSliceConfig) RegisterEventHandler(handler EndpointSliceHandler) {
	c.eventHandlers = append(c.eventHandlers, handler)
}

// Run waits for cache synced and invokes handlers after syncing.
func (c *EndpointSliceConfig) Run(stopCh <-chan struct{}) {
	klog.InfoS("EndpointSliceConfig.Run Starting endpoint slice config controller")

	if !cache.WaitForNamedCacheSync("endpoint slice config", stopCh, c.listerSynced) {
		return
	}

	for _, h := range c.eventHandlers {
		klog.V(3).InfoS("Calling handler.OnEndpointSlicesSynced()")
		h.OnEndpointSlicesSynced()
	}
}

func (c *EndpointSliceConfig) handleAddEndpointSlice(obj interface{}) {
	endpointSlice, ok := obj.(*discovery.EndpointSlice)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("unexpected object type: %T", obj))
		return
	}
	for _, h := range c.eventHandlers {
		if endpointSlice.Namespace != "syncer-operator" {
			klog.Infof("EndpointSliceConfig.handleAddEndpointSlice will not deal: %v/%v", endpointSlice.Namespace, endpointSlice.Name)
			continue
		}
		klog.V(4).InfoS("Calling handler.OnEndpointSliceAdd", "endpointslice", klog.KObj(endpointSlice))
		h.OnEndpointSliceAdd(endpointSlice)
	}
}

func (c *EndpointSliceConfig) handleUpdateEndpointSlice(oldObj, newObj interface{}) {
	oldEndpointSlice, ok := oldObj.(*discovery.EndpointSlice)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("unexpected object type: %T", newObj))
		return
	}
	newEndpointSlice, ok := newObj.(*discovery.EndpointSlice)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("unexpected object type: %T", newObj))
		return
	}
	for _, h := range c.eventHandlers {
		if newEndpointSlice.Namespace != "syncer-operator" {
			klog.Infof("EndpointSliceConfig.handleUpdateEndpointSlice will not deal: %v/%v", newEndpointSlice.Namespace, newEndpointSlice.Name)
			continue
		}
		klog.V(4).InfoS("Calling handler.OnEndpointSliceUpdate", "endpointslice", klog.KObj(newEndpointSlice))
		h.OnEndpointSliceUpdate(oldEndpointSlice, newEndpointSlice)
	}
}

func (c *EndpointSliceConfig) handleDeleteEndpointSlice(obj interface{}) {
	endpointSlice, ok := obj.(*discovery.EndpointSlice)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("unexpected object type: %T", obj))
			return
		}
		if endpointSlice, ok = tombstone.Obj.(*discovery.EndpointSlice); !ok {
			utilruntime.HandleError(fmt.Errorf("unexpected object type: %T", obj))
			return
		}
	}
	for _, h := range c.eventHandlers {
		if endpointSlice.Namespace != "syncer-operator" {
			klog.Infof("EndpointSliceConfig.handleDeleteEndpointSlice will not deal: %v/%v", endpointSlice.Namespace, endpointSlice.Name)
			continue
		}

		klog.V(4).InfoS("Calling handler.OnEndpointsDelete", "endpointslice", klog.KObj(endpointSlice))
		h.OnEndpointSliceDelete(endpointSlice)
	}
}

// ServiceImportConfig tracks a set of service configurations.
type ServiceImportConfig struct {
	listerSynced  cache.InformerSynced
	eventHandlers []ServiceImportHandler
}

// NewServiceImportConfig creates a new ServiceImportConfig.
func NewServiceImportConfig(serviceImportInformer mcsinformer.ServiceImportInformer, resyncPeriod time.Duration) *ServiceImportConfig {
	result := &ServiceImportConfig{}

	handlerRegistration, err := serviceImportInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    result.handleAddServiceImport,
			UpdateFunc: result.handleUpdateServiceImport,
			DeleteFunc: result.handleDeleteServiceImport,
		},
		resyncPeriod,
	)
	if err != nil {
		klog.Fatalf("Create ServiceImport Informer failed: %v", err)
	}

	result.listerSynced = handlerRegistration.HasSynced

	return result
}

// RegisterEventHandler registers a handler which is called on every service change.
func (c *ServiceImportConfig) RegisterEventHandler(handler ServiceImportHandler) {
	c.eventHandlers = append(c.eventHandlers, handler)
}

// Run waits for cache synced and invokes handlers after syncing.
func (c *ServiceImportConfig) Run(stopCh <-chan struct{}) {
	klog.InfoS("ServiceImportConfig.Run Starting serviceImport config controller")

	if !cache.WaitForNamedCacheSync("service config", stopCh, c.listerSynced) {
		return
	}

	for i := range c.eventHandlers {
		klog.V(3).InfoS("Calling handler.OnServiceImportSynced()")
		c.eventHandlers[i].OnServiceImportSynced()
	}
}

func (c *ServiceImportConfig) handleAddServiceImport(obj interface{}) {
	serviceImport, ok := obj.(*v1alpha1.ServiceImport)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("unexpected object type: %v", obj))
		return
	}
	klog.V(4).InfoS("ServiceImportConfig.handleAddServiceImport", "serviceImport", klog.KObj(serviceImport))
	for i := range c.eventHandlers {
		klog.V(4).InfoS("Calling handler.OnServiceImportAdd", "serviceImport", klog.KObj(serviceImport))
		c.eventHandlers[i].OnServiceImportAdd(serviceImport)
	}
}

func (c *ServiceImportConfig) handleUpdateServiceImport(oldObj, newObj interface{}) {
	oldServiceImport, ok := oldObj.(*v1alpha1.ServiceImport)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("unexpected object type: %v", oldObj))
		return
	}
	serviceImport, ok := newObj.(*v1alpha1.ServiceImport)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("unexpected object type: %v", newObj))
		return
	}
	klog.V(4).InfoS("ServiceImportConfig.handleUpdateServiceImport", "serviceImport", klog.KObj(serviceImport))
	for i := range c.eventHandlers {
		klog.V(4).InfoS("Calling handler.OnServiceImportUpdate")
		c.eventHandlers[i].OnServiceImportUpdate(oldServiceImport, serviceImport)
	}
}

func (c *ServiceImportConfig) handleDeleteServiceImport(obj interface{}) {
	serviceImport, ok := obj.(*v1alpha1.ServiceImport)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("unexpected object type: %v", obj))
			return
		}
		if serviceImport, ok = tombstone.Obj.(*v1alpha1.ServiceImport); !ok {
			utilruntime.HandleError(fmt.Errorf("unexpected object type: %v", obj))
			return
		}
	}
	klog.V(4).InfoS("ServiceImportConfig.handleDeleteServiceImport", "serviceImport", klog.KObj(serviceImport))
	for i := range c.eventHandlers {
		klog.V(4).InfoS("Calling handler.OnServiceImportDelete")
		c.eventHandlers[i].OnServiceImportDelete(serviceImport)
	}
}
