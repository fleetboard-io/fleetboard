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

func GetDedicatedCNIIP(pod *v1.Pod) (ip net.IP, err error) {
	klog.Infof("Pod Annotation: %v :%v", pod.Name, pod.Annotations)
	if val, ok := pod.Annotations[known.FleetboardParallelIP]; ok && len(val) > 0 {
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
	podName := os.Getenv(known.EnvPodName)
	namespace := os.Getenv(known.EnvPodNamespace)

	pod, err := client.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get pod %s/%s failed: %v", namespace, podName, err)
	}

	return SetSpecificAnnotations(client, pod, []string{annotationKey}, []string{annotationValue}, override)
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
			if _, ok := pod.Labels[known.LeaderCNFLabelKey]; !ok {
				return nil // not need to update
			}
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

// DeprecatedPatchPodAnnotationsWithRetry update specific pod annotations
// replaced by SetSpecificAnnotations
func DeprecatedPatchPodAnnotationsWithRetry(client kubernetes.Interface, pod *v1.Pod,
	annotationKeys, annotationValues []string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		cachedPod := pod.DeepCopy()
		if pod.GetAnnotations() == nil {
			pod.Annotations = make(map[string]string)
		}
		for i, annoKey := range annotationKeys {
			pod.Annotations[annoKey] = annotationValues[i]
		}

		patch, err := GenerateMergePatchPayload(cachedPod, pod)
		if err != nil {
			klog.Errorf("failed to generate patch for pod %s/%s: %v", pod.Name, pod.Namespace, err)
			return err
		}
		_, patchErr := client.CoreV1().Pods(pod.Namespace).Patch(context.Background(), pod.Name,
			types.MergePatchType, patch, metav1.PatchOptions{}, "")
		if patchErr != nil {
			klog.Errorf("patch pod %s/%s failed: %v", pod.Namespace, pod.Name, err)
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

// SetSpecificAnnotations replaces DeprecatedPatchPodAnnotationsWithRetry
func SetSpecificAnnotations(client kubernetes.Interface, pod *v1.Pod, annotationKeys, annotationValues []string,
	override bool) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		cachedPod := pod.DeepCopy()
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}

		for i, annotationKey := range annotationKeys {
			annotationValue := annotationValues[i]
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
		}

		patch, err := GenerateMergePatchPayload(cachedPod, pod)
		if err != nil {
			err = fmt.Errorf("get pod %s/%s patch payload failed: %v", pod.Namespace, pod.Name, err)
			klog.Error(err)
			return err
		}
		_, err = client.CoreV1().Pods(pod.Namespace).Patch(context.TODO(), pod.Name,
			types.MergePatchType, patch, metav1.PatchOptions{}, "")
		if err != nil {
			err = fmt.Errorf("patch pod %s/%s failed: %v", pod.Namespace, pod.Name, err)
			klog.Error(err)
			return err
		}
		return nil
	})
}

// GetSpecificAnnotation may be confusing when one key has one value but another has multiple values.
func GetSpecificAnnotation(pod *v1.Pod, annotationKeys ...string) []string {
	annotations := pod.Annotations
	allAnnoValue := make([]string, 0)
	if annotations == nil {
		return allAnnoValue
	}

	for _, annotationKey := range annotationKeys {
		annotationKey = fmt.Sprintf(annotationKey, known.FleetboardPrefix)
		if val, ok := annotations[annotationKey]; ok {
			existingValuesSlice := strings.Split(val, ",")
			allAnnoValue = append(allAnnoValue, existingValuesSlice...)
		}
	}

	return allAnnoValue
}

// CheckIfMasterOrControlNode return if node is master or controlPlane
func CheckIfMasterOrControlNode(clientset *kubernetes.Clientset, nodeName string) bool {
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting node: %v\n", err)
		return false
	}
	_, isMaster := node.Labels["node-role.kubernetes.io/master"]
	_, isControlPlane := node.Labels["node-role.kubernetes.io/control-plane"]
	return isMaster || isControlPlane
}
