package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/kubeovn/kube-ovn/pkg/util"
	"github.com/nauti-io/nauti/pkg/known"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
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
	if p.DeletionTimestamp != nil {
		return false
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

func AddAnnotationToSelf(client kubernetes.Interface, annotationKey, annotationValue string, override bool) error {
	// Get the Pod's name and namespace from the environment variables
	podName := os.Getenv("NAUTI_PODNAME")
	namespace := os.Getenv("NAUTI_PODNAMESPACE")

	klog.Infof("podname is %s and namespace is %s ", podName, namespace)
	// Get the Pod
	pod, err := client.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("can't find pod with name %s in %s", podName, namespace)
		return err
	}
	return setSpecificAnnotation(client, pod, annotationKey, annotationValue, override)
}

func UpdatePodLabels(client kubernetes.Interface, podName string, isLeader bool) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, err := client.CoreV1().Pods(known.NautiSystemNamespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}

		if isLeader {
			pod.Labels[known.LeaderCNFLabelKey] = "true"
		} else {
			delete(pod.Labels, known.LeaderCNFLabelKey)
		}

		_, err = client.CoreV1().Pods(known.NautiSystemNamespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		klog.Errorf("can't set label for myself")
		return
	}
}

func PatchPodConfig(client kubernetes.Interface, cachedPod, pod *v1.Pod) error {
	patch, err := util.GenerateMergePatchPayload(cachedPod, pod)
	if err != nil {
		klog.Errorf("failed to generate patch for pod %s/%s: %v", pod.Name, pod.Namespace, err)
		return err
	}
	_, err = client.CoreV1().Pods(pod.Namespace).Patch(context.Background(), pod.Name,
		types.MergePatchType, patch, metav1.PatchOptions{}, "")
	if err != nil {
		klog.Errorf("patch pod %s/%s failed: %v", pod.Name, pod.Namespace, err)
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func setSpecificAnnotation(client kubernetes.Interface, pod *v1.Pod, annotationKey, annotationValue string,
	override bool) error {
	annoChanged := true
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	annotationKey = fmt.Sprintf(annotationKey, known.NautiPrefix)

	existingValues, ok := pod.Annotations[annotationKey]
	if ok && !override {
		existingValuesSlice := strings.Split(existingValues, ",")
		if ContainsString(existingValuesSlice, annotationValue) {
			annoChanged = false
		} else {
			pod.Annotations[annotationKey] = existingValues + "," + annotationValue
		}
	} else {
		pod.Annotations[annotationKey] = annotationValue
	}
	if annoChanged {
		_, err := client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// GetSpecificAnnotation get DaemonCIDR from pod annotation return "" if is empty.
func GetSpecificAnnotation(pod *v1.Pod, annotationKeys ...string) []string {
	annotations := pod.Annotations
	allAnnoValue := make([]string, 0)
	if annotations == nil {
		return allAnnoValue
	}

	for _, item := range annotationKeys {
		if val, ok := annotations[fmt.Sprintf(item, known.NautiPrefix)]; ok {
			existingValuesSlice := strings.Split(val, ",")
			allAnnoValue = append(allAnnoValue, existingValuesSlice...)
		}
	}

	return allAnnoValue
}
