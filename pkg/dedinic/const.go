package dedinic

const (
	ProtocolIPv4 = "IPv4"
	ProtocolIPv6 = "IPv6"
	ProtocolDual = "Dual"

	AllocatedAnnotation  = "ovn.kubernetes.io/allocated"
	RoutedAnnotation     = "ovn.kubernetes.io/routed"
	RoutesAnnotation     = "ovn.kubernetes.io/routes"
	MacAddressAnnotation = "ovn.kubernetes.io/mac_address"
	IPAddressAnnotation  = "ovn.kubernetes.io/ip_address"
	CidrAnnotation       = "ovn.kubernetes.io/cidr"
	GatewayAnnotation    = "ovn.kubernetes.io/gateway"
	IPPoolAnnotation     = "ovn.kubernetes.io/ip_pool"
	BgpAnnotation        = "ovn.kubernetes.io/bgp"
	SnatAnnotation       = "ovn.kubernetes.io/snat"
	EipAnnotation        = "ovn.kubernetes.io/eip"
	EipNameAnnotation    = "ovn.kubernetes.io/eip_name"
	FipNameAnnotation    = "ovn.kubernetes.io/fip_name"
	FipEnableAnnotation  = "ovn.kubernetes.io/enable_fip"
	FipFinalizer         = "ovn.kubernetes.io/fip"
	VipAnnotation        = "ovn.kubernetes.io/vip"
	AAPsAnnotation       = "ovn.kubernetes.io/aaps"
	ChassisAnnotation    = "ovn.kubernetes.io/chassis"

	gatewayCheckMaxRetry = 200
)
