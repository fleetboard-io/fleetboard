package known

import "time"

// These are internal finalizer values must be qualified name.
const (
	AppFinalizer            string = "apps.clusternet.io/finalizer"
	FeedProtectionFinalizer string = "apps.clusternet.io/feed-protection"
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

	// DefaultRetryPeriod means the default retry period
	DefaultRetryPeriod = 5 * time.Second

	// NoResyncPeriod indicates that informer resync should be delayed as long as possible
	NoResyncPeriod = 0 * time.Second

	// DefaultThreadiness defines default number of threads
	DefaultThreadiness = 10
)

const (
	HubSecretName          = "hub-syncer"
	ClusterAPIServerURLKey = "apiserver-advertise-url"
)
