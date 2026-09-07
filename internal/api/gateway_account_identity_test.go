package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/sunxu/relay-station-control/internal/drivers/gatewaydirectory"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewaySourceV1ToHTTPIdentityPrecision(t *testing.T) {
	for _, id := range []string{"9007199254740991", "9007199254740992", "9007199254740993", "9223372036854775807"} {
		t.Run(id, func(t *testing.T) {
			// Source v1 is deliberately numeric and decoded directly into Go int64.
			source := fmt.Sprintf(`{"schema_version":1,"generated_at":"2026-09-07T00:00:00Z","accounts":[{"id":%s,"name":"synthetic","platform":"openai","type":"apikey","url":null,"status":"active"}]}`, id)
			parsed, _, err := gatewaydirectory.ParseDirectoryResponse([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.SchemaVersion != 1 || strconv.FormatInt(parsed.Accounts[0].ID, 10) != id {
				t.Fatal("source identity changed")
			}
			encoded, err := json.Marshal(accountContextResponse(store.GatewayAccountContext{AccountID: parsed.Accounts[0].ID}))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"account_id":"`+id+`"`) {
				t.Fatalf("HTTP must serialize string: %s", encoded)
			}
			var body BindRelayNodeRequest
			if err := json.Unmarshal([]byte(`{"gateway_account_id":"`+id+`"}`), &body); err != nil {
				t.Fatal(err)
			}
			value, ok := parseGatewayAccountID(body.GatewayAccountId)
			if !ok || value != parsed.Accounts[0].ID {
				t.Fatal("request parse changed identity")
			}
		})
	}
}
