package utils

import (
	"context"
	"errors"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// FindServiceIPRange returns the service ip range for the cluster.
func FindServiceIPRange(kubeClientSet kubernetes.Interface) (string, error) {
	// Try to find the service ip range from the kube-apiserver first
	// and then kube-controller-manager if failed
	labelKeys := []string{"component", "app.kubernetes.io/component"}
	labelValues := []string{"kube-apiserver", "kube-controller-manager"}
	parameter := "--service-cluster-ip-range"
	for _, labelValue := range labelValues {
		for _, labelKey := range labelKeys {
			labelSelector := labels.SelectorFromSet(labels.Set{labelKey: labelValue})
			serviceIPRange, err := FindPodCommandParameter(kubeClientSet, labelSelector, parameter)
			if err != nil || serviceIPRange != "" { // if err is not nil, meaning something wrong, return it directly
				return serviceIPRange, err
			}
		}
	}

	// Try to find the service ip range from the env.
	serviceIPRange := os.Getenv("SERVICE_CIDR")
	if serviceIPRange != "" {
		return serviceIPRange, nil
	}
	return "", errors.New("can't get service ip range")
}

// FindPodIPRange returns the pod ip range for the cluster.
func FindPodIPRange(kubeClientSet kubernetes.Interface) (string, error) {
	// Try to find the pod ip range from the kube-controller-manager not including kube-apiserver
	labelKeys := []string{"component", "app.kubernetes.io/component"}
	labelValues := []string{"kube-controller-manager"}
	parameter := "--cluster-cidr"
	for _, labelValue := range labelValues {
		for _, labelKey := range labelKeys {
			labelSelector := labels.SelectorFromSet(labels.Set{labelKey: labelValue})
			podIPRange, err := FindPodCommandParameter(kubeClientSet, labelSelector, parameter)
			if err != nil || podIPRange != "" {
				return podIPRange, err
			}
		}
	}

	// Try to find the pod ip range from the kube-proxy
	labelKey, labelValue := "k8s-app", "kube-proxy"
	labelSelector := labels.SelectorFromSet(labels.Set{labelKey: labelValue})
	podIPRange, err := FindPodCommandParameter(kubeClientSet, labelSelector, parameter)
	if err != nil || podIPRange != "" {
		return podIPRange, err
	}

	// Try to find the pod ip range from the env.
	podIPRange = os.Getenv("CLUSTER_CIDR")
	if podIPRange != "" {
		return podIPRange, nil
	}
	return "", errors.New("can't get pod ip range")
}

// FindPodCommandParameter returns the pod container command parameter by the given labelSelector.
func FindPodCommandParameter(kubeClientSet kubernetes.Interface, labelSelector labels.Selector,
	parameter string) (string, error) {
	pods, err := findPods(kubeClientSet, labelSelector)
	if err != nil {
		return "", err
	}

	for _, pod := range pods {
		for _, container := range pod.Spec.Containers {
			if val := getParameterValue(container.Command, parameter); val != "" {
				return val, nil
			}
			if val := getParameterValue(container.Args, parameter); val != "" {
				return val, nil
			}
		}
	}

	return "", nil
}

// findPods returns the pods filter by the given labelSelector.
func findPods(kubeClientSet kubernetes.Interface, labelSelector labels.Selector) ([]corev1.Pod, error) {
	podList, err := kubeClientSet.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labelSelector.String(),
		TimeoutSeconds: func() *int64 {
			var timeout int64 = 10
			return &timeout
		}(),
	})
	if err != nil {
		klog.Errorf("Failed to list pods by label selector %q: %v", labelSelector, err)
		return nil, err
	}

	return podList.Items, nil
}

func getParameterValue(args []string, parameter string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, parameter) {
			return strings.Split(arg, "=")[1]
		}

		// Handling the case where the command is in the form of /bin/sh -c exec ....
		if strings.Contains(arg, " ") {
			for _, subArg := range strings.Split(arg, " ") {
				if strings.HasPrefix(subArg, parameter) {
					return strings.Split(subArg, "=")[1]
				}
			}
		}
	}
	return ""
}
