package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestSecretPatchKeepsRawStringForStoreValidation(t *testing.T) {
	for name, value := range map[string]string{
		"empty":      "",
		"4096 bytes": strings.Repeat("a", 4096),
		"4097 bytes": strings.Repeat("a", 4097),
	} {
		t.Run(name, func(t *testing.T) {
			patch := secretPatch(map[string]json.RawMessage{"credential": json.RawMessage(strconv.Quote(value))}, "credential")
			if patch.Operation != assetstore.SecretSet || patch.Value != value {
				t.Fatalf("secretPatch() = %#v, want SecretSet with exact value", patch)
			}
		})
	}
}
