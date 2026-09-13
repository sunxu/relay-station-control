package store

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGatewayCanonicalIntentV1Fixtures(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	repository := &GatewayLifecycleRepository{key: key}
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	command := GatewayCommand{NewInstanceID: id, DisplayName: StringPatch{Present: true, Value: "Gateway \u2028 \n <&>"}, ManagementEndpoint: StringPatch{Present: true, Value: "http://gateway.example"}, Secret: SecretPatch{Operation: SecretSet, Value: "env://gateway/reader"}}
	encoded, version, err := repository.intent("gateway.register", command, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if version == nil || *version != 1 {
		t.Fatalf("key version=%v", version)
	}
	text := string(encoded)
	if !strings.Contains(text, "Gateway \u2028 "+`\u000a`+" <&>") || strings.Contains(text, "env://gateway/reader") || strings.Contains(text, `\n`) {
		t.Fatalf("canonical encoding=%q", text)
	}
	const wantFingerprint = "9182de8e01725d52fd7270b4d84f2d5ad0547a17405881bd0f042be6db5b2dc7"
	if !strings.Contains(text, wantFingerprint) {
		t.Fatalf("canonical fingerprint fixture changed: %s", text)
	}
	if _, err := hex.DecodeString(wantFingerprint); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayCanonicalIntentSecretKeyFailsClosed(t *testing.T) {
	repository := &GatewayLifecycleRepository{}
	_, _, err := repository.intent("gateway.register", GatewayCommand{NewInstanceID: uuid.New(), DisplayName: StringPatch{Present: true, Value: "Gateway"}, ManagementEndpoint: StringPatch{Present: true, Value: "http://gateway.example"}, Secret: SecretPatch{Operation: SecretSet, Value: "env://gateway/reader"}}, false, nil)
	if err != ErrReceiptKeyUnavailable {
		t.Fatalf("error=%v", err)
	}
	_, version, err := repository.intent("gateway.retire", GatewayCommand{InstanceID: uuid.New(), ExpectedRevision: 1, Secret: SecretPatch{Operation: SecretAbsent}}, false, nil)
	if err != nil || version != nil {
		t.Fatalf("non-secret command: version=%v error=%v", version, err)
	}
	_, version, err = repository.intent("gateway.edit", GatewayCommand{InstanceID: uuid.New(), ExpectedRevision: 1, Secret: SecretPatch{Operation: SecretClear}}, false, nil)
	if err != nil || version != nil {
		t.Fatalf("secret clear command: version=%v error=%v", version, err)
	}
}
