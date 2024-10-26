package mcs

import (
	"context"
	"fmt"
	"sync"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	v1informers "k8s.io/client-go/informers/core/v1"
	discoveryinformerv1 "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1 "k8s.io/client-go/listers/core/v1"
	discoverylisterv1 "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"
	alpha1 "sigs.k8s.io/mcs-api/pkg/client/listers/apis/v1alpha1"

	"github.com/dixudx/yacht"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/utils"
)

func init() {
	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
}

type ServiceExportController struct {
	YachtController *yacht.Controller
	// local msc client
	localClusterID     string
	mcsClientset       *mcsclientset.Clientset
	parentk8sClient    kubernetes.Interface
	mcsInformerFactory mcsInformers.SharedInformerFactory
	// child cluster dedicated namespace
	operatorNamespace    string
	serviceExportLister  alpha1.ServiceExportLister
	serviceLister        v1.ServiceLister
	endpointSlicesLister discoverylisterv1.EndpointSliceLister
}

func NewServiceExportController(clusteID string, epsInformer discoveryinformerv1.EndpointSliceInformer,
	services v1informers.ServiceInformer, mcsClientset *mcsclientset.Clientset,
	mcsInformerFactory mcsInformers.SharedInformerFactory) (*ServiceExportController, error) {
	seInformer := mcsInformerFactory.Multicluster().V1alpha1().ServiceExports()
	sec := &ServiceExportController{
		localClusterID:       clusteID,
		mcsClientset:         mcsClientset,
		mcsInformerFactory:   mcsInformerFactory,
		serviceLister:        services.Lister(),
		endpointSlicesLister: epsInformer.Lister(),
		serviceExportLister:  seInformer.Lister(),
	}

	// add event handler
	yachtcontroller := yacht.NewController("serviceexport").
		WithCacheSynced(seInformer.Informer().HasSynced, epsInformer.Informer().HasSynced).
		WithHandlerFunc(sec.Handle)
	_, err := seInformer.Informer().AddEventHandler(yachtcontroller.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	_, err = epsInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			endpointSlice := obj.(*discoveryv1.EndpointSlice)
			if serviceName, ok := endpointSlice.Labels[discoveryv1.LabelServiceName]; ok {
				if _, err2 := sec.serviceExportLister.ServiceExports(endpointSlice.Namespace).Get(
					serviceName); err2 == nil {
					return true
				}
			}
			return false
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if se, err2 := sec.getServiceExportFromEndpointSlice(obj); err2 == nil {
					yachtcontroller.Enqueue(se)
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				if se, err2 := sec.getServiceExportFromEndpointSlice(newObj); err2 == nil {
					yachtcontroller.Enqueue(se)
				}
			},
			DeleteFunc: func(obj interface{}) {
				if se, err2 := sec.getServiceExportFromEndpointSlice(obj); err2 == nil {
					yachtcontroller.Enqueue(se)
				}
			},
		},
	})
	if err != nil {
		return nil, err
	}
	sec.YachtController = yachtcontroller
	return sec, nil
}

func (c *ServiceExportController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	ctx := context.Background()
	key := obj.(string)
	isHeadless := "false"
	namespace, seName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid service export key: %s", key))
		return nil, nil
	}

	cachedSe, errSe := c.serviceExportLister.ServiceExports(namespace).Get(seName)
	if errSe != nil {
		if errors.IsNotFound(errSe) {
			utilruntime.HandleError(fmt.Errorf("service export '%s' in work queue no longer exists", key))
			return nil, nil
		}
		return nil, errSe
	}
	cachaedSerice, errService := c.serviceLister.Services(namespace).Get(seName)
	if errService != nil {
		if errors.IsNotFound(errService) {
			utilruntime.HandleError(fmt.Errorf("service related to ServiceExport '%s' not exists", key))
			return nil, nil
		}
		return nil, errService
	}
	if cachaedSerice.Spec.ClusterIP == "None" {
		isHeadless = "true"
	}

	se := cachedSe.DeepCopy()
	if se.Labels == nil {
		se.Labels = map[string]string{}
	}
	se.Labels[known.IsHeadlessKey] = isHeadless
	seTerminating := se.DeletionTimestamp != nil

	if !utils.ContainsString(se.Finalizers, known.AppFinalizer) && !seTerminating {
		se.Finalizers = append(se.Finalizers, known.AppFinalizer)
		se, err = c.mcsClientset.MulticlusterV1alpha1().ServiceExports(namespace).Update(context.TODO(),
			se, metav1.UpdateOptions{})
		if err != nil {
			d := time.Second
			return &d, err
		}
	}

	// recycle corresponding endpoint slice in parent cluster.
	if seTerminating {
		if err = c.parentk8sClient.DiscoveryV1().EndpointSlices(c.operatorNamespace).
			DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
				LabelSelector: labels.SelectorFromSet(labels.Set{
					discoveryv1.LabelServiceName: utils.DerivedName(c.localClusterID, namespace, seName)}).String(),
			}); err != nil {
			// try next time, make sure we clear endpoint slice
			d := time.Second
			return &d, err
		}
		se.Finalizers = utils.RemoveString(se.Finalizers, known.AppFinalizer)
		se, err = c.mcsClientset.MulticlusterV1alpha1().ServiceExports(namespace).Update(context.TODO(),
			se, metav1.UpdateOptions{})
		if err != nil {
			d := time.Second
			return &d, err
		}
		klog.Infof("service export %s has been recycled successfully", se.Name)
		return nil, nil
	}
	// src endpoint slice with label of service export name is same to service name.
	srcLabelMap := labels.Set{
		discoveryv1.LabelServiceName: se.Name,
		discoveryv1.LabelManagedBy:   known.LabelValueManagedBy,
	}
	// dst endpoint slice with label of derived service name combined with namespace and service export name
	dstLabelMap := labels.Set{discoveryv1.LabelServiceName: utils.DerivedName(c.localClusterID, namespace, seName)}
	endpointSliceList, err := utils.RemoveNonexistentEndpointslice(c.endpointSlicesLister, c.localClusterID, namespace,
		srcLabelMap, c.parentk8sClient, c.operatorNamespace, dstLabelMap)
	if err != nil {
		d := time.Second
		return &d, err
	}

	wg := sync.WaitGroup{}
	var allErrs []error
	errCh := make(chan error, len(endpointSliceList))
	for index := range endpointSliceList {
		wg.Add(1)
		slice := endpointSliceList[index].DeepCopy()
		newSlice := constructEndpointSlice(slice, se, c.operatorNamespace, c.localClusterID)
		go func(slice *discoveryv1.EndpointSlice) {
			defer wg.Done()
			if err = utils.ApplyEndPointSliceWithRetry(c.parentk8sClient, slice); err != nil {
				errCh <- err
				klog.Infof("slice %s sync err: %s", slice.Name, err)
			}
		}(newSlice)
	}
	wg.Wait()
	// collect errors
	close(errCh)
	for err := range errCh {
		allErrs = append(allErrs, err)
	}
	if len(allErrs) > 0 {
		reason := utilerrors.NewAggregate(allErrs).Error()
		msg := fmt.Sprintf("failed to sync endpoint slices of service export %s: %s", klog.KObj(se), reason)
		klog.ErrorDepth(5, msg)
		d := time.Second
		return &d, err
	}
	klog.Infof("service export %s has been synced successfully", se.Name)
	return nil, nil
}

func (c *ServiceExportController) Run(ctx context.Context, parentDedicatedKubeConfig *rest.Config,
	delicatedNamespace string) error {
	c.mcsInformerFactory.Start(ctx.Done())
	// set parent cluster related filed.
	c.operatorNamespace = delicatedNamespace

	parentClient := kubernetes.NewForConfigOrDie(parentDedicatedKubeConfig)
	c.parentk8sClient = parentClient

	c.YachtController.Run(ctx)
	return nil
}

func (c *ServiceExportController) getServiceExportFromEndpointSlice(obj interface{}) (*v1alpha1.ServiceExport, error) {
	slice := obj.(*discoveryv1.EndpointSlice)
	if serviceName, ok := slice.Labels[discoveryv1.LabelServiceName]; ok {
		if se, err := c.serviceExportLister.ServiceExports(slice.Namespace).Get(serviceName); err == nil {
			return se, nil
		}
	}
	return nil, fmt.Errorf("can't get service export from this slice %s/%s", slice.Namespace, slice.Name)
}

// constructEndpointSlice construct a new endpoint slice from local slice.
func constructEndpointSlice(slice *discoveryv1.EndpointSlice, se *v1alpha1.ServiceExport,
	namespace, clusterID string) *discoveryv1.EndpointSlice {
	// mutate slice fields before upload to parent cluster.
	newSlice := &discoveryv1.EndpointSlice{}
	newSlice.AddressType = slice.AddressType
	newSlice.Endpoints = slice.Endpoints
	newSlice.Ports = slice.Ports
	newSlice.Labels = make(map[string]string)

	newSlice.Labels[known.LabelServiceName] = se.Name
	newSlice.Labels[known.LabelClusterID] = clusterID
	newSlice.Labels[known.LabelServiceNameSpace] = se.Namespace
	newSlice.Labels[discoveryv1.LabelServiceName] = utils.DerivedName(clusterID, se.Namespace, se.Name)
	newSlice.Labels[known.IsHeadlessKey] = se.GetLabels()[known.IsHeadlessKey]

	newSlice.Namespace = namespace
	newSlice.Name = fmt.Sprintf("%s-%s-%s", clusterID, se.Namespace, slice.Name)
	return newSlice
}
