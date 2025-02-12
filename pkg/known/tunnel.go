package known

type RouteOperation int

const (
	Add RouteOperation = iota
	Delete
)

// wireguard tunnel
const (
	// DefaultDeviceName specifies name of WireGuard network device.
	DefaultDeviceName = "wg0"
	DediNIC           = "eth-fleet"

	CNFBridgeName = Fleetboard

	UDPPort = 31820
)
