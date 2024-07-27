package cnf

import (
	"context"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	syncerConfig "github.com/nauti-io/nauti/pkg/config"
	"github.com/nauti-io/nauti/pkg/controller/syncer"
	tunnelcontroller "github.com/nauti-io/nauti/pkg/controller/tunnel"
	octopusClientset "github.com/nauti-io/nauti/pkg/generated/clientset/versioned"
	kubeinformers "github.com/nauti-io/nauti/pkg/generated/informers/externalversions"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/nauti-io/nauti/pkg/tunnel"
	"github.com/nauti-io/nauti/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

// Manager defines configuration for cnf-related controllers
type Manager struct {
	agentSpec                 tunnel.Specification
	localK8sClient            *kubernetes.Clientset
	wireguard                 *tunnel.Wireguard
	innerConnectionController *tunnelcontroller.InnerClusterTunnelController
	leaderLock                *resourcelock.LeaseLock
	// current leader name of cnf daemon-set
	currentLeader       string
	innerControllerOnce sync.Once
	hubKubeConfig       *rest.Config
	octoClient          *octopusClientset.Clientset
	interController     *tunnelcontroller.PeerController
	syncerAgent         *syncer.Syncer
}

func (m *Manager) Run(ctx context.Context) error {
	m.startLeaderElection(m.leaderLock, ctx)
	return nil
}

// NewCNFManager returns a new CNFController.
func NewCNFManager(opts *tunnel.Options) (*Manager, error) {
	localConfig, err := clientcmd.BuildConfigFromFlags("", opts.ClientConnection.Kubeconfig)
	if err != nil {
		return nil, err
	}
	agentSpec := tunnel.Specification{}
	var w *tunnel.Wireguard
	var hubKubeConfig *rest.Config
	var octoClient *octopusClientset.Clientset
	if err = envconfig.Process(known.NautiPrefix, &agentSpec); err != nil {
		return nil, err
	}
	agentSpec.Options = *opts
	klog.Infof("got config info %v", agentSpec)
	localK8sClient := kubernetes.NewForConfigOrDie(localConfig)
	// create and init wire guard device
	w, err = tunnel.CreateAndUpTunnel(localK8sClient, agentSpec)
	if err != nil {
		klog.Fatalf("can't init wire guard tunnel: %v", err)
	}
	// 配置 Leader 选举
	// TODO leader pod location with filter.
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "cnf-leader-election",
			Namespace: known.NautiSystemNamespace,
		},
		Client: localK8sClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: agentSpec.PodName,
		},
	}

	innerController, err := tunnelcontroller.NewInnerClusterTunnelController(w, localK8sClient)
	if err != nil {
		klog.Fatalf("get inner cluster tunnel controller failed: %v", err)
	}
	if !agentSpec.AsHub {
		// wait until secret is ready.
		hubKubeConfig, err = syncerConfig.GetHubConfig(localK8sClient, &agentSpec)
		if err != nil {
			klog.Fatalf("get hub kubeconfig failed: %v", err)
		}
	} else {
		hubKubeConfig = localConfig
	}
	if octoClient, err = octopusClientset.NewForConfig(hubKubeConfig); err != nil {
		klog.Fatalf("get hub octopus client failed: %v", err)
		return nil, err
	}
	hubInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(octoClient, known.DefaultResync,
		kubeinformers.WithNamespace(agentSpec.ShareNamespace))
	interController, err := tunnelcontroller.NewPeerController(agentSpec, localK8sClient, w,
		octoClient, hubInformerFactory)
	if err != nil {
		klog.Fatalf("start peer controller failed: %v", err)
	}

	if errScheme := mcsv1a1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exitf("error adding multi-cluster v1alpha1 to the scheme: %v", errScheme)
	}
	dynamicLocalClient, dynamicError := dynamic.NewForConfig(localConfig)
	if dynamicError != nil {
		klog.Fatalf("error creating dynamic client: %v", err)
	}

	syncerAgent, errSyncerController := syncer.New(&agentSpec, known.SyncerConfig{
		LocalRestConfig: localConfig,
		LocalClient:     dynamicLocalClient,
		LocalNamespace:  agentSpec.LocalNamespace,
		LocalClusterID:  agentSpec.ClusterID,
	}, hubKubeConfig)
	if errSyncerController != nil {
		klog.Fatalf("Failed to create syncer agent: %v", errSyncerController)
		return nil, errSyncerController
	}

	manager := &Manager{
		agentSpec:                 agentSpec,
		wireguard:                 w,
		localK8sClient:            localK8sClient,
		innerConnectionController: innerController,
		leaderLock:                lock,
		currentLeader:             "",
		hubKubeConfig:             hubKubeConfig,
		octoClient:                octoClient,
		interController:           interController,
		syncerAgent:               syncerAgent,
	}
	return manager, nil
}

func (m *Manager) startLeaderElection(lock resourcelock.Interface, ctx context.Context) {
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				klog.Infof("I am the leader: %s", m.wireguard.Spec.PodName)
				m.innerConnectionController.RecycleAllResources()
				m.currentLeader = m.wireguard.Spec.PodName
				m.innerConnectionController.SetCurrentLeader(m.currentLeader)
				m.interController.Start(ctx)

				if m.agentSpec.AsCluster {
					go func() {
						if syncerStartErr := m.syncerAgent.Start(ctx); syncerStartErr != nil {
							klog.Fatalf("Failed to start syncer agent: %v", syncerStartErr)
						}
					}()
					if errConfig := m.innerConnectionController.ConfigWithExistingCIDR(m.octoClient); errConfig != nil {
						klog.Fatalf("failed to config annotation: %v", errConfig)
					}
					m.innerConnectionController.EnqueueExistingAdditionalInnerConnectionHandle()
					m.innerControllerOnce.Do(func() {
						go m.innerConnectionController.Start(ctx)
					})
				}
			},
			OnStoppedLeading: func() {
				klog.Infof("I am no longer the leader: %s", m.wireguard.Spec.PodName)
				m.currentLeader = ""
			},
			OnNewLeader: func(identity string) {
				if identity == m.wireguard.Spec.PodName {
					// already handled, so ignore.
					return
				}
				klog.Infof("New leader elected: %s", identity)
				m.interController.RecycleAllResources()
				m.innerConnectionController.RecycleAllResources()
				m.currentLeader = identity
				m.innerConnectionController.SetCurrentLeader(m.currentLeader)
				utils.UpdatePodLabels(m.localK8sClient, m.wireguard.Spec.PodName, false)

				if m.agentSpec.AsCluster {
					m.innerConnectionController.EnqueueAdditionalInnerConnectionHandleObj(identity)
					m.innerControllerOnce.Do(func() {
						go m.innerConnectionController.Start(ctx)
					})
				}
			},
		},
		ReleaseOnCancel: true,
		Name:            "cnf-leader-election",
	})
}
