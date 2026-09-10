package jobs

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"testing"
	"time"
)

func testDefinition(executor Executor) Definition {
	if executor == nil {
		executor = testNoopExecutor{}
	}
	return Definition{
		Kind: "test.synthetic", SchemaVersion: 1,
		Schema: Schema{Fields: map[string]Field{
			"enabled":   {Type: FieldBoolean, Required: true},
			"revision":  {Type: FieldInteger, Required: true},
			"target_id": {Type: FieldUUID, Required: true},
			"mode":      {Type: FieldString, Required: true, MinLength: 1, MaxLength: 16, Pattern: regexp.MustCompile(`^[a-z]+$`)},
			"providers": {Type: FieldStringArray, MaxLength: 16, MaxItems: 4},
		}},
		Timeout: 2 * time.Second, LeaseDuration: 5 * time.Second,
		HeartbeatInterval: time.Second, MaxAttempts: 3, MaxVerifyAttempts: 2,
		AllowRollback: true, ReplaySafe: true,
		ErrorCodes: map[string]struct{}{"synthetic_failure": {}}, Executor: executor,
	}
}

type testNoopExecutor struct{}

func (testNoopExecutor) Execute(context.Context, Execution) ExecuteResult {
	return ExecuteResult{Disposition: ExecuteNeedsVerification}
}
func (testNoopExecutor) Verify(context.Context, Execution) VerifyResult {
	return VerifyResult{Disposition: VerifyEffectUnknown}
}
func (testNoopExecutor) Rollback(context.Context, Execution) RollbackResult {
	return RollbackResult{Disposition: RollbackUnknown}
}

func validPayload() []byte {
	return []byte(`{"revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe","providers":["a","b"]}`)
}

func TestProductionRegistryIsEmpty(t *testing.T) {
	if count := NewProductionRegistry().Len(); count != 0 {
		t.Fatalf("production registry has %d executors, want 0", count)
	}
}

func TestStringArrayDuplicatesRequireExplicitOptIn(t *testing.T) {
	for _, allow := range []bool{false, true} {
		definition := testDefinition(nil)
		field := definition.Schema.Fields["providers"]
		field.AllowDuplicates = allow
		definition.Schema.Fields["providers"] = field
		registry, err := NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		payload := bytes.Replace(validPayload(), []byte(`["a","b"]`), []byte(`["a","a"]`), 1)
		canonical, hash, cloned, err := registry.ValidateAndHash(definition.Kind, 1, payload)
		if !allow {
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("default duplicate validation: %v", err)
			}
			continue
		}
		if err != nil || !cloned.Schema.Fields["providers"].AllowDuplicates {
			t.Fatalf("opt-in lost: %v", err)
		}
		again, againHash, _, err := registry.ValidateAndHash(definition.Kind, 1, canonical)
		if err != nil || !bytes.Equal(canonical, again) || hash != againHash {
			t.Fatal("non-deterministic duplicate canonicalization")
		}
		for _, invalid := range []string{`["b","a"]`, `["a","a","a","a","a"]`, `["a","12345678901234567"]`} {
			_, _, _, err := registry.ValidateAndHash(definition.Kind, 1, bytes.Replace(validPayload(), []byte(`["a","b"]`), []byte(invalid), 1))
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("array bounds/order relaxed: %s", invalid)
			}
		}
	}
	definition := testDefinition(nil)
	field := definition.Schema.Fields["mode"]
	field.AllowDuplicates = true
	definition.Schema.Fields["mode"] = field
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("non-array duplicate policy accepted")
	}
}

func TestSemanticPayloadValidationUsesCanonicalInput(t *testing.T) {
	definition := testDefinition(nil)
	expected, err := definition.Schema.Canonicalize(validPayload())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	definition.ValidatePayload = func(raw []byte) error {
		called = true
		if !bytes.Equal(raw, expected) {
			t.Fatal("semantic validation received non-canonical input")
		}
		raw[0] = 'x' // Validator cannot mutate the canonical bytes to be hashed/stored.
		return nil
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, _, err := registry.ValidateAndHash(definition.Kind, 1, validPayload())
	if err != nil || !called || !bytes.Equal(canonical, expected) {
		t.Fatalf("semantic validation: %v", err)
	}
	definition.ValidatePayload = func([]byte) error { return errors.New("private validator detail") }
	registry, err = NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	canonical, hash, _, err := registry.ValidateAndHash(definition.Kind, 1, validPayload())
	if err != ErrInvalidPayload || canonical != nil || hash != [32]byte{} {
		t.Fatalf("invalid semantics returned payload/hash: %v", err)
	}
}

func TestStringArrayPreserveOrderCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		preserve, duplicates bool
		values               string
		valid                bool
	}{
		{"legacy unsorted", false, false, `["z","a"]`, false},
		{"duplicates alone require sorting", false, true, `["z","a"]`, false},
		{"ordered unique", true, false, `["z","a"]`, true},
		{"ordered nonadjacent duplicates rejected", true, false, `["z","a","z"]`, false},
		{"ordered repeated display", true, true, `["z","a","z"]`, true},
		{"ordered still bounded", true, true, `["z","a","z","a","z"]`, false},
		{"ordered strings still bounded", true, true, `["z","12345678901234567"]`, false},
		{"ordered string controls rejected", true, true, `["z","a\n"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := testDefinition(nil)
			field := definition.Schema.Fields["providers"]
			field.PreserveOrder, field.AllowDuplicates = tc.preserve, tc.duplicates
			definition.Schema.Fields["providers"] = field
			registry, err := NewRegistry(definition)
			if err != nil {
				t.Fatal(err)
			}
			raw := bytes.Replace(validPayload(), []byte(`["a","b"]`), []byte(tc.values), 1)
			canonical, hash, cloned, err := registry.ValidateAndHash(definition.Kind, 1, raw)
			if !tc.valid {
				if !errors.Is(err, ErrInvalidPayload) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || !bytes.Contains(canonical, []byte(tc.values)) || !cloned.Schema.Fields["providers"].PreserveOrder {
				t.Fatalf("order policy lost: %s %v", canonical, err)
			}
			again, againHash, _, err := registry.ValidateAndHash(definition.Kind, 1, canonical)
			if err != nil || !bytes.Equal(canonical, again) || hash != againHash {
				t.Fatal("order/hash changed")
			}
		})
	}
	definition := testDefinition(nil)
	field := definition.Schema.Fields["mode"]
	field.PreserveOrder = true
	definition.Schema.Fields["mode"] = field
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("non-array preserve-order accepted")
	}
}

func TestStrictPayloadCanonicalizationAndHash(t *testing.T) {
	registry, err := NewRegistry(testDefinition(nil))
	if err != nil {
		t.Fatal(err)
	}
	first, firstHash, _, err := registry.ValidateAndHash("test.synthetic", 1, validPayload())
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, _, err := registry.ValidateAndHash("test.synthetic", 1,
		[]byte(`{ "providers":["a","b"], "mode":"safe", "enabled":true, "target_id":"2CF45C9D-EA70-4D1A-AE2B-550701C22A55", "revision":01 }`))
	if err == nil {
		t.Fatal("non-JSON leading-zero number unexpectedly accepted")
	}
	second, secondHash, _, err = registry.ValidateAndHash("test.synthetic", 1,
		[]byte(`{ "providers":["a","b"], "mode":"safe", "enabled":true, "target_id":"2CF45C9D-EA70-4D1A-AE2B-550701C22A55", "revision":1 }`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstHash != secondHash {
		t.Fatalf("equivalent payloads are not deterministic:\n%s\n%s", first, second)
	}
	if len(first) > MaxPayloadBytes {
		t.Fatal("canonical payload exceeds bound")
	}
}

func TestStrictPayloadRejectsDangerousOrAmbiguousInput(t *testing.T) {
	registry, err := NewRegistry(testDefinition(nil))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown field":     []byte(`{"revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe","extra":1}`),
		"secret field":      []byte(`{"revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe","api_token":"canary"}`),
		"wrong type":        []byte(`{"revision":"1","target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe"}`),
		"duplicate key":     []byte(`{"revision":1,"revision":2,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe"}`),
		"control in string": []byte(`{"revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe\u0001"}`),
		"unsorted array":    []byte(`{"revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe","providers":["b","a"]}`),
		"duplicate array":   []byte(`{"revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"safe","providers":["a","a"]}`),
		"trailing document": append(validPayload(), []byte(` {}`)...),
		"trailing garbage":  append(validPayload(), []byte(` definitely-not-json`)...),
		"array root":        []byte(`[]`),
		"oversized":         append([]byte(`{"`), bytes.Repeat([]byte("x"), MaxPayloadBytes)...),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := registry.ValidateAndHash("test.synthetic", 1, payload)
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error = %v, want invalid payload", err)
			}
		})
	}
	if _, _, _, err := registry.ValidateAndHash("unknown", 1, validPayload()); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind error = %v", err)
	}
	if _, _, _, err := registry.ValidateAndHash("test.synthetic", 2, validPayload()); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("unknown schema error = %v", err)
	}
}

func TestRegistryRejectsSecretLikeSchemaAndUnsafePolicy(t *testing.T) {
	definition := testDefinition(nil)
	definition.Schema.Fields["password"] = Field{Type: FieldString}
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("secret-like schema field accepted")
	}
	definition = testDefinition(nil)
	definition.HeartbeatInterval = definition.LeaseDuration
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("heartbeat at lease boundary accepted")
	}
	definition = testDefinition(nil)
	definition.Executor = nil
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("definition without executor accepted")
	}
	definition = testDefinition(nil)
	definition.Timeout = time.Second + time.Millisecond
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("sub-second timeout precision unsupported by PostgreSQL was accepted")
	}
	definition = testDefinition(nil)
	definition.LeaseDuration = 4 * time.Second
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("lease below PostgreSQL minimum was accepted")
	}
	definition = testDefinition(nil)
	definition.HeartbeatInterval = time.Second + time.Millisecond
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("sub-second heartbeat precision unsupported by PostgreSQL was accepted")
	}
}

func TestRegistryDefinitionsAreImmutableCopies(t *testing.T) {
	definition := testDefinition(nil)
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.Schema.Fields["password"] = Field{Type: FieldString}
	definition.ErrorCodes["mutated"] = struct{}{}
	lookedUp, ok := registry.Lookup("test.synthetic")
	if !ok {
		t.Fatal("registered definition missing")
	}
	lookedUp.Schema.Fields["second_mutation"] = Field{Type: FieldString}
	lookedUp.ErrorCodes["second_mutation"] = struct{}{}
	again, _ := registry.Lookup("test.synthetic")
	if _, exists := again.Schema.Fields["password"]; exists {
		t.Fatal("caller mutated registry schema after construction")
	}
	if _, exists := again.Schema.Fields["second_mutation"]; exists {
		t.Fatal("caller mutated registry schema returned by Lookup")
	}
	if _, exists := again.ErrorCodes["mutated"]; exists {
		t.Fatal("caller mutated registry error codes")
	}
}

func TestRegistryCatalogIsSortedExactAndExecutorFree(t *testing.T) {
	second := testDefinition(nil)
	second.Kind = "z.synthetic"
	second.SchemaVersion = 2
	second.Timeout = 3 * time.Second
	second.LeaseDuration = 6 * time.Second
	second.HeartbeatInterval = 2 * time.Second
	second.MaxAttempts = 4
	second.MaxVerifyAttempts = 5
	second.ReplaySafe = false
	second.AllowRollback = false
	first := testDefinition(nil)
	first.Kind = "a.synthetic"
	registry, err := NewRegistry(second, first)
	if err != nil {
		t.Fatal(err)
	}

	catalog := registry.Catalog()
	if len(catalog) != 2 || catalog[0].Kind != "a.synthetic" || catalog[1].Kind != "z.synthetic" {
		t.Fatalf("catalog order = %+v", catalog)
	}
	want := CatalogEntry{
		Kind: "z.synthetic", SchemaVersion: 2, Timeout: 3 * time.Second,
		LeaseDuration: 6 * time.Second, HeartbeatInterval: 2 * time.Second,
		MaxAttempts: 4, MaxVerifyAttempts: 5, ReplaySafe: false, AllowRollback: false,
	}
	if catalog[1] != want {
		t.Fatalf("catalog policy = %+v, want %+v", catalog[1], want)
	}
	catalog[0].Kind = "caller-mutated"
	if again := registry.Catalog(); again[0].Kind != "a.synthetic" {
		t.Fatal("caller mutated registry catalog")
	}
	if production := NewProductionRegistry().Catalog(); production == nil || len(production) != 0 {
		t.Fatalf("empty production catalog = %#v", production)
	}
}
