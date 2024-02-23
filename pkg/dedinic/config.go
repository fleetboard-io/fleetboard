package dedinic

import (
	"context"
	"fmt"
	"github.com/kubeovn/kube-ovn/pkg/util"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"net"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"strings"
)

var (
	StopCh = signals.SetupSignalHandler().Done()
)

type Configuration struct {
	// interface being used for tunnel
	tunnelIface           string
	Iface                 string
	MTU                   int
	MSS                   int
	OvsSocket             string
	NodeName              string
	ServiceClusterIPRange string
	ClusterRouter         string
	EncapChecksum         bool
	MacLearningFallback   bool
	NetworkType           string
	DefaultProviderName   string
	DefaultInterfaceName  string
	OVSVsctlConcurrency   int32

	KubeClient kubernetes.Interface
}

var Conf *Configuration

func InitConfig() error {

	Conf = &Configuration{
		tunnelIface:           "",
		Iface:                 "",
		MTU:                   0,
		MSS:                   0,
		OvsSocket:             "",
		NodeName:              "",
		ServiceClusterIPRange: "",
		ClusterRouter:         "",
		EncapChecksum:         false,
		MacLearningFallback:   false,
		NetworkType:           "",
		DefaultProviderName:   "",
		DefaultInterfaceName:  "",
		OVSVsctlConcurrency:   0,
		KubeClient:            nil,
	}

	err := Conf.initKubeClient()
	if err != nil {
		return err
	}
	err = Conf.initNicConfig()
	return err
}
func (config *Configuration) initKubeClient() error {
	var cfg *rest.Config
	var err error
	cfg, err = rest.InClusterConfig()
	if err != nil {
		klog.Errorf("use in cluster config failed %v", err)
		return err
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Errorf("init kubernetes client failed %v", err)
		return err
	}
	config.KubeClient = kubeClient
	return nil
}

func (config *Configuration) initNicConfig() error {
	if config.NodeName == "" {
		klog.Info("node name not specified in command line parameters, fall back to the environment variable")
		if config.NodeName = strings.ToLower(os.Getenv(util.HostnameEnv)); config.NodeName == "" {
			klog.Info("node name not specified in environment variables, fall back to the hostname")
			hostname, err := os.Hostname()
			if err != nil {
				return fmt.Errorf("failed to get hostname: %v", err)
			}
			config.NodeName = strings.ToLower(hostname)
		}
	}

	klog.V(5).Infof("initNicConfig")
	// Support to specify node network card separately
	node, err := config.KubeClient.CoreV1().Nodes().Get(context.Background(), config.NodeName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to find node info, err: %v", err)
		return err
	}

	encapIP := config.getEncapIP(node)
	if config.Iface, _, err = getIfaceByIP(encapIP); err != nil {
		klog.Errorf("failed to get interface by IP %s: %v", encapIP, err)
		return err
	}
	return setEncapIP(encapIP)
}

func (config *Configuration) getEncapIP(node *corev1.Node) string {
	if podIP := os.Getenv(util.PodIP); podIP != "" {
		return podIP
	}

	klog.Info("environment variable POD_IP not found, fall back to node address")
	ipv4, ipv6 := util.GetNodeInternalIP(*node)
	if ipv4 != "" {
		return ipv4
	}
	return ipv6
}

func getIfaceByIP(ip string) (string, int, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return "", 0, err
	}

	for _, link := range links {
		addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return "", 0, fmt.Errorf("failed to get addresses of link %s: %v", link.Attrs().Name, err)
		}
		for _, addr := range addrs {
			if addr.IPNet.Contains(net.ParseIP(ip)) && addr.IP.String() == ip {
				return link.Attrs().Name, link.Attrs().MTU, nil
			}
		}
	}

	return "", 0, fmt.Errorf("failed to find interface by address %s", ip)
}
