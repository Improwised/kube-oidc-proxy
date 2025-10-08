package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// CAPIRoleBindingSpec defines the desired state of CAPIRoleBinding.
type CAPIRoleBindingSpec struct {
	TargetNamespaces  []string `json:"targetNamespaces,omitempty"`
	CommonBindingSpec `json:",inline"`
}

// CAPIRoleBindingStatus defines the observed state of CAPIRoleBinding.
type CAPIRoleBindingStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// CAPIRoleBinding is the Schema for the CAPIrolebindings API.
type CAPIRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIRoleBindingSpec   `json:"spec,omitempty"`
	Status CAPIRoleBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CAPIRoleBindingList contains a list of CAPIRoleBinding.
type CAPIRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CAPIRoleBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CAPIRoleBinding{}, &CAPIRoleBindingList{})
}
