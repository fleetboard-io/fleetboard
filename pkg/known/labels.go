package known

const (
	LabelServiceName        = "services.fleetboard.io/multi-cluster-service-name"
	LabelServiceNameSpace   = "services.fleetboard.io/multi-cluster-service-LocalNamespace"
	ObjectCreatedByLabel    = "fleetboard.io/created-by"
	LabelClusterID          = "services.fleetboard.io/multi-cluster-cluster-ID"
	RouterCNFCreatedByLabel = "router.fleetboard.io/cnf=true"
	LeaderCNFLabelKey       = "router.fleetboard.io/leader"
	IsHeadlessKey           = "services.fleetboard.io/is-headless"
	VirtualClusterIPKey     = "services.fleetboard.io/clusterip"
)
