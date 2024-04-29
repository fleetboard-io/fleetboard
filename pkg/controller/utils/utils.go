package utils

import (
	"errors"
	"net"
	"os"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

var ParallelIPKey string

func init() {
	ParallelIPKey = os.Getenv("PARALLEL_IP_ANNOTATION")
	if ParallelIPKey == "" {
		ParallelIPKey = "ovn.kubernetes.io/ip_address"
	}
}
func GetDedicatedCNIIP(pod *v1.Pod) (ip net.IP, err error) {
	klog.Infof("KEY: %v", ParallelIPKey)
	klog.Infof("Pod Annotation: %v :%v", pod.Name, pod.Annotations)
	if val, ok := pod.Annotations[ParallelIPKey]; ok && len(val) > 0 {
		return net.ParseIP(val), nil
	}
	return nil, errors.New("there is no dedicated ip")
}
