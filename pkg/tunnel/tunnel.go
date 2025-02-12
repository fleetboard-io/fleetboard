package tunnel

import (
	"fmt"
	"net"
	"os"
	"sync"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/fleetboard-io/fleetboard/pkg/apis/fleetboard.io/v1alpha1"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/utils"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

type managedKeys struct {
	psk        wgtypes.Key
	privateKey wgtypes.Key
	PublicKey  wgtypes.Key
}

type DaemonCNFTunnelConfig struct {
	NodeID        string
	PodID         string
	endpointIP    string
	SecondaryCIDR []string
	ServiceCIDR   []string
	port          int
	PublicKey     []string `json:"public_key"` // wire-guard public key
}

type Wireguard struct {
	interConnections map[string]*v1alpha1.Peer         // clusterID -> remote ep connection
	innerConnections map[string]*DaemonCNFTunnelConfig // NodeID -> inner cluster connection
	sync.Mutex
	link   netlink.Link // your link
	Spec   *Specification
	client *wgctrl.Client
	Keys   *managedKeys
}

func (w *Wireguard) GetAllExistingInnerConnection() map[string]*DaemonCNFTunnelConfig {
	return w.innerConnections
}

func (w *Wireguard) GetAllExistingInterConnection() map[string]*v1alpha1.Peer {
	return w.interConnections
}

func (w *Wireguard) GetExistingInnerConnection(nodeID string) (*DaemonCNFTunnelConfig, bool) {
	w.Lock()
	defer w.Unlock()
	config, found := w.innerConnections[nodeID]
	return config, found
}

func (w *Wireguard) DeleteExistingInnerConnection(nodeID string) {
	w.Lock()
	defer w.Unlock()
	delete(w.innerConnections, nodeID)
}

func DaemonConfigFromPod(pod *v1.Pod, isLeader bool) *DaemonCNFTunnelConfig {
	daemonConfig := &DaemonCNFTunnelConfig{
		NodeID:        pod.Spec.NodeName,
		PodID:         pod.Name,
		endpointIP:    utils.GetEth0IP(pod),
		SecondaryCIDR: utils.GetSpecificAnnotation(pod, known.FleetboardNodeCIDR),
		ServiceCIDR:   utils.GetSpecificAnnotation(pod, known.FleetboardServiceCIDR),
		port:          known.UDPPort,
		PublicKey:     utils.GetSpecificAnnotation(pod, known.PublicKey),
	}
	if !isLeader {
		daemonConfig.SecondaryCIDR = utils.GetSpecificAnnotation(pod, known.FleetboardTunnelCIDR)
	}
	return daemonConfig
}

func NewTunnel(spec *Specification) (*Wireguard, error) {
	var err error

	w := &Wireguard{
		interConnections: make(map[string]*v1alpha1.Peer),
		innerConnections: make(map[string]*DaemonCNFTunnelConfig),
		Keys:             &managedKeys{},
		Spec:             spec,
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

	// set wire-guard Keys.
	if err = w.setKeyPair(); err != nil {
		return nil, err
	}
	// Configure the device - still not up.
	peerConfigs := make([]wgtypes.PeerConfig, 0)
	cfg := wgtypes.Config{
		PrivateKey:   &w.Keys.privateKey,
		ListenPort:   ptr.To(known.UDPPort),
		FirewallMark: nil,
		ReplacePeers: true,
		Peers:        peerConfigs,
	}

	if err = w.client.ConfigureDevice(known.DefaultDeviceName, cfg); err != nil {
		return nil, errors.Wrap(err, "failed to configure WireGuard device")
	}

	return w, err
}

func (w *Wireguard) Init(client *kubernetes.Clientset) error {
	w.Lock()
	defer w.Unlock()

	klog.Info("Initializing WireGuard device...")

	l, err := net.InterfaceByName(known.DefaultDeviceName)
	if err != nil {
		return errors.Wrapf(err, "cannot get wireguard link by name %s", known.DefaultDeviceName)
	}

	d, err := w.client.Device(known.DefaultDeviceName)
	if err != nil {
		return errors.Wrap(err, "wgctrl cannot find WireGuard device")
	}

	// IP link set $DefaultDeviceName up.
	if upErr := netlink.LinkSetUp(w.link); upErr != nil {
		return errors.Wrap(upErr, "failed to bring up WireGuard device")
	}

	klog.Infof("WireGuard device %s, is up on i/f number %d, listening on port :%d, with key %s",
		w.link.Attrs().Name, l.Index, d.ListenPort, d.PublicKey)

	return utils.AddAnnotationToSelf(client, known.PublicKey, w.Keys.PublicKey.String(), true)
}

func CreateAndUpTunnel(k8sClient *kubernetes.Clientset, agentSpec *Specification) (*Wireguard, error) {
	w, err := NewTunnel(agentSpec)
	if err != nil {
		klog.Fatal(err)
		return nil, err
	}
	// up the interface.
	if errInit := w.Init(k8sClient); errInit != nil {
		klog.Fatal(errInit)
		return nil, errInit
	}
	return w, nil
}

func (w *Wireguard) Cleanup() error {
	return nil
}
