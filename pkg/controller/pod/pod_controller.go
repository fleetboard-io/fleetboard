package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nauti-io/nauti/pkg/controller/utils"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	v1informer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	v1lister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/dixudx/yacht"
	ovnipam "github.com/kubeovn/kube-ovn/pkg/ipam"
	"github.com/kubeovn/kube-ovn/pkg/ovs"
	"github.com/kubeovn/kube-ovn/pkg/request"
	"github.com/kubeovn/kube-ovn/pkg/util"
	"github.com/nauti-io/nauti/pkg/api"
	"github.com/nauti-io/nauti/pkg/known"
)

type PodController struct {
	podAddController   *yacht.Controller
	k8sClient          kubernetes.Interface
	podLister          v1lister.PodLister
	k8sInformerFactory informers.SharedInformerFactory
	subnet             *api.SubnetSpec
	nbClient           *ovs.OVNNbClient
	ipam               *ovnipam.IPAM
	initSkipedIPs      map[string]string
	podSynced          cache.InformerSynced
}

func NewPodController(podInformer v1informer.PodInformer, kubeClientSet kubernetes.Interface, subnet *api.SubnetSpec,
	k8sInformerFactory informers.SharedInformerFactory, client *ovs.OVNNbClient) (*PodController, error) {
	podController := &PodController{
		podLister:          podInformer.Lister(),
		k8sClient:          kubeClientSet,
		k8sInformerFactory: k8sInformerFactory,
		subnet:             subnet,
		nbClient:           client,
		ipam:               ovnipam.NewIPAM(),
		podSynced:          podInformer.Informer().HasSynced,
	}

	err := podController.ipam.AddOrUpdateSubnet(subnet.Name, subnet.CIDRBlock, subnet.Gateway, subnet.ExcludeIps)
	if err != nil {
		return nil, err
	}
	podAddController := yacht.NewController("pod").
		WithCacheSynced(podInformer.Informer().HasSynced).
		WithHandlerFunc(podController.Handle).WithEnqueueFilterFunc(func(oldObj, newObj interface{}) (bool, error) {
		var oldPod *v1.Pod
		var newPod *v1.Pod

		switch {
		case oldObj == nil:
			newPod = newObj.(*v1.Pod)
			if newPod.Spec.HostNetwork {
				return false, nil
			}
			if newPod.Annotations == nil {
				return true, nil
			}
		case newObj == nil:
			oldPod = oldObj.(*v1.Pod)
			if oldPod.Spec.HostNetwork {
				return false, nil
			}
		default:
			return false, nil
		}
		return true, nil
	})
	_, err = podInformer.Informer().AddEventHandler(podAddController.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	podController.podAddController = podAddController
	return podController, nil
}

func (c *PodController) Run(ctx context.Context) error {
	c.k8sInformerFactory.Start(ctx.Done())
	// wait for pod synced
	cache.WaitForCacheSync(ctx.Done(), c.podSynced)
	var err error
	// get all pod allocated ips
	c.initSkipedIPs, err = getAllExistingPodAllocatedIPs(c.podLister)
	if err != nil {
		return err
	}
	klog.Infof("init we get skipped ips are %v", c.initSkipedIPs)
	// remove unneeded lsp.
	err = removeUnexsitLogicalPort(c.nbClient, c.initSkipedIPs)
	if err != nil {
		return err
	}
	c.podAddController.Run(ctx)
	return nil
}

func (c *PodController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	key := obj.(string)
	namespace, podName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, nil
	}
	podNotExist := false
	pod, err := c.podLister.Pods(namespace).Get(podName)
	if err != nil {
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("pods '%s' no longer exists", key))
			podNotExist = true
		}
	}
	// pod is been deleting
	if podNotExist || !utils.IsPodAlive(pod) {
		// recycle related resources.
		errRecycle := c.recycleResources(key)
		if errRecycle != nil {
			d := 2 * time.Second
			return &d, errRecycle
		}
		return nil, nil
	}

	if len(pod.Annotations) == 0 {
		pod.Annotations = map[string]string{}
	}

	// check and do hotnoplug nic
	if err = c.syncKubeOvnNet(pod); err != nil {
		klog.Errorf("failed to sync pod nets %v", err)
		return nil, err
	}

	cachedPod := pod.DeepCopy()
	if err := c.reconcileAllocateSubnets(cachedPod, pod); err != nil {
		klog.Errorf("failed to reconcile pod nets %v", err)
		d := 2 * time.Second
		return &d, err
	}

	return nil, nil
}

func (c *PodController) syncKubeOvnNet(pod *v1.Pod) error {
	podName := pod.Name
	key := fmt.Sprintf("%s/%s", pod.Namespace, podName)

	portsNeedToDel := []string{}
	subnetUsedByPort := make(map[string]string)
	portName := fmt.Sprintf("%s.%s", pod, pod.Namespace)

	ports, err := c.nbClient.ListNormalLogicalSwitchPorts(true, map[string]string{"pod": key})
	if err != nil {
		klog.Errorf("failed to list lsps of pod '%s', %v", pod.Name, err)
		return err
	}

	for _, port := range ports {
		if portName != port.Name {
			portsNeedToDel = append(portsNeedToDel, port.Name)
			subnetUsedByPort[port.Name] = port.ExternalIDs["ls"]
		}
	}

	if len(portsNeedToDel) == 0 {
		return nil
	}

	for _, portNeedDel := range portsNeedToDel {
		if subnet, ok := c.ipam.Subnets[subnetUsedByPort[portNeedDel]]; ok {
			subnet.ReleaseAddressWithNicName(podName, portNeedDel)
		}

		if err := c.nbClient.DeleteLogicalSwitchPort(portNeedDel); err != nil {
			klog.Errorf("failed to delete lsp %s, %v", portNeedDel, err)
			return err
		}
	}
	return nil
}

// do the same thing as add pod
func (c *PodController) reconcileAllocateSubnets(cachedPod, pod *v1.Pod) error {
	podNet := c.subnet
	klog.Infof("sync pod %s/%s allocated", pod.Namespace, pod.Name)
	// Avoid create lsp for already running pod in ovn-nb when controller restart

	// set default config.
	ipStr, _, mac, err := c.acquireAddress(pod, podNet)
	if err != nil {
		klog.Error(err)
		return err
	}
	pod.Annotations[fmt.Sprintf(known.IPAddressAnnotationTemplate, known.NautiPrefix)] = ipStr
	if mac == "" {
		delete(pod.Annotations, fmt.Sprintf(known.MacAddressAnnotationTemplate, known.NautiPrefix))
	} else {
		pod.Annotations[fmt.Sprintf(known.MacAddressAnnotationTemplate, known.NautiPrefix)] = mac
	}
	pod.Annotations[fmt.Sprintf(known.CidrAnnotationTemplate, known.NautiPrefix)] = podNet.CIDRBlock
	pod.Annotations[fmt.Sprintf(known.GatewayAnnotationTemplate, known.NautiPrefix)] = podNet.Gateway
	pod.Annotations[fmt.Sprintf(known.LogicalSwitchAnnotationTemplate, known.NautiPrefix)] = podNet.Name
	pod.Annotations[fmt.Sprintf(known.PodNicAnnotationTemplate, known.NautiPrefix)] = "veth-pair"
	pod.Annotations[fmt.Sprintf(known.AllocatedAnnotationTemplate, known.NautiPrefix)] = known.NautiTrue

	// cnf pod need no route to gateway pod.
	if !strings.HasSuffix(pod.Labels[known.CNFLabel], known.NautiTrue) {
		routes := []request.Route{
			{
				Destination: podNet.GlobalCIDR,
				Gateway:     podNet.Gateway,
			},
		}
		routeBytes, errMarshal := json.Marshal(routes)
		if errMarshal != nil {
			klog.Errorf("Marshal error: %v", errMarshal)
		}
		pod.Annotations[fmt.Sprintf(known.RoutesAnnotationTemplate, known.NautiPrefix)] = string(routeBytes)
	}

	if crdrErr := util.ValidatePodCidr(podNet.CIDRBlock, ipStr); crdrErr != nil {
		klog.Errorf("validate pod %s/%s failed: %v", pod.Namespace, pod.Name, crdrErr)
		return crdrErr
	}

	portName := fmt.Sprintf("%s.%s", pod.Name, pod.Namespace)

	if lsErr := c.nbClient.CreateLogicalSwitchPort(podNet.Name, portName, ipStr, mac, pod.Name, pod.Namespace,
		false, "", "", false, nil, ""); lsErr != nil {
		klog.Errorf("%v", lsErr)
		return lsErr
	}

	patch, err := util.GenerateMergePatchPayload(cachedPod, pod)
	if err != nil {
		klog.Errorf("failed to generate patch for pod %s/%s: %v", pod.Name, pod.Namespace, err)
		return err
	}
	_, err = c.k8sClient.CoreV1().Pods(pod.Namespace).Patch(context.Background(), pod.Name,
		types.MergePatchType, patch, metav1.PatchOptions{}, "")
	if err != nil {
		klog.Errorf("patch pod %s/%s failed: %v", pod.Name, pod.Namespace, err)
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	klog.Infof("pod has been patch with annotation %v", pod.Annotations)

	return nil
}

func (c *PodController) acquireAddress(pod *v1.Pod, podNet *api.SubnetSpec) (string, string, string, error) {
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	portName := fmt.Sprintf("%s.%s", pod.Name, pod.Namespace)
	var macStr *string
	var ipStr string
	isCNFPod := strings.HasSuffix(pod.Labels[known.CNFLabel], known.NautiTrue)
	needRandomAddress := true

	klog.Infof("pod annotations are %v", pod.Annotations)

	if pod.Annotations[fmt.Sprintf(known.AllocatedAnnotationTemplate, known.NautiPrefix)] == known.NautiTrue {
		needRandomAddress = false
	}

	// Random allocate
	var skippedAddrs []string
	for k := range c.initSkipedIPs {
		skippedAddrs = append(skippedAddrs, k)
	}
	// common pod.
	if !isCNFPod && needRandomAddress {
		ipv4, ipv6, mac, err := c.ipam.GetRandomAddress(key, portName, macStr, podNet.Name, "", skippedAddrs, true)
		if err != nil {
			klog.Error(err)
			klog.Errorf("alloc address for %s failed, return NoAvailableAddress, with error %s", key, err)
			return "", "", "", err
		}
		return ipv4, ipv6, mac, nil
	}
	var v4IP, v6IP, mac string
	var err error
	// cnf pod allocate
	if isCNFPod {
		ipStr = podNet.Gateway
	} else {
		ipStr = pod.Annotations[fmt.Sprintf(known.IPAddressAnnotationTemplate, known.NautiPrefix)]
	}

	if v4IP, v6IP, mac, err = c.ipam.GetStaticAddress(key, portName, ipStr, macStr, podNet.Name, true); err != nil {
		klog.Errorf("failed to get static ip %v, mac %v, subnet %v, err %v", ipStr, mac, podNet, err)
		return "", "", "", err
	}
	// TODO  or maybe we don't need async.
	delete(c.initSkipedIPs, ipStr)
	klog.Infof("skipped ips are %s", c.initSkipedIPs)
	return v4IP, v6IP, mac, err
}

func (c *PodController) recycleResources(key string) error {
	ports, err := c.nbClient.ListNormalLogicalSwitchPorts(true, map[string]string{"pod": key})
	if err != nil {
		klog.Errorf("failed to list lsps of pod '%s', %v", key, err)
		return err
	}
	for _, port := range ports {
		// when lsp is deleted, the port of pod is deleted from any port-group automatically.
		klog.Infof("gc logical switch port %s", port.Name)
		if err := c.nbClient.DeleteLogicalSwitchPort(port.Name); err != nil {
			klog.Errorf("failed to delete lsp %s, %v", port.Name, err)
			return err
		}
	}
	c.ipam.ReleaseAddressByPod(key, c.subnet.Name)
	return nil
}
