package tunnel

import (
	"fmt"

	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/config"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"

	"github.com/nauti-io/nauti/pkg/known"
)

type Specification struct {
	Options
	known.EnvConfig
}

type Options struct {
	// hub secret located
	HubSecretNamespace string
	// hub secret name
	HubSecretName string
	// used to share endpoint slices in hub
	ShareNamespace string
	// used to store endpoint slices in local clusters
	LocalNamespace string
	// true means run as hub
	AsHub bool
	// true means run as cluster
	AsCluster bool
	// cidr means which cidr this cluster will use, it is usually empty if is not hub.
	CIDR string
	// hub url is the service url for hub cluster api-server
	HubURL string

	Logs *logs.Options
	// ClientConnection specifies the kubeconfig file and client connection
	// settings for the proxy server to use when communicating with the apiserver.
	ClientConnection config.ClientConnectionConfiguration
}

// NewOptions creates a new Options object with default parameters
func NewOptions() *Options {
	o := Options{
		ClientConnection: config.ClientConnectionConfiguration{},
		Logs:             logs.NewOptions(),
	}
	o.Logs.Verbosity = logsapi.VerbosityLevel(2)

	return &o
}

func (o *Options) Validate() []error {
	var allErrors []error
	if o.AsHub && len(o.CIDR) == 0 {
		allErrors = append(allErrors, fmt.Errorf("--cidr must be specified when run as hub"))
	}
	if !o.AsHub && len(o.HubURL) == 0 {
		allErrors = append(allErrors, fmt.Errorf("--hub-url must be specified when run as cluster"))
	}

	if len(o.ShareNamespace) == 0 {
		allErrors = append(allErrors, fmt.Errorf("--shared-namespace must be specified"))
	}

	if !o.AsHub && len(o.LocalNamespace) == 0 {
		allErrors = append(allErrors, fmt.Errorf("--local-namespace must be specified when run as cluster"))
	}

	if !o.AsHub && len(o.HubSecretNamespace) == 0 {
		allErrors = append(allErrors, fmt.Errorf("--hub-secret-namespace must be specified when run as cluser"))
	}

	if !o.AsHub && len(o.HubSecretName) == 0 {
		allErrors = append(allErrors, fmt.Errorf("--hub-secret-name must be specified when run as cluser"))
	}

	return allErrors
}

func (o *Options) Complete() error {
	if o.AsHub {
		if len(o.CIDR) == 0 {
			o.CIDR = "20.112.0.0/12"
		}
	}
	return nil
}

// Flags returns flags for a specific APIServer by section name
func (o *Options) Flags() (fss cliflag.NamedFlagSets) {
	logsapi.AddFlags(o.Logs, fss.FlagSet("logs"))

	fs := fss.FlagSet("misc")
	fs.StringVar(&o.ClientConnection.Kubeconfig, "kubeconfig", o.ClientConnection.Kubeconfig,
		"Path to a kubeconfig file pointing at the 'core' kubernetes server. Only required if out-of-cluster.")

	fs.BoolVar(&o.AsHub, "as-hub", false, "If true, run as hub. [default=false]")

	fs.BoolVar(&o.AsCluster, "as-cluster", false, "If true, run as cluster. [default=true]")

	fs.StringVar(&o.CIDR, "cidr", o.CIDR, "usually global cidr used in multi-cluster ipam,"+
		" or your cluster local ip range")

	fs.StringVar(&o.HubURL, "hub-url", o.HubURL, "hub public url, used by cluster.")

	fs.StringVar(&o.HubSecretNamespace, "hub-secret-namespace", o.HubSecretNamespace,
		"hub secret locate namespace to access peer crd.")

	fs.StringVar(&o.HubSecretName, "hub-secret-name", o.HubSecretName,
		"hub secret locate name to access peer crd.")

	fs.StringVar(&o.ShareNamespace, "shared-namespace", o.ShareNamespace,
		"shared namespace in hub used to share endpoint slices across clusters")

	fs.StringVar(&o.LocalNamespace, "local-namespace", o.LocalNamespace,
		"local namespace in cluster used to share endpoint slices across clusters")

	return fss
}
