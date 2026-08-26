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
