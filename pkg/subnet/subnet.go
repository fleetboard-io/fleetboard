package subnet

import (
	"github.com/multi-cluster-network/ovn-builder/pkg/api"
	"github.com/ovn-org/libovsdb/ovsdb"
	"k8s.io/klog/v2"

	"github.com/kubeovn/kube-ovn/pkg/ovs"
)

// InitDefaultLogicSwitch just init subnet with logic switch.
func InitDefaultLogicSwitch(defaultSubnet *api.SubnetSpec) (*ovs.OVNNbClient, error) {
	OVNNbClient, err := ovs.NewOvnNbClient("tcp:[172.18.0.2]:6641", 60)
	if err != nil {
		klog.Errorf("failed to create ovn nb client %s", err)
		return nil, err
	}
	// create or update logical switch
	if err := OVNNbClient.CreateLogicalSwitch(defaultSubnet.Name, "", defaultSubnet.CIDRBlock, defaultSubnet.Gateway, false, false); err != nil {
		klog.Errorf("create logical switch %s: %v", defaultSubnet.Name, err)
		return nil, err
	}
	// disable broadcast.
	multicastSnoopFlag := map[string]string{"mcast_snoop": "true", "mcast_querier": "false"}
	err = OVNNbClient.LogicalSwitchUpdateOtherConfig(defaultSubnet.Name, ovsdb.MutateOperationDelete, multicastSnoopFlag)
	if err != nil {
		klog.Errorf("disable logical switch multicast snoop  %s: %v", defaultSubnet.Name, err)
		return nil, err
	}

	// disable dhcp.
	return OVNNbClient, nil
}
