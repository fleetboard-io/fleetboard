package controller

import (
	"context"
	"fmt"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listenrv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/dixudx/yacht"
	"github.com/nauti-io/nauti/pkg/controller/utils"
	"github.com/nauti-io/nauti/pkg/generated/listers/octopus.io/v1alpha1"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/nauti-io/nauti/pkg/util"
)

type InnerClusterTunnelController struct {
	yachtController *yacht.Controller
	// specific namespace.
	podLister           listenrv1.PodLister
	kubeInformerFactory informers.SharedInformerFactory
	tunnelManager       *Wireguard
	spec                *known.Specification
	podSynced           cache.InformerSynced
	peerLister          v1alpha1.PeerLister
}

func (ict *InnerClusterTunnelController) Handle(podKey interface{}) (requeueAfter *time.Duration, err error) {
	failedPeriod := 2 * time.Second
	// get pod info
	key := podKey.(string)
	namespace, podName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, nil
	}
	podNotExist := false
	pod, err := ict.podLister.Pods(namespace).Get(podName)
	if err != nil {
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("pods '%s' no longer exists", key))
			podNotExist = true
		}
	}
	// pod is been deleting
	if podNotExist || !utils.IsPodAlive(pod) {
		// recycle related resources.
		errRecycle := ict.recycleResources(pod)
		if errRecycle != nil {
			d := 2 * time.Second
			return &d, errRecycle
		}
		return nil, nil
	}

	daemonConfig := DaemonConfigFromPodAnntotation(pod)
	// empty cidr on nri pod annotation
	if len(daemonConfig.secondaryCIDR) == 0 {
		// in cnf pod, we can allocate
		if len(pod.GetLabels()[known.CNFLabel]) == 0 {
			// prepare subnet in cluster
			existingCIDR := make([]string, 0)
			if podList, errListPod := ict.podLister.Pods(namespace).List(labels.Everything()); errListPod == nil {
				for _, existingPod := range podList {
					cidr := getSpecificAnnotation(existingPod, known.DaemonCIDR)
					if len(cidr) != 0 {
						existingCIDR = append(existingCIDR, cidr[0])
					}
				}
			} else {
				klog.Errorf("peers get with %v", err)
				return &failedPeriod, err
			}
			podCIDR, errAnnotation := getAnnotationFromSelf(ict.tunnelManager.k8sClient, known.CNFCIDR)
			if errAnnotation != nil {
				return &failedPeriod, err
			}
			if secondaryCIDR, allocateError := util.FindAvailableCIDR(podCIDR[0], existingCIDR,
				24); allocateError != nil {
				return &failedPeriod, err
			} else {
				daemonConfig.secondaryCIDR = []string{secondaryCIDR}
				if setSpecificAnnotation(ict.tunnelManager.k8sClient, pod, known.DaemonCIDR, secondaryCIDR) != nil {
					return &failedPeriod, err
				}
			}
		} else {
			// in nri pod, wait next time
			return &failedPeriod, err
		}
	}

	if errAddInnerTunnel := ict.tunnelManager.AddInnerClusterTunnel(daemonConfig); errAddInnerTunnel != nil {
		klog.Infof("add inner cluster tunnel failed %v", daemonConfig)
		return &failedPeriod, errAddInnerTunnel
	}
	klog.Infof("inner cluster tunnel with pod: %s has been added successfully", pod.Name)

	// add route for target inner cluster tunnel pod
	if errRoute := configHostRoutingRules(daemonConfig.secondaryCIDR, known.Add); errRoute != nil {
		klog.Infof("add route inner cluster in cnf failed for %s, with error %v",
			daemonConfig.secondaryCIDR, errRoute)
		return &failedPeriod, errRoute
	}
	return nil, nil
}

func (ict *InnerClusterTunnelController) recycleResources(pod *v1.Pod) error {
	ict.tunnelManager.mutex.Lock()
	defer ict.tunnelManager.mutex.Unlock()
	// check if we have had a tunnel for it.
	_, found := ict.tunnelManager.innerConnections[pod.Spec.NodeName]
	if !found {
		// do nothing if we have not established any tunnel for this node
		return nil
	}
	publicKey := getSpecificAnnotation(pod, known.PublicKey)
	if oldKey, err := wgtypes.ParseKey(publicKey[0]); err != nil {
		klog.Infof("Can't parse key for %s with key %s", pod.Name, publicKey)
		return err
	} else {
		delete(ict.tunnelManager.innerConnections, pod.Spec.NodeName)
		cidr := getSpecificAnnotation(pod, known.DaemonCIDR)
		removeTunnelError := ict.tunnelManager.RemoveInnerClusterTunnel(&oldKey)
		if removeTunnelError != nil {
			klog.Infof("failed to remove tunnel for %s on node %s", pod.Name, pod.Spec.NodeName)
			return removeTunnelError
		}
		if errRemoveRoute := configHostRoutingRules(cidr, known.Delete); errRemoveRoute != nil {
			klog.Infof("delete route failed for %v", errRemoveRoute)
			return errRemoveRoute
		}
	}
	return nil
}

func NewInnerClusterTunnelController(w *Wireguard, kubeClientSet kubernetes.Interface,
	labelSelector string) (*InnerClusterTunnelController, error) {
	// only nauti system namespace pod is responsible for tunnelManager
	k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(kubeClientSet, 10*time.Minute,
		informers.WithNamespace(known.NautiSystemNamespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labelSelector
		}),
	)
	podInformer := k8sInformerFactory.Core().V1().Pods()

	ictController := &InnerClusterTunnelController{
		tunnelManager:       w,
		kubeInformerFactory: k8sInformerFactory,
		podLister:           podInformer.Lister(),
		podSynced:           podInformer.Informer().HasSynced,
	}
	podController := yacht.NewController("daemon pod for inner cluster tunnel connection").
		WithCacheSynced(podInformer.Informer().HasSynced).
		WithHandlerFunc(ictController.Handle).WithEnqueueFilterFunc(func(oldObj, newObj interface{}) (bool, error) {
		var newPod *v1.Pod
		// delete event
		if newObj == nil {
			return true, nil
		} else {
			newPod = newObj.(*v1.Pod)
			publicKey := getSpecificAnnotation(newPod, known.PublicKey)
			if isRunningAndHasIP(newPod) && len(publicKey) != 0 {
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

func (ict *InnerClusterTunnelController) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	// octopus has been started before.
	ict.kubeInformerFactory.Start(ctx.Done())
	klog.Info("Starting inner cluster tunnel controller...")
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		ict.yachtController.Run(ctx)
	}, time.Duration(0))
}
