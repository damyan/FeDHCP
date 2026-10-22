// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package metal

import (
	"net"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
	"github.com/ironcore-dev/fedhcp/internal/api"
	"github.com/ironcore-dev/fedhcp/internal/macdb"
	metalv1alpha1 "github.com/ironcore-dev/metal-operator/api/v1alpha1"
	"github.com/mdlayher/netx/eui64"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// relayedRequestForMAC builds a relayed DHCPv6 request whose peer address encodes
// the given MAC (EUI-64), and a reply carrying an IANA address for that MAC.
func relayedRequestForMAC(mac net.HardwareAddr) (*dhcpv6.RelayMessage, *dhcpv6.Message) {
	linkLocalIPV6Addr, err := eui64.ParseMAC(net.ParseIP(linkLocalIPV6Prefix), mac)
	Expect(err).NotTo(HaveOccurred())

	req, _ := dhcpv6.NewMessage()
	req.MessageType = dhcpv6.MessageTypeRequest
	relayedRequest, _ := dhcpv6.EncapsulateRelay(req, dhcpv6.MessageTypeRelayForward, net.IPv6loopback, linkLocalIPV6Addr)
	return relayedRequest, dhcpv6ResponseWithIANA(linkLocalIPV6Addr)
}

var _ = Describe("Metal Plugin handler (BMC discovery)", func() {
	_ = SetupTest()

	AfterEach(func(ctx SpecContext) {
		Expect(k8sClient.DeleteAllOf(ctx, &metalv1alpha1.BMC{})).To(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &metalv1alpha1.BMCSecret{})).To(Succeed())
	})

	It("Should create a BMC and BMCSecret for a known machine (static)", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)
		relayedRequest, stub := relayedRequestForMAC(mac)
		_, _ = handler6(relayedRequest, stub)

		leasedIP := stub.Options.OneIANA().Options.OneAddress().IPv6Addr

		bmc := &metalv1alpha1.BMC{ObjectMeta: metav1.ObjectMeta{Name: machineWithIPAddressName}}
		Eventually(Object(bmc)).Should(SatisfyAll(
			HaveField("Spec.Endpoint.MACAddress", machineWithIPAddressMACAddress),
			HaveField("Spec.Endpoint.IP", metalv1alpha1.MustParseIP(leasedIP.String())),
			HaveField("Spec.BMCSecretRef.Name", machineWithIPAddressName),
			HaveField("Spec.Protocol.Name", metalv1alpha1.ProtocolName(bmcProtocol)),
			HaveField("Spec.Protocol.Port", int32(bmcPort)),
			HaveField("Spec.Protocol.Scheme", metalv1alpha1.HTTPSProtocolScheme),
			HaveField("Spec.ConsoleProtocol.Name", metalv1alpha1.ConsoleProtocolNameSSH),
		))

		secret := &metalv1alpha1.BMCSecret{ObjectMeta: metav1.ObjectMeta{Name: machineWithIPAddressName}}
		Eventually(Object(secret)).Should(SatisfyAll(
			HaveField("Data", HaveKeyWithValue(metalv1alpha1.BMCSecretUsernameKeyName, []byte(bmcUsername))),
			HaveField("Data", HaveKeyWithValue(metalv1alpha1.BMCSecretPasswordKeyName, []byte(bmcPassword))),
		))
	})

	It("Should not create a BMC for a machine without a MAC database entry", func(ctx SpecContext) {
		// machineWithoutIPAddressMACAddress is in the inventory but not in the MAC database
		mac, _ := net.ParseMAC(machineWithoutIPAddressMACAddress)
		relayedRequest, stub := relayedRequestForMAC(mac)
		_, _ = handler6(relayedRequest, stub)

		bmcList := &metalv1alpha1.BMCList{}
		Consistently(ObjectList(bmcList)).Should(HaveField("Items", BeEmpty()))
	})

	It("Should not create a BMC for an unknown inventory MAC", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(unknownMachineMACAddress)
		relayedRequest, stub := relayedRequestForMAC(mac)
		_, _ = handler6(relayedRequest, stub)

		bmcList := &metalv1alpha1.BMCList{}
		Consistently(ObjectList(bmcList)).Should(HaveField("Items", BeEmpty()))
	})

	It("Should not create a BMC if the DHCP response has no IANA address", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)
		linkLocalIPV6Addr, _ := eui64.ParseMAC(net.ParseIP(linkLocalIPV6Prefix), mac)
		req, _ := dhcpv6.NewMessage()
		req.MessageType = dhcpv6.MessageTypeRequest
		relayedRequest, _ := dhcpv6.EncapsulateRelay(req, dhcpv6.MessageTypeRelayForward, net.IPv6loopback, linkLocalIPV6Addr)

		stub, _ := dhcpv6.NewMessage()
		stub.MessageType = dhcpv6.MessageTypeReply
		resp, breakChain := handler6(relayedRequest, stub)
		Expect(resp).NotTo(BeNil())
		Expect(breakChain).To(BeFalse())

		bmcList := &metalv1alpha1.BMCList{}
		Consistently(ObjectList(bmcList)).Should(HaveField("Items", BeEmpty()))
	})

	It("Should return and break plugin chain for a non-relayed IPv6 request", func(ctx SpecContext) {
		req, _ := dhcpv6.NewMessage()
		req.MessageType = dhcpv6.MessageTypeRequest
		stub, _ := dhcpv6.NewMessage()
		stub.MessageType = dhcpv6.MessageTypeReply

		resp, breakChain := handler6(req, stub)
		Expect(resp).To(BeNil())
		Expect(breakChain).To(BeTrue())
	})

	It("Should use the MAC from the ClientLinkLayerAddress option (RFC6939)", func(ctx SpecContext) {
		// relay reports an unknown peer address, but carries the known MAC in the option
		unknownMAC, _ := net.ParseMAC(unknownMachineMACAddress)
		unknownAddr, _ := eui64.ParseMAC(net.ParseIP(linkLocalIPV6Prefix), unknownMAC)
		req, _ := dhcpv6.NewMessage()
		req.MessageType = dhcpv6.MessageTypeRequest
		relayedRequest, _ := dhcpv6.EncapsulateRelay(req, dhcpv6.MessageTypeRelayForward, net.IPv6loopback, unknownAddr)

		knownMAC, _ := net.ParseMAC(machineWithIPAddressMACAddress)
		relayedRequest.AddOption(dhcpv6.OptClientLinkLayerAddress(iana.HWTypeEthernet, knownMAC))
		knownAddr, _ := eui64.ParseMAC(net.ParseIP(linkLocalIPV6Prefix), knownMAC)
		stub := dhcpv6ResponseWithIANA(knownAddr)

		_, _ = handler6(relayedRequest, stub)

		bmc := &metalv1alpha1.BMC{ObjectMeta: metav1.ObjectMeta{Name: machineWithIPAddressName}}
		Eventually(Object(bmc)).Should(HaveField("Spec.Endpoint.MACAddress", machineWithIPAddressMACAddress))
	})

	It("Should not create a second BMC for a repeated request (static)", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)
		for range 2 {
			relayedRequest, stub := relayedRequestForMAC(mac)
			_, _ = handler6(relayedRequest, stub)
		}

		bmcList := &metalv1alpha1.BMCList{}
		Eventually(ObjectList(bmcList)).Should(HaveField("Items", HaveLen(1)))
		Consistently(ObjectList(bmcList)).Should(HaveField("Items", HaveLen(1)))
	})

	It("Should not create a BMC if one created from an Endpoint already handles the MAC", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)

		By("simulating a BMC created by the deprecated Endpoint flow: endpointRef, no FeDHCP label")
		const endpointName = "endpoint-created"
		operatorBMC := &metalv1alpha1.BMC{
			ObjectMeta: metav1.ObjectMeta{Name: endpointName},
			Spec: metalv1alpha1.BMCSpec{
				// deprecated on purpose: this is what the metal operator still creates from an Endpoint
				EndpointRef:  &corev1.LocalObjectReference{Name: endpointName}, //nolint:staticcheck // SA1019
				BMCSecretRef: corev1.LocalObjectReference{Name: endpointName},
				Protocol: metalv1alpha1.Protocol{
					Name: metalv1alpha1.ProtocolName(bmcProtocol),
					Port: int32(bmcPort),
				},
			},
		}
		Expect(k8sClient.Create(ctx, operatorBMC)).To(Succeed())
		By("the operator having populated status.MACAddress from the Endpoint")
		Eventually(UpdateStatus(operatorBMC, func() {
			operatorBMC.Status.MACAddress = machineWithIPAddressMACAddress
		})).Should(Succeed())

		relayedRequest, stub := relayedRequestForMAC(mac)
		_, _ = handler6(relayedRequest, stub)

		By("recognizing it by MAC and not creating a second BMC")
		bmcList := &metalv1alpha1.BMCList{}
		Consistently(ObjectList(bmcList)).Should(SatisfyAll(
			HaveField("Items", HaveLen(1)),
			HaveField("Items", ContainElement(HaveField("ObjectMeta.Name", endpointName))),
		))
	})

	It("Should not create another BMC if multiple BMCs already handle the same MAC", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)

		By("creating two BMCs for the same MAC, e.g. a leftover duplicate")
		for _, name := range []string{"duplicate-b", "duplicate-a"} {
			bmc := &metalv1alpha1.BMC{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec: metalv1alpha1.BMCSpec{
					Endpoint:     &metalv1alpha1.InlineEndpoint{MACAddress: machineWithIPAddressMACAddress},
					BMCSecretRef: corev1.LocalObjectReference{Name: name},
					Protocol: metalv1alpha1.Protocol{
						Name: metalv1alpha1.ProtocolName(bmcProtocol),
						Port: int32(bmcPort),
					},
				},
			}
			Expect(k8sClient.Create(ctx, bmc)).To(Succeed())
		}

		relayedRequest, stub := relayedRequestForMAC(mac)
		_, _ = handler6(relayedRequest, stub)

		By("keeping both BMCs untouched, without creating a third one")
		bmcList := &metalv1alpha1.BMCList{}
		Consistently(ObjectList(bmcList)).Should(SatisfyAll(
			HaveField("Items", HaveLen(2)),
			HaveField("Items", ContainElement(HaveField("ObjectMeta.Name", "duplicate-a"))),
			HaveField("Items", ContainElement(HaveField("ObjectMeta.Name", "duplicate-b"))),
		))
	})

	It("Should not change the IP of an existing BMC (access is immutable)", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)
		relayedRequest, stub := relayedRequestForMAC(mac)
		_, _ = handler6(relayedRequest, stub)
		leasedIP := stub.Options.OneIANA().Options.OneAddress().IPv6Addr

		bmc := &metalv1alpha1.BMC{ObjectMeta: metav1.ObjectMeta{Name: machineWithIPAddressName}}
		Eventually(Object(bmc)).Should(HaveField("Spec.Endpoint.IP", metalv1alpha1.MustParseIP(leasedIP.String())))

		By("offering a different IP for the same MAC")
		Expect(ApplyBMCForInventory(ctx, machineWithIPAddressName, mac, net.ParseIP("2001:db8::1"))).To(Succeed())
		Consistently(Object(bmc)).Should(HaveField("Spec.Endpoint.IP", metalv1alpha1.MustParseIP(leasedIP.String())))
	})

	It("Should create a BMC with a generated name for a known MAC prefix (dynamic)", func(ctx SpecContext) {
		var err error
		inventory, err = loadConfig(writeMetalConfig(api.MetalConfig{
			NamePrefix: "server-",
			Filter:     api.Filter{MacPrefix: []string{machineWithIPAddressMACAddressPrefFilter}},
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(inventory.Strategy).To(Equal(OnboardingStrategyDynamic))

		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)
		for range 2 {
			relayedRequest, stub := relayedRequestForMAC(mac)
			_, _ = handler6(relayedRequest, stub)
		}

		expectedSecretName := "server-" + macdb.SanitizeMAC(machineWithIPAddressMACAddress)
		bmcList := &metalv1alpha1.BMCList{}
		Eventually(ObjectList(bmcList)).Should(SatisfyAll(
			HaveField("Items", HaveLen(1)),
			HaveField("Items", ContainElement(SatisfyAll(
				HaveField("ObjectMeta.Name", HavePrefix("server-")),
				HaveField("Spec.Endpoint.MACAddress", machineWithIPAddressMACAddress),
				HaveField("Spec.BMCSecretRef.Name", expectedSecretName),
			))),
		))
		Consistently(ObjectList(bmcList)).Should(HaveField("Items", HaveLen(1)))

		By("creating the referenced BMCSecret, as the generated BMC name is not usable for it")
		secret := &metalv1alpha1.BMCSecret{ObjectMeta: metav1.ObjectMeta{Name: expectedSecretName}}
		Eventually(Object(secret)).Should(
			HaveField("Data", HaveKeyWithValue(metalv1alpha1.BMCSecretUsernameKeyName, []byte(bmcUsername))))

		secretList := &metalv1alpha1.BMCSecretList{}
		Consistently(ObjectList(secretList)).Should(HaveField("Items", HaveLen(1)))
	})

	It("Should create a BMC for an IPv4 DHCP request", func(ctx SpecContext) {
		mac, _ := net.ParseMAC(machineWithIPAddressMACAddress)
		req, _ := dhcpv4.NewDiscovery(mac)
		stub, _ := dhcpv4.NewReplyFromRequest(req)
		stub.YourIPAddr = net.ParseIP(privateIPV4Address)
		_, _ = handler4(req, stub)

		bmc := &metalv1alpha1.BMC{ObjectMeta: metav1.ObjectMeta{Name: machineWithIPAddressName}}
		Eventually(Object(bmc)).Should(SatisfyAll(
			HaveField("Spec.Endpoint.MACAddress", machineWithIPAddressMACAddress),
			HaveField("Spec.Endpoint.IP", metalv1alpha1.MustParseIP(privateIPV4Address)),
		))
	})
})
