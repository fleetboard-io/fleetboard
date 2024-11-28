package cnf

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

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

	syncerConfig "github.com/fleetboard-io/fleetboard/pkg/config"
	"github.com/fleetboard-io/fleetboard/pkg/controller/syncer"
	tunnelcontroller "github.com/fleetboard-io/fleetboard/pkg/controller/tunnel"
	"github.com/fleetboard-io/fleetboard/pkg/dedinic"
	fleetboardClientset "github.com/fleetboard-io/fleetboard/pkg/generated/clientset/versioned"
	fleetinformers "github.com/fleetboard-io/fleetboard/pkg/generated/informers/externalversions"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/pkg/tunnel"
	"github.com/fleetboard-io/fleetboard/utils"
	"github.com/kelseyhightower/envconfig"
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
	octoClient          *fleetboardClientset.Clientset
	interController     *tunnelcontroller.PeerController
	syncerAgent         *syncer.Syncer
}

func (m *Manager) Run(ctx context.Context) error {
	// only cnf pod in master of control plane will be a candidate.
	isCandidate := utils.CheckIfMasterOrControlNode(m.localK8sClient, m.agentSpec.NodeName)
	if !m.agentSpec.AsHub {
		if isCandidate {
			go m.startLeaderElection(m.leaderLock, ctx)
		} else {
			go m.innerConnectionController.Start(ctx)
		}
		m.dedinicEngine(ctx)
	} else {
		m.startLeaderElection(m.leaderLock, ctx)
	}
	return nil
}

func (m *Manager) dedinicEngine(ctx context.Context) {
	dedinic.CNFPodName = os.Getenv("FLEETBOARD_PODNAME")
	if dedinic.CNFPodName == "" {
		klog.Fatalf("get self pod name failed")
	}
	dedinic.CNFPodNamespace = os.Getenv("FLEETBOARD_PODNAMESPACE")
	if dedinic.CNFPodName == "" {
		klog.Fatalf("get self pod namespace failed")
	}
	waitForCIDRReady(ctx, m.localK8sClient)
	// todo if nri is invalid
	<-time.After(5 * time.Second)
	// add bridge
	err := dedinic.CreateBridge(dedinic.CNFBridgeName)
	if err != nil {
		klog.Fatalf("create fleetboard bridge failed: %v", err)
	}

	klog.Info("start nri dedicated plugin run")
	dedinic.InitNRIPlugin(m.localK8sClient)
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
	var octoClient *fleetboardClientset.Clientset
	if err = envconfig.Process(known.FleetboardPrefix, &agentSpec); err != nil {
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
			Namespace: known.FleetboardSystemNamespace,
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
	if octoClient, err = fleetboardClientset.NewForConfig(hubKubeConfig); err != nil {
		klog.Fatalf("get hub fleetboard client failed: %v", err)
		return nil, err
	}
	hubInformerFactory := fleetinformers.NewSharedInformerFactoryWithOptions(octoClient, known.DefaultResync,
		fleetinformers.WithNamespace(agentSpec.ShareNamespace))
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

func waitForCIDRReady(ctx context.Context, k8sClient *kubernetes.Clientset) {
	klog.Infof("wait for cidr ready")
	for dedinic.NodeCIDR == "" || dedinic.GlobalCIDR == "" || dedinic.CNFPodIP == "" || dedinic.InnerClusterIPCIDR == "" {
		pod, err := k8sClient.CoreV1().Pods(dedinic.CNFPodNamespace).Get(ctx, dedinic.CNFPodName, metav1.GetOptions{})
		if err == nil && pod != nil {
			klog.Infof("cnf pod annotions: %v", pod.Annotations)
			dedinic.NodeCIDR = pod.Annotations[fmt.Sprintf(known.DaemonCIDR, known.FleetboardPrefix)]
			dedinic.GlobalCIDR = pod.Annotations[fmt.Sprintf(known.CNFCIDR, known.FleetboardPrefix)]
			dedinic.InnerClusterIPCIDR = pod.Annotations[fmt.Sprintf(known.InnerClusterIPCIDR, known.FleetboardPrefix)]
			dedinic.CNFPodIP = pod.Status.PodIP
		} else {
			klog.Errorf("have not find the cnf pod")
		}
		<-time.After(5 * time.Second)
	}
	klog.Infof("cnf cidr ready, nodecidr: %v, globalcidr: %v, cnfpodip: %v, innerclusteripcidr: %v",
		dedinic.NodeCIDR, dedinic.GlobalCIDR, dedinic.CNFPodIP, dedinic.InnerClusterIPCIDR)
}
