package main

import (
	"context"
	"flag"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/kelseyhightower/envconfig"
	"github.com/nauti-io/nauti/pkg/api"
	"github.com/nauti-io/nauti/pkg/config"
	"github.com/nauti-io/nauti/pkg/controller/pod"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/nauti-io/nauti/pkg/subnet"
	"github.com/nauti-io/nauti/pkg/util"
)

var (
	masterURL  string
	kubeConfig string
)

func main() {
	// set up signals, so we handle the first shutdown signal gracefully
	ctx := signals.SetupSignalHandler()
	klog.InitFlags(nil)

	flag.Parse()
	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	kubeClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	agentSpec := known.Specification{}
	if err = envconfig.Process("IPAM", &agentSpec); err != nil {
		klog.Infof("got config info %v", agentSpec)
		klog.Fatal(err)
	}
	// wait happens
	CIDR, globalCIDR := config.WaitGetGlobalNetworkInfo(kubeClientSet, &agentSpec)
	gateway, err := util.GetIndexIPFromCIDR(CIDR, 1)
	if err != nil {
		klog.Fatalf("invalid gateway of cidr", err.Error())
	}
	cnfPodIP, err := util.GetIndexIPFromCIDR(CIDR, 2)
	if err != nil {
		klog.Fatalf("invalid second ip of cidr", err.Error())
	}
	// config it.
	defaultSubnet := &api.SubnetSpec{
		Name:       "default",
		Default:    true,
		CIDRBlock:  CIDR,
		GlobalCIDR: globalCIDR,
		Gateway:    gateway,
		ExcludeIps: []string{gateway, cnfPodIP},
		Provider:   "default",
	}

	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}
	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClientSet, time.Hour*12)

	nbClient, err := subnet.InitDefaultLogicSwitch(defaultSubnet)
	if err != nil {
		klog.Fatalf("Failed to init default logic switch: %v", err)
	}

	poController, err := pod.NewPodController(kubeInformerFactory.Core().V1().Pods(), kubeClientSet, defaultSubnet,
		kubeInformerFactory, nbClient)
	if err != nil {
		klog.Fatal(err.Error())
	}
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := poController.Run(ctx); err != nil {
			klog.Error(err)
		}
	}, time.Duration(0))

	<-ctx.Done()

	klog.Info("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
