package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// CAPIClusterRoleBindingSpec defines the desired state of CAPIClusterRoleBinding.
type CAPIClusterRoleBindingSpec struct {
	CommonBindingSpec `json:",inline"`
}

// CAPIClusterRoleBindingStatus defines the observed state of CAPIClusterRoleBinding.
type CAPIClusterRoleBindingStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// CAPIClusterRoleBinding is the Schema for the CAPIclusterrolebindings API.
type CAPIClusterRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIClusterRoleBindingSpec   `json:"spec,omitempty"`
	Status CAPIClusterRoleBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CAPIClusterRoleBindingList contains a list of CAPIClusterRoleBinding.
type CAPIClusterRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CAPIClusterRoleBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CAPIClusterRoleBinding{}, &CAPIClusterRoleBindingList{})
}
