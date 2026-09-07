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
	if len(validated.AllowedDNSNames) != 0 || len(validated.AllowedManagementPrefixes) != 0 || len(validated.AllowedPlainHTTPPrefixes) != 0 {
		t.Fatal("legacy target policy was retained")
	}
}

func TestManagementConfigIgnoresRetiredTargetPolicy(t *testing.T) {
	validated, err := (ManagementConfig{
		AllowedDNSNames:        []string{"not a hostname"},
		AllowedManagementCIDRs: []string{"127.0.0.0/8"},
		AllowedPlainHTTPCIDRs:  []string{"0.0.0.0/0"},
	}).Validate()
	if err != nil {
		t.Fatalf("retired target policy must be ignored: %v", err)
	}
	if len(validated.AllowedDNSNames) != 0 || len(validated.AllowedManagementPrefixes) != 0 || len(validated.AllowedPlainHTTPPrefixes) != 0 {
		t.Fatal("retired target policy was retained")
	}
}

func TestManagementConfigFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ManagementConfig)
	}{
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
