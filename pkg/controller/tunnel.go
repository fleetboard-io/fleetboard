package controller

import (
	"fmt"
	"net"
	"os"
	"sync"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/nauti-io/nauti/pkg/apis/octopus.io/v1alpha1"
	"github.com/nauti-io/nauti/pkg/generated/clientset/versioned"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/nauti-io/nauti/utils"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

type managedKeys struct {
	psk        wgtypes.Key
	privateKey wgtypes.Key
	publicKey  wgtypes.Key
}

type DaemonNRITunnelConfig struct {
	nodeID        string
	podID         string
	endpointIP    string
	secondaryCIDR string
	port          int
	PublicKey     string `json:"public_key"` // wire-guard public key
}

type Wireguard struct {
	interConnections map[string]*v1alpha1.Peer         // clusterID -> remote ep connection
	innerConnections map[string]*DaemonNRITunnelConfig // nodeID -> inner cluster connection
	mutex            sync.Mutex
	link             netlink.Link // your link
	spec             *known.Specification
	client           *wgctrl.Client
	keys             *managedKeys
	StopCh           <-chan struct{}
	octopusClient    *versioned.Clientset
}

func DaemonConfigFromPodAnntotation(pod *v1.Pod) *DaemonNRITunnelConfig {
	return &DaemonNRITunnelConfig{
		nodeID:        pod.Spec.NodeName,
		podID:         pod.Name,
		endpointIP:    getEth0IP(pod),
		secondaryCIDR: getSpecificAnnotation(pod, known.DaemonCIDR),
		port:          31080,
		PublicKey:     getSpecificAnnotation(pod, known.PublicKey),
	}
}

func NewTunnel(octopusClient *versioned.Clientset, spec *known.Specification, done <-chan struct{},
) (*Wireguard, error) {
	var err error

	w := &Wireguard{
		interConnections: make(map[string]*v1alpha1.Peer),
		innerConnections: make(map[string]*DaemonNRITunnelConfig),
		StopCh:           done,
		octopusClient:    octopusClient,
		keys:             &managedKeys{},
		spec:             spec,
	}

	if err = w.setWGLink(); err != nil {
		return nil, errors.Wrap(err, "failed to add WireGuard link")
	}

	// Create the wireguard controller.
	if w.client, err = wgctrl.New(); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("wgctrl is not available on this system")
		}

		return nil, errors.Wrap(err, "failed to open wgctl client")
	}

	defer func() {
		if err != nil {
			if e := w.client.Close(); e != nil {
				klog.Errorf("failed to close wgctrl client: %v", e)
			}
			w.client = nil
		}
	}()

	// set wire-guard keys.
	if err = w.setKeyPair(); err != nil {
		return nil, err
	}
	// Configure the device - still not up.
	peerConfigs := make([]wgtypes.PeerConfig, 0)
	cfg := wgtypes.Config{
		PrivateKey:   &w.keys.privateKey,
		ListenPort:   ptr.To(UDPPort),
		FirewallMark: nil,
		ReplacePeers: true,
		Peers:        peerConfigs,
	}

	if err = w.client.ConfigureDevice(DefaultDeviceName, cfg); err != nil {
		return nil, errors.Wrap(err, "failed to configure WireGuard device")
	}

	return w, err
}

func (w *Wireguard) Init() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	klog.Infof("Initializing WireGuard device for cluster %s", w.spec.ClusterID)

	l, err := net.InterfaceByName(DefaultDeviceName)
	if err != nil {
		return errors.Wrapf(err, "cannot get wireguard link by name %s", DefaultDeviceName)
	}

	d, err := w.client.Device(DefaultDeviceName)
	if err != nil {
		return errors.Wrap(err, "wgctrl cannot find WireGuard device")
	}

	// IP link set $DefaultDeviceName up.
	if err := netlink.LinkSetUp(w.link); err != nil {
		return errors.Wrap(err, "failed to bring up WireGuard device")
	}

	klog.Infof("WireGuard device %s, is up on i/f number %d, listening on port :%d, with key %s",
		w.link.Attrs().Name, l.Index, d.ListenPort, d.PublicKey)

	peer := &v1alpha1.Peer{
		Spec: v1alpha1.PeerSpec{
			ClusterID: w.spec.ClusterID,
			PodCIDR:   w.spec.CIDR,
			Endpoint:  w.spec.Endpoint,
			IsHub:     w.spec.IsHub,
			Port:      UDPPort,
			IsPublic:  len(w.spec.Endpoint) != 0,
			PublicKey: w.keys.publicKey.String(),
		},
	}
	peer.Namespace = w.spec.ShareNamespace
	peer.Name = w.spec.ClusterID
	return utils.ApplyPeerWithRetry(w.octopusClient, peer)
}

func (w *Wireguard) Cleanup() error {
	// return utils.DeletePeerWithRetry(w.octopusClient, w.spec.ClusterID, w.spec.ShareNamespace)
	// it pretty hard to handle the case, when we update the deployment of the cnf pod, as to roll-update mechanism.
	return nil
}
