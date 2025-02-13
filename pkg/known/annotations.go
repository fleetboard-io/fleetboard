package known

// IPAM annotation const.
const (
	FleetboardConfigPrefix = Fleetboard
	FleetboardTunnelCIDR   = "fleetboard.io/tunnel_cidr"
	FleetboardClusterCIDR  = "fleetboard.io/cluster_cidr"
	FleetboardNodeCIDR     = "fleetboard.io/node_cidr"
	FleetboardServiceCIDR  = "fleetboard.io/service_cidr"

	PublicKey            = "fleetboard.io/public_key"
	FleetboardParallelIP = "router.fleetboard.io/parallel_ip"
)
