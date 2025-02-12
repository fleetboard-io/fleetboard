package known

const (
	LabelCNFPod = "app=cnf-fleetboard"

	LabelServiceName        = "services.fleetboard.io/multi-cluster-service-name"
	LabelServiceNameSpace   = "services.fleetboard.io/multi-cluster-service-LocalNamespace"
	LabelClusterID          = "services.fleetboard.io/multi-cluster-cluster-ID"
	IsHeadlessKey           = "services.fleetboard.io/is-headless"
	VirtualClusterIPKey     = "services.fleetboard.io/clusterip"
	ObjectCreatedByLabel    = "fleetboard.io/created-by"
	RouterCNFCreatedByLabel = "router.fleetboard.io/cnf=true"
	LeaderCNFLabelKey       = "router.fleetboard.io/leader"
)

const (
	LabelValueManagedBy = "mcs.fleetboard.io"
)
