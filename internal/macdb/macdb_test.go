// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: MIT

package macdb

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func mustParseMAC(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	mac, err := net.ParseMAC(s)
	if err != nil {
		t.Fatalf("failed to parse MAC %q: %v", s, err)
	}
	return mac
}

func writeDB(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "macdb.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("does-not-exist.yaml"); err == nil {
		t.Fatal("expected an error for a missing file, got none")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	if _, err := Load(writeDB(t, "not: [valid")); err == nil {
		t.Fatal("expected an error for invalid YAML, got none")
	}
}

func TestLoadRejectsEmptyPrefix(t *testing.T) {
	if _, err := Load(writeDB(t, "macPrefixes:\n- macPrefix: \"\"\n  type: bmc\n")); err == nil {
		t.Fatal("expected an error for an empty macPrefix, got none")
	}
}

func TestLoadRejectsMalformedPrefix(t *testing.T) {
	for _, prefix := range []string{
		"00:2a", // colon separated
		"00-2a", // hyphen separated
		"00 2a", // blank separated
		"zz",    // not hexadecimal
	} {
		t.Run(prefix, func(t *testing.T) {
			db := "macPrefixes:\n- macPrefix: \"" + prefix + "\"\n  type: bmc\n  defaultCredentials:\n    - username: u\n      password: p\n"
			if _, err := Load(writeDB(t, db)); err == nil {
				t.Errorf("expected an error for macPrefix %q, got none", prefix)
			}
		})
	}
}

// TestLoadAcceptsUpperCasePrefix ensures an upper case prefix is accepted, as MAC
// addresses are normalized before they are matched against it.
func TestLoadAcceptsUpperCasePrefix(t *testing.T) {
	db := "macPrefixes:\n- macPrefix: \"002A2B\"\n  type: bmc\n  protocol: Redfish\n  defaultCredentials:\n    - username: u\n      password: p\n"
	loaded, err := Load(writeDB(t, db))
	if err != nil {
		t.Fatalf("expected no error for an upper case macPrefix, got %v", err)
	}
	if got := loaded.LookupBMC(mustParseMAC(t, "00:2a:2b:3c:4d:5e")); got == nil {
		t.Error("expected the upper case prefix to match a lower case MAC address")
	}
}

func TestLoadRejectsBMCWithoutCredentials(t *testing.T) {
	if _, err := Load(writeDB(t, "macPrefixes:\n- macPrefix: \"002a\"\n  type: bmc\n")); err == nil {
		t.Fatal("expected an error for a BMC prefix without credentials, got none")
	}
}

// TestLoadMetalOperatorFormat ensures the FeDHCP MAC database type stays
// compatible with the metal-operator MAC prefix database format.
func TestLoadMetalOperatorFormat(t *testing.T) {
	db, err := Load("testdata/metal-operator-macdb.yaml")
	if err != nil {
		t.Fatalf("failed to load metal-operator format MAC database: %v", err)
	}
	if len(db.MacPrefixes) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(db.MacPrefixes))
	}
	p := db.MacPrefixes[0]
	if p.MacPrefix != "23" || p.Type != BMCType || p.Port != 8000 {
		t.Errorf("unexpected entry: %+v", p)
	}
	if p.Console.Type != "SSH" || p.Console.Port != 22 {
		t.Errorf("unexpected console: %+v", p.Console)
	}
	if len(p.DefaultCredentials) != 1 || p.DefaultCredentials[0].Username != "foo" {
		t.Errorf("unexpected credentials: %+v", p.DefaultCredentials)
	}
}

func TestLookupBMC(t *testing.T) {
	db := &MacPrefixes{MacPrefixes: []MacPrefix{
		{MacPrefix: "00", Type: BMCType, Protocol: "Redfish", DefaultCredentials: []Credential{{Username: "u", Password: "p"}}},
		{MacPrefix: "002a2b", Type: BMCType, Protocol: "Redfish", DefaultCredentials: []Credential{{Username: "u", Password: "p"}}},
		{MacPrefix: "aa", Type: "switch"},
	}}

	tests := []struct {
		name       string
		mac        string
		wantPrefix string // "" means no match
	}{
		{"longest prefix wins", "00:2a:2b:3c:4d:5e", "002a2b"},
		{"shorter prefix", "00:ff:ff:ff:ff:ff", "00"},
		{"non-bmc type ignored", "aa:bb:cc:dd:ee:ff", ""},
		{"no match", "ff:ff:ff:ff:ff:ff", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := db.LookupBMC(mustParseMAC(t, tt.mac))
			if tt.wantPrefix == "" {
				if got != nil {
					t.Fatalf("expected no match, got %q", got.MacPrefix)
				}
				return
			}
			if got == nil || got.MacPrefix != tt.wantPrefix {
				t.Fatalf("expected prefix %q, got %v", tt.wantPrefix, got)
			}
		})
	}
}
