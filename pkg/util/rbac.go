package util

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Role struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Namespace   string `yaml:"namespace,omitempty"`
		ClusterName string `yaml:"clusterName,omitempty"`
	} `yaml:"metadata"`
	Rules []Rules `yaml:"rules"`
}

type RoleBinding struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Namespace   string `yaml:"namespace,omitempty"`
		ClusterName string `yaml:"clusterName,omitempty"`
	} `yaml:"metadata"`
	RoleRef struct {
		APIGroup string `yaml:"apiGroup"`
		Kind     string `yaml:"kind"`
		Name     string `yaml:"name"`
	} `yaml:"roleRef"`
	Subjects []Subject `yaml:"subjects"`
}

type ClusterRole struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		ClusterName string `yaml:"clusterName,omitempty"`
	} `yaml:"metadata"`
	Rules []Rules `yaml:"rules"`
}

type ClusterRoleBinding struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		ClusterName string `yaml:"clusterName,omitempty"`
	} `yaml:"metadata"`
	RoleRef struct {
		APIGroup string `yaml:"apiGroup"`
		Kind     string `yaml:"kind"`
		Name     string `yaml:"name"`
	} `yaml:"roleRef"`
	Subjects []Subject `yaml:"subjects"`
}

type Subject struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	APIGroup  string `yaml:"apiGroup,omitempty"`
	Namespace string `yaml:"namespace,omitempty"`
}

type Rules struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

type RBAC struct {
	Roles               []*v1.Role
	RoleBindings        []*v1.RoleBinding
	ClusterRoles        []*v1.ClusterRole
	ClusterRoleBindings []*v1.ClusterRoleBinding
}

func LoadRBACConfig(path string) (map[string]RBAC, error) {
	var clusterRBACMapper = map[string]RBAC{}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	decoder := yaml.NewDecoder(strings.NewReader(string(data)))

	// Decode objects in YAML
	for {
		var obj map[string]interface{}
		err := decoder.Decode(&obj)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break // End of file
			}
			return nil, fmt.Errorf("failed to decode YAML: %w", err)
		}

		// Handle object by kind
		kind, ok := obj["kind"].(string)
		if !ok {
			continue
		}

		switch kind {
		case "Role":
			if err := addRole(obj, clusterRBACMapper); err != nil {
				return nil, err
			}
		case "RoleBinding":
			if err := addRoleBinding(obj, clusterRBACMapper); err != nil {
				return nil, err
			}
		case "ClusterRole":
			if err := addClusterRole(obj, clusterRBACMapper); err != nil {
				return nil, err
			}
		case "ClusterRoleBinding":
			if err := addClusterRoleBinding(obj, clusterRBACMapper); err != nil {
				return nil, err
			}
		default:
			// Log unsupported kinds for debugging
			fmt.Printf("unsupported kind: %s\n", kind)
		}
	}

	return clusterRBACMapper, nil
}

// Helper functions for handling specific kinds
func addRole(obj map[string]interface{}, mapper map[string]RBAC) error {
	var role Role
	if err := mapToStruct(obj, &role); err != nil {
		return fmt.Errorf("failed to parse Role: %w", err)
	}

	RBAC := mapper[role.Metadata.ClusterName]
	RBAC.Roles = append(RBAC.Roles, &v1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      role.Metadata.Name,
			Namespace: role.Metadata.Namespace,
		},
		Rules: getRules(role.Rules),
	})
	mapper[role.Metadata.ClusterName] = RBAC
	return nil
}

func addRoleBinding(obj map[string]interface{}, mapper map[string]RBAC) error {
	var roleBinding RoleBinding
	if err := mapToStruct(obj, &roleBinding); err != nil {
		return fmt.Errorf("failed to parse RoleBinding: %w", err)
	}

	RBAC := mapper[roleBinding.Metadata.ClusterName]
	RBAC.RoleBindings = append(RBAC.RoleBindings, &v1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBinding.Metadata.Name,
			Namespace: roleBinding.Metadata.Namespace,
		},
		Subjects: getSubject(roleBinding.Subjects),
		RoleRef: v1.RoleRef{
			Kind: roleBinding.RoleRef.Kind,
			Name: roleBinding.RoleRef.Name,
		},
	})
	mapper[roleBinding.Metadata.ClusterName] = RBAC
	return nil
}

func addClusterRole(obj map[string]interface{}, mapper map[string]RBAC) error {
	var clusterRole ClusterRole
	if err := mapToStruct(obj, &clusterRole); err != nil {
		return fmt.Errorf("failed to parse ClusterRole: %w", err)
	}

	RBAC := mapper[clusterRole.Metadata.ClusterName]
	RBAC.ClusterRoles = append(RBAC.ClusterRoles, &v1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRole.Metadata.Name,
		},
		Rules: getRules(clusterRole.Rules),
	})
	mapper[clusterRole.Metadata.ClusterName] = RBAC
	return nil
}

func addClusterRoleBinding(obj map[string]interface{}, mapper map[string]RBAC) error {
	var clusterRoleBinding ClusterRoleBinding
	if err := mapToStruct(obj, &clusterRoleBinding); err != nil {
		return fmt.Errorf("failed to parse ClusterRoleBinding: %w", err)
	}

	RBAC := mapper[clusterRoleBinding.Metadata.ClusterName]
	RBAC.ClusterRoleBindings = append(RBAC.ClusterRoleBindings, &v1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRoleBinding.Metadata.Name,
		},
		Subjects: getSubject(clusterRoleBinding.Subjects),
		RoleRef: v1.RoleRef{
			Kind: clusterRoleBinding.RoleRef.Kind,
			Name: clusterRoleBinding.RoleRef.Name,
		},
	})
	mapper[clusterRoleBinding.Metadata.ClusterName] = RBAC
	return nil
}
func getSubject(Subjects []Subject) []v1.Subject {
	var subjects []v1.Subject
	for _, subject := range Subjects {
		subjects = append(subjects, v1.Subject{
			Kind:      subject.Kind,
			Name:      subject.Name,
			APIGroup:  subject.APIGroup,
			Namespace: subject.Namespace,
		})
	}
	return subjects
}

func getRules(Rules []Rules) []v1.PolicyRule {
	var rules []v1.PolicyRule
	for _, rule := range Rules {
		rules = append(rules, v1.PolicyRule{
			APIGroups: rule.APIGroups,
			Resources: rule.Resources,
			Verbs:     rule.Verbs,
		})
	}
	return rules
}

func mapToStruct(input map[string]interface{}, output interface{}) error {
	data, err := yaml.Marshal(input)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, output)
}
