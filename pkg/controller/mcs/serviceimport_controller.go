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
	discoveryinformerv1 "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	discoverylisterv1 "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions/apis/v1alpha1"
	alpha1 "sigs.k8s.io/mcs-api/pkg/client/listers/apis/v1alpha1"

	"github.com/dixudx/yacht"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/utils"
)

func init() {
	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
}

type ServiceImportController struct {
	// child cluster dedicated namespace
	operatorNamespace string

	IPAM                        *utils.IPAM
	mcsClientset                *mcsclientset.Clientset
	localk8sClient              kubernetes.Interface
	localSILister               alpha1.ServiceImportLister
	sourceEndpointSlicesLister  discoverylisterv1.EndpointSliceLister
	localSIInformer             mcsv1alpha1.ServiceImportInformer
	sourceEndpointSliceInformer discoveryinformerv1.EndpointSliceInformer
	yachtController             *yacht.Controller
}

func NewServiceImportController(kubeclient kubernetes.Interface,
	epsInformer discoveryinformerv1.EndpointSliceInformer,
	mcsClientset *mcsclientset.Clientset,
	mcsInformerFactory mcsInformers.SharedInformerFactory) (*ServiceImportController, error) {
	siInformer := mcsInformerFactory.Multicluster().V1alpha1().ServiceImports()
	sic := &ServiceImportController{
		mcsClientset:                mcsClientset,
		localk8sClient:              kubeclient,
		localSIInformer:             siInformer,
		localSILister:               siInformer.Lister(),
		sourceEndpointSlicesLister:  epsInformer.Lister(),
		sourceEndpointSliceInformer: epsInformer,
		IPAM:                        utils.NewIPAM(),
	}
	// add event handler for ServiceImport
	yachtcontroller := yacht.NewController("serviceimport").
		WithCacheSynced(siInformer.Informer().HasSynced, epsInformer.Informer().HasSynced).
		WithHandlerFunc(sic.Handle).
		WithEnqueueFilterFunc(preFilter)
	_, err := sic.localSIInformer.Informer().AddEventHandler(yachtcontroller.DefaultResourceEventHandlerFuncs())
	if err != nil {
		klog.Errorf("failed to add event handler for serviceimport: %v", err)
		return nil, err
	}
	_, err = epsInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			if si, err2 := sic.getServiceImportFromEndpointSlice(obj); err2 == nil {
				yachtcontroller.Enqueue(si)
			}
			return false
		},
	})
	if err != nil {
		klog.Errorf("failed to add event handler for serviceimport: %v", err)
		return nil, err
	}
	sic.yachtController = yachtcontroller
	return sic, nil
}

func (s *ServiceImportController) AddInitialInfoToServiceImport(si *v1alpha1.ServiceImport) (bool, error) {
	siChanged := false
	if len(si.Spec.IPs) == 0 {
		si.Spec.IPs = make([]string, 0)
	}
	isHeadless := si.Spec.Type == v1alpha1.Headless

	hasFinalizer := utils.ContainsString(si.Finalizers, known.AppFinalizer)
	// has no finalizer and not terminating
	if !hasFinalizer {
		si.Finalizers = append(si.Finalizers, known.AppFinalizer)
		siChanged = true
	}
	// no need of virtual ServiceIP
	if isHeadless {
		if len(si.Spec.IPs) != 0 {
			si.Spec.IPs[0] = ""
			siChanged = true
		}
	} else {
		if len(si.Spec.IPs) == 0 {
			ip, errAllocate := s.IPAM.AllocateIP()
			if errAllocate != nil {
				return siChanged, errAllocate
			} else {
				siChanged = true
				si.Spec.IPs = append(si.Spec.IPs, ip)
			}
		}
	}
	return siChanged, nil
}

// getServiceImportFromEndpointSlice get ServiceImport from endpointSlice labels, get the first if more than one.
func (s *ServiceImportController) getServiceImportFromEndpointSlice(obj interface{}) (*v1alpha1.ServiceImport, error) {
	slice := obj.(*discoveryv1.EndpointSlice)
	rawServiceName, serviceExist := slice.Labels[known.LabelServiceName]
	rawServiceNamespace, serviceNamespaceExsit := slice.Labels[known.LabelServiceNameSpace]
	if serviceExist && serviceNamespaceExsit {
		if siList, err := s.localSILister.ServiceImports(s.operatorNamespace).List(
			labels.SelectorFromSet(labels.Set{
				known.LabelServiceName:      rawServiceName,
				known.LabelServiceNameSpace: rawServiceNamespace,
			})); err == nil && len(siList) > 0 {
			return siList[0], nil
		}
	}
	klog.Infof("can't resolve service import from this slice %s/%s", slice.Namespace, slice.Name)
	return nil, fmt.Errorf("can't resolve service import from this slice %s/%s", slice.Namespace, slice.Name)
}

// preFilter filter ServiceImport if has no label known.LabelServiceName and known.LabelServiceNameSpace
func preFilter(oldObj, newObj interface{}) (bool, error) {
	var si *v1alpha1.ServiceImport
	if newObj == nil {
		// Delete
		si = oldObj.(*v1alpha1.ServiceImport)
	} else {
		// Add or Update
		si = newObj.(*v1alpha1.ServiceImport)
	}
	_, serviceExist := si.Labels[known.LabelServiceName]
	_, serviceNamespaceExist := si.Labels[known.LabelServiceNameSpace]
	if !serviceExist || !serviceNamespaceExist {
		return false, nil
	}
	return true, nil
}

func (s *ServiceImportController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	ctx := context.Background()
	key := obj.(string)
	namespace, siName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid service import key: %s", key))
		return nil, nil
	}
	cachedSi, err := s.localSILister.ServiceImports(namespace).Get(siName)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("service import '%s' in work queue no longer exists", key))
			return nil, nil
		}
		return nil, err
	}
	si := cachedSi.DeepCopy()
	// recycle corresponding endpoint slice in parent cluster.
	if si.DeletionTimestamp != nil {
		if err = s.recycleServiceImport(ctx, si); err != nil {
			d := time.Second
			return &d, err
		}
		klog.Infof("service import %s has been recycled successfully", si.Name)
		return nil, nil
	}

	changed, initErr := s.AddInitialInfoToServiceImport(si)
	if initErr != nil {
		klog.Errorf("Init failed for serviceimport %s, for %v", siName, initErr)
		d := time.Second
		return &d, err
	}
	if changed {
		si, err = s.mcsClientset.MulticlusterV1alpha1().ServiceImports(namespace).Update(context.TODO(),
			si, metav1.UpdateOptions{})
		if err != nil {
			d := time.Second
			return &d, err
		}
	}

	// apply endpoint slices.
	srcLabelMap := labels.Set{
		known.LabelServiceName:      si.Labels[known.LabelServiceName],
		known.LabelServiceNameSpace: si.Labels[known.LabelServiceNameSpace],
	}
	dstLabelMap := labels.Set{
		known.LabelServiceName:      si.Labels[known.LabelServiceName],
		known.LabelServiceNameSpace: si.Labels[known.LabelServiceNameSpace],
	}

	endpointSliceList, err := utils.RemoveNonexistentEndpointslice(s.sourceEndpointSlicesLister, "",
		s.operatorNamespace, srcLabelMap, s.localk8sClient, namespace, dstLabelMap, false)
	if err != nil {
		d := time.Second
		return &d, err
	}

	// transport endpointslice from delicate ns to target ns.
	wg := sync.WaitGroup{}
	var allErrs []error
	errCh := make(chan error, len(endpointSliceList))
	for index := range endpointSliceList {
		wg.Add(1)
		slice := endpointSliceList[index].DeepCopy()
		newSlice := forkEndpointSlice(slice, namespace)
		go func(slice *discoveryv1.EndpointSlice) {
			defer wg.Done()
			if err = utils.ApplyEndPointSliceWithRetry(s.localk8sClient, slice); err != nil {
				errCh <- err
				klog.Infof("slice %s sync err from %s to %s for: %v",
					slice.Name, slice.Namespace, namespace, err)
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
		msg := fmt.Sprintf("failed to sync endpoint slices for %s: %s", klog.KObj(si), reason)
		klog.ErrorDepth(5, msg)
		d := time.Second
		return &d, err
	}
	klog.Infof("service import %s has been synced successfully", si.Name)
	return nil, nil
}

func (s *ServiceImportController) Run(ctx context.Context, delicatedNamespace string) {
	// set parent cluster related filed.
	s.operatorNamespace = delicatedNamespace
	s.yachtController.Run(ctx)
}

// recycleServiceImport recycle derived service and derived endpoint slices.
func (s *ServiceImportController) recycleServiceImport(ctx context.Context, si *v1alpha1.ServiceImport) error {
	rawServiceName := si.Labels[known.LabelServiceName]
	rawServiceNamespace := si.Labels[known.LabelServiceNameSpace]
	// 1. recycle endpoint slices.
	if err := s.localk8sClient.DiscoveryV1().EndpointSlices(si.Namespace).
		DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(labels.Set{
				known.LabelServiceName:      rawServiceName,
				known.LabelServiceNameSpace: rawServiceNamespace}).String(),
		}); err != nil {
		// try next time, make sure we clear all related endpoint slices
		return err
	}
	// 2. recycle virtual cluster ip if needed
	if si.Spec.Type != v1alpha1.Headless && len(si.Spec.IPs[0]) != 0 {
		allocateError := s.IPAM.ReleaseIP(si.Spec.IPs[0])
		if allocateError != nil {
			return allocateError
		}
	}
	// 3. remove finalizer in service import
	si.Finalizers = utils.RemoveString(si.Finalizers, known.AppFinalizer)
	_, err := s.mcsClientset.MulticlusterV1alpha1().ServiceImports(si.Namespace).Update(context.TODO(),
		si, metav1.UpdateOptions{})
	return err
}

// forkEndpointSlice construct a new endpoint slice from source slice.
func forkEndpointSlice(slice *discoveryv1.EndpointSlice, namespace string) *discoveryv1.EndpointSlice {
	// mutate slice fields before upload to parent cluster.
	newSlice := &discoveryv1.EndpointSlice{
		AddressType: slice.AddressType,
		Endpoints:   slice.Endpoints,
		Ports:       slice.Ports,
	}
	delete(slice.Labels, known.ObjectCreatedByLabel)
	newSlice.Labels = slice.Labels
	newSlice.Namespace = namespace
	newSlice.Name = slice.Name
	return newSlice
}
