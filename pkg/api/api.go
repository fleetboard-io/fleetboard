package api

type SubnetSpec struct {
	Name       string
	Default    bool
	CIDRBlock  string
	GlobalCIDR string
	Gateway    string
	ExcludeIps []string
	Provider   string
}
