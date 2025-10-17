package constants

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	Group                      = "rbac.platformengineers.io"
	Version                    = "v1"
	CAPIClusterRoleKind        = "capiclusterroles"
	CAPIClusterRoleBindingKind = "capiclusterrolebindings"
	CAPIRoleKind               = "capiroles"
	CAPIRoleBindingKind        = "capirolebindings"
)

// test constants
const (
	ClusterName = "kind-cluster"
)

var (
	CAPIRoleGVR = schema.GroupVersionResource{
		Group:    Group,
		Version:  Version,
		Resource: CAPIRoleKind,
	}
	CAPIRoleBindingGVR = schema.GroupVersionResource{
		Group:    Group,
		Version:  Version,
		Resource: CAPIRoleBindingKind,
	}
	CAPIClusterRoleGVR = schema.GroupVersionResource{
		Group:    Group,
		Version:  Version,
		Resource: CAPIClusterRoleKind,
	}
	CAPIClusterRoleBindingGVR = schema.GroupVersionResource{
		Group:    Group,
		Version:  Version,
		Resource: CAPIClusterRoleBindingKind,
	}
)
