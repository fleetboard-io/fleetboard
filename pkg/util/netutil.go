package util

import (
	"errors"
	"github.com/multi-cluster-network/ovn-builder/pkg/dedinic"
	"net"
	"strings"
)

// GetIndexIpFromCIDR return index ip in the cidr, index start from 1 not 0, because 0 is not a valid ip.
func GetIndexIpFromCIDR(cidr string, index int) (string, error) {
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

func inc(ip_a net.IP) {
	for j := len(ip_a) - 1; j >= 0; j-- {
		ip_a[j]++
		if ip_a[j] > 0 {
			break
		}
	}
}

func CheckCidrs(cidr string) error {
	for _, cidrBlock := range strings.Split(cidr, ",") {
		if _, _, err := net.ParseCIDR(cidrBlock); err != nil {
			return errors.New("CIDRInvalid")
		}
	}
	return nil
}

func CIDRContainIP(cidrStr, ipStr string) bool {
	cidrs := strings.Split(cidrStr, ",")
	ips := strings.Split(ipStr, ",")

	if len(cidrs) == 1 {
		for _, ip := range ips {
			if CheckProtocol(cidrStr) != CheckProtocol(ip) {
				return false
			}
		}
	}

	for _, cidr := range cidrs {
		_, cidrNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return false
		}

		for _, ip := range ips {
			if CheckProtocol(cidr) != CheckProtocol(ip) {
				continue
			}
			ipAddr := net.ParseIP(ip)
			if ipAddr == nil {
				return false
			}

			if !cidrNet.Contains(ipAddr) {
				return false
			}
		}
	}
	// v4 and v6 address should be both matched for dualstack check
	return true
}

func CheckProtocol(address string) string {
	ips := strings.Split(address, ",")
	if len(ips) == 2 {
		IP1 := net.ParseIP(strings.Split(ips[0], "/")[0])
		IP2 := net.ParseIP(strings.Split(ips[1], "/")[0])
		if IP1.To4() != nil && IP2.To4() == nil && IP2.To16() != nil {
			return dedinic.ProtocolDual
		}
		if IP2.To4() != nil && IP1.To4() == nil && IP1.To16() != nil {
			return dedinic.ProtocolDual
		}
		return ""
	}

	address = strings.Split(address, "/")[0]
	ip := net.ParseIP(address)
	if ip.To4() != nil {
		return dedinic.ProtocolIPv4
	} else if ip.To16() != nil {
		return dedinic.ProtocolIPv6
	}

	// cidr formal error
	return ""
}
