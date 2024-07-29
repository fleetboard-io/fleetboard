package known

import "time"

const (
	LabelValueManagedBy  = "mcs.nauti.io"
	OriginName           = "origin-name"
	OriginNamespace      = "origin-namespace"
	LabelSourceNamespace = "submariner-io/originatingNamespace"
	LabelSourceCluster   = "syncer.nauti.io/sourceCluster"
	LabelSourceName      = "syncer.nauti.io/sourceName"
	LabelOriginNameSpace = "syncer.nauti.io/sourceNamespace"
)

const (
	MaxNamespaceLength = 10
	MaxNameLength      = 10
	MaxClusternName    = 10
)

type RouteOperation int

const (
	Add RouteOperation = iota
	Delete
)

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
	HubSecretName        = "octopus"
	NautiSystemNamespace = "nauti-system"
	HubClusterName       = "hub"
)

// IPAM annotation const.
const (
	NautiPrefix = "nauti"
	DaemonCIDR  = "%s.io/daemon_cidr"
	CNFCIDR     = "%s.io/cnf_cidr"
	CLUSTERCIDR = "%s.io/cluster_cidr"
	PublicKey   = "%s.io/public_key"
)

const (
	// DefaultDeviceName specifies name of WireGuard network device.
	DefaultDeviceName = "wg0"

	UDPPort = 31820
)
