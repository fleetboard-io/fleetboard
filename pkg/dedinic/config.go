package dedinic

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
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
