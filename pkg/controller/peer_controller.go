package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/dixudx/yacht"
	v1alpha1app "github.com/multi-cluster-network/ovn-builder/pkg/apis/octopus.io/v1alpha1"
	octopusinformers "github.com/multi-cluster-network/ovn-builder/pkg/generated/informers/externalversions"
	"github.com/multi-cluster-network/ovn-builder/pkg/generated/listers/octopus.io/v1alpha1"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type PeerController struct {
	yachtController *yacht.Controller
	// specific namespace.
	peerLister     v1alpha1.PeerLister
	myClusterID    string
	octopusFactory octopusinformers.SharedInformerFactory
	tunnel         *wireguard
}

func NewPeerController(spec Specification, w *wireguard, octopusFactory octopusinformers.SharedInformerFactory) (*PeerController, error) {
	peerController := &PeerController{
		peerLister:     octopusFactory.Octopus().V1alpha1().Peers().Lister(),
		myClusterID:    spec.ClusterID,
		octopusFactory: octopusFactory,
		tunnel:         w,
	}
	peerInformer := octopusFactory.Octopus().V1alpha1().Peers()

	yachtController := yacht.NewController("peer").
		WithCacheSynced(peerInformer.Informer().HasSynced).
		WithHandlerFunc(peerController.Handle).
		WithEnqueueFilterFunc(func(oldObj, newObj interface{}) (bool, error) {
			var tempObj interface{}
			if newObj != nil {
				tempObj = newObj
			} else {
				tempObj = oldObj
			}
			//klog.Infof("we got a peer connection %v", tempObj)
			if tempObj != nil {
				newPeer := tempObj.(*v1alpha1app.Peer)
				// hub connect with nohub, nohub connect with hub.
				//if !spec.IsHub && !newPeer.Spec.IsHub || spec.IsHub && newPeer.Spec.IsHub {
				if spec.IsHub == newPeer.Spec.IsHub {
					return false, nil
				}
			}
			return true, nil
		})
	_, err := peerInformer.Informer().AddEventHandler(yachtController.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	peerController.yachtController = yachtController
	return peerController, nil
}

func (c *PeerController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	failedPeriod := 2 * time.Second
	key := obj.(string)
	namespace, peerName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, nil
	}

	hubNotExist := false
	cachedPeer, err := c.peerLister.Peers(namespace).Get(peerName)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("peer '%s' in hub work queue no longer exists, try to delete in this cluster.", key))
			hubNotExist = true
		} else {
			return nil, err
		}
	}

	peerTerminating := hubNotExist || cachedPeer.DeletionTimestamp != nil
	// recycle corresponding endpoint slice.
	if peerTerminating {
		// TODO try to recycle peer in this cnf client.
		var oldKey wgtypes.Key
		if oldKey, err = wgtypes.ParseKey(cachedPeer.Spec.PublicKey); err != nil {
			klog.Infof("can't find key for %s with key %s", peerName, cachedPeer.Spec.PublicKey)

			return &failedPeriod, err
		}
		if c.tunnel.RemovePeer(&oldKey) != nil {
			return &failedPeriod, err
		}
		klog.Infof("peer %s has been recycled successfully", peerName)
	}

	if err := c.tunnel.AddPeer(cachedPeer); err != nil {
		klog.Infof("add peer failed %v", cachedPeer)
		return &failedPeriod, err
	}
	klog.Infof("peer %s has been synced successfully", peerName)
	return nil, nil
}

func (c *PeerController) Run(ctx context.Context) error {
	c.octopusFactory.Start(ctx.Done())
	c.yachtController.Run(ctx)
	return nil
}

func (p *PeerController) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting octopus")
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := p.Run(ctx); err != nil {
			klog.Error(err)
		}
	}, time.Duration(0))
	<-ctx.Done()
}
