package dedinic

import (
	"os/exec"
	"time"

	"github.com/kubeovn/kube-ovn/pkg/util"
	v1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	k8sexec "k8s.io/utils/exec"
)

// Controller watch pod and namespace changes to update iptables, ipset and ovs qos
type Controller struct {
	config *Configuration

	podsLister listerv1.PodLister
	podsSynced cache.InformerSynced
	podQueue   workqueue.RateLimitingInterface

	nodesLister listerv1.NodeLister
	nodesSynced cache.InformerSynced

	recorder record.EventRecorder

	k8sExec k8sexec.Interface
}

// NewController init a daemon controller
func NewController(config *Configuration, stopCh <-chan struct{}, podInformerFactory,
	nodeInformerFactory informers.SharedInformerFactory) (*Controller, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: config.KubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: config.NodeName})

	podInformer := podInformerFactory.Core().V1().Pods()
	nodeInformer := nodeInformerFactory.Core().V1().Nodes()

	controller := &Controller{
		config: config,

		podsLister: podInformer.Lister(),
		podsSynced: podInformer.Informer().HasSynced,
		podQueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Pod"),

		nodesLister: nodeInformer.Lister(),
		nodesSynced: nodeInformer.Informer().HasSynced,

		recorder: recorder,
		k8sExec:  k8sexec.New(),
	}

	podInformerFactory.Start(stopCh)
	nodeInformerFactory.Start(stopCh)

	if !cache.WaitForCacheSync(stopCh,
		controller.podsSynced, controller.nodesSynced) {
		util.LogFatalAndExit(nil, "failed to wait for caches to sync")
	}

	return controller, nil
}

func (c *Controller) loopEncapIPCheck() {
	klog.V(5).Info("encapip check ...")
	node, err := c.nodesLister.Get(c.config.NodeName)
	if err != nil {
		klog.Errorf("failed to get node %s %v", c.config.NodeName, err)
		return
	}
	klog.V(5).Infof("encapip check node: %s", node.Annotations)
}

// Run starts controller
func (c *Controller) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.podQueue.ShutDown()

	// go wait.Until(ovs.CleanLostInterface, time.Minute, stopCh)
	go wait.Until(recompute, 10*time.Minute, stopCh)
	go wait.Until(rotateLog, 1*time.Hour, stopCh)

	klog.Info("Started workers")
	go wait.Until(c.loopEncapIPCheck, 3*time.Second, stopCh)

	<-stopCh
	klog.Info("Shutting down workers")
}

func recompute() {
	output, err := exec.Command("ovn-appctl", "-t", "ovn-controller", "inc-engine/recompute").CombinedOutput()
	if err != nil {
		klog.Errorf("failed to recompute ovn-controller %q", output)
	}
}
func rotateLog() {
	output, err := exec.Command("logrotate", "/etc/logrotate.d/openvswitch").CombinedOutput()
	if err != nil {
		klog.Errorf("failed to rotate openvswitch log %q", output)
	}
	output, err = exec.Command("logrotate", "/etc/logrotate.d/ovn").CombinedOutput()
	if err != nil {
		klog.Errorf("failed to rotate ovn log %q", output)
	}
	output, err = exec.Command("logrotate", "/etc/logrotate.d/kubeovn").CombinedOutput()
	if err != nil {
		klog.Errorf("failed to rotate kube-ovn log %q", output)
	}
}

// var lastNoPodOvsPort map[string]bool

// func (c *Controller) markAndCleanInternalPort() error {
// 	klog.V(4).Infof("start to gc ovs internal ports")
// 	residualPorts := ovs.GetResidualInternalPorts()
// 	if len(residualPorts) == 0 {
// 		return nil
// 	}
//
// 	noPodOvsPort := map[string]bool{}
// 	for _, portName := range residualPorts {
// 		if !lastNoPodOvsPort[portName] {
// 			noPodOvsPort[portName] = true
// 		} else {
// 			klog.Infof("gc ovs internal port %s", portName)
// 			// Remove ovs port
// 			output, err := ovs.Exec(ovs.IfExists, "--with-iface", "del-port", "br-int", portName)
// 			if err != nil {
// 				return fmt.Errorf("failed to delete ovs port %v, %q", err, output)
// 			}
// 		}
// 	}
// 	lastNoPodOvsPort = noPodOvsPort
//
// 	return nil
// }
