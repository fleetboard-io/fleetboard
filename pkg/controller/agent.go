package controller

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	validations "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"

	"github.com/multi-cluster-network/ovn-builder/pkg/config"
	"github.com/multi-cluster-network/ovn-builder/pkg/constants"
	"github.com/multi-cluster-network/ovn-builder/pkg/controller/mcs"
	"github.com/multi-cluster-network/ovn-builder/pkg/known"
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

func New(spec *known.AgentSpecification, syncerConf known.SyncerConfig, kubeClientSet kubernetes.Interface, mcsClientSet *mcsclientset.Clientset) (*Syncer, error) {
	if errs := validations.IsDNS1123Label(spec.ClusterID); len(errs) > 0 {
		return nil, errors.Errorf("%s is not a valid ClusterID %v", spec.ClusterID, errs)
	}
	// creates the informer factory
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClientSet, known.DefaultResync)
	mcsInformerFactory := mcsInformers.NewSharedInformerFactory(mcsClientSet, known.DefaultResync)

	serviceExportController, err := mcs.NewServiceExportController(spec.ClusterID, kubeInformerFactory.Discovery().V1().EndpointSlices(), mcsClientSet, mcsInformerFactory)
	if err != nil {
		return nil, err
	}

	hubKubeConfig, err := config.GetHubConfig(kubeClientSet, spec.HubURL, spec.LocalNamespace)
	hubK8sClient := kubernetes.NewForConfigOrDie(hubKubeConfig)
	hubInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(hubK8sClient, known.DefaultResync, kubeinformers.WithNamespace(spec.ShareNamespace))
	epsController, err := mcs.NewEpsController(spec.ClusterID, syncerConf.LocalNamespace, hubInformerFactory.Discovery().V1().EndpointSlices(),
		kubeClientSet, hubInformerFactory, serviceExportController, mcsClientSet)

	syncerConf.LocalNamespace = spec.LocalNamespace
	syncerConf.LocalClusterID = spec.ClusterID
	syncerConf.RemoteNamespace = spec.ShareNamespace

	syncer := &Syncer{
		SyncerConf:              syncerConf,
		KubeClientSet:           kubeClientSet,
		KubeInformerFactory:     kubeInformerFactory,
		ServiceExportController: serviceExportController,
		HubKubeConfig:           hubKubeConfig,
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

func (a *Syncer) newServiceImport(name, namespace string) *mcsv1a1.ServiceImport {
	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name: a.getObjectNameWithClusterID(name, namespace),
			Annotations: map[string]string{
				constants.OriginName:      name,
				constants.OriginNamespace: namespace,
			},
			Labels: map[string]string{
				constants.LabelSourceName:      name,
				constants.LabelSourceNamespace: namespace,
				constants.LabelSourceCluster:   a.ClusterID,
				constants.LabelOriginNameSpace: namespace,
			},
		},
	}
}

func (a *Syncer) getPortsForService(service *corev1.Service) []mcsv1a1.ServicePort {
	mcsPorts := make([]mcsv1a1.ServicePort, 0, len(service.Spec.Ports))

	for _, port := range service.Spec.Ports {
		mcsPorts = append(mcsPorts, mcsv1a1.ServicePort{
			Name:     port.Name,
			Protocol: port.Protocol,
			Port:     port.Port,
		})
	}

	return mcsPorts
}

func generateSliceName(clusterName, namespace, name string) string {
	clusterName = fmt.Sprintf("%s%s%s", clusterName, namespace, name)
	hasher := md5.New()
	hasher.Write([]byte(clusterName))
	var namespacePart, namePart string
	if len(namespace) > constants.MaxNamespaceLength {
		namespacePart = namespace[0:constants.MaxNamespaceLength]
	} else {
		namespacePart = namespace
	}

	if len(name) > constants.MaxNameLength {
		namePart = name[0:constants.MaxNameLength]
	} else {
		namePart = name
	}

	hashPart := hex.EncodeToString(hasher.Sum(nil))

	return fmt.Sprintf("%s-%s-%s", namespacePart, namePart, hashPart[8:24])
}

func (a *Syncer) getObjectNameWithClusterID(name, namespace string) string {
	return generateSliceName(a.ClusterID, namespace, name)
}
