package controller

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
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/dixudx/yacht"
	v1alpha1app "github.com/nauti-io/nauti/pkg/apis/octopus.io/v1alpha1"
	octopusinformers "github.com/nauti-io/nauti/pkg/generated/informers/externalversions"
	"github.com/nauti-io/nauti/pkg/generated/listers/octopus.io/v1alpha1"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/nauti-io/nauti/utils"
	"github.com/vishvananda/netlink"
)

type PeerController struct {
	yachtController *yacht.Controller
	// specific namespace.
	peerLister     v1alpha1.PeerLister
	octopusFactory octopusinformers.SharedInformerFactory
	tunnel         *Wireguard
	spec           *known.Specification
}

func NewPeerController(spec known.Specification, w *Wireguard,
	octopusFactory octopusinformers.SharedInformerFactory) (*PeerController, error) {
	peerController := &PeerController{
		peerLister:     octopusFactory.Octopus().V1alpha1().Peers().Lister(),
		octopusFactory: octopusFactory,
		tunnel:         w,
		spec:           &spec,
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
			// klog.Infof("we got a peer connection %v", tempObj)
			if tempObj != nil {
				newPeer := tempObj.(*v1alpha1app.Peer)
				// hub connect with nohub, nohub connect with hub.
				// make sure there is only ONE Hub.
				// TODO should we create tunnelManager for public child cluster in hub?
				if newPeer.Spec.IsHub || (len(newPeer.Spec.Endpoint) != 0 && newPeer.Spec.IsPublic) {
					return !spec.IsHub, nil
				} else {
					// child cluster without public ip
					return spec.IsHub || len(spec.Endpoint) != 0, nil
				}
			}
			return false, nil
		})
	_, err := peerInformer.Informer().AddEventHandler(yachtController.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	peerController.yachtController = yachtController
	return peerController, nil
}

func (p *PeerController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	failedPeriod := 2 * time.Second
	key := obj.(string)
	namespace, peerName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, nil
	}

	noCIDR := false
	hubNotExist := false
	cachedPeer, err := p.peerLister.Peers(namespace).Get(peerName)
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
		// TODO try to recycle peer in this cnf client.
		var oldKey wgtypes.Key
		if oldKey, err = wgtypes.ParseKey(cachedPeer.Spec.PublicKey); err != nil {
			klog.Infof("can't find key for %s with key %s", peerName, cachedPeer.Spec.PublicKey)
			return &failedPeriod, err
		}
		if p.tunnel.RemoveInterClusterTunnel(&oldKey) != nil {
			return &failedPeriod, err
		}
		if errRemoveRoute := configHostRoutingRules(cachedPeer.Spec.PodCIDR, known.Delete); errRemoveRoute != nil {
			klog.Infof("delete route failed for %v", cachedPeer)
			return &failedPeriod, errRemoveRoute
		}
		klog.Infof("peer %s has been recycled successfully", peerName)
		return nil, nil
	}

	if !p.spec.IsHub {
		// just cluster, only wait if the coming peer has no cidr.
		if len(cachedPeer.Spec.PodCIDR) == 0 || len(cachedPeer.Spec.PodCIDR[0]) == 0 {
			return &failedPeriod, errors.NewServiceUnavailable("cidr is not allocated.")
		}
		// other child cluster has public ip.
		if cachedPeer.Name != known.HubClusterName {
			if annoError := addAnnotationToSelf(p.tunnel.k8sClient, known.DaemonCIDR, cachedPeer.Spec.PodCIDR[0],
				true); annoError != nil {
				return &failedPeriod, errors.NewServiceUnavailable("cidr is not allocated.")
			}
		}
	} else {
		if len(cachedPeer.Spec.PodCIDR) == 0 || len(cachedPeer.Spec.PodCIDR[0]) == 0 {
			//  prepare data...
			existingCIDR := make([]string, 0)
			noCIDR = true
			if peerList, errListPeer := p.peerLister.Peers(namespace).List(labels.Everything()); errListPeer == nil {
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
			cachedPeer.Spec.PodCIDR[0], err = utils.FindAvailableCIDR(p.spec.CIDR[0], existingCIDR, 16)
			if err != nil {
				klog.Infof("allocate peer cidr failed %v", err)
				return &failedPeriod, err
			}
		}
	}

	if errAddPeer := p.tunnel.AddInterClusterTunnel(cachedPeer); errAddPeer != nil {
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
		_, err = p.tunnel.OctopusClient.OctopusV1alpha1().Peers(namespace).Update(context.TODO(),
			cachedPeer, metav1.UpdateOptions{})
		if err != nil {
			return &failedPeriod, err
		}
	}
	return nil, nil
}

func (p *PeerController) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	klog.Info("Starting inter cluster tunnel controller...")
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		p.yachtController.Run(ctx)
	}, time.Duration(0))
}

func configHostRoutingRules(cidrs []string, operation known.RouteOperation) error {
	var ifaceIndex int
	if wg, err := net.InterfaceByName(known.DefaultDeviceName); err == nil {
		ifaceIndex = wg.Index
	} else {
		klog.Errorf("%s not found in octopus.", known.DefaultDeviceName)
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
