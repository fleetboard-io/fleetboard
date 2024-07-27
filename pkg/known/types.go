package known

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type SyncerConfig struct {
	// LocalRestConfig the REST config used to access the local resources to sync.
	LocalRestConfig *rest.Config

	// LocalClient the client used to access local resources to sync. This is optional and is provided for unit testing
	// in lieu of the LocalRestConfig. If not specified, one is created from the LocalRestConfig.
	LocalClient     dynamic.Interface
	LocalNamespace  string
	LocalClusterID  string
	RemoteNamespace string
}

type EnvConfig struct {
	PodName        string
	Endpoint       string
	ClusterID      string
	BootStrapToken string
}
