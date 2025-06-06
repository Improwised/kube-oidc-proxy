package rbac

import (
	"context"
	"fmt"

	"github.com/Improwised/kube-oidc-proxy/pkg/models"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	apisv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
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
		Resources: []string{"pods", "pods/log"},
	},
	"developer-portforward": {
		Verbs:     []string{"get", "list", "watch", "create"},
		APIGroups: []string{""},
		Resources: []string{"pods", "pods/log", "pods/portforward"},
	},
	"watcher": {
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{"*"},
		Resources: []string{"*"},
	},
	"developer-exec-portforward": {
		Verbs:     []string{"get", "list", "watch", "create"},
		APIGroups: []string{"", "metrics.k8s.io"},
		Resources: []string{"pods", "pods/log", "pods/portforward", "pods/exec"},
	},
}

func LoadRBAC(cluster *models.Cluster) error {

	// First load existing RBAC resources from the cluster
	err := loadExistingRBAC(cluster)
	if err != nil {
		return fmt.Errorf("failed to load existing RBAC: %v", err)
	}

	// Set up watchers for RBAC resources
	err = setupRBACWatchers(cluster)
	if err != nil {
		return fmt.Errorf("failed to setup RBAC watchers: %v", err)
	}

	// Watch for namespace
	// if namespace is created then create role and rolebinding
	watchNamespace, err := cluster.Kubeclient.CoreV1().Namespaces().Watch(context.Background(), apisv1.ListOptions{Watch: true})
	if err != nil {
		return err
	}

	for roleName, role := range defalutRole {
		// Create ClusterRole
		cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles, &v1.ClusterRole{
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
		cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings, &v1.ClusterRoleBinding{
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
					cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles, &v1.Role{
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
					cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings, &v1.RoleBinding{
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
					for i, r := range cluster.RBACConfig.Roles {
						if r.Name == fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name) {
							cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles[:i], cluster.RBACConfig.Roles[i+1:]...)
						}
					}
					// Delete RoleBinding
					for i, r := range cluster.RBACConfig.RoleBindings {
						if r.Name == fmt.Sprintf("%s:%s:%s", cluster.Name, role, e.Object.(*corev1.Namespace).Name) {
							cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings[:i], cluster.RBACConfig.RoleBindings[i+1:]...)
						}
					}
				}
			case watch.Modified:
				continue

			}

			updateAuthorizer(cluster)

		}
	}()
	return nil
}

func loadExistingRBAC(cluster *models.Cluster) error {
	// List existing ClusterRoles
	clusterRoles, err := cluster.Kubeclient.RbacV1().ClusterRoles().List(context.Background(), apisv1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ClusterRoles: %v", err)
	}
	for i := range clusterRoles.Items {
		cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles, &clusterRoles.Items[i])
	}

	// List existing ClusterRoleBindings
	clusterRoleBindings, err := cluster.Kubeclient.RbacV1().ClusterRoleBindings().List(context.Background(), apisv1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ClusterRoleBindings: %v", err)
	}
	for i := range clusterRoleBindings.Items {
		cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings, &clusterRoleBindings.Items[i])
	}

	// For each namespace, list Roles and RoleBindings
	// List all namespaces to get roles and role bindings
	namespaces, err := cluster.Kubeclient.CoreV1().Namespaces().List(context.Background(), apisv1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}
	for _, ns := range namespaces.Items {
		// List Roles
		roles, err := cluster.Kubeclient.RbacV1().Roles(ns.Name).List(context.Background(), apisv1.ListOptions{})
		if err != nil {
			klog.Warningf("Failed to list Roles in namespace %s: %v", ns.Name, err)
			continue
		}
		for i := range roles.Items {
			cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles, &roles.Items[i])
		}

		// List RoleBindings
		roleBindings, err := cluster.Kubeclient.RbacV1().RoleBindings(ns.Name).List(context.Background(), apisv1.ListOptions{})
		if err != nil {
			klog.Warningf("Failed to list RoleBindings in namespace %s: %v", ns.Name, err)
			continue
		}
		for i := range roleBindings.Items {
			cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings, &roleBindings.Items[i])
		}

	}

	updateAuthorizer(cluster)

	return nil
}

func setupRBACWatchers(cluster *models.Cluster) error {
	// Watch ClusterRoles
	watchClusterRoles, err := cluster.Kubeclient.RbacV1().ClusterRoles().Watch(context.Background(), apisv1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch ClusterRoles: %v", err)
	}

	// Watch ClusterRoleBindings
	watchClusterRoleBindings, err := cluster.Kubeclient.RbacV1().ClusterRoleBindings().Watch(context.Background(), apisv1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch ClusterRoleBindings: %v", err)
	}

	// Watch Namespaces for Role/RoleBinding changes
	watchNamespaces, err := cluster.Kubeclient.CoreV1().Namespaces().Watch(context.Background(), apisv1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch Namespaces: %v", err)
	}

	// Start ClusterRole watcher
	go func() {
		for event := range watchClusterRoles.ResultChan() {
			clusterRole, ok := event.Object.(*v1.ClusterRole)
			if !ok {
				klog.Errorf("Unexpected object type in ClusterRole watch: %T", event.Object)
				continue
			}

			switch event.Type {
			case watch.Added:
				cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles, clusterRole)
			case watch.Modified:
				for i, cr := range cluster.RBACConfig.ClusterRoles {
					if cr.Name == clusterRole.Name {
						cluster.RBACConfig.ClusterRoles[i] = clusterRole
						break
					}
				}
			case watch.Deleted:
				for i, cr := range cluster.RBACConfig.ClusterRoles {
					if cr.Name == clusterRole.Name {
						cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles[:i], cluster.RBACConfig.ClusterRoles[i+1:]...)
						break
					}
				}
			}
			updateAuthorizer(cluster)
		}
	}()

	// Start ClusterRoleBinding watcher
	go func() {
		for event := range watchClusterRoleBindings.ResultChan() {
			crb, ok := event.Object.(*v1.ClusterRoleBinding)
			if !ok {
				klog.Errorf("Unexpected object type in ClusterRoleBinding watch: %T", event.Object)
				continue
			}

			switch event.Type {
			case watch.Added:
				cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings, crb)
			case watch.Modified:
				for i, binding := range cluster.RBACConfig.ClusterRoleBindings {
					if binding.Name == crb.Name {
						cluster.RBACConfig.ClusterRoleBindings[i] = crb
						break
					}
				}
			case watch.Deleted:
				for i, binding := range cluster.RBACConfig.ClusterRoleBindings {
					if binding.Name == crb.Name {
						cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings[:i], cluster.RBACConfig.ClusterRoleBindings[i+1:]...)
						break
					}
				}
			}
			updateAuthorizer(cluster)
		}
	}()

	// Start Namespace watcher for Role and RoleBinding changes
	go func() {
		for event := range watchNamespaces.ResultChan() {
			ns, ok := event.Object.(*corev1.Namespace)
			if !ok {
				klog.Errorf("Unexpected object type in Namespace watch: %T", event.Object)
				continue
			}

			switch event.Type {
			case watch.Added:
				// Set up Role watcher for new namespace
				watchRoles, err := cluster.Kubeclient.RbacV1().Roles(ns.Name).Watch(context.Background(), apisv1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to watch Roles in namespace %s: %v", ns.Name, err)
					continue
				}

				// Set up RoleBinding watcher for new namespace
				watchRoleBindings, err := cluster.Kubeclient.RbacV1().RoleBindings(ns.Name).Watch(context.Background(), apisv1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to watch RoleBindings in namespace %s: %v", ns.Name, err)
					continue
				}

				// Start Role watcher for this namespace
				go watchNamespaceRoles(watchRoles, cluster)

				// Start RoleBinding watcher for this namespace
				go watchNamespaceRoleBindings(watchRoleBindings, cluster)
			}
		}
	}()

	return nil
}

func watchNamespaceRoles(watchRoles watch.Interface, cluster *models.Cluster) {
	for event := range watchRoles.ResultChan() {
		role, ok := event.Object.(*v1.Role)
		if !ok {
			klog.Errorf("Unexpected object type in Role watch: %T", event.Object)
			continue
		}

		switch event.Type {
		case watch.Added:
			cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles, role)
		case watch.Modified:
			for i, r := range cluster.RBACConfig.Roles {
				if r.Name == role.Name && r.Namespace == role.Namespace {
					cluster.RBACConfig.Roles[i] = role
					break
				}
			}
		case watch.Deleted:
			for i, r := range cluster.RBACConfig.Roles {
				if r.Name == role.Name && r.Namespace == role.Namespace {
					cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles[:i], cluster.RBACConfig.Roles[i+1:]...)
					break
				}
			}
		}
		updateAuthorizer(cluster)
	}
}

func watchNamespaceRoleBindings(watchRoleBindings watch.Interface, cluster *models.Cluster) {
	for event := range watchRoleBindings.ResultChan() {
		rb, ok := event.Object.(*v1.RoleBinding)
		if !ok {
			klog.Errorf("Unexpected object type in RoleBinding watch: %T", event.Object)
			continue
		}

		switch event.Type {
		case watch.Added:
			cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings, rb)
		case watch.Modified:
			for i, binding := range cluster.RBACConfig.RoleBindings {
				if binding.Name == rb.Name && binding.Namespace == rb.Namespace {
					cluster.RBACConfig.RoleBindings[i] = rb
					break
				}
			}
		case watch.Deleted:
			for i, binding := range cluster.RBACConfig.RoleBindings {
				if binding.Name == rb.Name && binding.Namespace == rb.Namespace {
					cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings[:i], cluster.RBACConfig.RoleBindings[i+1:]...)
					break
				}
			}
		}
		updateAuthorizer(cluster)
	}
}

func updateAuthorizer(cluster *models.Cluster) {
	_, staticRoles := rbacvalidation.NewTestRuleResolver(
		cluster.RBACConfig.Roles,
		cluster.RBACConfig.RoleBindings,
		cluster.RBACConfig.ClusterRoles,
		cluster.RBACConfig.ClusterRoleBindings,
	)
	cluster.Authorizer = util.NewAuthorizer(staticRoles)
}
