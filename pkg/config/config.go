package config

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/pkg/known"
	"github.com/nauti-io/nauti/utils"
)

// GetHubConfig will loop until we can get a valid secret.
func GetHubConfig(kubeClientSet kubernetes.Interface, spec *known.Specification) (*rest.Config, error) {
	hubSecret, err := kubeClientSet.CoreV1().Secrets(known.NautiSystemNamespace).
		Get(context.TODO(), known.HubSecretName, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		// not exist, so create get bootstrap kube config from token
		clientConfig, tokenGenerateErr := utils.GenerateKubeConfigFromToken(spec.HubURL, spec.BootStrapToken, nil)
		if tokenGenerateErr != nil {
			return nil, fmt.Errorf("error while creating kubeconfig from bootstrap token: %v", tokenGenerateErr)
		}
		bootClient := kubernetes.NewForConfigOrDie(clientConfig)
		if secretList, secretListErr := bootClient.CoreV1().Secrets(known.NautiSystemNamespace).List(context.Background(),
			metav1.ListOptions{}); tokenGenerateErr != nil {
			return nil, fmt.Errorf("can't list hubSecret list from hub cluster: %v", secretListErr)
		} else {
			hubSecret = nil
			for _, secret := range secretList.Items {
				if secret.Type == corev1.SecretTypeServiceAccountToken &&
					secret.Annotations[corev1.ServiceAccountNameKey] == known.HubSecretName {
					// make sure it success.
					storeHubClusterCredentials(kubeClientSet, secret)
					hubSecret = secret.DeepCopy()
					break
				}
			}
			if hubSecret == nil {
				// can't get anything from hub
				return nil, fmt.Errorf("can't list a secret used for cross-cluster connection")
			}
		}
	} else if err != nil && !apierrors.IsNotFound(err) {
		// other error, can't handle
		return nil, fmt.Errorf("can't get a hub auth secre")
	}

	parentKubeConfig, err2 := utils.GenerateKubeConfigFromToken(
		spec.HubURL,
		string(hubSecret.Data[corev1.ServiceAccountTokenKey]),
		hubSecret.Data[corev1.ServiceAccountRootCAKey],
	)
	if err2 == nil {
		return parentKubeConfig, nil
	}
	return nil, err2
}

func storeHubClusterCredentials(kubeClientSet kubernetes.Interface, secret corev1.Secret) {
	klog.V(5).Infof("store parent cluster credentials to secret for later use")
	secretCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	huSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: known.HubSecretName,
			Labels: map[string]string{
				"created-by": "nauti-created",
			},
		},
		Data: map[string][]byte{
			corev1.ServiceAccountRootCAKey: secret.Data[corev1.ServiceAccountRootCAKey],
			corev1.ServiceAccountTokenKey:  secret.Data[corev1.ServiceAccountTokenKey],
		},
	}

	wait.JitterUntilWithContext(secretCtx, func(ctx context.Context) {
		_, err := kubeClientSet.CoreV1().Secrets(secret.Namespace).Create(ctx,
			huSecret, metav1.CreateOptions{})
		if err == nil {
			klog.V(5).Infof("successfully store parent cluster credentials")
			cancel()
			return
		}

		if apierrors.IsAlreadyExists(err) {
			klog.V(5).Infof("found existed parent cluster credentials, will try to update if needed")
			_, err = kubeClientSet.CoreV1().Secrets(secret.Namespace).Update(ctx,
				&secret, metav1.UpdateOptions{})
			if err == nil {
				cancel()
				return
			}
		}
		klog.ErrorDepth(5, fmt.Sprintf("failed to store parent cluster credentials: %v", err))
	}, 15*time.Second, 0.4, true)
}
