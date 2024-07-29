package dedinic

import (
	"context"
	"fmt"
	"strings"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/pkg/known"
)

const cniConf = `{
    "cniVersion": "0.3.1",
    "name": "dedicate-cni",
    "ipam": {
        "type": "host-local",
        "ranges": [
            [
                {
                    "subnet": "%s"
                }
            ]
        ]
    }
}
`

type cniHandler struct {
	cniConfStr string
	kubeClient *kubernetes.Clientset
}

func createCniHandler(kubeClinet *kubernetes.Clientset) *cniHandler {
	ch := &cniHandler{
		cniConfStr: fmt.Sprintf(cniConf, NodeCIDR),
		kubeClient: kubeClinet,
	}
	return ch
}

func (ch cniHandler) handleAdd(rq *CniRequest) error {
	// do not handel the CNF pod self
	if isCNFSelf(rq.PodNamespace, rq.PodName) {
		return nil
	}
	klog.Infof("add port request: %v", rq)
	var err error

	ip, err := GetIP(rq, ch.cniConfStr)
	if err != nil {
		klog.Errorf("get ip failed: %v", err)
	} else {
		klog.Infof("pod ip info: %v", ip)
	}

	ipStr := ip.IPs[0].Address.String()
	route := Route{
		Destination: GlobalCIDR,
		Gateway:     CNFBridgeIP,
	}
	err = ch.configureNic(rq.NetNs, rq.ContainerID, rq.IfName, ipStr, []Route{route})
	if err != nil {
		klog.Errorf("add nic failed: %v", err)
	}
	// add IP to the pod annotation
	if err = ch.updateTheIPToPod(rq.PodName, rq.PodNamespace, strings.Split(ipStr, "/")[0]); err != nil {
		klog.Errorf("update annotaion failed: %v/%v", rq.PodNamespace, rq.PodName)
	}

	return err
}

func isCNFSelf(podNamespace, podName string) bool {
	if podName == CNFPodName && podNamespace == CNFPodNamespace {
		return true
	}
	return false
}

func (ch cniHandler) configureNic(netns, containerID,
	ifName, ip string, routes []Route) error {
	var err error
	var hostNicName, containerNicName string

	hostNicName, containerNicName, err = setupVethPair(containerID, ifName)
	if err != nil {
		klog.Errorf("failed to create veth pair %v", err)
		return err
	}

	if containerNicName == "" {
		return nil
	}

	podNS, err := ns.GetNS(netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", netns, err)
	}
	klog.Infof("hostnicname: %v", hostNicName)
	err = addVethToBridge(hostNicName, CNFBridgeName)
	if err != nil {
		klog.Errorf("add nic to bridge failed: %v", err)
	}
	return configureContainerNic(containerNicName, ifName, ip, routes, podNS)
}

func (ch cniHandler) handleDel(rq *CniRequest) error {
	klog.V(6).Infof("del nic for pod: %v/%v", rq.PodNamespace, rq.PodName)
	if isCNFSelf(rq.PodNamespace, rq.PodName) {
		br, err := netlink.LinkByName(CNFBridgeName)
		if err != nil {
			klog.Errorf("Failed to get bridge: %v", err)
			return err
		}
		if err := netlink.LinkDel(br); err != nil {
			klog.Errorf("Failed to delete bridge %s: %v", br, err)
			return err
		}
		return nil
	}

	err := DelIP(rq, ch.cniConfStr)
	if err != nil {
		klog.Errorf("del nic failed: %v", err)
		return err
	}

	return ch.deleteNic(rq.NetNs, rq.IfName)
}

func (ch cniHandler) deleteNic(nsPath, ifName string) error {
	klog.V(6).Infof("deleteNic nsPath: %s, ifName: %s", nsPath, ifName)

	podNs, err := netns.GetFromPath(nsPath)
	if err != nil {
		klog.Errorf("error get podNs: %v", err)
		return err
	}
	defer func(podNs *netns.NsHandle) {
		err = podNs.Close()
		if err != nil {
			klog.Errorf("close podNs failed: %v", err)
		}
	}(&podNs)

	origns, err := netns.Get()
	if err != nil {
		klog.Errorf("Failed to get current namespace: %v", err)
		return err
	}
	defer func(origns *netns.NsHandle) {
		err = origns.Close()
		if err != nil {
			klog.Errorf("close cnf netns failed: %v", err)
		}
	}(&origns)

	if err = netns.Set(podNs); err != nil {
		klog.Errorf("Failed to set namespace: %v", err)
		return err
	}
	defer func(ns netns.NsHandle) {
		err = netns.Set(ns)
		if err != nil {
			klog.Errorf("change namespace to cnf pod failed: %v", err)
		}
	}(origns)
	links, err := netlink.LinkList()
	if err != nil {
		klog.Errorf("Failed to list links: %v", err)
		return err
	}

	for _, link := range links {
		klog.V(6).Infof("links: %v/%v/%v", link.Attrs().NetNsID, link.Attrs().Name, link.Type())
	}

	var iface netlink.Link
	for _, link := range links {
		if link.Attrs().Name == ifName {
			iface = link
			break
		}
	}
	if iface == nil {
		klog.Infof("Interface %s not found", ifName)
		return nil
	}

	veth, ok := iface.(*netlink.Veth)
	if !ok {
		klog.Infof("Interface %s is not veth", ifName)
		return nil
	}
	peer, err := netlink.LinkByName(veth.PeerName)
	if err != nil {
		klog.Errorf("Failed to get peer link: %v", err)
		return err
	}
	klog.Infof("the peer name is %v", peer)
	if err := netlink.LinkDel(iface); err != nil {
		klog.Errorf("Failed to delete link %s: %v", ifName, err)
		return err
	}
	return nil
}

func (ch cniHandler) updateTheIPToPod(podName, podNamespace, ip string) error {
	// Get the Pod
	pod, err := ch.kubeClient.CoreV1().Pods(podNamespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}

	// Add an annotation to the Pod
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[known.DEDINICIP] = ip

	// Update the Pod
	_, err = ch.kubeClient.CoreV1().Pods(podNamespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("update IP to pod %v/%v annotation failed", podNamespace, podName)
	}
	return err
}
