package dedinic

import (
	"fmt"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/kubeovn/kube-ovn/pkg/request"
	"k8s.io/klog/v2"
)

type cniHandler struct {
}

func createCniHandler() *cniHandler {
	ch := &cniHandler{}
	return ch
}

func (ch cniHandler) handleAdd(rq *request.CniRequest) error {
	// do not handel the CNF pod self
	if rq.PodName == CNFPodName && rq.PodNamespace == CNFPodNamespace {
		return nil
	}
	klog.Infof("add port request: %v", rq)
	var err error

	cniConf := `{
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
	cniConfStr := fmt.Sprintf(cniConf, NodeCIDR)

	ip, err := GetIP(rq, cniConfStr)
	if err != nil {
		klog.Errorf("get ip failed: %v", err)
	} else {
		klog.Infof("pod ip info: %v", ip)
	}

	ipStr := ip.IPs[0].Address.String()
	route := request.Route{
		Destination: GlobalCIDR,
		Gateway:     CNFBridgeIP,
	}
	err = ch.configureNic(rq.NetNs, rq.ContainerID, rq.IfName, ipStr, []request.Route{route})
	if err != nil {
		klog.Errorf("add nic failed: %v", err)
	}

	return err
}

func (ch cniHandler) configureNic(netns, containerID,
	ifName, ip string, routes []request.Route) error {
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

func (ch cniHandler) handleDel(podRequest *request.CniRequest) error {
	return nil
}
