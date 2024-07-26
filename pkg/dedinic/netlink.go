package dedinic

import (
	"fmt"
	"net"
	"strings"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/utils"
)

func setupVethPair(containerID, ifName string) (string, string, error) {
	var err error
	hostNicName, containerNicName := generateNicName(containerID, ifName)
	// Create a veth pair, put one end to container ,the other to CNF container
	veth := netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: hostNicName}, PeerName: containerNicName}
	if err = netlink.LinkAdd(&veth); err != nil {
		if err = netlink.LinkDel(&veth); err != nil {
			klog.Errorf("failed to delete veth %v", err)
			return "", "", err
		}
		return "", "", fmt.Errorf("failed to create veth for %v", err)
	}
	return hostNicName, containerNicName, nil
}

func generateNicName(containerID, ifname string) (string, string) {
	if ifname == "eth0" {
		return fmt.Sprintf("%s_h", containerID[0:12]), fmt.Sprintf("%s_c", containerID[0:12])
	}
	// The nic name is 14 length and have prefix pod in the Kubevirt v1.0.0
	if strings.HasPrefix(ifname, "pod") && len(ifname) == 14 {
		ifname = ifname[3 : len(ifname)-4]
		return fmt.Sprintf("%s_%s_h", containerID[0:12-len(ifname)], ifname),
			fmt.Sprintf("%s_%s_c", containerID[0:12-len(ifname)], ifname)
	}
	return fmt.Sprintf("%s_%s_h", containerID[0:12-len(ifname)], ifname),
		fmt.Sprintf("%s_%s_c", containerID[0:12-len(ifname)], ifname)
}
func CreateBridge(bridgeName string) error {
	// Create a new bridge
	bridge := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: bridgeName}}
	err := netlink.LinkDel(bridge)
	if err != nil {
		klog.Errorf("remove exist link failed: %v", err)
	}
	if err = netlink.LinkAdd(bridge); err != nil {
		return fmt.Errorf("could not add %s: %v", bridgeName, err)
	}

	// Find the bridge link
	bridgeLink, err := netlink.LinkByName(bridgeName)
	if err != nil {
		return fmt.Errorf("could not find %s: %v", bridgeName, err)
	}

	// Bring the bridge up
	if err = netlink.LinkSetUp(bridgeLink); err != nil {
		return fmt.Errorf("could not set up %s: %v", bridgeName, err)
	}

	CNFBridgeIP, err = utils.GetIndexIPFromCIDR(NodeCIDR, 1)
	if err != nil {
		return fmt.Errorf("get index ip failed: %v", err)
	}

	subNetMask, err := GetSubNetMask(NodeCIDR)
	if err != nil {
		return err
	}
	klog.Infof("nodeCIDR: %v, subnet: %v", NodeCIDR, subNetMask)
	err = configureNic(CNFBridgeName, fmt.Sprintf("%v/%v", CNFBridgeIP, subNetMask))
	if err != nil {
		return fmt.Errorf("br add ip failed: %v", err)
	}

	return nil
}

func GetSubNetMask(cidr string) (string, error) {
	subNet := strings.Split(cidr, "/")
	if len(subNet) < 2 {
		return "", fmt.Errorf("can not get subnet")
	}
	return subNet[1], nil
}

func addVethToBridge(vethName, bridgeName string) error {
	curNs, err := netns.Get()
	if err != nil {
		klog.Errorf("Failed to get current namespace: %v", err)
		return err
	}
	defer curNs.Close()
	klog.V(6).Infof("Current network namespace: %v\n", curNs)

	// Find the bridge link
	bridgeLink, err := netlink.LinkByName(bridgeName)
	if err != nil {
		return fmt.Errorf("could not find bridge %s: %v", bridgeName, err)
	}

	// Find the veth link
	vethLink, err := netlink.LinkByName(vethName)
	if err != nil {
		return fmt.Errorf("could not find veth %s: %v", vethName, err)
	}
	// Attach veth to the bridge
	if err := netlink.LinkSetMaster(vethLink, bridgeLink.(*netlink.Bridge)); err != nil {
		return fmt.Errorf("could not attach %s to bridge %s: %v", vethName, bridgeName, err)
	}
	// Bring up the veth pair
	if err := netlink.LinkSetUp(vethLink); err != nil {
		return fmt.Errorf("could not set up %s: %v", vethName, err)
	}

	return nil
}

func configureContainerNic(nicName, ifName, ipAddr string,
	routes []Route, netns ns.NetNS) error {
	var err error
	containerLink, err := netlink.LinkByName(nicName)
	if err != nil {
		return fmt.Errorf("can not find container nic %s: %v", nicName, err)
	}

	// Set link alias to its origin link name for fastpath to recognize and bypass netfilter
	if err = netlink.LinkSetAlias(containerLink, nicName); err != nil {
		klog.Errorf("failed to set link alias for container nic %s: %v", nicName, err)
		return err
	}

	if err = netlink.LinkSetNsFd(containerLink, int(netns.Fd())); err != nil {
		return fmt.Errorf("failed to move link to netns: %v", err)
	}

	return ns.WithNetNSPath(netns.Path(), func(_ ns.NetNS) error {
		if err = netlink.LinkSetName(containerLink, ifName); err != nil {
			return err
		}

		if err = configureNic(ifName, ipAddr); err != nil {
			return err
		}

		for _, r := range routes {
			var dst *net.IPNet
			if r.Destination != "" {
				if _, dst, err = net.ParseCIDR(r.Destination); err != nil {
					klog.Errorf("invalid route destination %s: %v", r.Destination, err)
					continue
				}
			}

			var gw net.IP
			if r.Gateway != "" {
				if gw = net.ParseIP(r.Gateway); gw == nil {
					klog.Errorf("invalid route gateway %s", r.Gateway)
					continue
				}
			}

			route := &netlink.Route{
				Dst:       dst,
				Gw:        gw,
				LinkIndex: containerLink.Attrs().Index,
			}
			if err = netlink.RouteReplace(route); err != nil {
				klog.Errorf("failed to add route %+v: %v", r, err)
			}
		}

		return nil
	})
}

func configureNic(link, ip string) error {
	nodeLink, err := netlink.LinkByName(link)
	if err != nil {
		return fmt.Errorf("can not find nic %s: %v", link, err)
	}

	if nodeLink.Attrs().OperState != netlink.OperUp {
		if err = netlink.LinkSetUp(nodeLink); err != nil {
			return fmt.Errorf("can not set node nic %s up: %v", link, err)
		}
	}

	ipDelMap := make(map[string]netlink.Addr)
	ipAddMap := make(map[string]netlink.Addr)
	ipAddrs, err := netlink.AddrList(nodeLink, unix.AF_UNSPEC)
	if err != nil {
		return fmt.Errorf("can not get addr %s: %v", nodeLink, err)
	}
	for _, ipAddr := range ipAddrs {
		if ipAddr.IP.IsLinkLocalUnicast() {
			// skip 169.254.0.0/16 and fe80::/10
			continue
		}
		ipDelMap[ipAddr.IPNet.String()] = ipAddr
	}

	for _, ipStr := range strings.Split(ip, ",") {
		// Do not reassign same address for link
		if _, ok := ipDelMap[ipStr]; ok {
			delete(ipDelMap, ipStr)
			continue
		}

		var ipAddr *netlink.Addr
		ipAddr, err = netlink.ParseAddr(ipStr)
		if err != nil {
			return fmt.Errorf("can not parse address %s: %v", ipStr, err)
		}
		ipAddMap[ipStr] = *ipAddr
	}

	for ip, address := range ipDelMap {
		addr := address
		klog.Infof("delete ip address %s on %s", ip, link)
		if err = netlink.AddrDel(nodeLink, &addr); err != nil {
			return fmt.Errorf("delete address %s: %v", addr, err)
		}
	}
	for ip, address := range ipAddMap {
		addr := address
		klog.Infof("add ip address %s to %s", ip, link)
		if err = netlink.AddrAdd(nodeLink, &addr); err != nil {
			return fmt.Errorf("can not add address %v to nic %s: %v", addr, link, err)
		}
	}

	return nil
}
