package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
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

func TestParseAccountMultipartClassifiesSizeAndSyntaxFailures(t *testing.T) {
	makeRequest := func(t *testing.T, credentialSize int, malformed bool) (*http.Request, *httptest.ResponseRecorder) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		requestPart, err := writer.CreateFormField("request")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(requestPart, `{"command_id":"00000000-0000-0000-0000-000000000001","node_instance_id":"00000000-0000-0000-0000-000000000002","account_key":"antigravity:upload@example.invalid"}`)
		credential, err := writer.CreateFormFile("credential", "credential.json")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = credential.Write(bytes.Repeat([]byte{'x'}, credentialSize))
		if malformed {
			_, _ = body.WriteString("broken")
		} else if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body.Bytes()))
		if !malformed {
			r.Header.Set("Content-Type", writer.FormDataContentType())
		} else {
			r.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
		}
		return r, httptest.NewRecorder()
	}

	for _, test := range []struct {
		name           string
		credentialSize int
		malformed      bool
		want           accountMultipartFailure
	}{
		{name: "oversized credential", credentialSize: 1<<20 + 1, want: accountMultipartTooLarge},
		{name: "oversized aggregate", credentialSize: int(accountMultipartLimit), want: accountMultipartTooLarge},
		{name: "malformed multipart", malformed: true, want: accountMultipartInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, w := makeRequest(t, test.credentialSize, test.malformed)
			_, _, got := parseAccountMultipart(w, r)
			if got != test.want {
				t.Fatalf("failure=%q, want %q", got, test.want)
			}
		})
	}
}
