package controller

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/nauti-io/nauti/pkg/known"
	"github.com/nauti-io/nauti/utils"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/pkg/apis/octopus.io/v1alpha1"
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
	w.mutex.Lock()
	defer w.mutex.Unlock()

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
	klog.Infof("Adding connection for cluster %s, %v", peer.Spec.ClusterID, peer)
	w.interConnections[peer.Spec.ClusterID] = peer

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

func (w *Wireguard) AddInnerClusterTunnel(daemonPeerConfig *DaemonNRITunnelConfig) error {
	var endpoint *net.UDPAddr
	// should we connect daemon nri to cnf in same node?

	// Parse remote addresses and allowed IPs.
	remoteIP := net.ParseIP(daemonPeerConfig.endpointIP)
	remotePort := daemonPeerConfig.port
	if remoteIP == nil {
		klog.Infof("failed to parse pod %s on node %s eth0 IP.", daemonPeerConfig.podID, daemonPeerConfig.nodeID)
		return errors.New("failed to parse ")
	} else {
		endpoint = &net.UDPAddr{
			IP:   remoteIP,
			Port: remotePort,
		}
	}

	allowedIPs := parseSubnets(daemonPeerConfig.secondaryCIDR)

	// Parse remote public key.
	remoteKey, err := wgtypes.ParseKey(daemonPeerConfig.PublicKey[0])
	if err != nil {
		return errors.Wrap(err, "failed to parse daemonPeerConfig public key")
	}

	klog.Infof("Connecting daemon nri endpoint %s with publicKey %s",
		daemonPeerConfig.nodeID, remoteKey)
	w.mutex.Lock()
	defer w.mutex.Unlock()

	// Delete or update old peers for ClusterID.
	oldCon, found := w.innerConnections[daemonPeerConfig.nodeID]
	if found {
		if oldKey, e := wgtypes.ParseKey(oldCon.PublicKey[0]); e == nil {
			// because every time when nri pod restart it will change the public key and the tunnel should be re-build.
			if oldKey.String() == remoteKey.String() {
				// Existing connection, update status and skip.
				klog.Infof("Skipping connect for existing daemonPeerConfig key %s", oldKey)
				return nil
			}
			// new daemonPeerConfig will take over subnets so can ignore error
			_ = w.RemoveInterClusterTunnel(&oldKey)
		}

		delete(w.innerConnections, daemonPeerConfig.nodeID)
	}

	// create connection, overwrite existing connection
	klog.Infof("Adding inner cluster tunnel connection for node %s, %v", daemonPeerConfig.nodeID, daemonPeerConfig)
	w.innerConnections[daemonPeerConfig.nodeID] = daemonPeerConfig

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

func setSpecificAnnotation(client *kubernetes.Clientset, pod *v1.Pod, annotationKey, annotationValue string,
	override bool) error {
	annoChanged := true
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	annotationKey = fmt.Sprintf(annotationKey, known.NautiPrefix)

	existingValues, ok := pod.Annotations[annotationKey]
	if ok && !override {
		existingValuesSlice := strings.Split(existingValues, ",")
		if utils.ContainsString(existingValuesSlice, annotationValue) {
			annoChanged = false
		} else {
			pod.Annotations[annotationKey] = existingValues + "," + annotationValue
		}
	} else {
		pod.Annotations[annotationKey] = annotationValue
	}
	if annoChanged {
		_, err := client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// getSpecificAnnotation get DaemonCIDR from pod annotation return "" if is empty.
func getSpecificAnnotation(pod *v1.Pod, annotationKeys ...string) []string {
	annotations := pod.Annotations
	allAnnoValue := make([]string, 0)
	if annotations == nil {
		return allAnnoValue
	}

	for _, item := range annotationKeys {
		if val, ok := annotations[fmt.Sprintf(item, known.NautiPrefix)]; ok {
			existingValuesSlice := strings.Split(val, ",")
			allAnnoValue = append(allAnnoValue, existingValuesSlice...)
		}
	}

	return allAnnoValue
}

func hasIPChanged(oldPod, newPod *v1.Pod) bool {
	oldIP := getEth0IP(oldPod)
	newIP := getEth0IP(newPod)
	return oldIP != newIP
}

func getEth0IP(pod *v1.Pod) string {
	for _, podIP := range pod.Status.PodIPs {
		if podIP.IP != "" {
			return podIP.IP
		}
	}
	return ""
}

func isRunningAndHasIP(pod *v1.Pod) bool {
	if pod.Status.Phase == v1.PodRunning {
		for _, podIP := range pod.Status.PodIPs {
			if podIP.IP != "" {
				return true
			}
		}
	}
	return false
}

func addAnnotationToSelf(client *kubernetes.Clientset, annotationKey, annotationValue string, override bool) error {
	// Get the Pod's name and namespace from the environment variables
	podName := os.Getenv("POD_NAME")
	namespace := os.Getenv("POD_NAMESPACE")

	// Get the Pod
	pod, err := client.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	return setSpecificAnnotation(client, pod, annotationKey, annotationValue, override)
}
