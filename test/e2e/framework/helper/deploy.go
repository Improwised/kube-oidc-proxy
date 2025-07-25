// Copyright Jetstack Ltd. See LICENSE for details.
package helper

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/Improwised/kube-oidc-proxy/constants"
	"github.com/Improwised/kube-oidc-proxy/test/kind"
	"github.com/Improwised/kube-oidc-proxy/test/util"
)

func (h *Helper) DeployProxy(ns *corev1.Namespace, issuerURL *url.URL, clientID string,
	oidcKeyBundle *util.KeyBundle, extraVolumes []corev1.Volume, extraArgs ...string) (*util.KeyBundle, *url.URL, error) {

	// Add clusters-config and kubeconfig setup
	kindKubeconfigBytes, err := os.ReadFile(h.cfg.KubeConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read KinD kubeconfig: %v", err)
	}

	serverRegex := regexp.MustCompile(`server: https://127\.0\.0\.1:\d+`)
	kindKubeConfig := serverRegex.ReplaceAllString(string(kindKubeconfigBytes),
		"server: https://kube-oidc-proxy-e2e-control-plane:6443")

	kubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kind-kubeconfig",
			Namespace: ns.Name,
		},
		Data: map[string][]byte{
			"config": []byte(kindKubeConfig),
		},
	}
	_, err = h.KubeClient.CoreV1().Secrets(ns.Name).Create(context.TODO(), kubeconfigSecret, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	clustersConfigYAML := fmt.Sprintf(`clusters:
  - name: %s
    kubeconfig: "/etc/kind-kubeconfig/config"
`, constants.ClusterName)

	clustersConfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clusters-config",
			Namespace: ns.Name,
		},
		Data: map[string][]byte{
			"clusters.yaml": []byte(clustersConfigYAML),
		},
	}
	_, err = h.KubeClient.CoreV1().Secrets(ns.Name).Create(context.TODO(), clustersConfigSecret, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	// rbacConfigYAML := generateRBACConfigYAML(ns.Name)
	rbacConfigYAML := ""

	rbacConfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rbac-config",
			Namespace: ns.Name,
		},
		Data: map[string][]byte{
			"rbac.yaml": []byte(rbacConfigYAML),
		},
	}
	_, err = h.KubeClient.CoreV1().Secrets(ns.Name).Create(context.TODO(), rbacConfigSecret, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	extraVolumes = append(extraVolumes,
		corev1.Volume{
			Name: "kind-kubeconfig",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "kind-kubeconfig",
				},
			},
		},
		corev1.Volume{
			Name: "clusters-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "clusters-config",
				},
			},
		},
		corev1.Volume{
			Name: "rbac-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "rbac-config",
				},
			},
		},
	)

	cnt := corev1.Container{
		Name:            kind.ProxyImageName,
		Image:           kind.ProxyImageName,
		ImagePullPolicy: corev1.PullNever,
		Command:         []string{"./proxy"},
		Args: append([]string{
			"--secure-port=6443",
			"--tls-cert-file=/tls/cert.pem",
			"--tls-private-key-file=/tls/key.pem",
			fmt.Sprintf("--oidc-client-id=%s", clientID),
			fmt.Sprintf("--oidc-issuer-url=%s", issuerURL),
			"--oidc-username-claim=email",
			"--oidc-groups-claim=groups",
			"--oidc-ca-file=/oidc/ca.pem",
			"--oidc-ca-file=/oidc/ca.pem",
			"--v=10",
			"--audit-webhook-server=https://127.0.0.1:8989",
			"--clusters-config=/etc/clusters-config/clusters.yaml",
			"--audit-webhook-server=https://127.0.0.1:8989",
		}, extraArgs...),
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				MountPath: "/tls",
				Name:      "tls",
				ReadOnly:  true,
			},
			corev1.VolumeMount{
				MountPath: "/oidc",
				Name:      "oidc",
				ReadOnly:  true,
			},
			corev1.VolumeMount{
				Name:      "kind-kubeconfig",
				MountPath: "/etc/kind-kubeconfig",
				ReadOnly:  true,
			},
			corev1.VolumeMount{
				Name:      "clusters-config",
				MountPath: "/etc/clusters-config",
				ReadOnly:  true,
			},
			corev1.VolumeMount{
				Name:      "rbac-config",
				MountPath: "/etc/rbac-config",
				ReadOnly:  true,
			},
		},
		Ports: []corev1.ContainerPort{
			corev1.ContainerPort{
				ContainerPort: 6443,
			},
			corev1.ContainerPort{
				ContainerPort: 8080,
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/ready",
					Port: intstr.FromInt(8080),
				},
			},
			InitialDelaySeconds: 1,
			PeriodSeconds:       3,
		},
		Env: []corev1.EnvVar{
			{
				Name:  "KUBECONFIG",
				Value: "/etc/kind-kubeconfig/config",
			},
		},
	}

	for _, v := range extraVolumes {
		cnt.VolumeMounts = append(cnt.VolumeMounts, corev1.VolumeMount{
			MountPath: fmt.Sprintf("/%s", v.Name),
			Name:      v.Name,
			ReadOnly:  true,
		})
	}

	volumes := append(extraVolumes, corev1.Volume{
		Name: "oidc",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "oidc-ca",
			},
		},
	})

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oidc-ca",
			Namespace: ns.Name,
		},
		Data: map[string][]byte{
			"ca.pem": oidcKeyBundle.CertBytes,
		},
	}

	_, err = h.KubeClient.CoreV1().Secrets(ns.Name).Create(context.TODO(), sec, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	pTrue := true
	pFalse := false

	crole, err := h.KubeClient.RbacV1().ClusterRoles().Create(context.TODO(), &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: kind.ProxyImageName + "-",
			OwnerReferences: []metav1.OwnerReference{
				metav1.OwnerReference{
					APIVersion:         "core/v1",
					BlockOwnerDeletion: &pTrue,
					Controller:         &pFalse,
					Kind:               "Namespace",
					Name:               ns.Name,
					UID:                ns.UID,
				},
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"users", "groups", "serviceaccounts"},
				Verbs:     []string{"impersonate"},
			},
			{
				APIGroups: []string{"authentication.k8s.io"},
				Resources: []string{"userextras/scopes", "tokenreviews", "userextras/originaluser.jetstack.io-user", "userextras/originaluser.jetstack.io-groups", "userextras/originaluser.jetstack.io-extra", "userextras/oktoimpersonateextra"},
				Verbs:     []string{"impersonate", "create"},
			},
			{
				APIGroups: []string{"authorization.k8s.io"},
				Resources: []string{"subjectaccessreviews"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	// Create a role that will allow a user to impersonate another user
	croleImpersonate, err := h.KubeClient.RbacV1().ClusterRoles().Create(context.TODO(), &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: kind.ProxyImageName + "-impersonate-",
			OwnerReferences: []metav1.OwnerReference{
				metav1.OwnerReference{
					APIVersion:         "core/v1",
					BlockOwnerDeletion: &pTrue,
					Controller:         &pFalse,
					Kind:               "Namespace",
					Name:               ns.Name,
					UID:                ns.UID,
				},
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"users"},
				ResourceNames: []string{"ok-to-impersonate@nodomain.dev"},
				Verbs:         []string{"impersonate"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"groups"},
				ResourceNames: []string{"ok-to-impersonate-group"},
				Verbs:         []string{"impersonate"},
			},
			{
				APIGroups:     []string{"authentication.k8s.io"},
				Resources:     []string{"userextras/oktoimpersonateextra"},
				ResourceNames: []string{"foo"},
				Verbs:         []string{"impersonate"},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	// Create a ClusterRoleBinding so the user can impersonate test users

	_, err = h.KubeClient.RbacV1().ClusterRoleBindings().Create(context.TODO(),
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: kind.ProxyImageName + "-impersonate-",
				OwnerReferences: []metav1.OwnerReference{
					metav1.OwnerReference{
						APIVersion:         "core/v1",
						BlockOwnerDeletion: &pTrue,
						Controller:         &pFalse,
						Kind:               "Namespace",
						Name:               ns.Name,
						UID:                ns.UID,
					},
				},
			},
			RoleRef: rbacv1.RoleRef{
				Name: croleImpersonate.Name, Kind: "ClusterRole",
			},
			Subjects: []rbacv1.Subject{
				{Name: "user@example.com", Kind: "User"},
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	_, err = h.KubeClient.RbacV1().ClusterRoleBindings().Create(context.TODO(),
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: kind.ProxyImageName + "-",
				OwnerReferences: []metav1.OwnerReference{
					metav1.OwnerReference{
						APIVersion:         "core/v1",
						BlockOwnerDeletion: &pTrue,
						Controller:         &pFalse,
						Kind:               "Namespace",
						Name:               ns.Name,
						UID:                ns.UID,
					},
				},
			},
			RoleRef: rbacv1.RoleRef{
				Name: crole.Name, Kind: "ClusterRole",
			},
			Subjects: []rbacv1.Subject{
				{Name: kind.ProxyImageName, Namespace: ns.Name, Kind: "ServiceAccount"},
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	bundle, appURL, err := h.deployApp(ns.Name, kind.ProxyImageName, corev1.ServiceTypeNodePort, cnt, volumes...)
	if err != nil {
		return nil, nil, err
	}

	return bundle, appURL, nil
}

func (h *Helper) DeployIssuer(ns string) (*util.KeyBundle, *url.URL, error) {
	cnt := corev1.Container{
		Name:            kind.IssuerImageName,
		Image:           kind.IssuerImageName,
		ImagePullPolicy: corev1.PullNever,
		Args: []string{
			"oidc-issuer",
			"--secure-port=6443",
			fmt.Sprintf("--issuer-url=https://oidc-issuer-e2e.%s.svc.cluster.local:6443", ns),
			"--tls-cert-file=/tls/cert.pem",
			"--tls-private-key-file=/tls/key.pem",
		},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				MountPath: "/tls",
				Name:      "tls",
				ReadOnly:  true,
			},
		},
		Ports: []corev1.ContainerPort{
			corev1.ContainerPort{
				ContainerPort: 6443,
			},
		},
	}

	bundle, appURL, err := h.deployApp(ns, kind.IssuerImageName, corev1.ServiceTypeClusterIP, cnt)
	if err != nil {
		return nil, nil, err
	}

	return bundle, appURL, nil
}

func (h *Helper) DeployFakeAPIServer(ns string) ([]corev1.Volume, *url.URL, error) {
	cnt := corev1.Container{
		Name:            kind.FakeAPIServerImageName,
		Image:           kind.FakeAPIServerImageName,
		ImagePullPolicy: corev1.PullNever,
		Args: []string{
			"fake-apiserver",
			"--secure-port=6443",
			"--tls-cert-file=/tls/cert.pem",
			"--tls-private-key-file=/tls/key.pem",
		},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				MountPath: "/tls",
				Name:      "tls",
				ReadOnly:  true,
			},
		},
		Ports: []corev1.ContainerPort{
			corev1.ContainerPort{
				ContainerPort: 6443,
			},
		},
	}

	bundle, appURL, err := h.deployApp(ns, kind.FakeAPIServerImageName, corev1.ServiceTypeClusterIP, cnt)
	if err != nil {
		return nil, nil, err
	}

	sec, err := h.KubeClient.CoreV1().Secrets(ns).Create(context.TODO(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fake-apiserver-ca-",
			Namespace:    ns,
		},
		Data: map[string][]byte{
			"ca.pem": bundle.CertBytes,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	extraVolumes := []corev1.Volume{
		{
			Name: "fake-apiserver",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: sec.Name,
				},
			},
		},
	}

	return extraVolumes, appURL, nil
}

func (h *Helper) DeployAuditWebhook(ns, logPath string) (corev1.Volume, *url.URL, error) {
	cnt := corev1.Container{
		Name:            kind.AuditWebhookImageName,
		Image:           kind.AuditWebhookImageName,
		ImagePullPolicy: corev1.PullNever,
		Args: []string{
			"audit-webhook",
			"--secure-port=6443",
			"--tls-cert-file=/tls/cert.pem",
			"--tls-private-key-file=/tls/key.pem",
			"--audit-file-path=" + logPath,
		},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				MountPath: "/tls",
				Name:      "tls",
				ReadOnly:  true,
			},
		},
		Ports: []corev1.ContainerPort{
			corev1.ContainerPort{
				ContainerPort: 6443,
			},
		},
	}

	bundle, appURL, err := h.deployApp(ns, kind.AuditWebhookImageName, corev1.ServiceTypeClusterIP, cnt)
	if err != nil {
		return corev1.Volume{}, nil, err
	}

	sec, err := h.KubeClient.CoreV1().Secrets(ns).Create(context.TODO(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "audit-webhook-ca-",
			Namespace:    ns,
		},
		Data: map[string][]byte{
			"ca.pem": bundle.CertBytes,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return corev1.Volume{}, nil, err
	}

	auditWebhookCAVol := corev1.Volume{
		Name: "audit-webhook-ca",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: sec.Name,
			},
		},
	}

	return auditWebhookCAVol, appURL, nil
}

func (h *Helper) deployApp(ns, name string, serviceType corev1.ServiceType, container corev1.Container, volumes ...corev1.Volume) (*util.KeyBundle, *url.URL, error) {
	host, appURL := h.appURL(ns, name, "6443")

	var netIPs []net.IP
	if serviceType == corev1.ServiceTypeNodePort {
		nodes, err := h.KubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return nil, nil, err
		}

		for _, n := range nodes.Items {
			for _, addr := range n.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP {
					netIPs = append(netIPs, net.ParseIP(addr.Address))
				}
			}
		}
	}

	keyBundle, err := util.NewTLSSelfSignedCertKey(host, netIPs, nil)
	if err != nil {
		return nil, nil, err
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       6443,
					Protocol:   "TCP",
					TargetPort: intstr.FromInt(6443),
				},
			},
			Type: serviceType,
			Selector: map[string]string{
				"app": name,
			},
		},
	}

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Data: map[string][]byte{
			"cert.pem": keyBundle.CertBytes,
			"key.pem":  keyBundle.KeyBytes,
		},
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},

				Spec: corev1.PodSpec{
					ServiceAccountName: name,
					Containers:         []corev1.Container{container},
					Volumes: append(volumes,
						corev1.Volume{
							Name: "tls",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: name,
								},
							},
						},
					),
				},
			},
		},
	}

	svc, err = h.KubeClient.CoreV1().Services(ns).Create(context.TODO(), svc, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	if len(netIPs) > 0 {
		appURL = fmt.Sprintf("https://%s:%s", netIPs[0],
			strconv.FormatUint(uint64(svc.Spec.Ports[0].NodePort), 10))
	}

	_, err = h.KubeClient.CoreV1().Secrets(ns).Create(context.TODO(), sec, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	_, err = h.KubeClient.CoreV1().ServiceAccounts(ns).Create(context.TODO(), sa, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	_, err = h.KubeClient.AppsV1().Deployments(ns).Create(context.TODO(), deploy, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	if err := h.WaitForDeploymentReady(ns, name, time.Second*20); err != nil {
		return nil, nil, err
	}

	appNetURL, err := url.Parse(appURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse app url %q: %s",
			appURL, err)
	}

	// this is a hack.  better to launch a pod that will wait until the URL is available
	time.Sleep(2 * time.Second)

	return keyBundle, appNetURL, nil
}

func (h *Helper) DeleteProxy(ns string) error {
	return h.deleteApp(ns, kind.ProxyImageName, "oidc-ca", "kind-kubeconfig", "clusters-config", "rbac-config")
}
func (h *Helper) DeleteIssuer(ns string) error {
	return h.deleteApp(ns, kind.IssuerImageName)
}
func (h *Helper) DeleteFakeAPIServer(ns string) error {
	return h.deleteApp(ns, kind.FakeAPIServerImageName)
}

func (h *Helper) deleteApp(ns, name string, extraSecrets ...string) error {
	err := h.KubeClient.AppsV1().Deployments(ns).Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}

	for _, s := range append(extraSecrets, name) {
		err = h.KubeClient.CoreV1().Secrets(ns).Delete(context.TODO(), s, metav1.DeleteOptions{})
		if err != nil && !k8sErrors.IsNotFound(err) {
			return err
		}
	}

	err = h.KubeClient.CoreV1().Services(ns).Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}

	err = h.KubeClient.CoreV1().ServiceAccounts(ns).Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}

	return nil
}

func (h *Helper) appURL(ns, serviceName, port string) (string, string) {
	host := fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, ns)
	return host, fmt.Sprintf("https://%s:%s", host, port)
}

func (h *Helper) DeployCRDFromFile(filePath string) error {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read CRD file: %v", err)
	}

	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

	obj := &v1.CustomResourceDefinition{}
	_, _, err = decoder.Decode(data, nil, obj)
	if err != nil {
		return fmt.Errorf("failed to decode CRD YAML: %v", err)
	}

	_, err = h.ApiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(
		context.TODO(), obj, metav1.CreateOptions{},
	)

	if err != nil {
		return err
	}

	// Wait for CRD to be established
	const timeout = 30 * time.Second
	interval := 1 * time.Second
	start := time.Now()

	for {
		crd, err := h.ApiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(
			context.TODO(), obj.Name, metav1.GetOptions{},
		)
		if err != nil {
			return fmt.Errorf("failed to get CRD: %v", err)
		}

		for _, cond := range crd.Status.Conditions {
			if cond.Type == v1.Established && cond.Status == v1.ConditionTrue {
				return nil
			}
		}

		if time.Since(start) > timeout {
			return fmt.Errorf("timed out waiting for CRD %s to be established", obj.Name)
		}

		time.Sleep(interval)
	}
}

func (h *Helper) DeleteCRDFromFile(filePath string) error {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read CRD file: %v", err)
	}

	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

	crd := &v1.CustomResourceDefinition{}
	_, _, err = decoder.Decode(data, nil, crd)
	if err != nil {
		return fmt.Errorf("failed to decode CRD YAML: %v", err)
	}

	if crd.Name == "" {
		return fmt.Errorf("CRD name is empty after decoding")
	}

	return h.ApiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Delete(
		context.TODO(),
		crd.Name,
		metav1.DeleteOptions{},
	)
}
