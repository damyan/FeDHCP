// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package api

type Inventory struct {
	Name       string `yaml:"name"`
	MacAddress string `yaml:"macAddress"`
}

type Filter struct {
	MacPrefix []string `yaml:"macPrefix"`
}

type MetalConfig struct {
	NamePrefix  string      `yaml:"namePrefix"`
	Inventories []Inventory `yaml:"hosts"`
	Filter      Filter      `yaml:"filter"`

	// MACDB is the path to the MAC prefix database file (required). It supplies the
	// BMC protocol, port, console and default credentials for a matching MAC prefix.
	MACDB string `yaml:"macDB"`
	// DefaultProtocolScheme fills BMC spec.protocol.scheme when the matching MAC
	// database entry leaves it empty (e.g. "https").
	DefaultProtocolScheme string `yaml:"defaultProtocolScheme"`
}
