package utils

import (
	"errors"
	"k8s.io/klog/v2"
	"net"
	"os"

	v1 "k8s.io/api/core/v1"
)

var ParallelIpKey string

func init() {
	ParallelIpKey = os.Getenv("PARALLEL_IP_ANNOTATION")
	if ParallelIpKey == "" {
		ParallelIpKey = "ovn.kubernetes.io/ip_address"
	}
}
func GetDedicatedCNIIP(pod *v1.Pod) (ip net.IP, err error) {
	klog.Infof("KEY: %v", ParallelIpKey)
	klog.Infof("Pod Annotation: %v :%v", pod.Name, pod.Annotations)
	if val, ok := pod.Annotations[ParallelIpKey]; ok && len(val) > 0 {
		return net.ParseIP(val), nil
	}
	return nil, errors.New("there is no dedicated ip")
}
