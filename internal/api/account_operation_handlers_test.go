package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestParseAccountUUIDRequiresCanonicalLowercase(t *testing.T) {
	id := uuid.New()
	for _, value := range []string{id.String(), strings.ToUpper(id.String()), "00000000-0000-0000-0000-000000000000"} {
		fields := map[string]json.RawMessage{"command_id": json.RawMessage(`"` + value + `"`)}
		got, ok := parseUUIDField(fields, "command_id")
		if (value == id.String()) != (ok && got == id) {
			t.Fatalf("parseUUIDField(%q) = %v, %v", value, got, ok)
		}
	}
}

func TestDecodeAccountObjectRejectsUnknownFields(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"command_id":"00000000-0000-0000-0000-000000000001","extra":true}`))
	w := httptest.NewRecorder()
	_, ok := decodeAccountObject(w, r, 8192, "command_id")
	if ok || w.Code != 400 {
		t.Fatalf("unknown account request accepted: ok=%v status=%d", ok, w.Code)
	}
}

func TestAccountOperationProjectionDoesNotExposeSecrets(t *testing.T) {
	secret := "credential-must-not-appear"
	op := assetstore.AccountAdminOperation{CommandID: uuid.New(), NodeInstanceID: uuid.New(), AccountKey: "antigravity:operator@example.com", OperationKind: assetstore.AccountUploadNew, ExecutionState: assetstore.AccountRemoteApplied, UploadIntentFingerprint: []byte(secret)}
	projection := accountOperationProjection(op)
	if projection.AccountKey != op.AccountKey || projection.OperationKind != AccountOperationProjectionOperationKind(assetstore.AccountUploadNew) {
		t.Fatal("operation projection is not stable")
	}
	if strings.Contains(projection.AccountKey, secret) {
		t.Fatal("operation projection contains credential material")
	}
}
