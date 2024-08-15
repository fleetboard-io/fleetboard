package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/fleetboard-io/fleetboard/pkg/known"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

var ParallelIPKey string

func init() {
	ParallelIPKey = os.Getenv("PARALLEL_IP_ANNOTATION")
	if ParallelIPKey == "" {
		ParallelIPKey = "router.fleetboard.io/dedicated_ip"
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
	podName := os.Getenv("FLEETBOARD_PODNAME")
	namespace := os.Getenv("FLEETBOARD_PODNAMESPACE")

	klog.Infof("podname is %s and namespace is %s ", podName, namespace)
	return setSpecificAnnotation(client, namespace, podName, annotationKey, annotationValue, override)
}

func UpdatePodLabels(client kubernetes.Interface, podName string, isLeader bool) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, err := client.CoreV1().Pods(known.FleetboardSystemNamespace).Get(context.TODO(), podName, metav1.GetOptions{})
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

		_, err = client.CoreV1().Pods(known.FleetboardSystemNamespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		klog.Errorf("can't set label for myself")
		return
	}
}

// PatchPodWithRetry get and update specific pod.
func PatchPodWithRetry(client kubernetes.Interface, pod *v1.Pod, secondaryCIDR, globalCIDR string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		cachedPod := pod.DeepCopy()
		if pod.GetAnnotations() == nil {
			pod.Annotations = make(map[string]string)
		}
		pod.Annotations[fmt.Sprintf(known.DaemonCIDR, known.FleetboardPrefix)] = secondaryCIDR
		pod.Annotations[fmt.Sprintf(known.CNFCIDR, known.FleetboardPrefix)] = globalCIDR
		patch, err := GenerateMergePatchPayload(cachedPod, pod)
		if err != nil {
			klog.Errorf("failed to generate patch for pod %s/%s: %v", pod.Name, pod.Namespace, err)
			return err
		}
		_, patchErr := client.CoreV1().Pods(pod.Namespace).Patch(context.Background(), pod.Name,
			types.MergePatchType, patch, metav1.PatchOptions{}, "")
		if patchErr != nil {
			klog.Errorf("patch pod %s/%s failed: %v", pod.Name, pod.Namespace, err)
			pod, err = client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.GetName(), metav1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return patchErr
		}
		return nil
	})
}

func PatchPodConfig(client kubernetes.Interface, cachedPod, pod *v1.Pod) error {
	patch, err := GenerateMergePatchPayload(cachedPod, pod)
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

func GenerateMergePatchPayload(original, modified runtime.Object) ([]byte, error) {
	originalJSON, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}

	modifiedJSON, err := json.Marshal(modified)
	if err != nil {
		return nil, err
	}

	data, err := createMergePatch(originalJSON, modifiedJSON, modified)
	if err != nil {
		return nil, err
	}
	return data, nil
}
func createMergePatch(originalJSON, modifiedJSON []byte, _ interface{}) ([]byte, error) {
	return jsonpatch.CreateMergePatch(originalJSON, modifiedJSON)
}

func setSpecificAnnotation(client kubernetes.Interface, namespace, podName, annotationKey, annotationValue string,
	override bool) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		// Get the Pod
		pod, err := client.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("can't find pod with name %s in %s", podName, namespace)
			if k8serrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		cachedPod := pod.DeepCopy()
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		annotationKey = fmt.Sprintf(annotationKey, known.FleetboardPrefix)

		existingValues, ok := pod.Annotations[annotationKey]
		if ok && !override {
			existingValuesSlice := strings.Split(existingValues, ",")
			if !ContainsString(existingValuesSlice, annotationValue) {
				pod.Annotations[annotationKey] = existingValues + "," + annotationValue
			}
		} else {
			pod.Annotations[annotationKey] = annotationValue
		}

		patch, err := GenerateMergePatchPayload(cachedPod, pod)
		if err != nil {
			klog.Errorf("failed to generate patch for pod %s/%s: %v", pod.Name, pod.Namespace, err)
			return err
		}
		_, patchErr := client.CoreV1().Pods(pod.Namespace).Patch(context.Background(), pod.Name,
			types.MergePatchType, patch, metav1.PatchOptions{}, "")
		return patchErr
	})
}

// GetSpecificAnnotation get DaemonCIDR from pod annotation return "" if is empty.
func GetSpecificAnnotation(pod *v1.Pod, annotationKeys ...string) []string {
	annotations := pod.Annotations
	allAnnoValue := make([]string, 0)
	if annotations == nil {
		return allAnnoValue
	}

	for _, item := range annotationKeys {
		if val, ok := annotations[fmt.Sprintf(item, known.FleetboardPrefix)]; ok {
			existingValuesSlice := strings.Split(val, ",")
			allAnnoValue = append(allAnnoValue, existingValuesSlice...)
		}
	}

	return allAnnoValue
}
