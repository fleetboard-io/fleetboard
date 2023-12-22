package api

type SubnetSpec struct {
	Name       string
	Default    bool
	CIDRBlock  string
	Gateway    string
	ExcludeIps []string
	Provider   string
}
