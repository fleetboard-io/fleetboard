package config

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/utils"
)

func GetHubConfig(kubeClientSet kubernetes.Interface, HubAPIServer, hubSecretNamespace,
	hubSecretName string) (*rest.Config, error) {
	secret, err := kubeClientSet.CoreV1().Secrets(hubSecretNamespace).
		Get(context.TODO(), hubSecretName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Errorf("failed to get secret of parent cluster: %v", err)
		return nil, err
	}
	if err == nil {
		klog.Infof("found existing secretFromParentCluster '%s/%s' that can be used to access parent cluster",
			hubSecretNamespace, hubSecretName)

		parentDedicatedKubeConfig, err2 := utils.GenerateKubeConfigFromToken(
			HubAPIServer,
			string(secret.Data[corev1.ServiceAccountTokenKey]),
			secret.Data[corev1.ServiceAccountRootCAKey],
		)
		if err2 == nil {
			return parentDedicatedKubeConfig, nil
		}
		return nil, err2
	}
	return nil, err
}
