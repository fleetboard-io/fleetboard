package main

import (
	"flag"
	"os"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	"github.com/kelseyhightower/envconfig"
	syncerConfig "github.com/nauti-io/nauti/pkg/config"
	"github.com/nauti-io/nauti/pkg/controller"
	octopusClientset "github.com/nauti-io/nauti/pkg/generated/clientset/versioned"
	"github.com/nauti-io/nauti/pkg/generated/informers/externalversions"
	kubeinformers "github.com/nauti-io/nauti/pkg/generated/informers/externalversions"
	"github.com/nauti-io/nauti/pkg/known"
)

var (
	localMasterURL  string
	localKubeconfig string
)

func init() {
	flag.StringVar(&localKubeconfig, "kubeconfig", "", "Path to kubeconfig of local cluster. Only required if out-of-cluster.")
	flag.StringVar(&localMasterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func main() {
	flag.Parse()
	ctx := signals.SetupSignalHandler()
	var oClient *octopusClientset.Clientset
	var hubKubeConfig *rest.Config

	agentSpec := known.Specification{}
	restConfig, err := clientcmd.BuildConfigFromFlags(localMasterURL, localKubeconfig)
	if err != nil {
		klog.Fatal(err)
		return
	}
	// we will merge this repo into syncer, so user syncer prefix for now.
	if err = envconfig.Process(known.HubSecretName, &agentSpec); err != nil {
		klog.Infof("got config info %v", agentSpec)
		klog.Fatal(err)
	}
	klog.Infof("got config info %v", agentSpec)
	klog.Infof("Arguments: %v", os.Args)

	k8sClient, clientErr := kubernetes.NewForConfig(restConfig)
	if clientErr != nil {
		klog.Fatalf("error creating dynamic client: %v", clientErr)
	}

	if !agentSpec.IsHub {
		if errScheme := mcsv1a1.AddToScheme(scheme.Scheme); err != nil {
			klog.Exitf("error adding multi-cluster v1alpha1 to the scheme: %v", errScheme)
		}
		localClient, dynamicError := dynamic.NewForConfig(restConfig)
		if dynamicError != nil {
			klog.Fatalf("error creating dynamic client: %v", err)
		}
		// wait until secret is ready.
		hubKubeConfig, err = syncerConfig.GetHubConfig(k8sClient, &agentSpec)

		// syncer only work as cluster level
		if agent, err := controller.New(&agentSpec, known.SyncerConfig{
			LocalRestConfig: restConfig,
			LocalClient:     localClient,
			LocalNamespace:  agentSpec.LocalNamespace,
			LocalClusterID:  agentSpec.ClusterID,
		}, hubKubeConfig); err != nil {
			klog.Fatalf("Failed to create syncer agent: %v", err)
		} else {
			go func() {
				if syncerStartErr := agent.Start(ctx); syncerStartErr != nil {
					klog.Fatalf("Failed to start syncer agent: %v", err)
				}
			}()
		}
	} else {
		hubKubeConfig = restConfig
	}

	if oClient, err = octopusClientset.NewForConfig(hubKubeConfig); err != nil {
		//
		return
	}
	w, err := controller.NewTunnel(oClient, &agentSpec, ctx.Done())
	if err != nil {
		klog.Fatal(err)
		return
	}
	// up the interface.
	if w.Init() != nil {
		klog.Fatal(err)
		return
	}
	hubInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(oClient, known.DefaultResync, kubeinformers.WithNamespace(agentSpec.ShareNamespace))
	peerController, err := controller.NewPeerController(agentSpec, w, hubInformerFactory)
	peerController.Start(ctx)
	<-ctx.Done()

	// remove your self from hub.
	if err := w.Cleanup(); err != nil {
		klog.Error(nil, "Error cleaning up resources before removing peer")
	}

	klog.Info("All controllers stopped or exited. Stopping main loop")
}
