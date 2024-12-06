package utils

import (
	"errors"
	"fmt"
	"math/big"
	"net"

	v1 "k8s.io/api/core/v1"
)

// GetIndexIPFromCIDR return index ip in the cidr, index start from 1 not 0, because 0 is not a valid ip.
func GetIndexIPFromCIDR(cidr string, index int) (string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	ipA := ip.Mask(ipnet.Mask)
	start := 0
	for start < index && ipnet.Contains(ipA) {
		start++
		inc(ipA)
	}
	if start != index {
		return "", errors.New("your index is out of the cidr")
	}
	// remove network address and broadcast address
	return ipA.String(), nil
}

func inc(ipA net.IP) {
	for j := len(ipA) - 1; j >= 0; j-- {
		ipA[j]++
		if ipA[j] > 0 {
			break
		}
	}
}

func FindTunnelAvailableCIDR(tunnelCIDR string, existingCIDRs []string) (string, error) {
	networkBits, err := divideTunnelNetwork(tunnelCIDR)
	if err != nil {
		return "", err
	}
	return findAvailableCIDR(tunnelCIDR, existingCIDRs, networkBits)
}

func FindClusterAvailableCIDR(clusterCIDR string, existingCIDRs []string) (string, error) {
	networkBits, err := divideClusterNetwork(clusterCIDR)
	if err != nil {
		return "", err
	}
	return findAvailableCIDR(clusterCIDR, existingCIDRs, networkBits)
}

/*
divideTunnelNetwork and divideClusterNetwork divide network cidr for peer clusters and nodes in cluster
as dynamically as possibly.
Generally speaking, divided into 4 parts by cidr size for clusters, nodes per cluster, and pods per node.

dividing table is as follows:
| network-cidr | host-bits | peer-cluster-bits | peer-cluster-cidr | node-pod-bits | node-cidr | cluster-node-bits |
| -----------: | --------: | ----------------: | ----------------: | ------------: | --------: | ----------------: |
|      /24~/21 |      8~11 |                 1 |           /25~/22 |             6 |       /26 |               1~4 |
|      /20~/15 |     12~17 |                 2 |           /22~/17 |             8 |       /24 |               2~7 |
|       /14~/9 |     18-23 |                 3 |           /17~/12 |             8 |       /24 |              7~12 |
|         >=/8 |      >=24 |                 4 |             >=/12 |            10 |       /22 |             10~18 |
*/
func divideTunnelNetwork(networkCIDR string) (subnetNetworkBits int, err error) {
	_, network, err := net.ParseCIDR(networkCIDR)
	if err != nil {
		return 0, err
	}

	networkBits, _ := network.Mask.Size()
	switch {
	case networkBits >= 21 && networkBits <= 24:
		subnetNetworkBits = networkBits + 1
	case networkBits >= 15 && networkBits <= 20:
		subnetNetworkBits = networkBits + 2
	case networkBits >= 9 && networkBits <= 14:
		subnetNetworkBits = networkBits + 3
	case networkBits <= 8:
		subnetNetworkBits = networkBits + 4

	default:
		err = errors.New("network cidr is too small")
	}
	return
}
func divideClusterNetwork(networkCIDR string) (subnetNetworkBits int, err error) {
	_, network, err := net.ParseCIDR(networkCIDR)
	if err != nil {
		return 0, err
	}

	networkBits, _ := network.Mask.Size()
	switch {
	case networkBits > 22 && networkBits <= 25: // consider overlap
		subnetNetworkBits = 26
	case networkBits > 17 && networkBits <= 22:
		subnetNetworkBits = 24
	case networkBits > 12 && networkBits <= 17:
		subnetNetworkBits = 24
	case networkBits <= 12:
		subnetNetworkBits = 22

	default:
		err = errors.New("network cidr is too small")
	}
	return
}

func findAvailableCIDR(networkCIDR string, existingCIDRs []string, networkBits int) (string, error) {
	// Split networkCIDR into 16 size blocks
	hostBits := 32 - networkBits // 主机位数
	_, network, err := net.ParseCIDR(networkCIDR)

	if err != nil {
		return "", err
	}

	// Create a map to store existing CIDRs
	existingCIDRSet := make(map[string]bool)
	for _, cidr := range existingCIDRs {
		// Trim existing CIDR to 16 bits network
		if len(cidr) == 0 {
			continue
		}
		_, ipNet, _ := net.ParseCIDR(cidr)
		ipNet.IP = ipNet.IP.Mask(net.CIDRMask(networkBits, 32))
		existingCIDRSet[ipNet.String()] = true
	}

	// Iterate over available blocks and find an unused one
	for i := 0; i <= (1<<hostBits)-1; i++ {
		// Calculate the next CIDR block
		nextIP := big.NewInt(0).SetBytes(network.IP)
		nextIP.Add(nextIP, big.NewInt(int64(i)<<uint(hostBits)))

		// Convert the next IP to string representation
		nextIPStr := net.IP(nextIP.Bytes()).String()
		newCIDR := nextIPStr + "/" + fmt.Sprintf("%d", networkBits)

		// Check if the generated CIDR overlaps with existing ones
		overlapping := false
		for cidr := range existingCIDRSet {
			if isOverlappingCIDR(cidr, newCIDR) {
				overlapping = true
				break
			}
		}
		if !overlapping {
			return newCIDR, nil
		}
	}
	return "", fmt.Errorf("no available CIDR found")
}

func isOverlappingCIDR(cidr1, cidr2 string) bool {
	_, ipNet1, _ := net.ParseCIDR(cidr1)
	_, ipNet2, _ := net.ParseCIDR(cidr2)

	return ipNet1.Contains(ipNet2.IP) || ipNet2.Contains(ipNet1.IP)
}

func GetEth0IP(pod *v1.Pod) string {
	for _, podIP := range pod.Status.PodIPs {
		if podIP.IP != "" {
			return podIP.IP
		}
	}
	return ""
}

func IsRunningAndHasIP(pod *v1.Pod) bool {
	if pod.Status.Phase == v1.PodRunning {
		for _, podIP := range pod.Status.PodIPs {
			if podIP.IP != "" {
				return true
			}
		}
	}
	return false
}
