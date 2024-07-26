package dedinic

type CniRequest struct {
	PodName      string  `json:"pod_name"`
	PodNamespace string  `json:"pod_namespace"`
	ContainerID  string  `json:"container_id"`
	NetNs        string  `json:"net_ns"`
	Routes       []Route `json:"routes"`
	IfName       string  `json:"if_name"`
	Provider     string  `json:"provider"`
}

// Route represents a requested route
type Route struct {
	Destination string `json:"dst"`
	Gateway     string `json:"gw"`
}
