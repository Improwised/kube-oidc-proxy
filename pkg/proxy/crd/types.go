package crd

import (
	"github.com/Improwised/kube-oidc-proxy/constants"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// RoleScope defines the scope of a role.
type RoleScope string

const (
	GlobalRole    RoleScope = "Global"
	ClusterRole   RoleScope = "Cluster"
	NamespaceRole RoleScope = "Namespace"
)

// CommonRoleSpec defines shared fields for role specifications.
type CommonRoleSpec struct {
	Name           string          `json:"name"`
	Scope          RoleScope       `json:"scope"`
	TargetClusters []string        `json:"targetClusters,omitempty"`
	Rules          []v1.PolicyRule `json:"rules,omitempty"`
}
type Subject struct {
	Group          string `json:"group,omitempty"`
	User           string `json:"user,omitempty"`
	ServiceAccount string `json:"serviceAccount,omitempty"`
}

// CommonBindingSpec defines shared fields for role binding specifications.
type CommonBindingSpec struct {
	TargetClusters []string  `json:"targetClusters,omitempty"`
	Name           string    `json:"name"`
	RoleRef        []string  `json:"roleRef"`
	Subjects       []Subject `json:"subjects"`
}

// CAPIClusterRoleSpec defines the desired state of CAPIClusterRole.
type CAPIClusterRoleSpec struct {
	CommonRoleSpec `json:",inline"`
}

// CAPIClusterRoleStatus defines the observed state of CAPIClusterRole.
type CAPIClusterRoleStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// CAPIClusterRole is the Schema for the CAPIclusterroles API.
type CAPIClusterRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIClusterRoleSpec   `json:"spec,omitempty"`
	Status CAPIClusterRoleStatus `json:"status,omitempty"`
}

// CAPIClusterRoleBindingSpec defines the desired state of CAPIClusterRoleBinding.
type CAPIClusterRoleBindingSpec struct {
	CommonBindingSpec `json:",inline"`
}

// CAPIClusterRoleBindingStatus defines the observed state of CAPIClusterRoleBinding.
type CAPIClusterRoleBindingStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// CAPIClusterRoleBinding is the Schema for the CAPIclusterrolebindings API.
type CAPIClusterRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIClusterRoleBindingSpec   `json:"spec,omitempty"`
	Status CAPIClusterRoleBindingStatus `json:"status,omitempty"`
}

// CAPIRoleSpec defines the desired state of CAPIRole.
type CAPIRoleSpec struct {
	CommonRoleSpec   `json:",inline"`
	TargetNamespaces []string `json:"targetNamespaces,omitempty"`
}

// CAPIRoleStatus defines the observed state of CAPIRole.
type CAPIRoleStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// CAPIRole is the Schema for the CAPIroles API.
type CAPIRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIRoleSpec   `json:"spec,omitempty"`
	Status CAPIRoleStatus `json:"status,omitempty"`
}

// CAPIRoleBindingSpec defines the desired state of CAPIRoleBinding.
type CAPIRoleBindingSpec struct {
	TargetNamespaces  []string `json:"targetNamespaces,omitempty"`
	CommonBindingSpec `json:",inline"`
}

// CAPIRoleBindingStatus defines the observed state of CAPIRoleBinding.
type CAPIRoleBindingStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// CAPIRoleBinding is the Schema for the CAPIrolebindings API.
type CAPIRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CAPIRoleBindingSpec   `json:"spec,omitempty"`
	Status CAPIRoleBindingStatus `json:"status,omitempty"`
}

var (
	CAPIRoleGVR = schema.GroupVersionResource{
		Group:    constants.Group,
		Version:  constants.Version,
		Resource: constants.CAPIRoleKind,
	}
	CAPIRoleBindingGVR = schema.GroupVersionResource{
		Group:    constants.Group,
		Version:  constants.Version,
		Resource: constants.CAPIRoleBindingKind,
	}
	CAPIClusterRoleGVR = schema.GroupVersionResource{
		Group:    constants.Group,
		Version:  constants.Version,
		Resource: constants.CAPIClusterRoleKind,
	}
	CAPIClusterRoleBindingGVR = schema.GroupVersionResource{
		Group:    constants.Group,
		Version:  constants.Version,
		Resource: constants.CAPIClusterRoleBindingKind,
	}
)
