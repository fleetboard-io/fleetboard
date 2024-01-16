package pod

import (
	"fmt"

	"github.com/kubeovn/kube-ovn/pkg/ovs"
	"github.com/kubeovn/kube-ovn/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	v1lister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

// getAllExistingPodAllocatedIPs return all IPs from pods annotation.
func getAllExistingPodAllocatedIPs(podLister v1lister.PodLister) (map[string]string, error) {
	existingIPs := make(map[string]string)
	pods, err := podLister.Pods(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list pods %v", err)
		return existingIPs, err
	}

	for _, pod := range pods {
		if pod.Annotations[fmt.Sprintf(util.AllocatedAnnotationTemplate, "ovn")] == "true" {
			// get ip from annotation.
			ipStr := pod.Annotations[fmt.Sprintf(util.IPAddressAnnotationTemplate, "ovn")]
			portName := fmt.Sprintf("%s.%s", pod.Name, pod.Namespace)
			existingIPs[ipStr] = portName
		}
	}

	return existingIPs, err
}

func removeUnexsitLogicalPort(nbClient *ovs.OVNNbClient, exsitPods map[string]string) error {
	// todo as const.
	ports, err := nbClient.ListNormalLogicalSwitchPorts(false, map[string]string{"ls": "default"})
	if err != nil {
		klog.Errorf("failed to list lsps by default logical switch with error %v", err)
		return err
	}

	// port to ip
	portToIPs := make(map[string]string, 0)
	for ip, portName := range exsitPods {
		portToIPs[portName] = ip
	}

	for _, port := range ports {
		if _, ok := portToIPs[port.Name]; !ok {
			if err := nbClient.DeleteLogicalSwitchPort(port.Name); err != nil {
				klog.Errorf("failed to delete lsp %s, %v", port.Name, err)
				//return err
				// don't mind, if delete failed, also worked well.
			}
		}
	}

	return nil
}
