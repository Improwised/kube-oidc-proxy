package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// CAPIClusterRoleSpec defines the desired state of CAPIClusterRole.
type CAPIClusterRoleSpec struct {
	CommonRoleSpec `json:",inline"`
}

// CAPIClusterRoleStatus defines the observed state of CAPIClusterRole.
type CAPIClusterRoleStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// CAPIClusterRole is the Schema for the CAPIclusterroles API.
type CAPIClusterRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIClusterRoleSpec   `json:"spec,omitempty"`
	Status CAPIClusterRoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CAPIClusterRoleList contains a list of CAPIClusterRole.
type CAPIClusterRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CAPIClusterRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CAPIClusterRole{}, &CAPIClusterRoleList{})
}
