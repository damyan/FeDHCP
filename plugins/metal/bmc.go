// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package metal

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/ironcore-dev/fedhcp/internal/kubernetes"
	"github.com/ironcore-dev/fedhcp/internal/macdb"
	metalv1alpha1 "github.com/ironcore-dev/metal-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyBMCForInventory creates a BMC (and its BMCSecret) for the given inventory
// entry, MAC and IP, using the MAC prefix database for the BMC protocol, port,
// console and default credentials.
//
// BMC spec.access is immutable, so an existing BMC is never patched: once a BMC
// for the MAC exists, this is a no-op. Applying a new IP would require deleting
// and recreating the BMC, which is deliberately not done from the DHCP path, as
// it disrupts an already running Server.
func ApplyBMCForInventory(ctx context.Context, name string, mac net.HardwareAddr, ip net.IP) error {
	cl := kubernetes.GetClient()
	if cl == nil {
		return fmt.Errorf("kubernetes client not initialized")
	}

	prefix := inventory.MACDB.LookupBMC(mac)
	if prefix == nil {
		log.Infof("No BMC MAC database entry for MAC %s, skipping", mac.String())
		return nil
	}

	existing, err := getBMCForMACAddress(ctx, cl, mac)
	if err != nil {
		return fmt.Errorf("failed to look up existing BMC: %w", err)
	}
	if existing != nil {
		log.Infof("BMC %s already exists for MAC %s, not creating", existing.Name, mac.String())
		return nil
	}

	return createBMCWithSecret(ctx, cl, name, mac, ip, prefix)
}

// createBMCWithSecret creates the BMCSecret before the BMC, so that a failure
// between the two leaves only an unreferenced BMCSecret behind. The next request
// for the same MAC then retries and reuses it. The other way around, the BMC
// would reference a BMCSecret which never gets created, wedging the BMC: the
// metal operator fails to reconcile it, while FeDHCP considers the MAC onboarded.
//
// As a consequence the BMCSecret cannot be owned by the BMC, so it is not
// garbage-collected when the BMC is deleted. The metal operator does not set an
// owner reference on the BMCSecrets it creates either.
func createBMCWithSecret(ctx context.Context, cl client.Client, name string, mac net.HardwareAddr, ip net.IP, prefix *macdb.MacPrefix) error {
	secretName, err := bmcSecretName(name, mac)
	if err != nil {
		return err
	}
	if err := applyBMCSecret(ctx, cl, secretName, prefix); err != nil {
		return fmt.Errorf("failed to apply BMCSecret %s: %w", secretName, err)
	}

	scheme := prefix.ProtocolScheme
	if scheme == "" {
		scheme = inventory.DefaultProtocolScheme
	}

	bmc := &metalv1alpha1.BMC{
		Spec: metalv1alpha1.BMCSpec{
			// Endpoint is the inline spec.access, replacing the deprecated EndpointRef.
			Endpoint: &metalv1alpha1.InlineEndpoint{
				MACAddress: mac.String(),
				IP:         metalv1alpha1.MustParseIP(ip.String()),
			},
			BMCSecretRef: corev1.LocalObjectReference{Name: secretName},
			Protocol: metalv1alpha1.Protocol{
				Name:   metalv1alpha1.ProtocolName(prefix.Protocol),
				Port:   prefix.Port,
				Scheme: scheme,
			},
		},
	}
	if prefix.Console.Type != "" {
		bmc.Spec.ConsoleProtocol = &metalv1alpha1.ConsoleProtocol{
			Name: metalv1alpha1.ConsoleProtocolName(prefix.Console.Type),
			Port: prefix.Console.Port,
		}
	}

	switch inventory.Strategy {
	case OnBoardingStrategyStatic:
		bmc.Name = name
	case OnboardingStrategyDynamic:
		bmc.GenerateName = name
	}

	if err := cl.Create(ctx, bmc); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Debugf("BMC %s already exists, nothing to do", bmc.Name)
			return nil
		}
		return fmt.Errorf("failed to create BMC: %w", err)
	}
	log.Infof("Created BMC %s (%s) with IP address %s", bmc.Name, mac.String(), ip.String())

	return nil
}

// bmcSecretName returns the name of the BMCSecret for an inventory entry. It has
// to be known before the BMC is created, so for the dynamic strategy, where the
// BMC name is generated, the MAC address is appended to the configured prefix.
func bmcSecretName(name string, mac net.HardwareAddr) (string, error) {
	switch inventory.Strategy {
	case OnBoardingStrategyStatic:
		return name, nil
	case OnboardingStrategyDynamic:
		return name + macdb.SanitizeMAC(mac.String()), nil
	default:
		return "", fmt.Errorf("unknown OnboardingStrategy %s", inventory.Strategy)
	}
}

func applyBMCSecret(ctx context.Context, cl client.Client, name string, prefix *macdb.MacPrefix) error {
	secret := &metalv1alpha1.BMCSecret{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data: map[string][]byte{
			metalv1alpha1.BMCSecretUsernameKeyName: []byte(prefix.DefaultCredentials[0].Username),
			metalv1alpha1.BMCSecretPasswordKeyName: []byte(prefix.DefaultCredentials[0].Password),
		},
	}

	if err := cl.Create(ctx, secret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Debugf("BMCSecret %s already exists, nothing to do", secret.Name)
			return nil
		}
		return fmt.Errorf("failed to create BMCSecret: %w", err)
	}
	log.Debugf("Created BMCSecret %s", secret.Name)
	return nil
}

// getBMCForMACAddress finds a BMC that already handles the MAC, or nil if there
// is none. It matches by MAC address rather than by a FeDHCP-specific marker, so
// that BMCs created by other means (e.g. the deprecated Endpoint flow, which
// uses spec.endpointRef) are recognized too and not duplicated. The MAC is read
// from spec.access for FeDHCP-created BMCs, and falls back to status.MACAddress,
// which the metal operator populates from either spec.access or spec.endpointRef.
func getBMCForMACAddress(ctx context.Context, cl client.Client, mac net.HardwareAddr) (*metalv1alpha1.BMC, error) {
	bmcList := &metalv1alpha1.BMCList{}
	if err := cl.List(ctx, bmcList); err != nil {
		return nil, fmt.Errorf("failed to list BMCs: %w", err)
	}

	sanitized := macdb.SanitizeMAC(mac.String())
	var matches []*metalv1alpha1.BMC
	for i := range bmcList.Items {
		if bmcMACAddress(&bmcList.Items[i]) == sanitized {
			matches = append(matches, &bmcList.Items[i])
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}

	// Sort by name, so the same BMC is picked on every request, no matter in which
	// order the API server returns them.
	slices.SortFunc(matches, func(a, b *metalv1alpha1.BMC) int {
		return strings.Compare(a.Name, b.Name)
	})
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, bmc := range matches {
			names = append(names, bmc.Name)
		}
		log.Warningf("Found %d BMCs for MAC %s (%s), using %s", len(names), mac.String(),
			strings.Join(names, " "), names[0])
	}

	return matches[0], nil
}

// bmcMACAddress returns the sanitized MAC address a BMC handles, from spec.access
// if set, otherwise from status.MACAddress. It returns "" if neither is known.
func bmcMACAddress(bmc *metalv1alpha1.BMC) string {
	if bmc.Spec.Endpoint != nil && bmc.Spec.Endpoint.MACAddress != "" {
		return macdb.SanitizeMAC(bmc.Spec.Endpoint.MACAddress)
	}
	if bmc.Status.MACAddress != "" {
		return macdb.SanitizeMAC(bmc.Status.MACAddress)
	}
	return ""
}
