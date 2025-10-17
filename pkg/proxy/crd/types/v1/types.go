package v1

import (
	v1 "k8s.io/api/rbac/v1"
)

// CommonRoleSpec defines shared fields for role specifications.
type CommonRoleSpec struct {
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
	TargetClusters []string     `json:"targetClusters,omitempty"`
	RoleRefs       []v1.RoleRef `json:"roleRefs"`
	Subjects       []Subject    `json:"subjects"`
	// +optional
	DurationMinutes *int32 `json:"durationMinutes,omitempty"`
}
