package util

import (
	"errors"
	"net"
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
