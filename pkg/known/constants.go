package known

import "time"

const DediNIC = "eth-fleet"
const (
	LabelValueManagedBy = "mcs.fleetboard.io"
)

const (
	MaxNamespaceLength = 10
	MaxNameLength      = 10
)

type RouteOperation int

const (
	Add RouteOperation = iota
	Delete
)

// These are internal finalizer values must be qualified name.
const (
	AppFinalizer string = "apps.fleetboard.io/finalizer"
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
	HubSecretName             = "fleetboard"
	FleetboardSystemNamespace = "fleetboard-system"
	HubClusterName            = "hub"
)

// IPAM annotation const.
const (
	FleetboardPrefix = "fleetboard"
	DaemonCIDR       = "%s.io/daemon_cidr"
	CNFCIDR          = "%s.io/cnf_cidr"
	CLUSTERCIDR      = "%s.io/cluster_cidr"
	PublicKey        = "%s.io/public_key"
	DEDINICIP        = "router.fleetboard.io/dedicated_ip"
)

const (
	// DefaultDeviceName specifies name of WireGuard network device.
	DefaultDeviceName = "wg0"

	UDPPort = 31820
)
