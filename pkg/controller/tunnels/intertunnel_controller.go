package tunnels

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/dixudx/yacht"
	v1alpha1app "github.com/fleetboard-io/fleetboard/pkg/apis/fleetboard.io/v1alpha1"
	"github.com/fleetboard-io/fleetboard/pkg/generated/clientset/versioned"
	fleetboardInformers "github.com/fleetboard-io/fleetboard/pkg/generated/informers/externalversions"
	"github.com/fleetboard-io/fleetboard/pkg/generated/listers/fleetboard.io/v1alpha1"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/pkg/tunnel"
	"github.com/fleetboard-io/fleetboard/utils"
	"github.com/vishvananda/netlink"
)

type InterClusterTunnelController struct {
	yachtController *yacht.Controller
	// specific namespace.
	peerLister        v1alpha1.PeerLister
	fleetboardFactory fleetboardInformers.SharedInformerFactory
	tunnel            *tunnel.Wireguard
	fleetboardClient  *versioned.Clientset
	spec              *tunnel.Specification
	localK8sClient    kubernetes.Interface
}

func NewInterClusterTunnelController(spec *tunnel.Specification, localK8sClient kubernetes.Interface,
	w *tunnel.Wireguard, fleetboardClient *versioned.Clientset,
	fleetboardFactory fleetboardInformers.SharedInformerFactory) (*InterClusterTunnelController, error) {
	ict := &InterClusterTunnelController{
		peerLister:        fleetboardFactory.Fleetboard().V1alpha1().Peers().Lister(),
		fleetboardFactory: fleetboardFactory,
		tunnel:            w,
		fleetboardClient:  fleetboardClient,
		spec:              spec,
		localK8sClient:    localK8sClient,
	}
	peerInformer := fleetboardFactory.Fleetboard().V1alpha1().Peers()

	yachtController := yacht.NewController("peer").
		WithCacheSynced(peerInformer.Informer().HasSynced).
		WithHandlerContextFunc(func(ctx context.Context, key interface{}) (*time.Duration, error) {
			select {
			case <-ctx.Done():
				return nil, nil
			default:
				return ict.Handle(key)
			}
		}).
		WithEnqueueFilterFunc(func(oldObj, newObj interface{}) (bool, error) {
			var tempObj interface{}
			if newObj != nil {
				tempObj = newObj
			} else {
				tempObj = oldObj
			}
			// klog.Infof("we got a peer connection %v", tempObj)
			if tempObj != nil {
				newPeer := tempObj.(*v1alpha1app.Peer)
				// hub connect with nohub, nohub connect with hub.
				// make sure there is only ONE Hub.
				// TODO should we create wireguard for public child cluster in hub?
				if newPeer.Spec.IsHub || (len(newPeer.Spec.Endpoint) != 0 && newPeer.Spec.IsPublic) {
					return !spec.AsHub, nil
				} else {
					// child cluster without public ip
					return spec.AsHub || len(spec.Endpoint) != 0, nil
				}
			}
			return false, nil
		})
	_, err := peerInformer.Informer().AddEventHandler(yachtController.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	ict.yachtController = yachtController
	return ict, nil
}

func (ict *InterClusterTunnelController) RecyclePeer(cachedPeer *v1alpha1app.Peer) (*time.Duration, error) {
	// TODO try to recycle peer in this cnf client.
	var oldKey wgtypes.Key
	var err error
	failedPeriod := 2 * time.Second
	if oldKey, err = wgtypes.ParseKey(cachedPeer.Spec.PublicKey); err != nil {
		klog.Infof("can't find key for %s with key %s", cachedPeer.Name, cachedPeer.Spec.PublicKey)
		return &failedPeriod, err
	}
	if ict.tunnel.RemoveInterClusterTunnel(&oldKey) != nil {
		return &failedPeriod, err
	}
	if errRemoveRoute := configHostRoutingRules(cachedPeer.Spec.PodCIDR, known.Delete); errRemoveRoute != nil {
		klog.Infof("delete route failed for %v", cachedPeer)
		return &failedPeriod, errRemoveRoute
	}
	klog.Infof("peer %s has been recycled successfully", cachedPeer.Name)
	return nil, nil
}

func (ict *InterClusterTunnelController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	failedPeriod := 2 * time.Second
	key := obj.(string)
	namespace, peerName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, nil
	}

	noCIDR := false
	hubNotExist := false
	cachedPeer, err := ict.peerLister.Peers(namespace).Get(peerName)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("peer '%s' in hub work queue no longer exists,"+
				" try to delete in this cluster", key))
			hubNotExist = true
		} else {
			return nil, err
		}
	}

	peerTerminating := hubNotExist || cachedPeer.DeletionTimestamp != nil
	// recycle corresponding endpoint slice.
	if peerTerminating {
		return ict.RecyclePeer(cachedPeer)
	}

	if ict.spec.AsCluster {
		// just cluster, only wait if the coming peer has no cidr.
		if len(cachedPeer.Spec.PodCIDR) == 0 || len(cachedPeer.Spec.PodCIDR[0]) == 0 {
			return &failedPeriod, errors.NewServiceUnavailable("cidr is not allocated.")
		}
		// other child cluster has public ip.
		if cachedPeer.Name != known.HubClusterName {
			if annoError := utils.AddAnnotationToSelf(ict.localK8sClient, known.FleetboardNodeCIDR, cachedPeer.Spec.PodCIDR[0],
				true); annoError != nil {
				return &failedPeriod, errors.NewServiceUnavailable("cidr is not allocated.")
			}
		}
	} else if len(cachedPeer.Spec.PodCIDR) == 0 || len(cachedPeer.Spec.PodCIDR[0]) == 0 {
		//  prepare data...
		existingCIDR := make([]string, 0)
		noCIDR = true
		if peerList, errListPeer := ict.peerLister.Peers(namespace).List(labels.Everything()); errListPeer == nil {
			for _, item := range peerList {
				if item.Name != "hub" && len(item.Spec.PodCIDR) != 0 {
					existingCIDR = append(existingCIDR, item.Spec.PodCIDR[0])
				}
			}
		} else {
			klog.Errorf("peers get with %v", err)
			return &failedPeriod, err
		}
		// cidr allocation here.
		cachedPeer.Spec.PodCIDR = make([]string, 1)
		cachedPeer.Spec.PodCIDR[0], err = utils.FindTunnelAvailableCIDR(ict.spec.CIDR, existingCIDR)
		if err != nil {
			klog.Infof("allocate peer cidr failed %v", err)
			return &failedPeriod, err
		}
	}
	if errAddPeer := ict.tunnel.AddInterClusterTunnel(cachedPeer); errAddPeer != nil {
		klog.Infof("add peer failed %v", cachedPeer)
		return &failedPeriod, errAddPeer
	}
	klog.Infof("peer %s has been synced successfully", peerName)

	// add route for target peer
	if errRoute := configHostRoutingRules(cachedPeer.Spec.PodCIDR, known.Add); errRoute != nil {
		klog.Infof("add route failed for %v", cachedPeer)
		return &failedPeriod, errRoute
	}

	// 需要回写peer
	if noCIDR {
		_, err = ict.fleetboardClient.FleetboardV1alpha1().Peers(namespace).Update(context.TODO(),
			cachedPeer, metav1.UpdateOptions{})
		if err != nil {
			return &failedPeriod, err
		}
	}
	return nil, nil
}

func (ict *InterClusterTunnelController) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	klog.Info("Starting inter cluster tunnel controller...")
	err := ict.ApplyPeerConfig()
	if err != nil {
		klog.Errorf("can't create or update peer in hub.")
		return
	}
	utils.UpdatePodLabels(ict.localK8sClient, ict.tunnel.Spec.PodName, true)
	ict.fleetboardFactory.Start(ctx.Done())
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		ict.yachtController.Run(ctx)
	}, time.Duration(0))
}

func (ict *InterClusterTunnelController) ApplyPeerConfig() error {
	w := ict.tunnel
	peer := &v1alpha1app.Peer{
		Spec: v1alpha1app.PeerSpec{
			ClusterID: w.Spec.ClusterID,
			PodCIDR:   []string{w.Spec.CIDR},
			Endpoint:  w.Spec.Endpoint,
			IsHub:     w.Spec.AsHub,
			Port:      known.UDPPort,
			IsPublic:  len(w.Spec.Endpoint) != 0,
			PublicKey: w.Keys.PublicKey.String(),
		},
	}
	peer.Namespace = w.Spec.ShareNamespace
	peer.Name = w.Spec.ClusterID
	return utils.ApplyPeerWithRetry(ict.fleetboardClient, peer)
}

func (ict *InterClusterTunnelController) RecycleAllResources() {
	for _, peer := range ict.tunnel.GetAllExistingInterConnection() {
		if _, err := ict.RecyclePeer(peer); err != nil {
			klog.Errorf("can't remove peer %s", peer.Name)
		}
	}
}

func configHostRoutingRules(cidrs []string, operation known.RouteOperation) error {
	klog.Infof("prepare to %v route with %s", operation, cidrs)
	var ifaceIndex int
	if wg, err := net.InterfaceByName(known.DefaultDeviceName); err == nil {
		ifaceIndex = wg.Index
	} else {
		klog.Errorf("%s not found in fleetboard.", known.DefaultDeviceName)
		return err
	}

	for _, cidr := range cidrs {
		_, dst, err := net.ParseCIDR(cidr)
		if err != nil {
			klog.Errorf("Can't parse cidr %s as route dst", cidr)
			return err
		}
		route := netlink.Route{
			Dst:       dst,
			LinkIndex: ifaceIndex,
			Protocol:  4,
			Table:     0,
		}
		route.Scope = unix.RT_SCOPE_LINK
		if operation == known.Add {
			err = netlink.RouteAdd(&route)
			if err != nil && !os.IsExist(err) {
				return err
			}
		} else {
			err = netlink.RouteDel(&route)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
