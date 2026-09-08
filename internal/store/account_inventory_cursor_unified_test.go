package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func TestUnifiedAccountCursorBindingsAndLegacyCompatibility(t *testing.T) {
	now := accountInventoryCursorTestNow
	codec := testAccountInventoryCursorCodec(t, testAccountInventoryCursorKeyring(t, authn.EnvironmentProduction, 1, 1), &now)
	legacy := AccountInventoryCursorFilters{Provider: "openai", Lifecycle: "present", BasicStatus: "reported_active", Email: "cursor@example.invalid"}
	// This byte sequence is the pre-change filter hash contract. New empty
	// fields must not invalidate an already-issued legacy Inventory cursor.
	sum := sha256.Sum256([]byte(`{"provider":"openai","lifecycle":"present","basic_status":"reported_active","email":"cursor@example.invalid"}`))
	if got := accountInventoryCursorFilterHash(legacy); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("legacy filter hash changed: %s", got)
	}
	filters := legacy
	filters.Window, filters.Quality, filters.Scope = "15m", "good", "node-account-quality"
	key := "openai:cursor@example.invalid"
	token, err := codec.Encode(accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters, key)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters)
	if err != nil || decoded != key {
		t.Fatalf("round trip=%q %v", decoded, err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("cursor@example.invalid")) || bytes.Contains(raw, []byte(key)) {
		t.Fatal("cursor exposes account identity")
	}
	changes := map[string]func(*AccountInventoryCursorFilters){
		"window":    func(f *AccountInventoryCursorFilters) { f.Window = "1h" },
		"quality":   func(f *AccountInventoryCursorFilters) { f.Quality = "bad" },
		"scope":     func(f *AccountInventoryCursorFilters) { f.Scope = "" },
		"provider":  func(f *AccountInventoryCursorFilters) { f.Provider = "other" },
		"lifecycle": func(f *AccountInventoryCursorFilters) { f.Lifecycle = "missing" },
		"status":    func(f *AccountInventoryCursorFilters) { f.BasicStatus = "disabled" },
		"email":     func(f *AccountInventoryCursorFilters) { f.Email = "other@example.invalid" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			altered := filters
			change(&altered)
			if _, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, altered); err == nil {
				t.Fatal("mismatched filter accepted")
			}
		})
	}
	if _, err := codec.Decode(token, uuid.New(), accountInventoryCursorTestInstance, filters); err == nil {
		t.Fatal("other actor accepted")
	}
	if _, err := codec.Decode(token, accountInventoryCursorTestActor, uuid.New(), filters); err == nil {
		t.Fatal("other Node accepted")
	}
	if _, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, legacy); err == nil {
		t.Fatal("legacy endpoint accepted unified cursor")
	}
	raw[len(raw)-1] ^= 1
	if _, err := codec.Decode(base64.RawURLEncoding.EncodeToString(raw), accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	now = now.Add(15*time.Minute + time.Second)
	if _, err := codec.Decode(token, accountInventoryCursorTestActor, accountInventoryCursorTestInstance, filters); err == nil {
		t.Fatal("expired cursor accepted")
	}
}
