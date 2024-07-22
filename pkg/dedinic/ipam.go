package dedinic

import (
	"fmt"
	"net"

	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/plugins/ipam/host-local/backend/allocator"
	"github.com/containernetworking/plugins/plugins/ipam/host-local/backend/disk"
)

func GetIP(rq *CniRequest, ipamConfStr string) (res *current.Result, err error) {
	ipamConf, _, err := allocator.LoadIPAMConfig([]byte(ipamConfStr), "")
	if err != nil {
		return nil, err
	}

	result := &current.Result{CNIVersion: current.ImplementedSpecVersion}

	store, err := disk.New(ipamConf.Name, ipamConf.DataDir)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	// Keep the allocators we used, so we can release all IPs if an error
	// occurs after we start allocating
	allocs := []*allocator.IPAllocator{}

	// Store all requested IPs in a map, so we can easily remove ones we use
	// and error if some remain
	requestedIPs := map[string]net.IP{} // net.IP cannot be a key

	for _, ip := range ipamConf.IPArgs {
		requestedIPs[ip.String()] = ip
	}

	for idx, rangeSet := range ipamConf.Ranges {
		v := rangeSet
		allocator := allocator.NewIPAllocator(&v, store, idx)

		// Check to see if there are any custom IPs requested in this range.
		var requestedIP net.IP
		for k, ip := range requestedIPs {
			if v.Contains(ip) {
				requestedIP = ip
				delete(requestedIPs, k)
				break
			}
		}

		ipConf, err := allocator.Get(rq.ContainerID, rq.IfName, requestedIP)
		if err != nil {
			// Deallocate all already allocated IPs
			for _, alloc := range allocs {
				_ = alloc.Release(rq.ContainerID, rq.IfName)
			}
			return nil, fmt.Errorf("failed to allocate for range %d: %v", idx, err)
		}

		allocs = append(allocs, allocator)

		result.IPs = append(result.IPs, ipConf)
	}

	// If an IP was requested that wasn't fulfilled, fail
	if len(requestedIPs) != 0 {
		for _, alloc := range allocs {
			_ = alloc.Release(rq.ContainerID, rq.IfName)
		}
		errstr := "failed to allocate all requested IPs:"
		for _, ip := range requestedIPs {
			errstr = errstr + " " + ip.String()
		}
		return nil, fmt.Errorf(errstr)
	}

	result.Routes = ipamConf.Routes

	return result, nil
}
