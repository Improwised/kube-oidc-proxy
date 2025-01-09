package rbac

import (
	"context"
	"fmt"

	"github.com/Improwised/kube-oidc-proxy/pkg/proxy"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	apisv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	rbacvalidation "k8s.io/kubernetes/pkg/registry/rbac/validation"
)

var defalutRole = map[string]v1.PolicyRule{
	"devops": {
		Verbs:     []string{"*"},
		APIGroups: []string{"*"},
		Resources: []string{"*"},
	},
	"developer": {
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"*"},
		Resources: []string{"pods", "pods/log", "pods/exec"},
	},
	"developer-portforward": {
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"*"},
		Resources: []string{"pods", "pods/log", "pods/exec", "pods/portforward"},
	},
	"watcher": {
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"*"},
		Resources: []string{"*"},
	},
}

func LoadRBAC(RBACConfig util.RBAC, cluster *proxy.ClusterConfig) error {
	// Watch for namespace
	// if namespace is created then create role and rolebinding
	watchNamespace, err := cluster.Kubeclient.CoreV1().Namespaces().Watch(context.Background(), apisv1.ListOptions{Watch: true})
	if err != nil {
		return err
	}

	for roleName, role := range defalutRole {
		// Create ClusterRole
		RBACConfig.ClusterRoles = append(RBACConfig.ClusterRoles, &v1.ClusterRole{
			TypeMeta: apisv1.TypeMeta{
				Kind:       "ClusterRole",
				APIVersion: "rbac.authorization.k8s.io/v1",
			},
			ObjectMeta: apisv1.ObjectMeta{
				Name: fmt.Sprintf("%s:%s", cluster.Name, roleName),
			},
			Rules: []v1.PolicyRule{role},
		})
		// Create ClusterRoleBinding
		RBACConfig.ClusterRoleBindings = append(RBACConfig.ClusterRoleBindings, &v1.ClusterRoleBinding{
			TypeMeta: apisv1.TypeMeta{
				Kind:       "ClusterRoleBinding",
				APIVersion: "rbac.authorization.k8s.io/v1",
			},
			ObjectMeta: apisv1.ObjectMeta{
				Name: fmt.Sprintf("%s:%s", cluster.Name, roleName),
			},
			Subjects: []v1.Subject{
				{
					Kind:     "Group",
					Name:     fmt.Sprintf("%s:%s", cluster.Name, roleName),
					APIGroup: "rbac.authorization.k8s.io",
				},
			},
			RoleRef: v1.RoleRef{
				Kind: "ClusterRole",
				Name: fmt.Sprintf("%s:%s", cluster.Name, roleName),
			},
		})
	}

	go func() {
		for e := range watchNamespace.ResultChan() {
			switch e.Type {
			// If namespace is created then create role and rolebinding
			case watch.Added:
				for role, policy := range defalutRole {
					// Create Role
					RBACConfig.Roles = append(RBACConfig.Roles, &v1.Role{
						TypeMeta: apisv1.TypeMeta{
							Kind:       "Role",
							APIVersion: "rbac.authorization.k8s.io/v1",
						},
						ObjectMeta: apisv1.ObjectMeta{
							Namespace: e.Object.(*corev1.Namespace).Name,
							Name:      fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name),
						},
						Rules: []v1.PolicyRule{policy},
					})
					// Create RoleBinding
					RBACConfig.RoleBindings = append(RBACConfig.RoleBindings, &v1.RoleBinding{
						TypeMeta: apisv1.TypeMeta{
							Kind:       "RoleBinding",
							APIVersion: "rbac.authorization.k8s.io/v1",
						},
						ObjectMeta: apisv1.ObjectMeta{
							Namespace: e.Object.(*corev1.Namespace).Name,
							Name:      fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name),
						},
						Subjects: []v1.Subject{
							{
								Kind:     "Group",
								Name:     fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name),
								APIGroup: "rbac.authorization.k8s.io",
							},
						},
						RoleRef: v1.RoleRef{
							Kind: "Role",
							Name: fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name),
						},
					})
				}
			// If namespace is deleted then delete role and rolebinding
			case watch.Deleted:
				for role := range defalutRole {
					// Delete Role
					for i, r := range RBACConfig.Roles {
						if r.Name == fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name) {
							RBACConfig.Roles = append(RBACConfig.Roles[:i], RBACConfig.Roles[i+1:]...)
						}
					}
					// Delete RoleBinding
					for i, r := range RBACConfig.RoleBindings {
						if r.Name == fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name) {
							RBACConfig.RoleBindings = append(RBACConfig.RoleBindings[:i], RBACConfig.RoleBindings[i+1:]...)
						}
					}
				}
			case watch.Modified:
				continue

			}
			_, StaticRoles := rbacvalidation.NewTestRuleResolver(RBACConfig.Roles, RBACConfig.RoleBindings, RBACConfig.ClusterRoles, RBACConfig.ClusterRoleBindings)
			cluster.Authorizer = util.NewAuthorizer(StaticRoles)

		}
	}()
	return nil
}
