package known

// IPAM annotation const.
const (
	FleetboardPrefix      = Fleetboard // todo: replace %s with constant?
	FleetboardTunnelCIDR  = "%s.io/tunnel_cidr"
	FleetboardClusterCIDR = "%s.io/cluster_cidr"
	FleetboardNodeCIDR    = "%s.io/node_cidr"
	FleetboardServiceCIDR = "%s.io/service_cidr"

	PublicKey            = "%s.io/public_key"
	FleetboardParallelIP = "router.fleetboard.io/parallel_ip"
)
