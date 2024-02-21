package dedinic

import (
	"encoding/json"
	"fmt"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/kubeovn/kube-ovn/pkg/ovs"
	"github.com/kubeovn/kube-ovn/pkg/request"
	"github.com/kubeovn/kube-ovn/pkg/util"
	"github.com/vishvananda/netlink"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"net"
	"strings"
	"time"
)

type cniHandler struct {
	//Config     *Configuration
	KubeClient kubernetes.Interface
	Controller *Controller
}

func createCniHandler(config *Configuration, controller *Controller) *cniHandler {
	ch := &cniHandler{
		KubeClient: config.KubeClient,
		Controller: controller,
	}
	return ch
}

func (ch cniHandler) handleAdd(podRequest *request.CniRequest) error {

	klog.Infof("add port request: %v", podRequest)

	var gatewayCheckMode int
	var (
		macAddr, ip, ipAddr, cidr, gw, subnet, ingress, egress, ifName, nicType    string
		podNicName, latency, limit, loss, jitter, u2oInterconnectionIP, oldPodName string
	)
	var routes []request.Route
	var isDefaultRoute bool
	var pod *v1.Pod
	var err error
	for i := 0; i < 20; i++ {
		if pod, err = ch.Controller.podsLister.Pods(podRequest.PodNamespace).Get(podRequest.PodName); err != nil {
			errMsg := fmt.Errorf("get pod %s/%s failed %v", podRequest.PodNamespace, podRequest.PodName, err)
			klog.Error(errMsg)
			return errMsg
		}
		if pod.Annotations[fmt.Sprintf(util.AllocatedAnnotationTemplate, podRequest.Provider)] != "true" {
			klog.Infof("wait address for pod %s/%s provider %s", podRequest.PodNamespace, podRequest.PodName, podRequest.Provider)
			time.Sleep(1 * time.Second)
			continue
		}

		if err := util.ValidatePodNetwork(pod.Annotations); err != nil {
			klog.Errorf("validate pod %s/%s failed, %v", podRequest.PodNamespace, podRequest.PodName, err)
			// wait controller assign an address
			time.Sleep(1 * time.Second)
			continue
		}
		ip = pod.Annotations[fmt.Sprintf(util.IPAddressAnnotationTemplate, podRequest.Provider)]
		cidr = pod.Annotations[fmt.Sprintf(util.CidrAnnotationTemplate, podRequest.Provider)]
		gw = pod.Annotations[fmt.Sprintf(util.GatewayAnnotationTemplate, podRequest.Provider)]
		subnet = pod.Annotations[fmt.Sprintf(util.LogicalSwitchAnnotationTemplate, podRequest.Provider)]

		ipAddr = util.GetIPAddrWithMask(ip, cidr)
		oldPodName = podRequest.PodName
		if s := pod.Annotations[fmt.Sprintf(util.RoutesAnnotationTemplate, podRequest.Provider)]; s != "" {
			if err = json.Unmarshal([]byte(s), &routes); err != nil {
				errMsg := fmt.Errorf("invalid routes for pod %s/%s: %v", pod.Namespace, pod.Name, err)
				klog.Error(errMsg)
				return errMsg
			}
		}
		if ifName = podRequest.IfName; ifName == "" {
			ifName = "eth-ovn"
		}
		isDefaultRoute = false

		break
	}

	if pod.Annotations[fmt.Sprintf(util.AllocatedAnnotationTemplate, podRequest.Provider)] != "true" {
		err := fmt.Errorf("no address allocated to pod %s/%s provider %s, please see kube-ovn-controller logs to find errors", pod.Namespace, pod.Name, podRequest.Provider)
		klog.Error(err)
		return err
	}

	if strings.HasSuffix(podRequest.Provider, util.OvnProvider) && subnet != "" {
		detectIPConflict := false
		var mtu int
		//mtu = ch.Config.MTU

		routes = append(podRequest.Routes, routes...)
		macAddr = pod.Annotations[fmt.Sprintf(util.MacAddressAnnotationTemplate, podRequest.Provider)]
		klog.Infof("create container interface %s mac %s, ip %s, cidr %s, gw %s, custom routes %v", ifName, macAddr, ipAddr, cidr, gw, routes)
		podNicName = ifName
		err = ch.configureNic(podRequest.PodName, podRequest.PodNamespace, podRequest.Provider, podRequest.NetNs, podRequest.ContainerID, podRequest.VfDriver, ifName, macAddr, mtu, ipAddr, gw, isDefaultRoute, detectIPConflict, routes, podRequest.DNS.Nameservers, podRequest.DNS.Search, ingress, egress, podRequest.DeviceID, nicType, latency, limit, loss, jitter, gatewayCheckMode, u2oInterconnectionIP, oldPodName)
		if err != nil {
			errMsg := fmt.Errorf("configure nic failed %v", err)
			klog.Error(errMsg)
			return errMsg
		}
	}

	response := &request.CniResponse{
		Protocol:   util.CheckProtocol(cidr),
		IPAddress:  ip,
		MacAddress: macAddr,
		CIDR:       cidr,
		PodNicName: podNicName,
	}
	if isDefaultRoute {
		response.Gateway = gw
	}
	return err
}

func (ch cniHandler) configureNic(podName, podNamespace, provider, netns, containerID,
	vfDriver, ifName, mac string, mtu int, ip, gateway string, isDefaultRoute,
	detectIPConflict bool, routes []request.Route, _, _ []string, ingress, egress,
	deviceID, nicType, latency, limit, loss, jitter string, gwCheckMode int, u2oInterconnectionIP, oldPodName string) error {
	var err error
	var hostNicName, containerNicName string

	hostNicName, containerNicName, err = setupVethPair(containerID, ifName, mtu)
	if err != nil {
		klog.Errorf("failed to create veth pair %v", err)
		return err
	}

	ipStr := util.GetIPWithoutMask(ip)
	ifaceID := ovs.PodNameToPortName(podName, podNamespace, provider)
	ovs.CleanDuplicatePort(ifaceID, hostNicName)
	// Add veth pair host end to ovs port
	output, err := ovs.Exec(ovs.MayExist, "add-port", "br-int", hostNicName, "--",
		"set", "interface", hostNicName, fmt.Sprintf("external_ids:iface-id=%s", ifaceID),
		fmt.Sprintf("external_ids:vendor=%s", util.CniTypeName),
		fmt.Sprintf("external_ids:pod_name=%s", podName),
		fmt.Sprintf("external_ids:pod_namespace=%s", podNamespace),
		fmt.Sprintf("external_ids:ip=%s", ipStr),
		fmt.Sprintf("external_ids:pod_netns=%s", netns))
	if err != nil {
		return fmt.Errorf("add nic to ovs failed %v: %q", err, output)
	}

	// add hostNicName and containerNicName into pod annotations
	// lsp and container nic must use same mac address, otherwise ovn will reject these packets by default
	macAddr, err := net.ParseMAC(mac)
	if err != nil {
		return fmt.Errorf("failed to parse mac %s %v", macAddr, err)
	}
	if err = configureHostNic(hostNicName); err != nil {
		return err
	}
	if err = ovs.SetInterfaceBandwidth(podName, podNamespace, ifaceID, egress, ingress); err != nil {
		return err
	}

	if err = ovs.SetNetemQos(podName, podNamespace, ifaceID, latency, limit, loss, jitter); err != nil {
		return err
	}

	if containerNicName == "" {
		return nil
	}
	isUserspaceDP, err := ovs.IsUserspaceDataPath()
	if err != nil {
		return err
	}
	if isUserspaceDP {
		// turn off tx checksum
		if err = turnOffNicTxChecksum(containerNicName); err != nil {
			return err
		}
	}

	podNS, err := ns.GetNS(netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", netns, err)
	}
	return configureContainerNic(containerNicName, ifName, ip, gateway, isDefaultRoute, detectIPConflict, routes, macAddr, podNS, mtu, nicType, gwCheckMode, u2oInterconnectionIP)
}

func configureHostNic(nicName string) error {
	hostLink, err := netlink.LinkByName(nicName)
	if err != nil {
		return fmt.Errorf("can not find host nic %s: %v", nicName, err)
	}

	if hostLink.Attrs().OperState != netlink.OperUp {
		if err = netlink.LinkSetUp(hostLink); err != nil {
			return fmt.Errorf("can not set host nic %s up: %v", nicName, err)
		}
	}
	if err = netlink.LinkSetTxQLen(hostLink, 1000); err != nil {
		return fmt.Errorf("can not set host nic %s qlen: %v", nicName, err)
	}

	return nil
}

func (ch cniHandler) handleDel(podRequest *request.CniRequest) error {

	pod, err := ch.Controller.podsLister.Pods(podRequest.PodNamespace).Get(podRequest.PodName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return err
		}

		errMsg := fmt.Errorf("parse del request failed %v", err)
		klog.Error(errMsg)
		return errMsg
	}

	klog.Infof("del port request: %v", podRequest)

	if pod.Annotations != nil && (podRequest.Provider == util.OvnProvider || podRequest.CniType == util.CniTypeName) {

		var nicType string

		nicType = pod.Annotations[fmt.Sprintf(util.PodNicAnnotationTemplate, podRequest.Provider)]
		vmName := pod.Annotations[fmt.Sprintf(util.VMTemplate, podRequest.Provider)]
		if vmName != "" {
			podRequest.PodName = vmName
		}

		err = ch.deleteNic(podRequest.PodName, podRequest.PodNamespace, podRequest.ContainerID, podRequest.NetNs, podRequest.DeviceID, podRequest.IfName, nicType, podRequest.Provider)
		if err != nil {
			errMsg := fmt.Errorf("del nic failed %v", err)
			klog.Error(errMsg)
			return errMsg
		}
	}

	return err
}

func (ch cniHandler) deleteNic(podName, podNamespace, containerID, netns, deviceID, ifName, nicType, provider string) error {
	var nicName string
	hostNicName, containerNicName := generateNicName(containerID, ifName)

	if nicType == util.InternalType {
		nicName = containerNicName
	} else {
		nicName = hostNicName
	}

	// Remove ovs port
	output, err := ovs.Exec(ovs.IfExists, "--with-iface", "del-port", "br-int", nicName)
	if err != nil {
		return fmt.Errorf("failed to delete ovs port %v, %q", err, output)
	}

	if err = ovs.ClearPodBandwidth(podName, podNamespace, ""); err != nil {
		return err
	}
	if err = ovs.ClearHtbQosQueue(podName, podNamespace, ""); err != nil {
		return err
	}

	hostLink, err := netlink.LinkByName(nicName)
	if err != nil {
		// If link already not exists, return quietly
		// E.g. Internal port had been deleted by Remove ovs port previously
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return nil
		}
		return fmt.Errorf("find host link %s failed %v", nicName, err)
	}

	hostLinkType := hostLink.Type()
	// Sometimes no deviceID input for vf nic, avoid delete vf nic.
	if hostLinkType == "veth" {
		if err = netlink.LinkDel(hostLink); err != nil {
			return fmt.Errorf("delete host link %s failed %v", hostLink, err)
		}
	}
	return nil
}
