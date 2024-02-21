package util

import (
	"fmt"
	"github.com/multi-cluster-network/ovn-builder/pkg/dedinic"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"net"
	"strings"
)

func ValidatePodNetwork(annotations map[string]string) error {
	errors := []error{}

	if ipAddress := annotations[dedinic.IPAddressAnnotation]; ipAddress != "" {
		// The format of IP Annotation in dual-stack is 10.244.0.0/16,fd00:10:244:0:2::/80
		for _, ip := range strings.Split(ipAddress, ",") {
			if strings.Contains(ip, "/") {
				if _, _, err := net.ParseCIDR(ip); err != nil {
					errors = append(errors, fmt.Errorf("%s is not a valid %s", ip, dedinic.IPAddressAnnotation))
					continue
				}
			} else {
				if net.ParseIP(ip) == nil {
					errors = append(errors, fmt.Errorf("%s is not a valid %s", ip, dedinic.IPAddressAnnotation))
					continue
				}
			}

			if cidrStr := annotations[dedinic.CidrAnnotation]; cidrStr != "" {
				if err := CheckCidrs(cidrStr); err != nil {
					errors = append(errors, fmt.Errorf("invalid cidr %s", cidrStr))
					continue
				}

				if !CIDRContainIP(cidrStr, ip) {
					errors = append(errors, fmt.Errorf("%s not in cidr %s", ip, cidrStr))
					continue
				}
			}
		}
	}

	mac := annotations[dedinic.MacAddressAnnotation]
	if mac != "" {
		if _, err := net.ParseMAC(mac); err != nil {
			errors = append(errors, fmt.Errorf("%s is not a valid %s", mac, dedinic.MacAddressAnnotation))
		}
	}

	ipPool := annotations[dedinic.IPPoolAnnotation]
	if ipPool != "" {
		if strings.ContainsRune(ipPool, ';') || strings.ContainsRune(ipPool, ',') || net.ParseIP(ipPool) != nil {
			for _, ips := range strings.Split(ipPool, ";") {
				found := false
				for _, ip := range strings.Split(ips, ",") {
					if net.ParseIP(strings.TrimSpace(ip)) == nil {
						errors = append(errors, fmt.Errorf("%s in %s is not a valid address", ip, dedinic.IPPoolAnnotation))
					}

					// After ns supports multiple subnets, the ippool static addresses can be allocated in any subnets, such as "ovn.kubernetes.io/ip_pool: 11.16.10.14,12.26.11.21"
					// so if anyone ip is included in cidr, return true
					if cidrStr := annotations[dedinic.CidrAnnotation]; cidrStr != "" {
						if CIDRContainIP(cidrStr, ip) {
							found = true
							break
						}
					} else {
						// annotation maybe empty when a pod is new created, do not return err in this situation
						found = true
						break
					}
				}

				if !found {
					errors = append(errors, fmt.Errorf("%s not in cidr %s", ips, annotations[dedinic.CidrAnnotation]))
					continue
				}
			}
		}
	}

	return utilerrors.NewAggregate(errors)

}
