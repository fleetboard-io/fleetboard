package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope="Namespaced",shortName=peer;peers,categories=octopus
type Peer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PeerSpec `json:"spec"`
}

type PeerSpec struct {
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	ClusterID string   `json:"cluster_id"` // name must be unique.
	PodCIDR   []string `json:"cluster_cidr"`
	Endpoint  string   `json:"endpoint"` // public node address: ip + node port
	Port      int      `json:"port"`
	PublicKey string   `json:"public_key"` // wire-guard public key
	IsHub     bool     `json:"ishub"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PeerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Peer `json:"items"`
}
