package known

import (
	"time"
)

const (
	Fleetboard                = "fleetboard"
	FleetboardSystemNamespace = "fleetboard-system"
	SyncNamespace             = "syncer-operator"
	HubClusterName            = "hub"
	HubSecretName             = Fleetboard
)

// pod environment variables
const (
	EnvPodName      = "FLEETBOARD_PODNAME"
	EnvPodNamespace = "FLEETBOARD_PODNAMESPACE"
)

const (
	// AppFinalizer are internal finalizer values must be qualified name.
	AppFinalizer string = "apps.fleetboard.io/finalizer"
	// DefaultResync means the default resync time
	DefaultResync = time.Hour * 12
)
const (
	MaxNamespaceLength = 10
	MaxNameLength      = 10
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
