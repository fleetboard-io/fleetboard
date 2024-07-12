package utils

import (
	"errors"
	"net"
	"os"
	"time"

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

func IsPodAlive(p *v1.Pod) bool {
	if p.DeletionTimestamp != nil && p.DeletionGracePeriodSeconds != nil {
		now := time.Now()
		deletionTime := p.DeletionTimestamp.Time
		gracePeriod := time.Duration(*p.DeletionGracePeriodSeconds) * time.Second
		if now.After(deletionTime.Add(gracePeriod)) {
			return false
		}
	}
	return isPodStatusPhaseAlive(p)
}

func isPodStatusPhaseAlive(p *v1.Pod) bool {
	if p.Status.Phase == v1.PodSucceeded && p.Spec.RestartPolicy != v1.RestartPolicyAlways {
		return false
	}

	if p.Status.Phase == v1.PodFailed && p.Spec.RestartPolicy == v1.RestartPolicyNever {
		return false
	}

	if p.Status.Phase == v1.PodFailed && p.Status.Reason == "Evicted" {
		return false
	}
	return true
}
