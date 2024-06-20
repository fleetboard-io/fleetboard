// ！！！！请不要让这个文件单独存在，它应该和dedinic合并
package main

import (
	"flag"

	"github.com/nauti-io/nauti/pkg/controller"
	"github.com/nauti-io/nauti/pkg/known"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
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

	<-ctx.Done()

	// remove your self from hub.
	if err := w.Cleanup(); err != nil {
		klog.Error(nil, "Error cleaning up resources before removing peer")
	}

}
