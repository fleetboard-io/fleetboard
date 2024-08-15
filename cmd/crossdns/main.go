package main

import (
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"
	_ "github.com/coredns/coredns/plugin/errors"
	_ "github.com/coredns/coredns/plugin/health"
	_ "github.com/coredns/coredns/plugin/ready"
	_ "github.com/coredns/coredns/plugin/trace"
	_ "github.com/coredns/coredns/plugin/whoami"
	_ "github.com/fleetboard-io/fleetboard/pkg/plugin"
)

var directives = []string{
	"trace",
	"errors",
	"health",
	"ready",
	"crossdns",
	"whoami",
}

func init() {
	dnsserver.Directives = directives
}

func main() {
	rand.Seed(time.Now().UnixNano())
	coremain.Run()
}
