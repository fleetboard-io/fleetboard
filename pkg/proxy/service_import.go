/*
Copyright 2017 The Kubernetes Authors.

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

package proxy

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/proxy/metrics"
	netutils "k8s.io/utils/net"
	"sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	"github.com/fleetboard-io/fleetboard/pkg/known"
)

// BaseServicePortInfo contains base information that defines a service.
// This could be used directly by proxier while processing services,
// or can be used for constructing a more specific ServiceInfo struct
// defined by the proxier if needed.
type BaseServicePortInfo struct {
	clusterIP                net.IP
	port                     int
	protocol                 v1.Protocol
	nodePort                 int
	loadBalancerStatus       v1.LoadBalancerStatus
	sessionAffinityType      v1.ServiceAffinity
	stickyMaxAgeSeconds      int
	externalIPs              []string
	loadBalancerSourceRanges []string
	healthCheckNodePort      int
	externalPolicyLocal      bool
	internalPolicyLocal      bool
	internalTrafficPolicy    *v1.ServiceInternalTrafficPolicy
	hintsAnnotation          string
}

var _ ServicePort = &BaseServicePortInfo{}

// String is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) String() string {
	return fmt.Sprintf("%s:%d/%s", bsvcPortInfo.clusterIP, bsvcPortInfo.port, bsvcPortInfo.protocol)
}

// ClusterIP is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) ClusterIP() net.IP {
	return bsvcPortInfo.clusterIP
}

// Port is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) Port() int {
	return bsvcPortInfo.port
}

// SessionAffinityType is part of the ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) SessionAffinityType() v1.ServiceAffinity {
	return bsvcPortInfo.sessionAffinityType
}

// StickyMaxAgeSeconds is part of the ServicePort interface
func (bsvcPortInfo *BaseServicePortInfo) StickyMaxAgeSeconds() int {
	return bsvcPortInfo.stickyMaxAgeSeconds
}

// Protocol is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) Protocol() v1.Protocol {
	return bsvcPortInfo.protocol
}

// LoadBalancerSourceRanges is part of ServicePort interface
func (bsvcPortInfo *BaseServicePortInfo) LoadBalancerSourceRanges() []string {
	return bsvcPortInfo.loadBalancerSourceRanges
}

// HealthCheckNodePort is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) HealthCheckNodePort() int {
	return bsvcPortInfo.healthCheckNodePort
}

// NodePort is part of the ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) NodePort() int {
	return bsvcPortInfo.nodePort
}

// ExternalIPStrings is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) ExternalIPStrings() []string {
	return bsvcPortInfo.externalIPs
}

// LoadBalancerIPStrings is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) LoadBalancerIPStrings() []string {
	var ips []string
	for _, ing := range bsvcPortInfo.loadBalancerStatus.Ingress {
		ips = append(ips, ing.IP)
	}
	return ips
}

// ExternalPolicyLocal is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) ExternalPolicyLocal() bool {
	return bsvcPortInfo.externalPolicyLocal
}

// InternalPolicyLocal is part of ServicePort interface
func (bsvcPortInfo *BaseServicePortInfo) InternalPolicyLocal() bool {
	return bsvcPortInfo.internalPolicyLocal
}

// InternalTrafficPolicy is part of ServicePort interface
func (bsvcPortInfo *BaseServicePortInfo) InternalTrafficPolicy() *v1.ServiceInternalTrafficPolicy {
	return bsvcPortInfo.internalTrafficPolicy
}

// HintsAnnotation is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) HintsAnnotation() string {
	return bsvcPortInfo.hintsAnnotation
}

// ExternallyAccessible is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) ExternallyAccessible() bool {
	return bsvcPortInfo.nodePort != 0 ||
		len(bsvcPortInfo.loadBalancerStatus.Ingress) != 0 || len(bsvcPortInfo.externalIPs) != 0
}

// UsesClusterEndpoints is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) UsesClusterEndpoints() bool {
	// The service port uses Cluster endpoints if the internal traffic policy is "Cluster",
	// or if it accepts external traffic at all. (Even if the external traffic policy is
	// "Local", we need Cluster endpoints to implement short circuiting.)
	return !bsvcPortInfo.internalPolicyLocal || bsvcPortInfo.ExternallyAccessible()
}

// UsesLocalEndpoints is part of ServicePort interface.
func (bsvcPortInfo *BaseServicePortInfo) UsesLocalEndpoints() bool {
	return bsvcPortInfo.internalPolicyLocal || (bsvcPortInfo.externalPolicyLocal && bsvcPortInfo.ExternallyAccessible())
}

func (sct *ServiceImportChangeTracker) newBaseServiceInfo(port *v1.ServicePort, serviceImport *v1alpha1.ServiceImport,
) *BaseServicePortInfo {
	clusterIP := serviceImport.Spec.IPs[0]
	klog.V(4).InfoS("newBaseServiceInfo", "clusterIP", clusterIP, "serviceImport", serviceImport)
	info := &BaseServicePortInfo{
		clusterIP: netutils.ParseIPSloppy(clusterIP),
		port:      int(port.Port),
		protocol:  port.Protocol,
		nodePort:  int(port.NodePort),
		// sessionAffinityType:   service.Spec.SessionAffinity,
		// stickyMaxAgeSeconds:   stickyMaxAgeSeconds,
		// externalPolicyLocal:   externalPolicyLocal,
		// internalPolicyLocal:   internalPolicyLocal,
		// internalTrafficPolicy: service.Spec.InternalTrafficPolicy,
	}
	return info
}

type makeServicePortFunc func(*v1.ServicePort, *v1alpha1.ServiceImport, *BaseServicePortInfo) ServicePort

// This handler is invoked by the apply function on every change. This function should not modify the
// ServicePortMap's but just use the changes for any Proxier specific cleanup.
type processServiceMapChangeFunc func(previous, current ServicePortMap)

// serviceImportChange contains all changes to services that happened since proxy rules were synced.
// For a single object,
// changes are accumulated, i.e. previous is state from before applying the changes,
// current is state after applying all of the changes.
type serviceImportChange struct {
	previous ServicePortMap
	current  ServicePortMap
}

// ServiceImportChangeTracker carries state about uncommitted changes to an arbitrary number of
// ServicesImport, keyed by their namespace and name.
type ServiceImportChangeTracker struct {
	// lock protects items.
	lock sync.Mutex
	// items maps a service to its serviceChange.
	items map[types.NamespacedName]*serviceImportChange
	// makeServiceInfo allows proxier to inject customized information when processing service.
	makeServiceInfo         makeServicePortFunc
	processServiceMapChange processServiceMapChangeFunc
	ipFamily                v1.IPFamily

	recorder events.EventRecorder
}

// NewServiceImportChangeTracker initializes a ServiceImportChangeTracker
func NewServiceImportChangeTracker(makeServiceInfo makeServicePortFunc, ipFamily v1.IPFamily,
	recorder events.EventRecorder, processServiceMapChange processServiceMapChangeFunc) *ServiceImportChangeTracker {
	return &ServiceImportChangeTracker{
		items:                   make(map[types.NamespacedName]*serviceImportChange),
		makeServiceInfo:         makeServiceInfo,
		recorder:                recorder,
		ipFamily:                ipFamily,
		processServiceMapChange: processServiceMapChange,
	}
}

// Update updates given service's change map based on the <previous, current> service pair.
//
//	It returns true if items changed,
//
// otherwise return false.  Update can be used to add/update/delete items of ServiceChangeMap.  For example,
// Add item
//   - pass <nil, service> as the <previous, current> pair.
//
// Update item
//   - pass <oldService, service> as the <previous, current> pair.
//
// Delete item
//   - pass <service, nil> as the <previous, current> pair.
func (sct *ServiceImportChangeTracker) Update(previous, current *v1alpha1.ServiceImport) bool {
	// This is unexpected, we should return false directly.
	if previous == nil && current == nil {
		return false
	}

	svc := current
	if svc == nil {
		svc = previous
	}
	metrics.ServiceChangesTotal.Inc()
	namespacedName := types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}

	sct.lock.Lock()
	defer sct.lock.Unlock()

	change, exists := sct.items[namespacedName]
	if !exists {
		change = &serviceImportChange{}
		change.previous = sct.serviceImportToServiceMap(previous)
		sct.items[namespacedName] = change
	}
	change.current = sct.serviceImportToServiceMap(current)
	// if change.previous equal to change.current, it means no change
	if reflect.DeepEqual(change.previous, change.current) {
		delete(sct.items, namespacedName)
	} else {
		klog.V(4).InfoS("Service updated ports", "service",
			klog.KObj(svc), "portCount", len(change.current))
	}
	metrics.ServiceChangesPending.Set(float64(len(sct.items)))
	return len(sct.items) > 0
}

// UpdateServiceMapResult is the updated results after applying service changes.
type UpdateServiceMapResult struct {
	// UpdatedServices lists the names of all services added/updated/deleted since the
	// last Update.
	UpdatedServices sets.Set[types.NamespacedName]

	// DeletedUDPClusterIPs holds stale (no longer assigned to a Service) Service IPs
	// that had UDP ports. Callers can use this to abort timeout-waits or clear
	// connection-tracking information.
	DeletedUDPClusterIPs sets.Set[string]
}

// HealthCheckNodePorts returns a map of Service names to HealthCheckNodePort values
// for all Services in sm with non-zero HealthCheckNodePort.
func (spm ServicePortMap) HealthCheckNodePorts() map[types.NamespacedName]uint16 {
	// TODO: If this will appear to be computationally expensive, consider
	// computing this incrementally similarly to svcPortMap.
	ports := make(map[types.NamespacedName]uint16)
	for svcPortName, info := range spm {
		if info.HealthCheckNodePort() != 0 {
			ports[svcPortName.NamespacedName] = uint16(info.HealthCheckNodePort())
		}
	}
	return ports
}

// ServicePortMap maps a service to its ServicePort.
type ServicePortMap map[ServicePortName]ServicePort

func (spm *ServicePortMap) String() string {
	spmListStr := ""
	for spn, servicePort := range *spm {
		spmListStr += fmt.Sprintf("%s: %s\n", spn.String(), servicePort.String())
	}
	return spmListStr
}

// serviceToServiceMap translates a single Service object to a ServicePortMap.
//
// NOTE: service object should NOT be modified.
func (sct *ServiceImportChangeTracker) serviceImportToServiceMap(serviceImport *v1alpha1.ServiceImport) ServicePortMap {
	if serviceImport == nil {
		return nil
	}
	// todo: add a check to see if the serviceImport is a headless service

	// Get clusterIP by family from the serviceImport
	if len(serviceImport.Spec.IPs) == 0 || serviceImport.Spec.IPs[0] == "" {
		klog.ErrorS(nil, "ServiceImport has no clusterIP", "serviceImport", klog.KObj(serviceImport))
		return nil
	}

	// get service name and namespace
	nameLabel, ok := serviceImport.Labels[known.LabelServiceName]
	if !ok {
		klog.ErrorS(nil, "ServiceImport has no name label", "serviceImport", klog.KObj(serviceImport))
		return nil
	}
	namespaceLabel, ok := serviceImport.Labels[known.LabelServiceNameSpace]
	if !ok {
		klog.ErrorS(nil, "ServiceImport has no namespace label", "serviceImport", klog.KObj(serviceImport))
		return nil
	}

	svcPortMap := make(ServicePortMap)
	svcName := types.NamespacedName{Namespace: namespaceLabel, Name: nameLabel}
	for i := range serviceImport.Spec.Ports {
		servicePort := serviceImport.Spec.Ports[i]

		svcV1Port := svcImportPortToV1Port(servicePort)
		// use the port index as the port name to mapping the service port to endpoint
		svcPortName := ServicePortName{NamespacedName: svcName, Port: strconv.Itoa(i), Protocol: servicePort.Protocol}
		baseSvcInfo := sct.newBaseServiceInfo(svcV1Port, serviceImport)
		if sct.makeServiceInfo != nil {
			svcPortMap[svcPortName] = sct.makeServiceInfo(svcV1Port, serviceImport, baseSvcInfo)
		} else {
			svcPortMap[svcPortName] = baseSvcInfo
		}
	}
	return svcPortMap
}

func svcImportPortToV1Port(svcImportPort v1alpha1.ServicePort) *v1.ServicePort {
	return &v1.ServicePort{
		Name:     svcImportPort.Name,
		Protocol: svcImportPort.Protocol,
		Port:     svcImportPort.Port,
	}
}

// Update updates ServicePortMap base on the given changes, returns information about the
// diff since the last Update, triggers processServiceMapChange on every change, and
// clears the changes map.
func (spm ServicePortMap) Update(sct *ServiceImportChangeTracker) UpdateServiceMapResult {
	klog.V(4).InfoS("ServicePortMap Update")
	sct.lock.Lock()
	defer sct.lock.Unlock()

	result := UpdateServiceMapResult{
		UpdatedServices:      sets.New[types.NamespacedName](),
		DeletedUDPClusterIPs: sets.New[string](),
	}

	for nn, change := range sct.items {
		if sct.processServiceMapChange != nil {
			sct.processServiceMapChange(change.previous, change.current)
		}
		result.UpdatedServices.Insert(nn)

		spm.merge(change.current)
		// filter out the Update event of current changes from previous changes
		// before calling unmerge() so that can skip deleting the Update events.
		change.previous.filter(change.current)
		spm.unmerge(change.previous, result.DeletedUDPClusterIPs)
	}
	// clear changes after applying them to ServicePortMap.
	sct.items = make(map[types.NamespacedName]*serviceImportChange)
	metrics.ServiceChangesPending.Set(0)

	return result
}

// merge adds other ServicePortMap's elements to current ServicePortMap.
// If collision, other ALWAYS win. Otherwise add the other to current.
// In other words, if some elements in current collisions with other, update the current by other.
// It returns a string type set which stores all the newly merged services' identifier,
//
//	ServicePortName.String(), to help users
//
// tell if a service is deleted or updated.
// The returned value is one of the arguments of ServicePortMap.unmerge().
// ServicePortMap A Merge ServicePortMap B will do following 2 things:
//   - update ServicePortMap A.
//   - produce a string set which stores all other ServicePortMap's ServicePortName.String().
//
// For example,
//
//	A{}
//	B{{"ns", "cluster-ip", "http"}: {"172.16.55.10", 1234, "TCP"}}
//	  A updated to be {{"ns", "cluster-ip", "http"}: {"172.16.55.10", 1234, "TCP"}}
//	  produce string set {"ns/cluster-ip:http"}
//
//	A{{"ns", "cluster-ip", "http"}: {"172.16.55.10", 345, "UDP"}}
//	B{{"ns", "cluster-ip", "http"}: {"172.16.55.10", 1234, "TCP"}}
//	  A updated to be {{"ns", "cluster-ip", "http"}: {"172.16.55.10", 1234, "TCP"}}
//	  produce string set {"ns/cluster-ip:http"}
func (spm *ServicePortMap) merge(other ServicePortMap) sets.Set[string] {
	// existingPorts is going to store all identifiers of all services in `other` ServicePortMap.
	existingPorts := sets.New[string]()
	for svcPortName, info := range other {
		// Take ServicePortName.String() as the newly merged service's identifier and put it into existingPorts.
		existingPorts.Insert(svcPortName.String())
		_, exists := (*spm)[svcPortName]
		if !exists {
			klog.V(4).InfoS("Adding new service port", "portName",
				svcPortName, "servicePort", info)
		} else {
			klog.V(4).InfoS("Updating existing service port", "portName",
				svcPortName, "servicePort", info)
		}
		(*spm)[svcPortName] = info
	}
	return existingPorts
}

// filter filters out elements from ServicePortMap base on given ports string sets.
func (spm *ServicePortMap) filter(other ServicePortMap) {
	for svcPortName := range *spm {
		// skip the delete for Update event.
		if _, ok := other[svcPortName]; ok {
			delete(*spm, svcPortName)
		}
	}
}

// unmerge deletes all other ServicePortMap's elements from current ServicePortMap and
// updates deletedUDPClusterIPs with all the newly-deleted UDP cluster IPs.
func (spm *ServicePortMap) unmerge(other ServicePortMap, deletedUDPClusterIPs sets.Set[string]) {
	for svcPortName := range other {
		info, exists := (*spm)[svcPortName]
		if exists {
			klog.V(4).InfoS("Removing service port", "portName", svcPortName)
			if info.Protocol() == v1.ProtocolUDP {
				deletedUDPClusterIPs.Insert(info.ClusterIP().String())
			}
			delete(*spm, svcPortName)
		} else {
			klog.ErrorS(nil, "Service port does not exists", "portName", svcPortName)
		}
	}
}
