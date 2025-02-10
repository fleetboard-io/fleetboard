package utils

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	"github.com/fleetboard-io/fleetboard/pkg/apis/fleetboard.io/v1alpha1"
	clientset "github.com/fleetboard-io/fleetboard/pkg/generated/clientset/versioned"
)

// ApplyEndPointSliceWithRetry create or update existed slices.
func ApplyPeerWithRetry(client clientset.Interface, peer *v1alpha1.Peer) error {
	return wait.ExponentialBackoffWithContext(context.TODO(), retry.DefaultBackoff,
		func(ctx context.Context) (bool, error) {
			var lastError error
			_, lastError = client.FleetboardV1alpha1().Peers(peer.GetNamespace()).
				Create(context.TODO(), peer, metav1.CreateOptions{})
			if lastError == nil {
				klog.Infof("create peer %s successfully", peer.Name)
				return true, nil
			}
			klog.Warningf("create with error %v", lastError)
			if !errors.IsAlreadyExists(lastError) {
				klog.Infof("create with error %v", lastError)
				return false, lastError
			}

			curObj, err := client.FleetboardV1alpha1().Peers(peer.GetNamespace()).
				Get(context.TODO(), peer.GetName(), metav1.GetOptions{})
			if err != nil || curObj.DeletionTimestamp != nil {
				lastError = err
				klog.Infof("get with error %v", lastError)
				return false, err
			} else {
				lastError = nil
			}

			if ResourceNeedResync(curObj, peer, false) {
				// try to update peer
				curObj.Spec.PodCIDR = peer.Spec.PodCIDR
				curObj.Spec.Endpoint = peer.Spec.Endpoint
				curObj.Spec.PublicKey = peer.Spec.PublicKey
				curObj.Spec.ClusterID = peer.Spec.ClusterID
				curObj.Spec.IsPublic = peer.Spec.IsPublic
				curObj.Spec.Port = peer.Spec.Port
				_, lastError = client.FleetboardV1alpha1().Peers(peer.GetNamespace()).
					Update(context.TODO(), curObj, metav1.UpdateOptions{})
			}
			if lastError == nil {
				return true, nil
			}
			klog.Infof("get with error %v", lastError)
			return false, lastError
		})
}

func DeletePeerWithRetry(client clientset.Interface, name, namespace string) error {
	var err error
	err = wait.ExponentialBackoffWithContext(context.TODO(), retry.DefaultBackoff,
		func(ctx context.Context) (bool, error) {
			if err = client.FleetboardV1alpha1().Peers(namespace).
				Delete(context.TODO(), name, metav1.DeleteOptions{}); err != nil {
				return false, err
			}

			if err == nil || (err != nil && errors.IsNotFound(err)) {
				return true, nil
			}
			return false, nil
		})
	if err == nil {
		return nil
	}
	return err
}
