package util

import (
	rbacvalidation "k8s.io/kubernetes/pkg/registry/rbac/validation"
	rbac "k8s.io/kubernetes/plugin/pkg/auth/authorizer/rbac"
)

func NewAuthorizer(r *rbacvalidation.StaticRoles) *rbac.RBACAuthorizer {
	return rbac.New(r, r, r, r)
}
