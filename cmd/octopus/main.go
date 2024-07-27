package main

import (
	"flag"
	"os"

	"github.com/nauti-io/nauti/pkg/apis/octopus.io/v1alpha1"
	"github.com/nauti-io/nauti/pkg/controller/syncer"
	tunnelcontroller "github.com/nauti-io/nauti/pkg/controller/tunnel"
	"github.com/nauti-io/nauti/pkg/tunnel"
	"github.com/nauti-io/nauti/utils"
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
	octopusClientset "github.com/nauti-io/nauti/pkg/generated/clientset/versioned"
	kubeinformers "github.com/nauti-io/nauti/pkg/generated/informers/externalversions"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/pkg/errors"
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
	var octoClient *octopusClientset.Clientset
	var hubKubeConfig *rest.Config

	agentSpec := tunnel.Specification{}
	restConfig, err := clientcmd.BuildConfigFromFlags(localMasterURL, localKubeconfig)
	if err != nil {
		klog.Fatal(err)
		return
	}
	// we will merge this repo into syncer, so user syncer prefix for now.
	if err = envconfig.Process(known.HubSecretName, &agentSpec); err != nil {
		klog.Infof("got config info %v", agentSpec)
		klog.Fatal(err)
		return
	}
	klog.Infof("got config info %v", agentSpec)
	klog.Infof("Arguments: %v", os.Args)

	k8sClient := kubernetes.NewForConfigOrDie(restConfig)

	if !agentSpec.AsHub {
		if errScheme := mcsv1a1.AddToScheme(scheme.Scheme); err != nil {
			klog.Exitf("error adding multi-cluster v1alpha1 to the scheme: %v", errScheme)
		}
		localClient, dynamicError := dynamic.NewForConfig(restConfig)
		if dynamicError != nil {
			klog.Fatalf("error creating dynamic client: %v", err)
		}
		// wait until secret is ready.
		hubKubeConfig, err = syncerConfig.GetHubConfig(k8sClient, &agentSpec)
		if err != nil {
			klog.Fatalf("get hub kubeconfig failed: %v", err)
		}

		// syncer only work as cluster level
		if agent, errSyncerController := syncer.New(&agentSpec, known.SyncerConfig{
			LocalRestConfig: restConfig,
			LocalClient:     localClient,
			LocalNamespace:  agentSpec.LocalNamespace,
			LocalClusterID:  agentSpec.ClusterID,
		}, hubKubeConfig); errSyncerController != nil {
			klog.Fatalf("Failed to create syncer agent: %v", errSyncerController)
		} else {
			go func() {
				if syncerStartErr := agent.Start(ctx); syncerStartErr != nil {
					klog.Fatalf("Failed to start syncer agent: %v", errSyncerController)
				}
			}()
		}
	} else {
		hubKubeConfig = restConfig
	}

	if octoClient, err = octopusClientset.NewForConfig(hubKubeConfig); err != nil {
		//
		return
	}
	w, err := InitGatewayTunnelConfig(k8sClient, octoClient, agentSpec)
	if err != nil {
		klog.Fatalf("Failed to start syncer agent: %v", err)
		return
	}
	hubInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(octoClient, known.DefaultResync,
		kubeinformers.WithNamespace(agentSpec.ShareNamespace))
	interController, err := tunnelcontroller.NewPeerController(agentSpec, k8sClient, w, octoClient, hubInformerFactory)
	if err != nil {
		klog.Fatalf("start peer controller failed: %v", err)
	}
	innerClusterController, errCreateError := tunnelcontroller.NewInnerClusterTunnelController(w, k8sClient)
	if errCreateError != nil {
		klog.Fatalf("start inner cluster tunnel controller failed: %v", errCreateError)
	}
	if errConfig := innerClusterController.ConfigWithExistingCIDR(octoClient); errConfig != nil {
		klog.Fatalf("failed to config annotation: %v", errConfig)
	}
	innerClusterController.Start(ctx)
	interController.Start(ctx)

	<-ctx.Done()

	// remove your self from hub.
	if err := w.Cleanup(); err != nil {
		klog.Error(nil, "Error cleaning up resources before removing peer")
	}

	klog.Info("All controllers stopped or exited. Stopping main loop")
}

func InitGatewayTunnelConfig(k8sClient *kubernetes.Clientset, oClient *octopusClientset.Clientset,
	agentSpec tunnel.Specification) (*tunnel.Wireguard, error) {
	w, err := tunnel.NewTunnel(&agentSpec)
	if err != nil {
		klog.Fatal(err)
		return nil, err
	}
	// up the interface.
	if errInit := w.Init(k8sClient); errInit != nil {
		klog.Fatal(errInit)
		return nil, errInit
	}
	peer := &v1alpha1.Peer{
		Spec: v1alpha1.PeerSpec{
			ClusterID: w.Spec.ClusterID,
			PodCIDR:   []string{w.Spec.CIDR},
			Endpoint:  w.Spec.Endpoint,
			IsHub:     w.Spec.AsHub,
			Port:      known.UDPPort,
			IsPublic:  len(w.Spec.Endpoint) != 0,
			PublicKey: w.Keys.PublicKey.String(),
		},
	}
	peer.Namespace = w.Spec.ShareNamespace
	peer.Name = w.Spec.ClusterID
	peerCreateErr := utils.ApplyPeerWithRetry(oClient, peer)
	if peerCreateErr != nil {
		return nil, errors.Wrap(peerCreateErr, "failed to create peer in hub")
	}
	return w, nil
}
