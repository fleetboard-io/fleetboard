package tunnel

import (
	"net"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/klog/v2"

	"github.com/fleetboard-io/fleetboard/pkg/apis/fleetboard.io/v1alpha1"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

// Create new wg link and assign addr from local subnets.
func (w *Wireguard) setWGLink() error {
	// delete existing wg device if needed
	if link, err := netlink.LinkByName(known.DefaultDeviceName); err == nil {
		// delete existing device
		if err := netlink.LinkDel(link); err != nil {
			return errors.Wrap(err, "failed to delete existing WireGuard device")
		}
	}

	// Create the wg device (ip link add dev $DefaultDeviceName type wireguard).
	la := netlink.NewLinkAttrs()
	la.Name = known.DefaultDeviceName
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

func (w *Wireguard) RemoveInterClusterTunnel(key *wgtypes.Key) error {
	klog.Infof("Removing WireGuard peer with key %s", key)

	peerCfg := []wgtypes.PeerConfig{
		{
			PublicKey: *key,
			Remove:    true,
		},
	}
	err := w.client.ConfigureDevice(known.DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		return errors.Wrapf(err, "failed to remove WireGuard peer with key %s", key)
	}

	klog.Infof("Done removing WireGuard peer with key %s", key)

	return nil
}

func (w *Wireguard) AddInterClusterTunnel(peer *v1alpha1.Peer) error {
	var endpoint *net.UDPAddr
	if w.Spec.ClusterID == peer.Spec.ClusterID {
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
	w.Lock()
	defer w.Unlock()

	// Delete or update old peers for ClusterID.
	oldCon, found := w.interConnections[peer.Spec.ClusterID]
	if found {
		if oldKey, e := wgtypes.ParseKey(oldCon.Spec.PublicKey); e == nil {
			// because every time we change the public key.
			if oldKey.String() == remoteKey.String() {
				// Existing connection, update status and skip.
				klog.Infof("Skipping connect for existing peer key %s", oldKey)
				return nil
			}
			// new peer will take over subnets so can ignore error
			_ = w.RemoveInterClusterTunnel(&oldKey)
		}

		delete(w.interConnections, peer.Spec.ClusterID)
	}

	// create connection, overwrite existing connection
	klog.Infof("Adding connection for cluster %s, with allowed ips %s,"+
		" %v", peer.Spec.ClusterID, allowedIPs, peer)
	w.interConnections[peer.Spec.ClusterID] = peer

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

	err = w.client.ConfigureDevice(known.DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure peer")
	}

	klog.Infof("Done connecting endpoint peer %s@%s", remoteKey, remoteIP)
	return nil
}

func (w *Wireguard) RemoveInnerClusterTunnel(key *wgtypes.Key) error {
	klog.Infof("Removing WireGuard peer with key %s", key)
	peerCfg := []wgtypes.PeerConfig{
		{
			PublicKey: *key,
			Remove:    true,
		},
	}
	err := w.client.ConfigureDevice(known.DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		return errors.Wrapf(err, "Failed to remove wireGuard connection inner cluster with key %s", key)
	}

	klog.Infof("Done removing wireGuard connection inner cluster with key %s", key)

	return nil
}

func (w *Wireguard) AddInnerClusterTunnel(daemonPeerConfig *DaemonCNFTunnelConfig) error {
	var endpoint *net.UDPAddr
	// Parse remote addresses and allowed IPs.
	remoteIP := net.ParseIP(daemonPeerConfig.endpointIP)
	remotePort := daemonPeerConfig.port
	if remoteIP == nil {
		return errors.Errorf("invalid eth0 IP '%s' of pod %s on node %s",
			daemonPeerConfig.endpointIP, daemonPeerConfig.PodID, daemonPeerConfig.NodeID)
	} else {
		endpoint = &net.UDPAddr{
			IP:   remoteIP,
			Port: remotePort,
		}
	}

	allowedIPs := parseSubnets(daemonPeerConfig.SecondaryCIDR)

	// Parse remote public key.
	if len(daemonPeerConfig.PublicKey) == 0 {
		return errors.Errorf("invalid empty public key of pod %s on node %s", daemonPeerConfig.PodID, daemonPeerConfig.NodeID)
	}
	remoteKey, err := wgtypes.ParseKey(daemonPeerConfig.PublicKey[0])
	if err != nil {
		return errors.Wrap(err, "failed to parse daemonPeerConfig public key")
	}

	klog.Infof("Connecting daemon nciri endpoint %s with publicKey %s",
		daemonPeerConfig.NodeID, remoteKey)
	w.Lock()
	defer w.Unlock()

	// Delete or update old peers for ClusterID.
	oldCon, found := w.innerConnections[daemonPeerConfig.NodeID]
	if found {
		if oldKey, e := wgtypes.ParseKey(oldCon.PublicKey[0]); e == nil {
			// because every time when cnf pod restart it will change the public key and the tunnel should be re-build.
			if oldKey.String() == remoteKey.String() {
				// Existing connection, update status and skip.
				klog.Infof("Skipping connect for existing daemonPeerConfig key %s", oldKey)
				return nil
			}
			// new daemonPeerConfig will take over subnets so can ignore error
			_ = w.RemoveInnerClusterTunnel(&oldKey)
		}

		delete(w.innerConnections, daemonPeerConfig.NodeID)
	}

	// create connection, overwrite existing connection
	klog.Infof("Adding inner cluster tunnel connection for node %s, %v", daemonPeerConfig.NodeID, daemonPeerConfig)
	w.innerConnections[daemonPeerConfig.NodeID] = daemonPeerConfig

	// configure daemonPeerConfig 10s default todo make it configurable.
	ka := 10 * time.Second
	peerCfg := []wgtypes.PeerConfig{{
		PublicKey:                   remoteKey,
		Remove:                      false,
		UpdateOnly:                  false,
		Endpoint:                    endpoint,
		PersistentKeepaliveInterval: &ka,
		ReplaceAllowedIPs:           true,
		AllowedIPs:                  allowedIPs,
	}}

	err = w.client.ConfigureDevice(known.DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure daemonPeerConfig")
	}

	klog.Infof("Done connecting endpoint daemonPeerConfig %s@%s", remoteKey, remoteIP)
	return nil
}

func (w *Wireguard) setKeyPair() error {
	var err error
	// Generate local Keys and set public key in BackendConfig.
	var psk, priKey, pubKey wgtypes.Key

	if psk, err = wgtypes.GenerateKey(); err != nil {
		return errors.Wrap(err, "error generating pre-shared key")
	}

	w.Keys.psk = psk

	if priKey, err = wgtypes.GeneratePrivateKey(); err != nil {
		return errors.Wrap(err, "error generating private key")
	}
	w.Keys.privateKey = priKey

	pubKey = priKey.PublicKey()
	w.Keys.PublicKey = pubKey
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
