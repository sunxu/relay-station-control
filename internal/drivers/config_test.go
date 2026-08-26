package drivers

import (
	"errors"
	"testing"
	"time"
)

func validManagementConfig() ManagementConfig {
	return ManagementConfig{
		AllowedDNSNames:        []string{"node.management.example.invalid", ".nodes.example.invalid"},
		AllowedManagementCIDRs: []string{"10.24.0.0/16", "2001:db8:1234::/48"},
		AllowedPlainHTTPCIDRs:  []string{"10.24.8.0/24"},
	}
}

func TestManagementConfigDefaults(t *testing.T) {
	original := validManagementConfig()
	validated, err := original.Validate()
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if validated.ConnectTimeout != DefaultConnectTimeout || validated.RequestTimeout != DefaultRequestTimeout ||
		validated.MaxHealthResponseBytes != DefaultHealthResponseBytes || validated.MaxInventoryResponseBytes != DefaultInventoryResponseBytes ||
		validated.MaxInventoryRecords != DefaultInventoryRecords || validated.MaxSecretBytes != DefaultSecretBytes {
		t.Fatalf("defaults = %#v", validated)
	}
	validated.AllowedDNSNames[0] = "mutated"
	if original.AllowedDNSNames[0] != "node.management.example.invalid" {
		t.Fatal("validated configuration aliases caller data")
	}
}

func TestManagementConfigFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ManagementConfig)
	}{
		{"missing DNS", func(value *ManagementConfig) { value.AllowedDNSNames = nil }},
		{"missing management CIDR", func(value *ManagementConfig) { value.AllowedManagementCIDRs = nil }},
		{"wildcard DNS", func(value *ManagementConfig) { value.AllowedDNSNames = []string{"*.example.invalid"} }},
		{"uppercase DNS", func(value *ManagementConfig) { value.AllowedDNSNames = []string{"NODE.example.invalid"} }},
		{"localhost", func(value *ManagementConfig) { value.AllowedDNSNames = []string{"localhost"} }},
		{"noncanonical CIDR", func(value *ManagementConfig) { value.AllowedManagementCIDRs = []string{"10.24.1.2/16"} }},
		{"loopback included", func(value *ManagementConfig) { value.AllowedManagementCIDRs = []string{"127.0.0.0/8"} }},
		{"link local included", func(value *ManagementConfig) { value.AllowedManagementCIDRs = []string{"169.254.0.0/16"} }},
		{"metadata included", func(value *ManagementConfig) { value.AllowedManagementCIDRs = []string{"100.64.0.0/10"} }},
		{"multicast included", func(value *ManagementConfig) { value.AllowedManagementCIDRs = []string{"224.0.0.0/4"} }},
		{"IPv6 special included", func(value *ManagementConfig) { value.AllowedManagementCIDRs = []string{"::/0"} }},
		{"plain HTTP outside management", func(value *ManagementConfig) { value.AllowedPlainHTTPCIDRs = []string{"10.30.0.0/16"} }},
		{"request shorter than connection", func(value *ManagementConfig) {
			value.ConnectTimeout = 4 * time.Second
			value.RequestTimeout = 3 * time.Second
		}},
		{"connection timeout too high", func(value *ManagementConfig) { value.ConnectTimeout = 11 * time.Second }},
		{"request timeout too high", func(value *ManagementConfig) { value.RequestTimeout = 31 * time.Second }},
		{"body limit too high", func(value *ManagementConfig) { value.MaxInventoryResponseBytes = DefaultInventoryResponseBytes + 1 }},
		{"record limit too high", func(value *ManagementConfig) { value.MaxInventoryRecords = DefaultInventoryRecords + 1 }},
		{"mapping below secret", func(value *ManagementConfig) { value.MaxSecretBytes = 4096; value.MaxSecretMappingBytes = 1024 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := validManagementConfig()
			test.mutate(&configuration)
			if _, err := configuration.Validate(); !errors.Is(err, ErrInvalidManagementConfig) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
