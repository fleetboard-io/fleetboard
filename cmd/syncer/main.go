package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"

	"github.com/kelseyhightower/envconfig"
	"github.com/nauti-io/nauti/pkg/controller"
	"github.com/nauti-io/nauti/pkg/known"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	masterURL  string
	kubeConfig string
)

func main() {
	agentSpec := known.AgentSpecification{}
	klog.InitFlags(nil)

	flag.Parse()

	err := envconfig.Process("syncer", &agentSpec)
	if err != nil {
		klog.Fatal(err)
	}

	klog.Infof("Arguments: %v", os.Args)
	klog.Infof("AgentSpec: %v", agentSpec)

	err = mcsv1a1.AddToScheme(scheme.Scheme)
	if err != nil {
		klog.Exitf("Error adding Multicluster v1alpha1 to the scheme: %v", err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeConfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}

	if err != nil {
		klog.Fatal(err.Error())
	}

	localClient, err := dynamic.NewForConfig(cfg)
	mcsClientSet := mcsclientset.NewForConfigOrDie(cfg)
	if err != nil {
		klog.Fatalf("error creating dynamic client: %v", err)
	}

	klog.Infof("Starting syncer %v", agentSpec)

	// set up signals so we handle the first shutdown signal gracefully
	ctx := signals.SetupSignalHandler()

	agent, err := controller.New(&agentSpec, known.SyncerConfig{
		LocalRestConfig: cfg,
		LocalClient:     localClient,
		LocalNamespace:  agentSpec.LocalNamespace,
		LocalClusterID:  agentSpec.ClusterID,
	}, kubeClientSet, mcsClientSet)
	if err != nil {
		klog.Fatalf("Failed to create syncer agent: %v", err)
	}

	if err := agent.Start(ctx); err != nil {
		klog.Fatalf("Failed to start syncer agent: %v", err)
	}

	httpServer := startHTTPServer()

	<-ctx.Done()

	klog.Info("All controllers stopped or exited. Stopping main loop")

	if err := httpServer.Shutdown(context.TODO()); err != nil {
		klog.Errorf("Error shutting down metrics HTTP server: %v", err)
	}
}

func init() {
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func startHTTPServer() *http.Server {
	srv := &http.Server{Addr: ":8082", ReadHeaderTimeout: 60 * time.Second}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			klog.Errorf("Error starting metrics server: %v", err)
		}
	}()

	return srv
}
