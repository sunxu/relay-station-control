package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAccountInventoryQueryValidationMatrix(t *testing.T) {
	base := AccountInventoryQuery{InstanceID: uuid.New(), Limit: 50}
	tests := []struct {
		name  string
		query AccountInventoryQuery
		valid bool
	}{
		{name: "unfiltered", query: base, valid: true},
		{name: "all filters", query: func() AccountInventoryQuery {
			query := base
			query.Filters = AccountInventoryQueryFilters{
				Provider: "openai", Lifecycle: AccountInventoryMissing,
				BasicStatus: AccountInventoryBasicStatusReportedActive,
				Email:       "operator@example.invalid",
			}
			query.AfterAccountKey = "openai:before@example.invalid"
			return query
		}(), valid: true},
		{name: "nil instance", query: func() AccountInventoryQuery { query := base; query.InstanceID = uuid.Nil; return query }()},
		{name: "zero limit", query: func() AccountInventoryQuery { query := base; query.Limit = 0; return query }()},
		{name: "large limit", query: func() AccountInventoryQuery { query := base; query.Limit = 101; return query }()},
		{name: "provider", query: func() AccountInventoryQuery { query := base; query.Filters.Provider = "OpenAI"; return query }()},
		{name: "lifecycle", query: func() AccountInventoryQuery { query := base; query.Filters.Lifecycle = "deleted"; return query }()},
		{name: "basic status", query: func() AccountInventoryQuery { query := base; query.Filters.BasicStatus = "active"; return query }()},
		{name: "email", query: func() AccountInventoryQuery {
			query := base
			query.Filters.Email = "Operator@example.invalid"
			return query
		}()},
		{name: "after key", query: func() AccountInventoryQuery { query := base; query.AfterAccountKey = "opaque"; return query }()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validAccountInventoryQuery(test.query); got != test.valid {
				t.Fatalf("valid=%v, want %v", got, test.valid)
			}
		})
	}
}

func TestAccountInventoryRepositoryFailsClosedUntilCompatibilityPasses(t *testing.T) {
	repository := &AccountInventoryRepository{}
	if _, err := repository.QueryPageAndAudit(nil, AccountInventoryQuery{}, AccountInventoryViewAudit{}); !errors.Is(err, ErrAccountInventoryInconsistent) {
		t.Fatalf("query before compatibility check error=%v", err)
	}
}

func TestAccountInventoryPageUsesLimitPlusOneWithoutReturningAccountKey(t *testing.T) {
	instanceID := uuid.New()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	providerDegraded := false
	row := func(email string) []byte {
		t.Helper()
		encoded, err := json.Marshal(accountInventoryRow{
			InstanceID: instanceID, Provider: "openai", AccountKey: "openai:" + email,
			NormalizedEmail: email, BasicStatus: AccountInventoryBasicStatusReportedActive,
			Lifecycle: AccountInventoryPresent, FirstSeenAt: now, LastSeenAt: now,
			ProviderLastCompleteAt: now, ProviderDegraded: &providerDegraded,
			SnapshotFreshness: AccountInventorySnapshotFreshnessFresh,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	page, err := accountInventoryPageFromRows(AccountInventoryQuery{InstanceID: instanceID, Limit: 2}, [][]byte{
		row("a@example.invalid"), row("b@example.invalid"), row("c@example.invalid"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Items) != 2 || page.ContinuationAccountKey != "openai:b@example.invalid" {
		t.Fatalf("unexpected bounded page: %+v", page)
	}
	if page.Items[0].Email != "a@example.invalid" || page.Items[1].Email != "b@example.invalid" {
		t.Fatal("page order or product identity projection changed")
	}
}

func TestAccountInventoryRowsFailClosed(t *testing.T) {
	instanceID := uuid.New()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	providerDegraded := false
	base := accountInventoryRow{
		InstanceID: instanceID, Provider: "openai", AccountKey: "openai:test@example.invalid",
		NormalizedEmail: "test@example.invalid", BasicStatus: AccountInventoryBasicStatusReportedActive,
		Lifecycle: AccountInventoryPresent, FirstSeenAt: now, LastSeenAt: now,
		ProviderLastCompleteAt: now, ProviderDegraded: &providerDegraded,
		SnapshotFreshness: AccountInventorySnapshotFreshnessFresh,
	}
	mutations := map[string]func(*accountInventoryRow){
		"instance":        func(row *accountInventoryRow) { row.InstanceID = uuid.New() },
		"identity":        func(row *accountInventoryRow) { row.AccountKey = "openai:other@example.invalid" },
		"status":          func(row *accountInventoryRow) { row.BasicStatus = "active" },
		"freshness":       func(row *accountInventoryRow) { row.SnapshotFreshness = "unknown" },
		"provider time":   func(row *accountInventoryRow) { row.ProviderLastCompleteAt = time.Time{} },
		"provider health": func(row *accountInventoryRow) { row.ProviderDegraded = nil },
		"lifecycle": func(row *accountInventoryRow) {
			row.Lifecycle = AccountInventoryMissing
			row.ConsecutiveMissingCount = 2
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if validAccountInventoryRow(instanceID, candidate) {
				t.Fatal("inconsistent row was accepted")
			}
		})
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, "missing"} {
		if value == "missing" {
			delete(fields, "provider_degraded")
		} else {
			fields["provider_degraded"] = nil
		}
		malformed, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := accountInventoryPageFromRows(
			AccountInventoryQuery{InstanceID: instanceID, Limit: 1}, [][]byte{malformed},
		); !errors.Is(err, ErrAccountInventoryInconsistent) {
			t.Fatalf("nullable Provider health error=%v", err)
		}
	}
}

func TestAccountInventoryDatabaseErrorsAreStable(t *testing.T) {
	for code, want := range map[string]error{
		"22023": ErrInvalidAccountInventoryQuery,
		"P0404": ErrAccountInventoryInstanceNotFound,
		"P0409": ErrAccountInventoryCapabilityUnsupported,
		"P0503": ErrAccountInventoryInconsistent,
	} {
		if got := accountInventoryDatabaseError(&pgconn.PgError{Code: code, Message: "identity must not escape"}); !errors.Is(got, want) {
			t.Fatalf("code %s mapped to %v, want %v", code, got, want)
		}
	}
}

func TestAccountInventoryProductDTOFormattingIsRedacted(t *testing.T) {
	marker := "identity-marker"
	values := []any{
		AccountInventoryQueryFilters{Email: marker},
		AccountInventoryQuery{AfterAccountKey: marker},
		AccountInventoryViewAudit{RequestID: marker},
		AccountInventoryItem{Email: marker},
		AccountInventoryPage{ContinuationAccountKey: marker},
	}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := strings.ToLower(fmt.Sprintf(format, value))
			if strings.Contains(text, marker) || !strings.Contains(text, "redacted") {
				t.Fatalf("protected DTO formatter leaked with %s: %s", format, text)
			}
		}
	}
}
