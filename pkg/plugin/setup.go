package plugin

import (
	"flag"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
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
		return plugin.Error("crossdns", err) // nolint:wrapcheck // No need to wrap this.
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
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Hour*2)
	endpointSlicesInformer := kubeInformerFactory.Discovery().V1().EndpointSlices()

	cd.endpointSlicesLister = endpointSlicesInformer.Lister()
	cd.epsSynced = endpointSlicesInformer.Informer().HasSynced

	kubeInformerFactory.Start(stopChannel)

	c.OnShutdown(func() error {
		close(stopChannel)
		return nil
	})

	if err != nil {
		klog.Fatalf("failed to add event handler for serviceimport: %w", err)
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
					return nil, c.Errf("unknown property '%s'", c.Val()) // nolint:wrapcheck // No need to wrap this.
				}
			}
		}
	}
	return cd, nil
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
