package dedinic

import (
	"testing"
)

func TestGetIP(t *testing.T) {
	cniConf := `{
    "cniVersion": "0.3.1",
    "name": "dedicate-cni",
    "ipam": {
        "type": "host-local",
        "ranges": [
            [
                {
                    "subnet": "20.112.0.0/24",
                    "rangeStart": "20.112.0.10",
                    "rangeEnd": "20.112.0.200",
                    "gateway": "20.112.0.1"
                }
            ]
        ]
    }
}
`
	rq := &CniRequest{
		CniType:      "",
		PodName:      "",
		PodNamespace: "",
		ContainerID:  "ContainerId",
		NetNs:        "ns",
		IfName:       "eth-99",
		Provider:     "",
		Routes:       nil,
	}

	ip, err := GetIP(rq, cniConf)
	if err != nil {
		t.Fatal(err)
	} else {
		t.Logf("ip: %v", ip)
	}
}
