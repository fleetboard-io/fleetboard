package known

import "time"

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
	SyncNamespace             = "syncer-operator"
	HubClusterName            = "hub"
)

// IPAM annotation const.
const (
	FleetboardPrefix      = "fleetboard" // todo: replace %s with constant?
	FleetboardTunnelCIDR  = "%s.io/tunnel_cidr"
	FleetboardClusterCIDR = "%s.io/cluster_cidr"
	FleetboardNodeCIDR    = "%s.io/node_cidr"
	FleetboardServiceCIDR = "%s.io/service_cidr"

	PublicKey = "%s.io/public_key"
	DedinicIP = "router.fleetboard.io/dedicated_ip"
)

const (
	// DefaultDeviceName specifies name of WireGuard network device.
	DefaultDeviceName = "wg0"
	DediNIC           = "eth-fleet"

	UDPPort = 31820
)

const (
	EnvPodName      = "FLEETBOARD_PODNAME"
	EnvPodNamespace = "FLEETBOARD_PODNAMESPACE"
)
