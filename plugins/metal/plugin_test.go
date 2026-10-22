// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package metal

import (
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/ironcore-dev/fedhcp/internal/api"
	"gopkg.in/yaml.v2"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func dhcpv6ResponseWithIANA(ipv6Addr net.IP) *dhcpv6.Message {
	stub, _ := dhcpv6.NewMessage()
	stub.MessageType = dhcpv6.MessageTypeReply
	stub.AddOption(&dhcpv6.OptIANA{
		IaId: [4]byte{1, 2, 3, 4},
		Options: dhcpv6.IdentityOptions{Options: []dhcpv6.Option{
			&dhcpv6.OptIAAddress{
				IPv6Addr:          ipv6Addr,
				PreferredLifetime: 24 * time.Hour,
				ValidLifetime:     24 * time.Hour,
			},
		}},
	})
	return stub
}

// writeRawConfig marshals a metal config as-is (without filling in the MAC
// database) and returns its path.
func writeRawConfig(data api.MetalConfig) string {
	configData, err := yaml.Marshal(data)
	Expect(err).NotTo(HaveOccurred())
	path := filepath.Join(GinkgoT().TempDir(), inventoryConfigFile)
	Expect(os.WriteFile(path, configData, 0644)).To(Succeed())
	return path
}

var _ = Describe("Metal Plugin config", func() {
	_ = SetupTest()

	It("Setup6 should return error if less arguments are provided", func() {
		_, err := setup6()
		Expect(err).To(HaveOccurred())
	})

	It("Setup6 should return error if more arguments are provided", func() {
		_, err := setup6("foo", "bar")
		Expect(err).To(HaveOccurred())
	})

	It("Setup6 should return error if config file does not exist", func() {
		_, err := setup6("does-not-exist.yaml")
		Expect(err).To(HaveOccurred())
	})

	It("Setup4 should return error if less arguments are provided", func() {
		_, err := setup4()
		Expect(err).To(HaveOccurred())
	})

	It("Setup4 should return error if more arguments are provided", func() {
		_, err := setup4("foo", "bar")
		Expect(err).To(HaveOccurred())
	})

	It("Setup4 should return error if config file does not exist", func() {
		_, err := setup4("does-not-exist.yaml")
		Expect(err).To(HaveOccurred())
	})

	It("Setup6 should return error if config file is invalid", func() {
		file := filepath.Join(GinkgoT().TempDir(), inventoryConfigFile)
		Expect(os.WriteFile(file, []byte("Invalid YAML"), 0644)).To(Succeed())
		_, err := setup6(file)
		Expect(err).To(HaveOccurred())
	})

	It("Setup6 should return error if the MAC database is not configured", func() {
		_, err := setup6(writeRawConfig(api.MetalConfig{
			Inventories: []api.Inventory{
				{Name: machineWithIPAddressName, MacAddress: machineWithIPAddressMACAddress},
			},
		}))
		Expect(err).To(MatchError(ContainSubstring("macDB is required")))
	})

	It("Setup6 should return error if the MAC database does not exist", func() {
		_, err := setup6(writeRawConfig(api.MetalConfig{MACDB: "does-not-exist.yaml"}))
		Expect(err).To(HaveOccurred())
	})

	It("Setup6 should return a non-nil handler for an empty hosts config", func() {
		h6, err := setup6(writeMetalConfig(api.MetalConfig{Inventories: []api.Inventory{}}))
		Expect(err).NotTo(HaveOccurred())
		Expect(h6).NotTo(BeNil())
	})

	It("Setup4 should return a non-nil handler for an empty hosts config", func() {
		h4, err := setup4(writeMetalConfig(api.MetalConfig{Inventories: []api.Inventory{}}))
		Expect(err).NotTo(HaveOccurred())
		Expect(h4).NotTo(BeNil())
	})

	It("Should return an empty inventory for an empty list", func() {
		i, err := loadConfig(writeMetalConfig(api.MetalConfig{
			Inventories: []api.Inventory{{}},
			Filter:      api.Filter{MacPrefix: []string{}},
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(i.Entries).To(BeEmpty())
	})

	It("Should return a valid inventory list with default name prefix for non-empty MAC address filter", func() {
		i, err := loadConfig(writeMetalConfig(api.MetalConfig{
			Filter: api.Filter{MacPrefix: []string{"aa:bb:cc:dd:ee:ff"}},
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(i.Entries).To(HaveKey("aa:bb:cc:dd:ee:ff"))
		Expect(i.Entries["aa:bb:cc:dd:ee:ff"]).To(HavePrefix(defaultNamePrefix))
	})

	It("Should return an inventory list with custom name prefix for non-empty MAC address filter and set prefix", func() {
		i, err := loadConfig(writeMetalConfig(api.MetalConfig{
			NamePrefix: "server-",
			Filter:     api.Filter{MacPrefix: []string{"aa:bb:cc:dd:ee:ff"}},
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(i.Entries).To(HaveKey("aa:bb:cc:dd:ee:ff"))
		Expect(i.Entries["aa:bb:cc:dd:ee:ff"]).To(HavePrefix("server-"))
	})

	It("Should return a valid inventory list for a non-empty inventory section, precedence over MAC filter", func() {
		i, err := loadConfig(writeMetalConfig(api.MetalConfig{
			NamePrefix:  "server-",
			Inventories: []api.Inventory{{Name: "compute-1", MacAddress: "aa:bb:cc:dd:ee:ff"}},
			Filter:      api.Filter{MacPrefix: []string{"aa:bb:cc:dd:ee:ff"}},
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(i.Entries).To(HaveKeyWithValue("aa:bb:cc:dd:ee:ff", "compute-1"))
	})
})
