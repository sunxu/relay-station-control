package auth

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyringDomainEnvironmentAndVersionSeparation(t *testing.T) {
	production := testKeyring(t, EnvironmentProduction, 2, 1, 2)
	dev := testKeyring(t, EnvironmentDev, 2, 1, 2)
	totp, err := production.Derive(2, DomainTOTPEncryption, 32)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := production.Derive(2, DomainSessionDigest, 32)
	assetCursor, _ := production.Derive(2, DomainAssetCursorDigest, 32)
	jobCursor, _ := production.Derive(2, DomainJobCursorDigest, 32)
	accountInventoryCursor, _ := production.Derive(2, DomainAccountInventoryCursorEncryption, 32)
	old, _ := production.Derive(1, DomainTOTPEncryption, 32)
	devKey, _ := dev.Derive(2, DomainTOTPEncryption, 32)
	if string(totp) == string(session) || string(totp) == string(old) || string(totp) == string(devKey) ||
		string(assetCursor) == string(jobCursor) || string(accountInventoryCursor) == string(assetCursor) ||
		string(accountInventoryCursor) == string(jobCursor) {
		t.Fatal("derived keys were not separated by domain, version, and environment")
	}
	if production.Environment() != EnvironmentProduction || (*Keyring)(nil).Environment() != "" {
		t.Fatal("keyring environment identity is unavailable")
	}
	if _, err := production.Derive(99, DomainTOTPEncryption, 32); err == nil {
		t.Fatal("unknown key version accepted")
	}
	if _, err := production.Derive(2, KeyDomain("attacker-controlled"), 32); err == nil {
		t.Fatal("unknown key domain accepted")
	}
}

func TestParseKeyringStrictValidation(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	tests := []string{
		`{"format_version":2,"environment":"production","current":1,"keys":[]}`,
		`{"format_version":1,"environment":"production","current":1,"keys":[{"version":1,"key":"short"}]}`,
		`{"format_version":1,"environment":"production","current":2,"keys":[{"version":1,"key":"` + key + `"}]}`,
		`{"format_version":1,"environment":"production","current":1,"keys":[{"version":1,"key":"` + key + `"}],"unknown":true}`,
	}
	for _, document := range tests {
		if _, err := ParseKeyring([]byte(document), EnvironmentProduction); err == nil {
			t.Fatalf("ParseKeyring accepted %s", document)
		}
	}
}

func TestLoadKeyringFilePermissions(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "keyring.json")
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	document := `{"format_version":1,"environment":"production","current":1,"keys":[{"version":1,"key":"` + key + `"}]}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyringFile(path, EnvironmentProduction); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyringFile(path, EnvironmentProduction); err == nil {
		t.Fatal("overly broad keyring permissions accepted")
	}
}
