package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
)

func TestValidateCredentialPlaintextUsesExactUTF8Bytes(t *testing.T) {
	if !validateCredentialPlaintext(" ") {
		t.Fatal("leading/trailing spaces must be preserved and accepted")
	}
	if !validateCredentialPlaintext(strings.Repeat("a", 4096)) {
		t.Fatal("4096 bytes should be accepted")
	}
	if validateCredentialPlaintext(strings.Repeat("a", 4097)) {
		t.Fatal("4097 bytes should be rejected")
	}
	if validateCredentialPlaintext("\x00") || validateCredentialPlaintext("line\nfeed") || validateCredentialPlaintext("carriage\rreturn") {
		t.Fatal("control characters should be rejected")
	}
	if validateCredentialPlaintext("\xff") {
		t.Fatal("invalid UTF-8 should be rejected")
	}
	if !validateCredentialPlaintext("租") {
		t.Fatal("valid multibyte UTF-8 should be accepted")
	}
}

type testCountingSealer struct{ calls int }

func (s *testCountingSealer) Available() bool { return true }
func (s *testCountingSealer) Seal(assetcredential.CredentialKind, uuid.UUID, []byte) ([]byte, error) {
	s.calls++
	return []byte(strings.Repeat("x", 29)), nil
}

func TestCredentialSealerUnavailableMatrixDoesNotSealKeepOrClear(t *testing.T) {
	sealer := &testCountingSealer{}
	node := &NodeLifecycleRepository{sealer: sealer}
	for _, operation := range []SecretOperation{SecretAbsent, SecretKeep, SecretClear} {
		if _, err := node.sealedCredential(assetcredential.NodeCredential, uuid.New(), SecretPatch{Operation: operation}); err != nil {
			t.Fatalf("node %s: %v", operation, err)
		}
	}
	if sealer.calls != 0 {
		t.Fatalf("non-setting operations called Seal %d times", sealer.calls)
	}

	sealer.calls = 0
	node.sealer = unavailableCredentialSealer{}
	if _, err := node.sealedCredential(assetcredential.NodeCredential, uuid.New(), SecretPatch{Operation: SecretSet, Value: "credential"}); err == nil {
		t.Fatal("Set with unavailable sealer succeeded")
	}
	gateway := &GatewayLifecycleRepository{sealer: unavailableCredentialSealer{}}
	for _, operation := range []SecretOperation{SecretAbsent, SecretKeep, SecretClear} {
		if _, err := gateway.sealedCredential(uuid.New(), SecretPatch{Operation: operation}); err != nil {
			t.Fatalf("gateway %s: %v", operation, err)
		}
	}
	if _, err := gateway.sealedCredential(uuid.New(), SecretPatch{Operation: SecretSet, Value: "credential"}); err == nil {
		t.Fatal("gateway Set with unavailable sealer succeeded")
	}
}
