package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// CAPIRoleSpec defines the desired state of CAPIRole.
type CAPIRoleSpec struct {
	CommonRoleSpec   `json:",inline"`
	TargetNamespaces []string `json:"targetNamespaces,omitempty"`
}

// CAPIRoleStatus defines the observed state of CAPIRole.
type CAPIRoleStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// CAPIRole is the Schema for the CAPIroles API.
type CAPIRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIRoleSpec   `json:"spec,omitempty"`
	Status CAPIRoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CAPIRoleList contains a list of CAPIRole.
type CAPIRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CAPIRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CAPIRole{}, &CAPIRoleList{})
}
