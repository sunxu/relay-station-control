package store

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

var (
	accountInventoryCursorTestActor    = uuid.MustParse("10000000-0000-4000-8000-000000000001")
	accountInventoryCursorTestInstance = uuid.MustParse("20000000-0000-4000-8000-000000000002")
	accountInventoryCursorTestNow      = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
)

func testAccountInventoryCursorKeyring(t *testing.T, environment authn.Environment, current int, versions ...int) *authn.Keyring {
	t.Helper()
	records := make([]string, 0, len(versions))
	for _, version := range versions {
		key := bytes.Repeat([]byte{byte(version + 31)}, 32)
		records = append(records, fmt.Sprintf(`{"version":%d,"key":%q}`,
			version, base64.RawStdEncoding.EncodeToString(key)))
	}
	document := fmt.Sprintf(`{"format_version":1,"environment":%q,"current":%d,"keys":[%s]}`,
		environment, current, strings.Join(records, ","))
	keyring, err := authn.ParseKeyring([]byte(document), environment)
	if err != nil {
		t.Fatalf("parse cursor keyring: %v", err)
	}
	return keyring
}

func testAccountInventoryCursorCodec(
	t *testing.T, keyring *authn.Keyring, now *time.Time,
) *AccountInventoryCursorCodec {
	t.Helper()
	codec, err := newAccountInventoryCursorCodec(keyring, func() time.Time { return *now }, bytes.NewReader(bytes.Repeat([]byte{0x5a}, 4096)))
	if err != nil {
		t.Fatalf("new cursor codec: %v", err)
	}
	return codec
}

func TestAccountInventoryCursorRoundTripRandomNonceAndNormalizedFilters(t *testing.T) {
	now := accountInventoryCursorTestNow
	keyring := testAccountInventoryCursorKeyring(t, authn.EnvironmentProduction, 1, 1)
	codec, err := NewAccountInventoryCursorCodec(keyring)
	if err != nil {
		t.Fatalf("NewAccountInventoryCursorCodec: %v", err)
	}
	codec.now = func() time.Time { return now }
	filters := AccountInventoryCursorFilters{
		Provider: "openai", Lifecycle: "present", BasicStatus: "reported_active",
		Email: "operator@example.invalid",
	}
	after := "openai:operator@example.invalid"
	first, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters, after)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	second, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters, after)
	if err != nil {
		t.Fatalf("second Encode: %v", err)
	}
	if first == second {
		t.Fatal("cursor encryption reused a nonce")
	}
	decoded, err := codec.Decode(first, accountInventoryCursorTestActor, accountInventoryCursorTestInstance,
		AccountInventoryCursorFilters{
			Provider: " OPENAI ", Lifecycle: " PRESENT ", BasicStatus: " REPORTED_ACTIVE ",
			Email: " Operator@Example.Invalid ",
		})
	if err != nil || decoded != after {
		t.Fatalf("Decode = %q, %v; want continuation", decoded, err)
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("cursor is not raw URL-safe base64: %q", first)
	}
}

func TestAccountInventoryCursorMaximumIdentityFitsContractLimit(t *testing.T) {
	now := accountInventoryCursorTestNow
	codec := testAccountInventoryCursorCodec(t,
		testAccountInventoryCursorKeyring(t, authn.EnvironmentDev, 1, 1), &now)
	provider := strings.Repeat("a", 64)
	// The established identity rule permits non-control Unicode/text bytes.
	// Backslashes exercise the canonical encoder's worst JSON expansion.
	email := strings.Repeat("\\", 320)
	after := provider + ":" + email
	filters := AccountInventoryCursorFilters{Provider: provider}
	token, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters, after)
	if err != nil {
		t.Fatalf("Encode maximum identity: %v", err)
	}
	if len(token) > accountInventoryCursorMaxTokenBytes {
		t.Fatalf("maximum identity token length = %d, want <= %d", len(token), accountInventoryCursorMaxTokenBytes)
	}
	if decoded, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err != nil || decoded != after {
		t.Fatalf("Decode maximum identity = length %d, %v", len(decoded), err)
	}
}

func TestAccountInventoryCursorBindsActorInstanceAndEveryFilter(t *testing.T) {
	now := accountInventoryCursorTestNow
	codec := testAccountInventoryCursorCodec(t,
		testAccountInventoryCursorKeyring(t, authn.EnvironmentDev, 1, 1), &now)
	filters := AccountInventoryCursorFilters{
		Provider: "openai", Lifecycle: "missing", BasicStatus: "error", Email: "one@example.invalid",
	}
	token, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters,
		"openai:one@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		actor    uuid.UUID
		instance uuid.UUID
		filters  AccountInventoryCursorFilters
	}{
		{name: "actor", actor: uuid.New(), instance: accountInventoryCursorTestInstance, filters: filters},
		{name: "instance", actor: accountInventoryCursorTestActor, instance: uuid.New(), filters: filters},
		{name: "provider", actor: accountInventoryCursorTestActor, instance: accountInventoryCursorTestInstance,
			filters: AccountInventoryCursorFilters{Provider: "codex", Lifecycle: "missing", BasicStatus: "error", Email: "one@example.invalid"}},
		{name: "lifecycle", actor: accountInventoryCursorTestActor, instance: accountInventoryCursorTestInstance,
			filters: AccountInventoryCursorFilters{Provider: "openai", Lifecycle: "present", BasicStatus: "error", Email: "one@example.invalid"}},
		{name: "basic status", actor: accountInventoryCursorTestActor, instance: accountInventoryCursorTestInstance,
			filters: AccountInventoryCursorFilters{Provider: "openai", Lifecycle: "missing", BasicStatus: "unknown", Email: "one@example.invalid"}},
		{name: "email", actor: accountInventoryCursorTestActor, instance: accountInventoryCursorTestInstance,
			filters: AccountInventoryCursorFilters{Provider: "openai", Lifecycle: "missing", BasicStatus: "error", Email: "two@example.invalid"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := codec.Decode(token, test.actor, test.instance, test.filters); err != ErrInvalidAccountInventoryCursor {
				t.Fatalf("Decode error = %v, want uniform invalid cursor", err)
			}
		})
	}
}

func TestAccountInventoryCursorTTLAndKeyRotation(t *testing.T) {
	issuedAt := accountInventoryCursorTestNow
	oldKeyring := testAccountInventoryCursorKeyring(t, authn.EnvironmentStaging, 1, 1)
	oldCodec := testAccountInventoryCursorCodec(t, oldKeyring, &issuedAt)
	filters := AccountInventoryCursorFilters{Provider: "openai"}
	token, err := oldCodec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters,
		"openai:rotate@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	stillValid := issuedAt.Add(accountInventoryCursorTTL - time.Second)

	t.Run("old key retained within TTL", func(t *testing.T) {
		rotated := testAccountInventoryCursorCodec(t,
			testAccountInventoryCursorKeyring(t, authn.EnvironmentStaging, 2, 1, 2), &stillValid)
		if decoded, err := rotated.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err != nil ||
			decoded != "openai:rotate@example.invalid" {
			t.Fatal("retained key cursor did not decode within TTL")
		}
		currentToken, err := rotated.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance,
			filters, "openai:current@example.invalid")
		if err != nil {
			t.Fatal("rotated keyring did not issue a current cursor")
		}
		if envelope := decodeAccountInventoryCursorTestEnvelope(t, currentToken); envelope.KeyVersion != 2 {
			t.Fatal("rotated keyring did not issue the cursor with the current key version")
		}
		if _, err := rotated.Decode(currentToken, accountInventoryCursorTestActor,
			accountInventoryCursorTestInstance, filters); err != nil {
			t.Fatal("cursor issued with the rotated current key did not decode")
		}
	})

	t.Run("old key removed within TTL", func(t *testing.T) {
		removed := testAccountInventoryCursorCodec(t,
			testAccountInventoryCursorKeyring(t, authn.EnvironmentStaging, 2, 2), &stillValid)
		if _, err := removed.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err != ErrInvalidAccountInventoryCursor {
			t.Fatal("removed key did not return the fixed invalid cursor error")
		}
	})

	t.Run("expired with old key retained", func(t *testing.T) {
		expired := issuedAt.Add(accountInventoryCursorTTL)
		rotated := testAccountInventoryCursorCodec(t,
			testAccountInventoryCursorKeyring(t, authn.EnvironmentStaging, 2, 1, 2), &expired)
		if _, err := rotated.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err != ErrInvalidAccountInventoryCursor {
			t.Fatal("expired cursor did not return the fixed invalid cursor error")
		}
	})

	t.Run("cross environment within TTL", func(t *testing.T) {
		wrongEnvironment := testAccountInventoryCursorCodec(t,
			testAccountInventoryCursorKeyring(t, authn.EnvironmentProduction, 1, 1), &stillValid)
		if _, err := wrongEnvironment.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err != ErrInvalidAccountInventoryCursor {
			t.Fatal("cross-environment cursor did not return the fixed invalid cursor error")
		}
	})
}

func TestAccountInventoryCursorRejectsMalformedEnvelopeAndCiphertextUniformly(t *testing.T) {
	now := accountInventoryCursorTestNow
	keyring := testAccountInventoryCursorKeyring(t, authn.EnvironmentDev, 1, 1)
	codec := testAccountInventoryCursorCodec(t, keyring, &now)
	filters := AccountInventoryCursorFilters{Provider: "openai"}
	valid, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters,
		"openai:strict@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	envelope := decodeAccountInventoryCursorTestEnvelope(t, valid)
	tamperedCiphertext := envelope
	tamperedCiphertext.Ciphertext = bytes.Clone(tamperedCiphertext.Ciphertext)
	tamperedCiphertext.Ciphertext[len(tamperedCiphertext.Ciphertext)-1] ^= 1
	wrongVersion := envelope
	wrongVersion.Version = 2
	unknownKey := envelope
	unknownKey.KeyVersion = 99
	badCiphertext := envelope
	badCiphertext.Ciphertext = []byte("short")
	unknownField := append(mustDecodeRawURLTest(t, valid), 0x01)
	tests := map[string]string{
		"invalid base64":         "not+base64",
		"truncated token":        valid[:len(valid)-1],
		"overlong":               strings.Repeat("A", accountInventoryCursorMaxTokenBytes+1),
		"unknown envelope bytes": base64.RawURLEncoding.EncodeToString(unknownField),
		"noncanonical base64":    valid + "=",
		"unknown version":        encodeAccountInventoryCursorTestEnvelope(t, wrongVersion),
		"unknown key":            encodeAccountInventoryCursorTestEnvelope(t, unknownKey),
		"bad ciphertext":         encodeAccountInventoryCursorTestEnvelope(t, badCiphertext),
		"wrong tag":              encodeAccountInventoryCursorTestEnvelope(t, tamperedCiphertext),
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err != ErrInvalidAccountInventoryCursor {
				t.Fatalf("Decode error = %v, want exact uniform sentinel", err)
			}
		})
	}
}

func TestAccountInventoryCursorRejectsMaliciousAuthenticatedPlaintext(t *testing.T) {
	now := accountInventoryCursorTestNow
	keyring := testAccountInventoryCursorKeyring(t, authn.EnvironmentDev, 1, 1)
	codec := testAccountInventoryCursorCodec(t, keyring, &now)
	filters := AccountInventoryCursorFilters{Provider: "openai"}
	base := accountInventoryCursorPayload{
		Version: accountInventoryCursorVersion, KeyVersion: 1,
		ActorAdminID: accountInventoryCursorTestActor.String(), InstanceID: accountInventoryCursorTestInstance.String(),
		FiltersHash: accountInventoryCursorFilterHash(filters), AfterAccountKey: "openai:safe@example.invalid",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(accountInventoryCursorTTL).Unix(),
	}
	tests := map[string][]byte{}
	unknown := mustMarshalAccountInventoryCursorTest(t, base)
	tests["unknown field"] = append(unknown[:len(unknown)-1], []byte(`,"unknown":"identity"}`)...)
	tests["duplicate field"] = append(unknown[:len(unknown)-1], []byte(`,"v":1}`)...)
	tests["trailing data"] = append(mustMarshalAccountInventoryCursorTest(t, base), []byte(`{}`)...)
	tests["oversize"] = bytes.Repeat([]byte("x"), accountInventoryCursorMaxPlaintext+1)
	for name, mutate := range map[string]func(*accountInventoryCursorPayload){
		"payload version":     func(payload *accountInventoryCursorPayload) { payload.Version = 2 },
		"payload key version": func(payload *accountInventoryCursorPayload) { payload.KeyVersion = 2 },
		"future issued":       func(payload *accountInventoryCursorPayload) { payload.IssuedAt++; payload.ExpiresAt++ },
		"non-15-minute ttl":   func(payload *accountInventoryCursorPayload) { payload.ExpiresAt++ },
		"invalid after key":   func(payload *accountInventoryCursorPayload) { payload.AfterAccountKey = "not-an-account-key" },
	} {
		payload := base
		mutate(&payload)
		tests[name] = mustMarshalAccountInventoryCursorTest(t, payload)
	}
	for name, plaintext := range tests {
		t.Run(name, func(t *testing.T) {
			token := sealAccountInventoryCursorTestPlaintext(t, keyring, plaintext)
			if _, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err != ErrInvalidAccountInventoryCursor {
				t.Fatalf("Decode error = %v, want exact uniform sentinel", err)
			}
		})
	}
}

func TestAccountInventoryCursorRejectsInvalidInputsAndRandomFailure(t *testing.T) {
	if _, err := NewAccountInventoryCursorCodec(nil); err != ErrInvalidAccountInventoryCursor {
		t.Fatalf("nil keyring error = %v", err)
	}
	now := accountInventoryCursorTestNow
	keyring := testAccountInventoryCursorKeyring(t, authn.EnvironmentDev, 1, 1)
	codec := testAccountInventoryCursorCodec(t, keyring, &now)
	invalidFilters := []AccountInventoryCursorFilters{
		{Provider: "bad provider"}, {Lifecycle: "deleted"}, {BasicStatus: "active"},
		{Email: "line\nbreak@example.invalid"},
	}
	for _, filters := range invalidFilters {
		if _, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters,
			"openai:safe@example.invalid"); err != ErrInvalidAccountInventoryCursor {
			t.Fatalf("invalid filters error = %v", err)
		}
	}
	if _, err := codec.Encode(uuid.Nil, accountInventoryCursorTestInstance, AccountInventoryCursorFilters{},
		"openai:safe@example.invalid"); err != ErrInvalidAccountInventoryCursor {
		t.Fatalf("nil actor error = %v", err)
	}
	codec.random = errAccountInventoryCursorReader{}
	if _, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance,
		AccountInventoryCursorFilters{}, "openai:safe@example.invalid"); err != ErrInvalidAccountInventoryCursor {
		t.Fatalf("random failure error = %v", err)
	}
}

func TestAccountInventoryCursorCanaryIsCiphertextOnly(t *testing.T) {
	now := accountInventoryCursorTestNow
	keyring := testAccountInventoryCursorKeyring(t, authn.EnvironmentProduction, 1, 1)
	codec := testAccountInventoryCursorCodec(t, keyring, &now)
	email := strings.Join([]string{"provider-email", "cursor-canary", "example.invalid"}, "@")
	after := "openai:" + email
	filters := AccountInventoryCursorFilters{Provider: "openai", Email: email}
	token, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters, after)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustDecodeRawURLTest(t, token)
	envelope := decodeAccountInventoryCursorTestEnvelope(t, token)
	visibleArtifacts := []string{
		token,
		string(raw),
		base64.RawURLEncoding.EncodeToString(envelope.Nonce),
		base64.RawURLEncoding.EncodeToString(envelope.Ciphertext),
		"/api/account-inventory/query",
		fmt.Sprint(ErrInvalidAccountInventoryCursor),
		fmt.Sprintf("%+v", filters),
		`{"instance_id":"redacted","cursor_used":true}`,
	}
	for _, artifact := range visibleArtifacts {
		if strings.Contains(artifact, email) || strings.Contains(artifact, after) {
			t.Fatalf("visible cursor artifact exposed identity material")
		}
	}
	decoded, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters)
	if err != nil || decoded != after {
		t.Fatalf("canary Decode = %q, %v", decoded, err)
	}
}

type errAccountInventoryCursorReader struct{}

func (errAccountInventoryCursorReader) Read([]byte) (int, error) {
	return 0, errors.New("random source canary")
}

type accountInventoryCursorTestEnvelope struct {
	Version    int
	KeyVersion uint32
	Nonce      []byte
	Ciphertext []byte
}

func decodeAccountInventoryCursorTestEnvelope(t *testing.T, token string) accountInventoryCursorTestEnvelope {
	t.Helper()
	raw := mustDecodeRawURLTest(t, token)
	if len(raw) < accountInventoryCursorHeaderBytes+12 {
		t.Fatal("test envelope is truncated")
	}
	return accountInventoryCursorTestEnvelope{
		Version: int(raw[0]), KeyVersion: binary.BigEndian.Uint32(raw[1:accountInventoryCursorHeaderBytes]),
		Nonce:      bytes.Clone(raw[accountInventoryCursorHeaderBytes : accountInventoryCursorHeaderBytes+12]),
		Ciphertext: bytes.Clone(raw[accountInventoryCursorHeaderBytes+12:]),
	}
}

func encodeAccountInventoryCursorTestEnvelope(t *testing.T, envelope accountInventoryCursorTestEnvelope) string {
	t.Helper()
	raw := make([]byte, accountInventoryCursorHeaderBytes+len(envelope.Nonce)+len(envelope.Ciphertext))
	raw[0] = byte(envelope.Version)
	binary.BigEndian.PutUint32(raw[1:accountInventoryCursorHeaderBytes], envelope.KeyVersion)
	copy(raw[accountInventoryCursorHeaderBytes:], envelope.Nonce)
	copy(raw[accountInventoryCursorHeaderBytes+len(envelope.Nonce):], envelope.Ciphertext)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func sealAccountInventoryCursorTestPlaintext(t *testing.T, keyring *authn.Keyring, plaintext []byte) string {
	t.Helper()
	key, err := keyring.Derive(1, authn.DomainAccountInventoryCursorEncryption, 32)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := accountInventoryCursorAEAD(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x41}, aead.NonceSize())
	ciphertext := aead.Seal(nil, nonce, plaintext, accountInventoryCursorAAD(keyring.Environment(), 1))
	return encodeAccountInventoryCursorTestEnvelope(t, accountInventoryCursorTestEnvelope{
		Version: accountInventoryCursorVersion, KeyVersion: 1,
		Nonce: nonce, Ciphertext: ciphertext,
	})
}

func mustDecodeRawURLTest(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode raw URL base64: %v", err)
	}
	return raw
}

func mustMarshalAccountInventoryCursorTest(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := marshalCanonicalAccountInventoryCursorJSON(value)
	if err != nil {
		t.Fatalf("marshal test value: %v", err)
	}
	return raw
}
