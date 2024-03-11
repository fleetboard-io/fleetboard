package mcs

import (
	"context"
	"fmt"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	kubeinformers "k8s.io/client-go/informers"
	discoveryinformerv1 "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/kubernetes"
	discoverylisterv1 "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"

	"github.com/dixudx/yacht"
	"github.com/multi-cluster-network/nauti/pkg/known"
	"github.com/multi-cluster-network/nauti/utils"
)

type EpsController struct {
	yachtController *yacht.Controller

	//local msc client
	srcNamespace string
	// specific namespace.
	srcEndpointSlicesLister discoverylisterv1.EndpointSliceLister

	targetK8sClient    kubernetes.Interface
	targetNamespace    string
	k8sInformerFactory kubeinformers.SharedInformerFactory
}

func NewEpsController(clusteID, targetNamespace string, epsInformer discoveryinformerv1.EndpointSliceInformer, kubeClientSet kubernetes.Interface,
	k8sInformerFactory kubeinformers.SharedInformerFactory, seController *ServiceExportController, mcsSet *mcsclientset.Clientset) (*EpsController, error) {
	epsController := &EpsController{
		srcEndpointSlicesLister: epsInformer.Lister(),
		targetK8sClient:         kubeClientSet,
		targetNamespace:         targetNamespace,
		k8sInformerFactory:      k8sInformerFactory,
	}

	yachtcontroller := yacht.NewController("eps").
		WithCacheSynced(epsInformer.Informer().HasSynced).
		WithHandlerFunc(epsController.Handle).WithEnqueueFilterFunc(func(oldObj, newObj interface{}) (bool, error) {
		var tempObj interface{}
		if newObj != nil {
			tempObj = newObj
		} else {
			tempObj = oldObj
		}

		if tempObj != nil {
			newEps := tempObj.(*discoveryv1.EndpointSlice)
			// ignore the eps sourced from it-self
			if newEps.GetLabels()[known.LabelClusterID] == clusteID {
				slice := tempObj.(*discoveryv1.EndpointSlice)
				seNamespace := slice.Labels[known.LabelServiceNameSpace]
				if serviceName, ok := slice.Labels[known.LabelServiceName]; ok {
					if se, err := mcsSet.MulticlusterV1alpha1().ServiceExports(seNamespace).Get(context.TODO(), serviceName, metav1.GetOptions{}); err == nil {
						seController.YachtController.Enqueue(se)
					}
				}
				return false, nil
			}
		}
		return true, nil
	})
	_, err := epsInformer.Informer().AddEventHandler(yachtcontroller.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	epsController.yachtController = yachtcontroller
	return epsController, nil
}

func (c *EpsController) Run(ctx context.Context) error {
	c.k8sInformerFactory.Start(ctx.Done())
	c.yachtController.Run(ctx)
	return nil
}

func (c *EpsController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	ctx := context.Background()
	key := obj.(string)
	namespace, epsName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, nil
	}

	hubNotExist := false
	cachedEps, err := c.srcEndpointSlicesLister.EndpointSlices(namespace).Get(epsName)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("endpointslice '%s' in hub work queue no longer exists, try to delete in this cluster.", key))
			hubNotExist = true
		} else {
			return nil, err
		}
	}

	epsTerminating := hubNotExist || cachedEps.DeletionTimestamp != nil

	// recycle corresponding endpoint slice.
	if epsTerminating {
		if err = c.targetK8sClient.DiscoveryV1().EndpointSlices(c.targetNamespace).Delete(ctx, epsName, metav1.DeleteOptions{}); err != nil {
			// try next time, make sure we clear endpoint slice
			d := time.Second
			return &d, err
		}
		klog.Infof("endpoint slice %s has been recycled successfully", epsName)
		return nil, nil
	}
	newSlice := &discoveryv1.EndpointSlice{}
	newSlice.AddressType = cachedEps.AddressType
	newSlice.Endpoints = cachedEps.Endpoints
	newSlice.Ports = cachedEps.Ports
	newSlice.Labels = cachedEps.GetLabels()
	newSlice.Namespace = c.targetNamespace
	newSlice.Name = cachedEps.Name

	if err = utils.ApplyEndPointSliceWithRetry(c.targetK8sClient, newSlice); err != nil {
		klog.Infof("slice %s sync err: %s", newSlice.Name, err)
		d := time.Second
		return &d, err
	}

	klog.Infof("endpoint slice %s has been synced successfully", newSlice.Name)
	return nil, nil
}
