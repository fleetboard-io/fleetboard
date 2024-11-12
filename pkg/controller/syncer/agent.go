package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	validations "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"

	"github.com/fleetboard-io/fleetboard/pkg/controller/mcs"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/pkg/tunnel"
	"github.com/pkg/errors"
)

type AgentConfig struct {
	ServiceImportCounterName string
	ServiceExportCounterName string
}

type Syncer struct {
	ClusterID      string
	LocalNamespace string

	HubKubeConfig           *rest.Config
	SyncerConf              known.SyncerConfig
	ServiceExportController *mcs.ServiceExportController
	ServiceImportController *mcs.ServiceImportController
	// local mcs related informer factory
	McsInformerFactory mcsInformers.SharedInformerFactory
	// local k8s informer factory
	KubeInformerFactory kubeinformers.SharedInformerFactory
	// local k8s clientset
	KubeClientSet kubernetes.Interface
	// hub k8s informer factory
	HubInformerFactory kubeinformers.SharedInformerFactory
	LocalMcsClientSet  *mcsclientset.Clientset
	LocalmcsClientSet  *mcsclientset.Clientset
	ClientSet          *kubernetes.Clientset
}

// New create a syncer client, it only works in cluster level
func New(spec *tunnel.Specification, syncerConf known.SyncerConfig, hubKubeConfig *rest.Config) (*Syncer, error) {
	if errs := validations.IsDNS1123Label(spec.ClusterID); len(errs) > 0 {
		return nil, errors.Errorf("%s is not a valid ClusterID %v", spec.ClusterID, errs)
	}

	localKubeClientSet := kubernetes.NewForConfigOrDie(syncerConf.LocalRestConfig)
	mcsClientSet := mcsclientset.NewForConfigOrDie(syncerConf.LocalRestConfig)

	hubK8sClient := kubernetes.NewForConfigOrDie(hubKubeConfig)
	hubInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(hubK8sClient, known.DefaultResync,
		kubeinformers.WithNamespace(spec.ShareNamespace))
	// creates the informer factory
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(localKubeClientSet, known.DefaultResync)
	mcsInformerFactory := mcsInformers.NewSharedInformerFactory(mcsClientSet, known.DefaultResync)

	serviceExportController, err := mcs.NewServiceExportController(spec.ClusterID, hubK8sClient,
		kubeInformerFactory.Discovery().V1().EndpointSlices(), kubeInformerFactory.Core().V1().Services(),
		mcsClientSet, mcsInformerFactory)
	if err != nil {
		return nil, err
	}

	serviceImportController, err := mcs.NewServiceImportController(localKubeClientSet,
		hubInformerFactory.Discovery().V1().EndpointSlices(), mcsClientSet, mcsInformerFactory)
	if err != nil {
		return nil, err
	}

	syncerConf.LocalNamespace = spec.LocalNamespace
	syncerConf.LocalClusterID = spec.ClusterID
	syncerConf.RemoteNamespace = spec.ShareNamespace

	syncer := &Syncer{
		SyncerConf:              syncerConf,
		LocalMcsClientSet:       mcsClientSet,
		HubKubeConfig:           hubKubeConfig,
		ServiceExportController: serviceExportController,
		ServiceImportController: serviceImportController,
		LocalNamespace:          syncerConf.LocalNamespace,
		KubeInformerFactory:     kubeInformerFactory,
		KubeClientSet:           localKubeClientSet,
		McsInformerFactory:      mcsInformerFactory,
		HubInformerFactory:      hubInformerFactory,
		ClientSet:               clientSet,
	}

	return syncer, nil
}

func (s *Syncer) Start(ctx context.Context) (err error) {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	s.KubeInformerFactory.Start(ctx.Done())
	s.McsInformerFactory.Start(ctx.Done())
	s.HubInformerFactory.Start(ctx.Done())

	klog.Info("Starting Syncer and init virtual CIDR...")
	var cidr string
	if cidr, err = s.ServiceImportController.IPAM.InitNewCIDR(s.LocalMcsClientSet,
		s.LocalNamespace, s.KubeClientSet); err != nil {
		klog.Errorf("we allocate for virtual service failed for %v", err)
		return err
	} else {
		klog.Infof("we allocate %s for virtual service in this cluster", cidr)
	}

	err = s.updateInnerClusterIPCIDRToCNFPod(ctx, cidr)
	if err != nil {
		klog.Errorf("Failed to update cnf pod : %v with inner cluster ip cidr", err)
		return err
	}

	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		s.ServiceExportController.Run(ctx, s.SyncerConf.RemoteNamespace)
	}, time.Duration(0))

	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		s.ServiceImportController.Run(ctx, s.SyncerConf.RemoteNamespace)
	}, time.Duration(0))

	<-ctx.Done()
	return nil
}

func (s *Syncer) updateInnerClusterIPCIDRToCNFPod(ctx context.Context, cidr string) error {
	// update cidr to all the cnf pod
	results, err := s.ClientSet.CoreV1().Pods(known.FleetboardSystemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: known.LabelCNFPod,
	})
	if err != nil {
		klog.Errorf("Failed to get latest version cnf pood: %v", err)
		return err
	}

	var errs error
	for _, pod := range results.Items {
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			newPod, err := s.ClientSet.CoreV1().Pods(known.FleetboardSystemNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
			if err != nil {
				klog.Errorf("Failed to get latest version cnf pood: %v", err)
				return err
			}
			if newPod.Annotations == nil {
				newPod.Annotations = make(map[string]string)
			}
			newPod.Annotations[fmt.Sprintf(known.InnerClusterIPCIDR, known.FleetboardPrefix)] = cidr
			_, updateErr := s.ClientSet.CoreV1().Pods(known.FleetboardSystemNamespace).Update(ctx, newPod, metav1.UpdateOptions{})
			return updateErr
		})
		if retryErr != nil {
			klog.Errorf("Failed to update cnf pod %s: %v", pod.Name, retryErr)
			errs = fmt.Errorf("%v; %v", errs, retryErr)
		}
	}
	return errs
}

func generateSliceName(clusterName, namespace, name string) string {
	clusterName = fmt.Sprintf("%s%s%s", clusterName, namespace, name)
	hasher := sha256.New()
	hasher.Write([]byte(clusterName))
	var namespacePart, namePart string
	if len(namespace) > known.MaxNamespaceLength {
		namespacePart = namespace[0:known.MaxNamespaceLength]
	} else {
		namespacePart = namespace
	}

	if len(name) > known.MaxNameLength {
		namePart = name[0:known.MaxNameLength]
	} else {
		namePart = name
	}

	hashPart := hex.EncodeToString(hasher.Sum(nil))

	return fmt.Sprintf("%s-%s-%s", namespacePart, namePart, hashPart[8:24])
}
