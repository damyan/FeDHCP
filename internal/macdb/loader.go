// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package macdb

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads and validates a MAC prefix database from the given file.
func Load(path string) (*MacPrefixes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read MAC database file: %w", err)
	}

	db := &MacPrefixes{}
	if err := yaml.Unmarshal(data, db); err != nil {
		return nil, fmt.Errorf("failed to parse MAC database file: %w", err)
	}

	if err := db.validate(); err != nil {
		return nil, fmt.Errorf("invalid MAC database: %w", err)
	}

	return db, nil
}

func (db *MacPrefixes) validate() error {
	for i, p := range db.MacPrefixes {
		if p.MacPrefix == "" {
			return fmt.Errorf("entry %d: empty macPrefix", i)
		}
		// MAC addresses are matched in their sanitized (lower case, separator-less)
		// notation, so a prefix must consist of hexadecimal digits only. Upper case is
		// accepted and normalized by SanitizeMAC.
		if !isHex(p.MacPrefix) {
			return fmt.Errorf("entry %d: macPrefix %q must contain hexadecimal digits only, "+
				"without ':' or '-' separators", i, p.MacPrefix)
		}
		if p.Type == BMCType && len(p.DefaultCredentials) == 0 {
			return fmt.Errorf("entry %d: no default credentials for BMC prefix %q", i, p.MacPrefix)
		}
	}
	return nil
}

// isHex reports whether s consists of hexadecimal digits only.
func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// LookupBMC returns the most specific (longest prefix) BMC entry matching the
// given MAC address, or nil if none matches.
func (db *MacPrefixes) LookupBMC(mac net.HardwareAddr) *MacPrefix {
	sanitized := SanitizeMAC(mac.String())

	var match *MacPrefix
	for i := range db.MacPrefixes {
		p := &db.MacPrefixes[i]
		if p.Type != BMCType {
			continue
		}
		if !strings.HasPrefix(sanitized, SanitizeMAC(p.MacPrefix)) {
			continue
		}
		if match == nil || len(p.MacPrefix) > len(match.MacPrefix) {
			match = p
		}
	}
	return match
}

// SanitizeMAC lowercases a MAC address or prefix and removes ':' and '-'
// separators, so that it can be compared against a MAC prefix or used as a
// Kubernetes label value or name.
func SanitizeMAC(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ":", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}
