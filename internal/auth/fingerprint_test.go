package auth

import (
	"net/netip"
	"strings"
	"testing"
)

func TestFingerprintsStableAndEnvironmentSeparated(t *testing.T) {
	production := testKeyring(t, EnvironmentProduction, 1, 1)
	dev := testKeyring(t, EnvironmentDev, 1, 1)
	first, _ := LoginFingerprint(production, "Operator.Name")
	second, _ := LoginFingerprint(production, "operator.name")
	otherEnvironment, _ := LoginFingerprint(dev, "operator.name")
	if first != second {
		t.Fatal("normalized login fingerprint is not stable")
	}
	if first == otherEnvironment {
		t.Fatal("login fingerprint is linkable across environments")
	}
	encoded := FingerprintString(first)
	if strings.Contains(encoded, "operator") {
		t.Fatal("fingerprint contains original login")
	}

	ipv4, _ := SourceFingerprint(production, netip.MustParseAddr("198.51.100.9"))
	mapped, _ := SourceFingerprint(production, netip.MustParseAddr("::ffff:198.51.100.9"))
	otherSourceEnvironment, _ := SourceFingerprint(dev, netip.MustParseAddr("198.51.100.9"))
	if ipv4 != mapped {
		t.Fatal("IPv4-mapped address fingerprint differs")
	}
	if ipv4 == otherSourceEnvironment {
		t.Fatal("source fingerprint is linkable across environments")
	}
}

func TestLoginAndDisplayNameValidation(t *testing.T) {
	if value, err := NormalizeLoginName("Admin.Name-1"); err != nil || value != "admin.name-1" {
		t.Fatalf("NormalizeLoginName() = %q, %v", value, err)
	}
	for _, value := range []string{"ab", "has space", "ümlaut", strings.Repeat("a", 65)} {
		if _, err := NormalizeLoginName(value); err == nil {
			t.Fatalf("NormalizeLoginName(%q) succeeded", value)
		}
	}
	if value, err := ValidateDisplayName("  张 三  "); err != nil || value != "张 三" {
		t.Fatalf("ValidateDisplayName() = %q, %v", value, err)
	}
}
