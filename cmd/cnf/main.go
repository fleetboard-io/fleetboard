package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/component-base/cli"
	"k8s.io/klog/v2"

	"github.com/fleetboard-io/fleetboard/cmd/cnf/app"
)

var gracefulStopCh = make(chan os.Signal, 2)

func main() {
	command := app.NewCNFCommand(GracefulStopWithContext())
	code := cli.Run(command)
	os.Exit(code)
}

func GracefulStopWithContext() context.Context {
	signal.Notify(gracefulStopCh, syscall.SIGTERM, syscall.SIGINT)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// waiting for os signal to stop the program
		oscall := <-gracefulStopCh
		klog.Warningf("shutting down, caused by %s", oscall)
		cancel()
		<-gracefulStopCh
		os.Exit(1)
	}()

	return ctx
}
