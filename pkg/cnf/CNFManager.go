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
	agentSpec      tunnel.Specification
	localK8sClient *kubernetes.Clientset
	hubConfig      *rest.Config
	hubClient      *fleetboardClientset.Clientset
	wireguard      *tunnel.Wireguard
	leaderLock     *resourcelock.LeaseLock
	// current leader name of cnf daemon-set
	currentLeader             string
	innerTunnelControllerOnce sync.Once
	innerTunnelController     *tunnelcontroller.InnerClusterTunnelController
	interTunnelController     *tunnelcontroller.InterClusterTunnelController
	serviceSyncer             *syncer.Syncer
}

func (m *Manager) Run(ctx context.Context) error {
	// only cnf pod in master of control plane will be a candidate.
	isCandidate := utils.CheckIfMasterOrControlNode(m.localK8sClient, m.agentSpec.NodeName)
	if m.agentSpec.AsCluster {
		if isCandidate {
			go m.startLeaderElection(m.leaderLock, ctx)
		} else {
			go m.innerTunnelController.Start(ctx)
		}
		m.dedinicEngine(ctx)
	} else {
		m.startLeaderElection(m.leaderLock, ctx)
	}
	return nil
}

func (m *Manager) dedinicEngine(ctx context.Context) {
	dedinic.CNFPodName = os.Getenv(known.EnvPodName)
	if dedinic.CNFPodName == "" {
		klog.Fatalf("get self pod name failed")
	}
	dedinic.CNFPodNamespace = os.Getenv(known.EnvPodNamespace)
	if dedinic.CNFPodNamespace == "" {
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

	klog.Info("start cnf dedicated plugin run")
	dedinic.InitNRIPlugin(m.localK8sClient)
}

// NewCNFManager returns a new CNFController.
func NewCNFManager(opts *tunnel.Options) (*Manager, error) {
	localConfig, err := clientcmd.BuildConfigFromFlags("", opts.ClientConnection.Kubeconfig)
	if err != nil {
		return nil, err
	}

	var agentSpec tunnel.Specification
	if err = envconfig.Process(known.FleetboardPrefix, &agentSpec); err != nil {
		return nil, err
	}
	agentSpec.Options = *opts
	klog.Infof("got config info %v", agentSpec)

	localK8sClient := kubernetes.NewForConfigOrDie(localConfig)

	// create and init wire guard device
	w, err := tunnel.CreateAndUpTunnel(localK8sClient, &agentSpec)
	if err != nil {
		klog.Fatalf("can't init wire guard tunnel: %v", err)
	}

	// 配置 Leader 选举
	// TODO leader pod location with filter.
	leaderLock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "cnf-leader-election",
			Namespace: known.FleetboardSystemNamespace,
		},
		Client: localK8sClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: agentSpec.PodName,
		},
	}

	innerTunnelController, err := tunnelcontroller.NewInnerClusterTunnelController(w, localK8sClient)
	if err != nil {
		klog.Fatalf("get inner cluster tunnel controller failed: %v", err)
	}

	var hubConfig *rest.Config
	if agentSpec.AsCluster {
		// wait until secret is ready.
		hubConfig, err = syncerConfig.GetHubConfig(localK8sClient, &agentSpec)
		if err != nil {
			klog.Fatalf("get hub kubeconfig failed: %v", err)
		}
	} else {
		hubConfig = localConfig
	}
	hubK8sClient, err := fleetboardClientset.NewForConfig(hubConfig)
	if err != nil {
		klog.Fatalf("get hub fleetboard client failed: %v", err)
		return nil, err
	}

	hubInformerFactory := fleetinformers.NewSharedInformerFactoryWithOptions(hubK8sClient, known.DefaultResync,
		fleetinformers.WithNamespace(agentSpec.ShareNamespace))
	interTunnelController, err := tunnelcontroller.NewInterClusterTunnelController(&agentSpec, localK8sClient, w,
		hubK8sClient, hubInformerFactory)
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

	serviceSyncer, errSyncer := syncer.New(&agentSpec, known.SyncerConfig{
		LocalRestConfig: localConfig,
		LocalClient:     dynamicLocalClient,
		LocalNamespace:  agentSpec.LocalNamespace,
		LocalClusterID:  agentSpec.ClusterID,
	}, hubConfig)
	if errSyncer != nil {
		klog.Fatalf("Failed to create syncer agent: %v", errSyncer)
		return nil, errSyncer
	}

	manager := &Manager{
		agentSpec:             agentSpec,
		wireguard:             w,
		localK8sClient:        localK8sClient,
		hubConfig:             hubConfig,
		hubClient:             hubK8sClient,
		leaderLock:            leaderLock,
		currentLeader:         "",
		innerTunnelController: innerTunnelController,
		interTunnelController: interTunnelController,
		serviceSyncer:         serviceSyncer,
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

				m.innerTunnelController.RecycleAllResources()
				m.currentLeader = m.wireguard.Spec.PodName
				m.innerTunnelController.SetCurrentLeader(m.currentLeader)

				m.interTunnelController.Start(ctx)
				if m.agentSpec.AsCluster {
					go func() {
						if syncerStartErr := m.serviceSyncer.Start(ctx); syncerStartErr != nil {
							klog.Fatalf("Failed to start syncer agent: %v", syncerStartErr)
						}
					}()
					if errConfig := m.innerTunnelController.ConfigWithExistingCIDR(m.hubClient); errConfig != nil {
						klog.Fatalf("failed to config annotation: %v", errConfig)
					}
					m.innerTunnelController.EnqueueExistingAdditionalInnerConnectionHandle()
					m.innerTunnelControllerOnce.Do(func() {
						go m.innerTunnelController.Start(ctx)
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

				m.interTunnelController.RecycleAllResources()
				m.innerTunnelController.RecycleAllResources()

				m.currentLeader = identity
				m.innerTunnelController.SetCurrentLeader(m.currentLeader)
				utils.UpdatePodLabels(m.localK8sClient, m.wireguard.Spec.PodName, false)

				if m.agentSpec.AsCluster {
					m.innerTunnelController.EnqueueAdditionalInnerConnectionHandleObj(identity)
					m.innerTunnelControllerOnce.Do(func() {
						go m.innerTunnelController.Start(ctx)
					})
				}
			},
		},
		ReleaseOnCancel: true,
		Name:            "cnf-leader-election",
	})
}

func waitForCIDRReady(ctx context.Context, k8sClient *kubernetes.Clientset) {
	for dedinic.NodeCIDR == "" || dedinic.TunnelCIDR == "" || dedinic.CNFPodIP == "" || dedinic.ServiceCIDR == "" {
		pod, err := k8sClient.CoreV1().Pods(dedinic.CNFPodNamespace).Get(ctx, dedinic.CNFPodName, metav1.GetOptions{})
		if err == nil && pod != nil {
			klog.Infof("wait for cnf cidr ready: cnf annotions: %v", pod.Annotations)
			dedinic.NodeCIDR = pod.Annotations[fmt.Sprintf(known.FleetboardNodeCIDR, known.FleetboardPrefix)]
			dedinic.TunnelCIDR = pod.Annotations[fmt.Sprintf(known.FleetboardTunnelCIDR, known.FleetboardPrefix)]
			dedinic.ServiceCIDR = pod.Annotations[fmt.Sprintf(known.FleetboardServiceCIDR, known.FleetboardPrefix)]
			dedinic.CNFPodIP = pod.Status.PodIP
		} else {
			klog.Errorf("wait for cnf cidr ready: not finding the cnf pod")
		}
		<-time.After(5 * time.Second)
	}
	klog.Infof("cnf cidr ready, nodecidr: %v, globalcidr: %v, cnfpodip: %v, innerclusteripcidr: %v",
		dedinic.NodeCIDR, dedinic.TunnelCIDR, dedinic.CNFPodIP, dedinic.ServiceCIDR)
}
