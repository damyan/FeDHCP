// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package metal

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/ironcore-dev/fedhcp/internal/helper"
	"github.com/ironcore-dev/fedhcp/internal/printer"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/logger"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/ironcore-dev/fedhcp/internal/api"
	"github.com/ironcore-dev/fedhcp/internal/macdb"
	metalv1alpha1 "github.com/ironcore-dev/metal-operator/api/v1alpha1"
	"gopkg.in/yaml.v3"
)

var log = logger.GetLogger("plugins/metal")

var Plugin = plugins.Plugin{
	Name:   "metal",
	Setup6: setup6,
	Setup4: setup4,
}

// map MAC address to inventory name
var inventory *Inventory

type Inventory struct {
	Entries  map[string]string
	Strategy OnBoardingStrategy

	// MACDB is the loaded MAC prefix database.
	MACDB *macdb.MacPrefixes
	// DefaultProtocolScheme fills BMC spec.protocol.scheme when the MAC database
	// entry leaves it empty.
	DefaultProtocolScheme metalv1alpha1.ProtocolScheme
}

// default inventory name prefix
const defaultNamePrefix = "compute-"

type OnBoardingStrategy string

const (
	OnBoardingStrategyStatic  OnBoardingStrategy = "Static"
	OnboardingStrategyDynamic OnBoardingStrategy = "Dynamic"
)

// args[0] = path to inventory file
func parseArgs(args ...string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("exactly one argument must be passed to the plugin, got %d", len(args))
	}
	return args[0], nil
}

func setup6(args ...string) (handler.Handler6, error) {
	var err error
	inventory, err = loadConfig(args...)
	if err != nil {
		return nil, err
	}

	return handler6, nil
}

func loadConfig(args ...string) (*Inventory, error) {
	path, err := parseArgs(args...)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration: %v", err)
	}

	log.Debugf("Reading config file %s", path)
	configData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	var config api.MetalConfig
	if err = yaml.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	inv := &Inventory{}
	entries := make(map[string]string)
	switch {
	// static inventory list has precedence, always
	case len(config.Inventories) > 0:
		inv.Strategy = OnBoardingStrategyStatic
		log.Debug("Using static list onboarding")
		for _, i := range config.Inventories {
			if i.MacAddress != "" && i.Name != "" {
				entries[strings.ToLower(i.MacAddress)] = i.Name
			}
		}
	case len(config.Filter.MacPrefix) > 0:
		inv.Strategy = OnboardingStrategyDynamic
		namePrefix := defaultNamePrefix
		if config.NamePrefix != "" {
			namePrefix = config.NamePrefix
		}
		log.Debugf("Using MAC address prefix filter onboarding with name prefix '%s'", namePrefix)
		for _, i := range config.Filter.MacPrefix {
			entries[strings.ToLower(i)] = namePrefix
		}
	default:
		log.Infof("No inventories loaded")
	}

	inv.Entries = entries

	if config.MACDB == "" {
		return nil, fmt.Errorf("macDB is required")
	}
	db, err := macdb.Load(config.MACDB)
	if err != nil {
		return nil, fmt.Errorf("failed to load MAC database: %w", err)
	}
	inv.MACDB = db
	inv.DefaultProtocolScheme = metalv1alpha1.ProtocolScheme(config.DefaultProtocolScheme)
	log.Infof("Loaded MAC database with %d entries", len(db.MacPrefixes))

	log.Infof("Loaded config with %d inventories", len(entries))
	return inv, nil
}

func setup4(args ...string) (handler.Handler4, error) {
	var err error
	inventory, err = loadConfig(args...)
	if err != nil {
		return nil, err
	}

	return handler4, nil
}

func handler6(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
	if req == nil {
		log.Error("Received nil IPv6 request")
		return nil, true
	}

	printer.VerboseRequest(req, log, printer.IPv6)
	defer printer.VerboseResponse(req, resp, log, printer.IPv6)

	if !req.IsRelay() {
		log.Info("Received non-relay DHCPv6 request. Dropping.")
		return nil, true
	}

	relayMsg := req.(*dhcpv6.RelayMessage)
	mac, err := helper.GetMAC(relayMsg, log)
	if err != nil {
		log.Errorf("Failed to obtain MAC, dropping.")
		return nil, true
	}

	ip, _ := helper.GetIANAAddressAndLifetime(resp)
	if ip == nil {
		log.Debug("No IANA address in response, nothing to do")
		return resp, false
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ApplyForMACAddress(ctx, mac, ip); err != nil {
		log.Errorf("Could not apply resource for mac %s: %s", mac.String(), err)
	}

	return resp, false
}

func handler4(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	if req == nil {
		log.Error("Received nil IPv4 request")
		return nil, true
	}

	printer.VerboseRequest(req, log, printer.IPv4)
	defer printer.VerboseResponse(req, resp, log, printer.IPv4)

	mac := req.ClientHWAddr

	ip := GetIPAddressFromDHCPv4Response(resp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ApplyForMACAddress(ctx, mac, ip); err != nil {
		log.Errorf("Could not apply resource for mac %s: %s", mac.String(), err)
	}

	return resp, false
}

// GetIPAddressFromDHCPv4Response extracts the offered IPv4 address from a
// DHCPv4 response. A preceding plugin must have set YourIPAddr.
// The result is normalized to a 4-byte slice so that String() produces the
// canonical IPv4 form (e.g. "192.168.1.1") rather than an IPv4-mapped IPv6
// representation.
func GetIPAddressFromDHCPv4Response(resp *dhcpv4.DHCPv4) net.IP {
	if resp == nil || resp.YourIPAddr == nil || resp.YourIPAddr.IsUnspecified() {
		return nil
	}
	if ip4 := resp.YourIPAddr.To4(); ip4 != nil {
		return ip4
	}
	return resp.YourIPAddr
}

// ApplyForMACAddress onboards the device with the given MAC and IP by creating
// a BMC (and its BMCSecret).
func ApplyForMACAddress(ctx context.Context, mac net.HardwareAddr, ip net.IP) error {
	inventoryName := GetInventoryEntryMatchingMACAddress(mac)
	if inventoryName == "" {
		log.Print("Unknown inventory, not processing")
		return nil
	}

	if ip == nil {
		log.Infof("No IP address in DHCP response for MAC %s", mac.String())
		return nil
	}

	return ApplyBMCForInventory(ctx, inventoryName, mac, ip)
}

func GetInventoryEntryMatchingMACAddress(mac net.HardwareAddr) string {
	if inventory == nil || len(inventory.Entries) == 0 {
		return ""
	}

	switch inventory.Strategy {
	case OnBoardingStrategyStatic:
		inventoryName, ok := inventory.Entries[strings.ToLower(mac.String())]
		if !ok {
			log.Debugf("Unknown inventory MAC address: %s", mac.String())
		} else {
			return inventoryName
		}
	case OnboardingStrategyDynamic:
		for i := range inventory.Entries {
			if strings.HasPrefix(strings.ToLower(mac.String()), strings.ToLower(i)) {
				return inventory.Entries[i]
			}
		}
		// we don't onboard by default yet, might change in the future
		log.Debugf("Inventory MAC address %s does not match any inventory MAC prefix", mac.String())
	default:
		log.Debugf("Unknown Onboarding strategy %s", inventory.Strategy)
	}

	return ""
}
