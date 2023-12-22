package pod

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kubeovn/kube-ovn/pkg/ovs"
	"github.com/kubeovn/kube-ovn/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/multi-cluster-network/ovn-builder/pkg/api"
)

type PodController struct {
	yachtController    *yacht.Controller
	k8sClient          kubernetes.Interface
	podLister          v1lister.PodLister
	k8sInformerFactory informers.SharedInformerFactory
	subnet             *api.SubnetSpec
	nbClient           *ovs.OVNNbClient
	ipam               *ovnipam.IPAM
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
	}

	err := podController.ipam.AddOrUpdateSubnet(subnet.Name, subnet.CIDRBlock, subnet.Gateway, subnet.ExcludeIps)
	if err != nil {
		return nil, err
	}
	yachtcontroller := yacht.NewController("pod").
		WithCacheSynced(podInformer.Informer().HasSynced).
		WithHandlerFunc(podController.Handle).WithEnqueueFilterFunc(func(oldObj, newObj interface{}) (bool, error) {
		var tempObj interface{}
		if newObj != nil {
			tempObj = newObj
		} else {
			tempObj = oldObj
		}

		if tempObj != nil {
			newPod := tempObj.(*v1.Pod)
			// ignore the eps sourced from it-self
			if newPod.GetAnnotations()[fmt.Sprintf(util.AllocatedAnnotationTemplate, "ovn")] == "true" {
				return false, nil
			}
		}
		return true, nil
	})
	_, err = podInformer.Informer().AddEventHandler(yachtcontroller.DefaultResourceEventHandlerFuncs())
	if err != nil {
		return nil, err
	}
	podController.yachtController = yachtcontroller
	return podController, nil
}

func (c *PodController) Run(ctx context.Context) error {
	c.k8sInformerFactory.Start(ctx.Done())
	c.yachtController.Run(ctx)
	return nil
}

func (c *PodController) Handle(obj interface{}) (requeueAfter *time.Duration, err error) {
	key := obj.(string)
	namespace, epsName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid endpointslice key: %s", key))
		return nil, nil
	}
	podNotExist := false
	pod, err := c.podLister.Pods(namespace).Get(epsName)
	if err != nil {
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("pods '%s' no longer exists", key))
			podNotExist = true
		}
	}
	// pod is been deleting
	if podNotExist || pod.GetDeletionTimestamp() != nil {
		// recycle related resources.
		err := c.recycleResources(pod)
		if err != nil {
			return nil, err
		}
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
	v4IP, v6IP, mac, err := c.acquireAddress(pod, podNet)
	if err != nil {
		klog.Error(err)
		return err
	}
	ipStr := util.GetStringIP(v4IP, v6IP)
	pod.Annotations[fmt.Sprintf(util.IPAddressAnnotationTemplate, "ovn")] = ipStr
	if mac == "" {
		delete(pod.Annotations, fmt.Sprintf(util.MacAddressAnnotationTemplate, "ovn"))
	} else {
		pod.Annotations[fmt.Sprintf(util.MacAddressAnnotationTemplate, "ovn")] = mac
	}
	pod.Annotations[fmt.Sprintf(util.CidrAnnotationTemplate, "ovn")] = podNet.CIDRBlock
	pod.Annotations[fmt.Sprintf(util.GatewayAnnotationTemplate, "ovn")] = podNet.Gateway
	pod.Annotations[fmt.Sprintf(util.LogicalSwitchAnnotationTemplate, "ovn")] = podNet.Name
	if pod.Annotations[fmt.Sprintf(util.PodNicAnnotationTemplate, "ovn")] == "" {
		pod.Annotations[fmt.Sprintf(util.PodNicAnnotationTemplate, "ovn")] = "veth-pair"
	}
	pod.Annotations[fmt.Sprintf(util.AllocatedAnnotationTemplate, "ovn")] = "true"

	if err := util.ValidatePodCidr(podNet.CIDRBlock, ipStr); err != nil {
		klog.Errorf("validate pod %s/%s failed: %v", pod.Namespace, pod.Name, err)
		return err
	}

	portName := fmt.Sprintf("%s.%s", pod.Name, pod.Namespace)

	if err := c.nbClient.CreateLogicalSwitchPort(podNet.Name, portName, ipStr, mac, pod.Name, pod.Namespace,
		false, "", "", false, nil, ""); err != nil {
		klog.Errorf("%v", err)
		return err
	}

	// set l2 forward true
	if err := c.nbClient.EnablePortLayer2forward(portName); err != nil {
		klog.Errorf("%v", err)
		return err
	}

	patch, err := util.GenerateMergePatchPayload(cachedPod, pod)
	if err != nil {
		klog.Errorf("failed to generate patch for pod %s/%s: %v", pod.Name, pod.Namespace, err)
		return err
	}
	_, err = c.k8sClient.CoreV1().Pods(pod.Namespace).Patch(context.Background(), pod.Name,
		types.MergePatchType, patch, metav1.PatchOptions{}, "")
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Sometimes pod is deleted between kube-ovn configure ovn-nb and patch pod.
			// Then we need to recycle the resource again.
			//key := strings.Join([]string{namespace, name}, "/")
			//c.deletingPodObjMap.Store(key, pod)
			//c.deletePodQueue.AddRateLimited(key)
			return nil
		}
		klog.Errorf("patch pod %s/%s failed: %v", pod.Name, pod.Namespace, err)
		return err
	}

	return nil
}

func (c *PodController) acquireAddress(pod *v1.Pod, podNet *api.SubnetSpec) (string, string, string, error) {
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	portName := fmt.Sprintf("%s.%s", pod.Name, pod.Namespace)
	var macStr *string

	// Random allocate
	var skippedAddrs []string
	// common pod.
	if !strings.HasSuffix(pod.Labels["cnf/clusternet.io"], "true") {
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
	ipStr := podNet.ExcludeIps[1]
	if v4IP, v6IP, mac, err = c.ipam.GetStaticAddress(key, portName, ipStr, macStr, podNet.Name, true); err != nil {
		klog.Errorf("failed to get static ip %v, mac %v, subnet %v, err %v", ipStr, mac, podNet, err)
		return "", "", "", err
	}
	return v4IP, v6IP, mac, err
}

func (c *PodController) recycleResources(pod *v1.Pod) error {
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	ports, err := c.nbClient.ListNormalLogicalSwitchPorts(true, map[string]string{"pod": key})
	if err != nil {
		klog.Errorf("failed to list lsps of pod '%s', %v", pod.Name, err)
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
