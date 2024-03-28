package known

import "time"

// These are internal finalizer values must be qualified name.
const (
	AppFinalizer string = "apps.nauti.io/finalizer"
)

// fields should be ignored when compared
const (
	MetaGeneration      = "/metadata/generation"
	CreationTimestamp   = "/metadata/creationTimestamp"
	ManagedFields       = "/metadata/managedFields"
	MetaUID             = "/metadata/uid"
	MetaSelflink        = "/metadata/selfLink"
	MetaResourceVersion = "/metadata/resourceVersion"

	SectionStatus = "/status"
)

const (
	// DefaultResync means the default resync time
	DefaultResync = time.Hour * 12
)

const (
	HubSecretName          = "hub-syncer"
	ClusterAPIServerURLKey = "apiserver-advertise-url"
)

// IPAM annotation const.
const (
	NautiPrefix                     = "nauti"
	AllocatedAnnotationTemplate     = "%s.io/allocated"
	RoutesAnnotationTemplate        = "%s.io/routes"
	MacAddressAnnotationTemplate    = "%s.io/mac_address"
	IPAddressAnnotationTemplate     = "%s.io/ip_address"
	CidrAnnotationTemplate          = "%s.io/cidr"
	GatewayAnnotationTemplate       = "%s.io/gateway"
	LogicalSwitchAnnotationTemplate = "%s.io/logical_switch"
	PodNicAnnotationTemplate        = "%s.io/pod_nic_type"

	CNFLabel = "nauti.io/cnf"
)
