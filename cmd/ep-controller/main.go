package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"

	"github.com/fleetboard-io/fleetboard/pkg/controller/endpointslice"
	"github.com/fleetboard-io/fleetboard/pkg/known"
)

var (
	kubeconfig      string
	processIdentify string
	namespace       = known.FleetboardSystemNamespace
)

func main() {
	klog.InitFlags(nil)
	flag.StringVar(&kubeconfig, "kubeconfig",
		filepath.Join(os.Getenv("HOME"), ".kube", "config"), "absolute path to the kubeconfig file")
	flag.Parse()

	ns := os.Getenv(known.EnvPodNamespace)
	if ns != "" {
		namespace = ns
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		// fallback to kube config
		if val := os.Getenv("KUBECONFIG"); len(val) != 0 {
			kubeconfig = val
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			logrus.Fatalf("The kubeconfig cannot be loaded: %v\n", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	// todo select master
	processIdentify = uuid.New().String()

	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})

	defer func() {
		close(stop)
		cancel()
	}()
	startLeaderElection(ctx, clientset, stop)
	select {}
}

func controllerRun(clientset *kubernetes.Clientset, stop chan struct{}, ctx context.Context) {
	factory := informers.NewSharedInformerFactory(clientset, time.Second*5)
	factory.Start(stop)
	eps := endpointslice.NewController(ctx,
		factory.Core().V1().Pods(),
		factory.Core().V1().Services(),
		factory.Core().V1().Nodes(),
		factory.Discovery().V1().EndpointSlices(),
		100,
		clientset,
		1*time.Second)

	factory.Start(wait.NeverStop)

	go eps.Run(ctx, 1)
}

// startLeaderElection
func startLeaderElection(ctx context.Context, clientset *kubernetes.Clientset, stop chan struct{}) {
	klog.Infof("[%s]creat master lock for election", processIdentify)
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "fleetboard-dedinic-controller",
			Namespace: namespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: processIdentify,
		},
	}
	klog.Infof("[%s]start election...", processIdentify)
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   10 * time.Second,
		RenewDeadline:   5 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				klog.Infof("[%s] this process is leader，only leader can executor the logic", processIdentify)
				controllerRun(clientset, stop, ctx)
			},
			OnStoppedLeading: func() {
				klog.Infof("[%s] lose leader", processIdentify)
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				if identity == processIdentify {
					klog.Infof("[%s]get leader result，the current process is leader", processIdentify)
					return
				}
				klog.Infof("[%s]get leader result，leader is : [%s]", processIdentify, identity)
			},
		},
	})
}
