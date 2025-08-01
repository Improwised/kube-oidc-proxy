// Copyright Jetstack Ltd. See LICENSE for details.
package framework

import (
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Improwised/kube-oidc-proxy/constants"
	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework/config"
	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework/helper"
	"github.com/Improwised/kube-oidc-proxy/test/kind"
	"github.com/Improwised/kube-oidc-proxy/test/util"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
)

var DefaultConfig = &config.Config{}

type Framework struct {
	BaseName string

	KubeClientSet kubernetes.Interface
	ProxyClient   kubernetes.Interface

	Namespace *corev1.Namespace

	config *config.Config
	helper *helper.Helper

	issuerKeyBundle, proxyKeyBundle *util.KeyBundle
	issuerURL, proxyURL             *url.URL
}

func NewDefaultFramework(baseName string) *Framework {
	return NewFramework(baseName, DefaultConfig)
}

func NewFramework(baseName string, config *config.Config) *Framework {
	f := &Framework{
		BaseName: baseName,
		config:   config,
	}

	JustBeforeEach(f.BeforeEach)
	AfterEach(f.AfterEach)

	return f
}

func (f *Framework) BeforeEach() {

	By("Creating a kubernetes client")

	clientConfigFlags := genericclioptions.NewConfigFlags(true)
	clientConfigFlags.KubeConfig = &f.config.KubeConfigPath
	config, err := clientConfigFlags.ToRESTConfig()
	Expect(err).NotTo(HaveOccurred())

	f.helper = helper.NewHelper(f.config, config)

	apiExtensionsClient, err := apiextensionsclientset.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	f.helper.ApiExtensionsClient = apiExtensionsClient

	f.KubeClientSet, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())

	By("Building a namespace api object")
	f.Namespace, err = f.CreateKubeNamespace(f.BaseName)
	Expect(err).NotTo(HaveOccurred())

	By("Using the namespace " + f.Namespace.Name)

	f.helper.KubeClient = f.KubeClientSet

	By("Deploying mock OIDC Issuer")
	issuerKeyBundle, issuerURL, err := f.helper.DeployIssuer(f.Namespace.Name)
	Expect(err).NotTo(HaveOccurred())

	By("Deploying Customrole CRD")
	crdPath := filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capiclusterrolebindings.yaml")
	err = f.Helper().DeployCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	crdPath = filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capiclusterroles.yaml")
	err = f.Helper().DeployCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	crdPath = filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capirolebindings.yaml")
	err = f.Helper().DeployCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	crdPath = filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capiroles.yaml")
	err = f.Helper().DeployCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	By("Deploying kube-oidc-proxy")
	proxyKeyBundle, proxyURL, err := f.helper.DeployProxy(f.Namespace,
		issuerURL, clientID, issuerKeyBundle, nil)
	Expect(err).NotTo(HaveOccurred())

	f.issuerURL, f.proxyURL = issuerURL, proxyURL
	f.issuerKeyBundle, f.proxyKeyBundle = issuerKeyBundle, proxyKeyBundle

	By("Creating Proxy Client")
	f.ProxyClient = f.NewProxyClient()
}

// AfterEach deletes the namespace, after reading its events.
func (f *Framework) AfterEach() {
	// Output logs from proxy of test case.
	err := f.Helper().Kubectl(f.Namespace.Name).Run("logs", "-lapp=kube-oidc-proxy-e2e")
	if err != nil {
		By("Failed to gather logs from kube-oidc-proxy: " + err.Error())
	}

	By("Deleting kube-oidc-proxy deployment")
	err = f.Helper().DeleteProxy(f.Namespace.Name)
	Expect(err).NotTo(HaveOccurred())

	By("Deleting Customrole CRD")
	crdPath := filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capiclusterrolebindings.yaml")
	err = f.Helper().DeleteCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	crdPath = filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capiclusterroles.yaml")
	err = f.Helper().DeleteCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	crdPath = filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capirolebindings.yaml")
	err = f.Helper().DeleteCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	crdPath = filepath.Join(f.Helper().Config().RepoRoot, "deploy/crds/rbac.platformengineers.io_capiroles.yaml")
	err = f.Helper().DeleteCRDFromFile(crdPath)
	Expect(err).NotTo(HaveOccurred())

	By("Deleting mock OIDC issuer")
	err = f.Helper().DeleteIssuer(f.Namespace.Name)
	Expect(err).NotTo(HaveOccurred())

	By("Deleting test namespace")
	err = f.DeleteKubeNamespace(f.Namespace.Name)
	Expect(err).NotTo(HaveOccurred())
}

func (f *Framework) DeployProxyWith(extraVolumes []corev1.Volume, extraArgs ...string) {
	By("Deleting kube-oidc-proxy deployment")
	err := f.Helper().DeleteProxy(f.Namespace.Name)
	Expect(err).NotTo(HaveOccurred())

	err = f.Helper().WaitForDeploymentToDelete(f.Namespace.Name, kind.ProxyImageName, time.Second*30)
	Expect(err).NotTo(HaveOccurred())

	By(fmt.Sprintf("Deploying kube-oidc-proxy with extra args %s", extraArgs))
	f.proxyKeyBundle, f.proxyURL, err = f.helper.DeployProxy(f.Namespace, f.issuerURL,
		clientID, f.issuerKeyBundle, extraVolumes, extraArgs...)
	Expect(err).NotTo(HaveOccurred())
}

func (f *Framework) Helper() *helper.Helper {
	return f.helper
}

func (f *Framework) IssuerKeyBundle() *util.KeyBundle {
	return f.issuerKeyBundle
}

func (f *Framework) ProxyKeyBundle() *util.KeyBundle {
	return f.proxyKeyBundle
}

func (f *Framework) IssuerURL() *url.URL {
	return f.issuerURL
}

func (f *Framework) ProxyURL() *url.URL {
	return f.proxyURL
}

func (f *Framework) ClientID() string {
	return clientID
}

func (f *Framework) NewProxyRestConfig() *rest.Config {
	config, err := f.Helper().NewValidRestConfig(f.issuerKeyBundle, f.proxyKeyBundle,
		f.issuerURL, f.proxyURL, clientID)
	Expect(err).NotTo(HaveOccurred())

	return config
}

func (f *Framework) NewProxyClient() kubernetes.Interface {
	proxyConfig := f.NewProxyRestConfig()

	proxyConfig.Host = fmt.Sprintf("%s/%s", proxyConfig.Host, constants.ClusterName)
	proxyClient, err := kubernetes.NewForConfig(proxyConfig)
	Expect(err).NotTo(HaveOccurred())

	return proxyClient
}

func CasesDescribe(text string, body func()) bool {
	return Describe("[TEST] "+text, body)
}
