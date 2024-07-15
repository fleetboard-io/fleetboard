package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/nauti-io/nauti/pkg/controller"
	"github.com/nauti-io/nauti/pkg/dedinic"
	"github.com/nauti-io/nauti/pkg/known"
)

var (
	localMasterURL  string
	localKubeconfig string
)

func init() {
	flag.StringVar(&localKubeconfig, "kubeconfig", "",
		"Path to kubeconfig of local cluster. Only required if out-of-cluster.")
	flag.StringVar(&localMasterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func main() {
	flag.Parse()
	ctx := signals.SetupSignalHandler()
	restConfig, err := clientcmd.BuildConfigFromFlags(localMasterURL, localKubeconfig)
	if err != nil {
		klog.Fatal(err)
		return
	}
	k8sClient, clientErr := kubernetes.NewForConfig(restConfig)
	if clientErr != nil {
		klog.Fatalf("error creating dynamic client: %v", clientErr)
	}

	dedinic.CNFPodName = os.Getenv("POD_NAME")
	if dedinic.CNFPodName == "" {
		klog.Fatalf("get self pod name failed")
	}
	dedinic.CNFPodNamespace = os.Getenv("POD_NAMESPACE")
	if dedinic.CNFPodName == "" {
		klog.Fatalf("get self pod namespace failed")
	}

	// init wire-guard device
	w, err := controller.NewTunnel(k8sClient, nil, nil, ctx.Done())
	if err != nil {
		klog.Fatal(err)
		return
	}
	// up the interface.
	if w.Init() != nil {
		klog.Fatal(err)
		return
	}
	innerClusterController, errCreateError := controller.NewInnerClusterTunnelController(w, k8sClient,
		known.RouterCNFCreatedByLabel)
	if errCreateError != nil {
		klog.Fatalf("start inner cluster tunnel controller failed: %v", errCreateError)
	}
	innerClusterController.Start(ctx)

	waitForCIDRReady(ctx, k8sClient)
	go dedinic.InitDelayQueue()
	go dedinic.InitNRIPlugin()

	// todo if nri is invalid
	<-time.After(5 * time.Second)
	// add bridge
	err = dedinic.CreateBridge(dedinic.CNFBridgeName)
	if err != nil {
		klog.Fatalf("create nauti bridge failed: %v", err)
	}

	klog.Info("start nri dedicated plugin run")

	<-ctx.Done()
	// remove your self from hub.
	if err := w.Cleanup(); err != nil {
		klog.Error(nil, "Error cleaning up resources before removing peer")
	}
}

func waitForCIDRReady(ctx context.Context, k8sClient *kubernetes.Clientset) {
	klog.Infof("wait for cidr ready")
	for dedinic.NodeCIDR == "" || dedinic.GlobalCIDR == "" || dedinic.CNFPodIP == "" {
		pod, err := k8sClient.CoreV1().Pods(dedinic.CNFPodNamespace).Get(ctx, dedinic.CNFPodName, v1.GetOptions{})
		if err == nil && pod != nil {
			klog.Infof("cnf pod: %v", pod)
			dedinic.NodeCIDR = pod.Annotations[fmt.Sprintf(known.DaemonCIDR, known.NautiPrefix)]
			dedinic.GlobalCIDR = pod.Annotations[fmt.Sprintf(known.CNFCIDR, known.NautiPrefix)]
			dedinic.CNFPodIP = pod.Status.PodIP
		} else {
			klog.Errorf("have not find the cnf pod")
		}
		<-time.After(5 * time.Second)
	}
	klog.Infof("cnf cidr ready, nodecidr: %v, globalcidr: %v, cnfpodip: %v",
		dedinic.NodeCIDR, dedinic.GlobalCIDR, dedinic.CNFPodIP)
}
