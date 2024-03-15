package controller

import (
	"net"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/pkg/apis/octopus.io/v1alpha1"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

const (
	// DefaultDeviceName specifies name of WireGuard network device.
	DefaultDeviceName = "wg0"

	UDPPort = 31820
)

// Create new wg link and assign addr from local subnets.
func (w *wireguard) setWGLink() error {
	// delete existing wg device if needed
	if link, err := netlink.LinkByName(DefaultDeviceName); err == nil {
		// delete existing device
		if err := netlink.LinkDel(link); err != nil {
			return errors.Wrap(err, "failed to delete existing WireGuard device")
		}
	}

	// Create the wg device (ip link add dev $DefaultDeviceName type wireguard).
	la := netlink.NewLinkAttrs()
	la.Name = DefaultDeviceName
	link := &netlink.GenericLink{
		LinkAttrs: la,
		LinkType:  "wireguard",
	}

	if err := netlink.LinkAdd(link); err == nil {
		w.link = link
	} else {
		return errors.Wrap(err, "failed to add WireGuard device")
	}

	return nil
}

func (w *wireguard) RemovePeer(key *wgtypes.Key) error {
	klog.Infof("Removing WireGuard peer with key %s", key)

	peerCfg := []wgtypes.PeerConfig{
		{
			PublicKey: *key,
			Remove:    true,
		},
	}
	err := w.client.ConfigureDevice(DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		return errors.Wrapf(err, "failed to remove WireGuard peer with key %s", key)
	}

	klog.Infof("Done removing WireGuard peer with key %s", key)

	return nil
}

func (w *wireguard) AddPeer(peer *v1alpha1.Peer) error {
	var endpoint *net.UDPAddr
	if w.spec.ClusterID == peer.Spec.ClusterID {
		klog.Infof("Will not connect to self")
		return nil
	}

	// Parse remote addresses and allowed IPs.
	remoteIP := net.ParseIP(peer.Spec.Endpoint)
	remotePort := peer.Spec.Port
	if remoteIP == nil {
		klog.Infof("failed to parse remote IP %s, never mind just ignore.", peer.Spec.Endpoint)
		endpoint = nil
	} else {
		endpoint = &net.UDPAddr{
			IP:   remoteIP,
			Port: remotePort,
		}
	}

	allowedIPs := parseSubnets(peer.Spec.PodCIDR)

	// Parse remote public key.
	remoteKey, err := wgtypes.ParseKey(peer.Spec.PublicKey)
	if err != nil {
		return errors.Wrap(err, "failed to parse peer public key")
	}

	klog.Infof("Connecting cluster %s endpoint %s with publicKey %s",
		peer.Spec.ClusterID, remoteIP, remoteKey)
	w.mutex.Lock()
	defer w.mutex.Unlock()

	// Delete or update old peers for ClusterID.
	oldCon, found := w.connections[peer.Spec.ClusterID]
	if found {
		if oldKey, err := wgtypes.ParseKey(oldCon.Spec.PublicKey); err == nil {
			// because every time we change the public key.
			if oldKey.String() == remoteKey.String() {
				// Existing connection, update status and skip.
				klog.Infof("Skipping connect for existing peer key %s", oldKey)
				return nil
			}
			// new peer will take over subnets so can ignore error
			_ = w.RemovePeer(&oldKey)
		}

		delete(w.connections, peer.Spec.ClusterID)
	}

	// create connection, overwrite existing connection
	klog.Infof("Adding connection for cluster %s, %v", peer.Spec.ClusterID, peer)
	w.connections[peer.Spec.ClusterID] = peer

	// configure peer 10s default todo make it configurable.
	ka := 10 * time.Second
	peerCfg := []wgtypes.PeerConfig{{
		PublicKey:  remoteKey,
		Remove:     false,
		UpdateOnly: false,
		// PresharedKey: w.psk, remove psk for now, because we haven't figure out how to keep and transfer it.
		Endpoint:                    endpoint,
		PersistentKeepaliveInterval: &ka,
		ReplaceAllowedIPs:           true,
		AllowedIPs:                  allowedIPs,
	}}

	err = w.client.ConfigureDevice(DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure peer")
	}

	klog.Infof("Done connecting endpoint peer %s@%s", remoteKey, remoteIP)
	return nil
}

func (w *wireguard) setKeyPair() error {
	var err error
	// Generate local keys and set public key in BackendConfig.
	var psk, priKey, pubKey wgtypes.Key

	if psk, err = wgtypes.GenerateKey(); err != nil {
		return errors.Wrap(err, "error generating pre-shared key")
	}

	w.keys.psk = psk

	if priKey, err = wgtypes.GeneratePrivateKey(); err != nil {
		return errors.Wrap(err, "error generating private key")
	}
	w.keys.privateKey = priKey

	pubKey = priKey.PublicKey()
	w.keys.publicKey = pubKey
	return nil
}

// Parse CIDR string and skip errors.
func parseSubnets(subnets []string) []net.IPNet {
	nets := make([]net.IPNet, 0, len(subnets))

	for _, sn := range subnets {
		_, cidr, err := net.ParseCIDR(sn)
		if err != nil {
			// This should not happen. Log and continue.
			klog.Errorf("failed to parse subnet %s", sn)
			continue
		}

		nets = append(nets, *cidr)
	}

	return nets
}
