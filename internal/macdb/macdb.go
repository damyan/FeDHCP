// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

// Package macdb provides a MAC prefix database that maps MAC address prefixes to
// device information (manufacturer, protocol, default credentials, ...).
//
// The format is intentionally kept compatible with the metal-operator MAC prefix
// database (its --mac-prefixes-file), so the same file can be used by both. The
// metal-operator type cannot be imported, as it lives in its internal/ package.
package macdb

import (
	metalv1alpha1 "github.com/ironcore-dev/metal-operator/api/v1alpha1"
)

// BMCType is the device type of a BMC entry.
const BMCType = "bmc"

// MacPrefixes is a list of MacPrefix entries.
type MacPrefixes struct {
	MacPrefixes []MacPrefix `json:"macPrefixes" yaml:"macPrefixes"`
}

// MacPrefix maps a MAC address prefix to the device information required to
// create a BMC and its secret.
type MacPrefix struct {
	// MacPrefix is the MAC address prefix (without separators, e.g. "002A2B").
	MacPrefix string `json:"macPrefix" yaml:"macPrefix"`
	// Manufacturer is the device manufacturer.
	Manufacturer string `json:"manufacturer" yaml:"manufacturer"`
	// Protocol is the BMC protocol (e.g. "Redfish").
	Protocol string `json:"protocol" yaml:"protocol"`
	// ProtocolScheme is the protocol scheme (http, https). May be empty.
	ProtocolScheme metalv1alpha1.ProtocolScheme `json:"protocolScheme,omitempty" yaml:"protocolScheme,omitempty"`
	// Port is the BMC port.
	Port int32 `json:"port" yaml:"port"`
	// Type is the device type (e.g. "bmc").
	Type string `json:"type" yaml:"type"`
	// DefaultCredentials are the default credentials used to access the device.
	DefaultCredentials []Credential `json:"defaultCredentials" yaml:"defaultCredentials"`
	// Console is the console access configuration.
	Console Console `json:"console,omitempty" yaml:"console,omitempty"`
}

// Credential is a username/password pair.
type Credential struct {
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
}

// Console is the console access configuration of a device.
type Console struct {
	Type string `json:"type" yaml:"type"`
	Port int32  `json:"port" yaml:"port"`
}
