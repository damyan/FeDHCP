// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package metal

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/ironcore-dev/controller-utils/modutils"
	"github.com/ironcore-dev/fedhcp/internal/api"
	"github.com/ironcore-dev/fedhcp/internal/kubernetes"
	"github.com/ironcore-dev/fedhcp/internal/macdb"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	metalv1alpha1 "github.com/ironcore-dev/metal-operator/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

const (
	pollingInterval                          = 50 * time.Millisecond
	eventuallyTimeout                        = 3 * time.Second
	consistentlyDuration                     = 1 * time.Second
	machineWithIPAddressName                 = "machine-with-ip-address"
	machineWithoutIPAddressName              = "machine-without-ip-address"
	machineWithIPAddressMACAddress           = "11:22:33:44:55:66"
	machineWithoutIPAddressMACAddress        = "47:11:47:11:47:11"
	unknownMachineMACAddress                 = "11:11:11:11:11:11"
	machineWithIPAddressMACAddressPrefFilter = "11:22:33"
	linkLocalIPV6Prefix                      = "fe80::"
	privateIPV4Address                       = "192.168.47.11"
	inventoryConfigFile                      = "config.yaml"

	// MAC database entry matching machineWithIPAddressMACAddress and the prefix filter.
	bmcMACPrefix   = "112233"
	bmcProtocol    = "Redfish"
	bmcPort        = 443
	bmcUsername    = "admin"
	bmcPassword    = "secret"
	bmcConsoleType = "SSH"
	bmcConsolePort = 22
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
)

func TestControllers(t *testing.T) {
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			modutils.Dir("github.com/ironcore-dev/metal-operator", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.36.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	DeferCleanup(testEnv.Stop)

	Expect(metalv1alpha1.AddToScheme(scheme.Scheme)).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// set komega client
	SetClient(k8sClient)

	// assign global k8s client in plugin
	kubernetes.SetClient(&k8sClient)
})

func SetupTest() *corev1.Namespace {
	ns := &corev1.Namespace{}

	BeforeEach(func(ctx SpecContext) {
		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-",
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed(), "failed to create test namespace")
		DeferCleanup(k8sClient.Delete, ns)

		var err error
		inventory, err = loadConfig(writeMetalConfig(api.MetalConfig{
			NamePrefix: "server-",
			Inventories: []api.Inventory{
				{Name: machineWithIPAddressName, MacAddress: machineWithIPAddressMACAddress},
				{Name: machineWithoutIPAddressName, MacAddress: machineWithoutIPAddressMACAddress},
			},
			Filter: api.Filter{MacPrefix: []string{machineWithIPAddressMACAddressPrefFilter}},
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(inventory.Entries).To(HaveKeyWithValue(machineWithIPAddressMACAddress, machineWithIPAddressName))
		Expect(inventory.Entries).To(HaveKeyWithValue(machineWithoutIPAddressMACAddress, machineWithoutIPAddressName))
		Expect(inventory.Strategy).To(Equal(OnBoardingStrategyStatic))
	})

	return ns
}

// writeMACDB writes a MAC prefix database with a single BMC entry matching
// machineWithIPAddressMACAddress (and the prefix filter) and returns its path.
func writeMACDB() string {
	db := macdb.MacPrefixes{MacPrefixes: []macdb.MacPrefix{{
		MacPrefix:    bmcMACPrefix,
		Manufacturer: "Test",
		Protocol:     bmcProtocol,
		Port:         bmcPort,
		Type:         macdb.BMCType,
		DefaultCredentials: []macdb.Credential{
			{Username: bmcUsername, Password: bmcPassword},
		},
		Console: macdb.Console{Type: bmcConsoleType, Port: bmcConsolePort},
	}}}
	data, err := yaml.Marshal(db)
	Expect(err).NotTo(HaveOccurred())
	path := filepath.Join(GinkgoT().TempDir(), "macdb.yaml")
	Expect(os.WriteFile(path, data, 0644)).To(Succeed())
	return path
}

// writeMetalConfig marshals a metal config to a file and returns its path. The
// MAC database path and default protocol scheme are filled in when unset.
func writeMetalConfig(data api.MetalConfig) string {
	if data.MACDB == "" {
		data.MACDB = writeMACDB()
	}
	if data.DefaultProtocolScheme == "" {
		data.DefaultProtocolScheme = "https"
	}
	configData, err := yaml.Marshal(data)
	Expect(err).NotTo(HaveOccurred())
	path := filepath.Join(GinkgoT().TempDir(), inventoryConfigFile)
	Expect(os.WriteFile(path, configData, 0644)).To(Succeed())
	return path
}
