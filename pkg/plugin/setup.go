package plugin

import (
	"flag"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/pkg/errors"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	masterURL  string
	kubeconfig string
)

// Hook for unit tests.
var buildKubeConfigFunc = clientcmd.BuildConfigFromFlags

// init registers this plugin within the Caddy plugin framework. It uses "example" as the
// name, and couples it to the Action "setup".
func init() {
	caddy.RegisterPlugin("crossdns", caddy.Plugin{
		ServerType: "dns",
		Action:     setup,
	})
}

func setup(c *caddy.Controller) error {
	klog.Infof("In setup")

	cd, err := CrossDNSParse(c)
	if err != nil {
		return plugin.Error("crossdns", err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		cd.Next = next
		return cd
	})

	return nil
}

func CrossDNSParse(c *caddy.Controller) (*CrossDNS, error) {
	cfg, err := buildKubeConfigFunc(masterURL, kubeconfig)
	if err != nil {
		return nil, errors.Wrap(err, "error building kubeconfig")
	}

	stopChannel := make(chan struct{})
	cd := &CrossDNS{}

	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	mcsClientSet := mcsclientset.NewForConfigOrDie(cfg)
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, known.DefaultResync)
	mcsInformerFactory := mcsInformers.NewSharedInformerFactory(mcsClientSet, known.DefaultResync)
	endpointSlicesInformer := kubeInformerFactory.Discovery().V1().EndpointSlices()
	siInformer := mcsInformerFactory.Multicluster().V1alpha1().ServiceImports()

	cd.endpointSlicesLister = endpointSlicesInformer.Lister()
	cd.SILister = siInformer.Lister()
	cd.epsSynced = endpointSlicesInformer.Informer().HasSynced
	cd.SISynced = siInformer.Informer().HasSynced

	kubeInformerFactory.Start(stopChannel)
	mcsInformerFactory.Start(stopChannel)

	c.OnShutdown(func() error {
		close(stopChannel)
		return nil
	})

	if err != nil {
		klog.Fatalf("failed to add event handler for service import: %v", err)
		return nil, err
	}

	if c.Next() {
		cd.Zones = c.RemainingArgs()
		if len(cd.Zones) == 0 {
			cd.Zones = make([]string, len(c.ServerBlockKeys))
			copy(cd.Zones, c.ServerBlockKeys)
		}

		for i, str := range cd.Zones {
			cd.Zones[i] = plugin.Host(str).Normalize()
		}

		for c.NextBlock() {
			switch c.Val() {
			case "fallthrough":
				cd.Fall.SetZonesFromArgs(c.RemainingArgs())
			default:
				if c.Val() != "}" {
					return nil, c.Errf("unknown property '%s'", c.Val())
				}
			}
		}
	}
	return cd, nil
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "",
		"Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server."+
			" Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
