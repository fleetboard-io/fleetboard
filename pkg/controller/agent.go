package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	validations "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"

	"github.com/nauti-io/nauti/pkg/controller/mcs"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/pkg/errors"
)

type AgentConfig struct {
	ServiceImportCounterName string
	ServiceExportCounterName string
}

type Syncer struct {
	ClusterID               string
	LocalNamespace          string
	KubeClientSet           kubernetes.Interface
	KubeInformerFactory     kubeinformers.SharedInformerFactory
	ServiceExportController *mcs.ServiceExportController
	HubKubeConfig           *rest.Config
	SyncerConf              known.SyncerConfig
	EpsController           *mcs.EpsController
}

// New create a syncer client, it only works in cluster level
func New(spec *known.Specification, syncerConf known.SyncerConfig, kubeConfig *rest.Config) (*Syncer, error) {
	if errs := validations.IsDNS1123Label(spec.ClusterID); len(errs) > 0 {
		return nil, errors.Errorf("%s is not a valid ClusterID %v", spec.ClusterID, errs)
	}

	kubeClientSet := kubernetes.NewForConfigOrDie(syncerConf.LocalRestConfig)
	mcsClientSet := mcsclientset.NewForConfigOrDie(syncerConf.LocalRestConfig)

	// creates the informer factory
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClientSet, known.DefaultResync)
	mcsInformerFactory := mcsInformers.NewSharedInformerFactory(mcsClientSet, known.DefaultResync)

	serviceExportController, err := mcs.NewServiceExportController(spec.ClusterID,
		kubeInformerFactory.Discovery().V1().EndpointSlices(), mcsClientSet, mcsInformerFactory)
	if err != nil {
		return nil, err
	}

	hubK8sClient := kubernetes.NewForConfigOrDie(kubeConfig)
	hubInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(hubK8sClient, known.DefaultResync,
		kubeinformers.WithNamespace(spec.ShareNamespace))

	var epsController *mcs.EpsController
	epsController, err = mcs.NewEpsController(spec.ClusterID, syncerConf.LocalNamespace,
		hubInformerFactory.Discovery().V1().EndpointSlices(),
		kubeClientSet, hubInformerFactory, serviceExportController, mcsClientSet)
	if err != nil {
		klog.Errorf("failed to create eps controller: %v", err)
	}

	syncerConf.LocalNamespace = spec.LocalNamespace
	syncerConf.LocalClusterID = spec.ClusterID
	syncerConf.RemoteNamespace = spec.ShareNamespace

	syncer := &Syncer{
		SyncerConf:              syncerConf,
		KubeClientSet:           kubeClientSet,
		KubeInformerFactory:     kubeInformerFactory,
		ServiceExportController: serviceExportController,
		HubKubeConfig:           kubeConfig,
		EpsController:           epsController,
	}

	return syncer, nil
}

func (a *Syncer) Start(ctx context.Context) error {
	defer utilruntime.HandleCrash()
	a.KubeInformerFactory.Start(ctx.Done())
	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Syncer")
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := a.ServiceExportController.Run(ctx, a.HubKubeConfig, a.SyncerConf.RemoteNamespace); err != nil {
			klog.Error(err)
		}
	}, time.Duration(0))

	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := a.EpsController.Run(ctx); err != nil {
			klog.Error(err)
		}
	}, time.Duration(0))
	<-ctx.Done()
	return nil
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

// func (a *Syncer) getObjectNameWithClusterID(name, namespace string) string {
// 	return generateSliceName(a.ClusterID, namespace, name)
// }
