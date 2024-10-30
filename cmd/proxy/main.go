package main

import (
	"os"

	"k8s.io/component-base/cli"
	_ "k8s.io/component-base/logs/json/register"

	"github.com/fleetboard-io/fleetboard/cmd/proxy/app"
)

func main() {
	command := app.NewProxyCommand()
	code := cli.Run(command)
	os.Exit(code)
}
