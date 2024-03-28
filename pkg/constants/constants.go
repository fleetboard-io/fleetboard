package constants

const (
	LabelValueManagedBy  = "mcs.nauti.io"
	OriginName           = "origin-name"
	OriginNamespace      = "origin-namespace"
	LabelSourceNamespace = "submariner-io/originatingNamespace"
	LabelSourceCluster   = "syncer.nauti.io/sourceCluster"
	LabelSourceName      = "syncer.nauti.io/sourceName"
	LabelOriginNameSpace = "syncer.nauti.io/sourceNamespace"
)

const (
	MaxNamespaceLength = 10
	MaxNameLength      = 10
	MaxClusternName    = 10
)

type RouteOperation int

const (
	Add RouteOperation = iota
	Delete
)
