package tunnel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listenrv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/dixudx/yacht"
	"github.com/fleetboard-io/fleetboard/pkg/config"
	fleetboardClientset "github.com/fleetboard-io/fleetboard/pkg/generated/clientset/versioned"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/pkg/tunnel"
	"github.com/fleetboard-io/fleetboard/utils"
)

type InnerClusterTunnelController struct {
	yachtController     *yacht.Controller
	podLister           listenrv1.PodLister
	kubeInformerFactory informers.SharedInformerFactory
	wireguard           *tunnel.Wireguard
	podSynced           cache.InformerSynced
	existingCIDR        []string
	clusterCIDR         string
	globalCIDR          string
	kubeClientSet       kubernetes.Interface
	currentLeader       string
	sync.RWMutex
}

func NewInnerClusterTunnelController(w *tunnel.Wireguard,
	kubeClientSet kubernetes.Interface) (*InnerClusterTunnelController, error) {
	// only fleetboard system namespace pod is responsible for wire guard
	k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(kubeClientSet, 10*time.Minute,
		informers.WithNamespace(known.FleetboardSystemNamespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = known.RouterCNFCreatedByLabel
		}),
	)
	podInformer := k8sInformerFactory.Core().V1().Pods()

	ictController := &InnerClusterTunnelController{
		wireguard:           w,
		kubeInformerFactory: k8sInformerFactory,
		podLister:           podInformer.Lister(),
		podSynced:           podInformer.Informer().HasSynced,
		kubeClientSet:       kubeClientSet,
		currentLeader:       "",
	}
	podController := yacht.NewController("daemon pod for inner cluster tunnel connection").
		WithCacheSynced(podInformer.Informer().HasSynced).
		WithHandlerFunc(ictController.Handle).WithEnqueueFilterFunc(func(oldObj, newObj interface{}) (bool, error) {
		var newPod *v1.Pod
		// delete event
		if newObj == nil {
			pod := oldObj.(*v1.Pod)
			return ictController.ShouldHandlerPod(pod), nil
		} else {
			newPod = newObj.(*v1.Pod)
			shouldHandle := ictController.ShouldHandlerPod(newPod)
			publicKey := utils.GetSpecificAnnotation(newPod, known.PublicKey)
			if shouldHandle && utils.IsRunningAndHasIP(newPod) && len(publicKey) != 0 {
				return true, nil
			}
		}
		return false, nil
	})
	_, err := podInformer.Informer().AddEventHandler(podController.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	ictController.yachtController = podController

	return ictController, nil
}

func (ict *InnerClusterTunnelController) EnqueueAdditionalInnerConnectionHandleObj(podName string) {
	if pod, err := ict.kubeClientSet.CoreV1().Pods(known.FleetboardSystemNamespace).
		Get(context.TODO(), podName, metav1.GetOptions{}); err == nil {
		ict.yachtController.Enqueue(pod)
	} else {
		klog.Errorf("can't get pod %s from this cluster", podName)
	}
}

func (ict *InnerClusterTunnelController) EnqueueExistingAdditionalInnerConnectionHandle() {
	if podList, err := ict.kubeClientSet.CoreV1().Pods(known.FleetboardSystemNamespace).
		List(context.TODO(), metav1.ListOptions{
			LabelSelector: known.LabelCNFPod,
		}); err == nil {
		for _, podItem := range podList.Items {
			pod := podItem
			ict.yachtController.Enqueue(&pod)
		}
	} else {
		klog.Errorf("can't get existing pod from this cluster")
	}
}

func (ict *InnerClusterTunnelController) SpawnNewCIDRForNRIPod(pod *v1.Pod) (string, error) {
	ict.Lock()
	defer ict.Unlock()
	existingCIDR := ict.existingCIDR
	secondaryCIDR, allocateError := utils.FindClusterAvailableCIDR(ict.clusterCIDR, existingCIDR)
	if allocateError != nil {
		klog.Errorf("allocate from %s with error %v", existingCIDR, allocateError)
		return "", allocateError
	}

	klog.Infof("pod get a cidr from %s with %s", existingCIDR, secondaryCIDR)
	if err := utils.SetSpecificAnnotations(ict.kubeClientSet, pod,
		[]string{known.FleetboardTunnelCIDR, known.FleetboardNodeCIDR},
		[]string{ict.globalCIDR, secondaryCIDR}, true); err != nil {
		klog.Errorf("set pod annotation with error %v", err)
		return "", err
	}

	ict.existingCIDR = append(ict.existingCIDR, secondaryCIDR)
	return secondaryCIDR, nil
}

func (ict *InnerClusterTunnelController) Handle(podKey interface{}) (*time.Duration, error) {
	requestAfter := 2 * time.Second
	isLeader := false
	// it may change when leader changed.
	isLeader = ict.wireguard.Spec.PodName == ict.currentLeader
	// get pod info
	key := podKey.(string)
	namespace, podName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, err
	}
	pod, err := ict.podLister.Pods(namespace).Get(podName)
	if err != nil {
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("pods '%s' no longer exists, we must have handled deletion event", key))
			return nil, nil
		}
		return nil, err
	}
	daemonConfig := tunnel.DaemonConfigFromPod(pod, isLeader)
	klog.Infof("inner cluster tunnel controller handle pod: %+v", daemonConfig)
	// pod is been deleting
	if !utils.IsPodAlive(pod) {
		klog.Infof("pod %s is not alive", key)
		// recycle related resources.
		if err := ict.recycleResources(daemonConfig); err != nil {
			return &requestAfter, err
		}
		return nil, nil
	}
	// empty cidr on cnf pod annotation
	if len(daemonConfig.SecondaryCIDR) == 0 {
		// in cnf pod, we can allocate
		if isLeader {
			// prepare subnet in cluster
			klog.Infof("pod %s has no secondary cidr, allocating", key)
			cidr, err := ict.SpawnNewCIDRForNRIPod(pod)
			if err != nil {
				klog.Errorf("allocate pod %s secondary cidr failed: %v, retrying", key, err)
				return &requestAfter, err
			}
			klog.Infof("allocate pod %s secondary cidr successfully", key)
			if daemonConfig.PodID == ict.wireguard.Spec.PodName {
				// coming pod is leader, only allocate cidr no need to establish tunnels.
				return nil, nil
			}
			daemonConfig.SecondaryCIDR = []string{cidr}
		} else {
			// in cnf pod, wait next time
			return &requestAfter, nil
		}
	}
	if len(daemonConfig.ServiceCIDR) == 0 {
		if isLeader {
			klog.Infof("pod %s has no service cidr, getting", key)
			cidr, err := utils.GetServiceCIDRFromCNFPod(ict.kubeClientSet)
			if err != nil || len(cidr) == 0 {
				klog.Errorf("get service cidr failed: %v, retrying", err)
				return &requestAfter, err
			}
			if err := utils.SetSpecificAnnotations(ict.kubeClientSet, pod,
				[]string{known.FleetboardServiceCIDR}, []string{cidr}, true); err != nil {
				// must patch virtual service annotation ahead for cnf pod to start
				klog.Errorf("set service cidr annotation failed: %v, retrying", err)
				return &requestAfter, err
			}
			klog.Infof("set pod %s service cidr %s successfully", key, cidr)
			daemonConfig.ServiceCIDR = []string{cidr}
		} else {
			// in cnf pod, wait next time
			return &requestAfter, nil
		}
	}
	// itself shouldn't add tunnel connection with itself.
	if ict.wireguard.Spec.PodName == podName {
		klog.Infof("pod %s is itself, skip", key)
		return nil, nil
	}

	if errAddInnerTunnel := ict.wireguard.AddInnerClusterTunnel(daemonConfig); errAddInnerTunnel != nil {
		klog.Errorf("add inner cluster tunnel failed: %v, retrying", errAddInnerTunnel)
		return &requestAfter, errAddInnerTunnel
	}
	klog.Infof("pod %s inner cluster tunnel has been added successfully", key)

	// add route for target inner cluster tunnel pod
	if errRoute := configHostRoutingRules(daemonConfig.SecondaryCIDR, known.Add); errRoute != nil {
		klog.Infof("add route inner cluster in cnf failed for %s, with error %v",
			daemonConfig.SecondaryCIDR, errRoute)
		return &requestAfter, errRoute
	}
	return nil, nil
}

func (ict *InnerClusterTunnelController) RecycleAllResources() {
	for _, innerConnection := range ict.wireguard.GetAllExistingInnerConnection() {
		if err := ict.recycleResources(innerConnection); err != nil {
			klog.Errorf("can't remove this inner connections %s", innerConnection.NodeID)
		}
	}
}

func (ict *InnerClusterTunnelController) recycleResources(podConfig *tunnel.DaemonCNFTunnelConfig) error {
	// check if we have had a tunnel for it.
	_, found := ict.wireguard.GetExistingInnerConnection(podConfig.NodeID)
	if !found {
		// do nothing if we have not established any tunnel for this node
		return nil
	}
	publicKey := podConfig.PublicKey
	if oldKey, err := wgtypes.ParseKey(publicKey[0]); err != nil {
		klog.Infof("Can't parse key for %s with key %s", podConfig.PodID, publicKey)
		return err
	} else {
		ict.Lock()
		ict.wireguard.DeleteExistingInnerConnection(podConfig.NodeID)
		utils.RemoveString(ict.existingCIDR, podConfig.SecondaryCIDR[0])
		ict.Unlock()
		removeTunnelError := ict.wireguard.RemoveInnerClusterTunnel(&oldKey)
		if removeTunnelError != nil {
			klog.Infof("failed to remove tunnel for %s on node %s", podConfig.PodID, podConfig.NodeID)
			return removeTunnelError
		}
		if errRemoveRoute := configHostRoutingRules(podConfig.SecondaryCIDR, known.Delete); errRemoveRoute != nil {
			klog.Infof("delete route failed for %v", errRemoveRoute)
			return errRemoveRoute
		}
	}
	return nil
}

func (ict *InnerClusterTunnelController) Start(ctx context.Context) {
	defer runtime.HandleCrash()
	ict.kubeInformerFactory.Start(ctx.Done())
	klog.Info("Starting inner cluster tunnel controller...")
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		ict.yachtController.Run(ctx)
	}, time.Duration(0))
}

func (ict *InnerClusterTunnelController) ShouldHandlerPod(pod *v1.Pod) bool {
	var myPodName = ict.wireguard.Spec.PodName
	var currentLeader = ict.GetCurrentLeader()
	if currentLeader == "" {
		if pod.Labels[known.LeaderCNFLabelKey] == "true" {
			ict.SetCurrentLeader(pod.Name)
			currentLeader = pod.Name
		} else {
			return false
		}
	} else {
		if pod.Labels[known.LeaderCNFLabelKey] == "true" && currentLeader != pod.Name {
			// leader changed
			currentLeader = pod.Name
			klog.Infof("leader has changed, recycle all tunnels and reconnect to new leader")
			ict.RecycleAllResources()
		}
	}

	// I am a leader, establish tunnel with non-leaders
	if myPodName == currentLeader {
		return true
	}
	// ignore myself
	if pod.Name == myPodName || currentLeader == "" {
		return false
	}
	return pod.Name == currentLeader
}

// ConfigWithExistingCIDR  only need invoke on cnf pod
func (ict *InnerClusterTunnelController) ConfigWithExistingCIDR(oClient *fleetboardClientset.Clientset) error {
	existingCIDR, clusterCIDR, globalCIDR, err := getInnerClusterExistingCIDR(ict.kubeClientSet,
		oClient, ict.wireguard.Spec)
	if err != nil {
		klog.Errorf("can't get or set annotation with existing cidr and global or cluster cidr")
		return err
	}
	ict.existingCIDR = existingCIDR
	ict.clusterCIDR = clusterCIDR
	ict.globalCIDR = globalCIDR
	return nil
}

func getInnerClusterExistingCIDR(k8sClient kubernetes.Interface, clientset *fleetboardClientset.Clientset,
	spec *tunnel.Specification) ([]string, string, string, error) {
	existingCIDR := make([]string, 0)
	globalCIDR, clusterCIDR := config.WaitGetCIDRFromHubclient(clientset, spec)
	if err := utils.AddAnnotationToSelf(k8sClient, known.FleetboardTunnelCIDR, globalCIDR, true); err != nil {
		return existingCIDR, "", "", err
	}
	if err := utils.AddAnnotationToSelf(k8sClient, known.FleetboardClusterCIDR, clusterCIDR, true); err != nil {
		return existingCIDR, "", "", err
	}
	if podList, errListPod := k8sClient.CoreV1().Pods(known.FleetboardSystemNamespace).List(context.TODO(),
		metav1.ListOptions{LabelSelector: known.RouterCNFCreatedByLabel}); errListPod == nil {
		for _, existingPod := range podList.Items {
			pod := existingPod
			cidr := utils.GetSpecificAnnotation(&pod, known.FleetboardNodeCIDR)
			if len(cidr) != 0 {
				existingCIDR = append(existingCIDR, cidr[0])
			}
		}
	} else {
		klog.Errorf("list all cnf pod error with %v", errListPod)
		return existingCIDR, "", "", errListPod
	}

	return existingCIDR, clusterCIDR, globalCIDR, nil
}

func (ict *InnerClusterTunnelController) SetCurrentLeader(leader string) {
	ict.Lock()
	defer ict.Unlock()
	ict.currentLeader = leader
}

func (ict *InnerClusterTunnelController) GetCurrentLeader() string {
	ict.RLock()
	defer ict.RUnlock()
	return ict.currentLeader
}
