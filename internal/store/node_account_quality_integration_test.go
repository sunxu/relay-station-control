package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	requestquality "github.com/sunxu/relay-station-control/internal/requestquality"
	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestNodeAccountQualityCompositionPostgres(t *testing.T) {
	ctx := context.Background()
	accounts := make([]lifecycleAccount, 110)
	for i := range accounts {
		accounts[i] = lifecycleAccount{email: "user" + testDecimal(i) + "@example.invalid"}
	}
	accounts[109].email = "zzzz@example.invalid"
	fixture := newReadonlyQueryFixture(t, ctx, accounts)
	repo, err := productstore.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	failed := "upstream"
	key := func(email string) string { return "openai:" + email }
	events := []requestquality.Event{
		{EventHash: "late-bad", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: stringPtr(key(accounts[109].email)), OccurredAt: now.Add(-time.Minute), Success: false, FailureClass: &failed},
		{EventHash: "unresolved", NodeID: fixture.lifecycle.instanceID, Provider: "openai", OccurredAt: now.Add(-time.Minute), Success: false, FailureClass: &failed},
	}
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	query := productstore.AccountQualityQuery{InstanceID: fixture.lifecycle.instanceID, Window: time.Hour, Provider: "openai", Quality: "bad", Limit: 1}
	page, err := repo.ListAccountQuality(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].AccountKey != key(accounts[109].email) || page.Items[0].Quality != "bad" {
		t.Fatalf("late filtered page=%+v", page)
	}
	if page.Items[0].Stats.UnresolvedRequestCount != 0 {
		t.Fatalf("account quality contaminated=%+v", page.Items[0].Stats)
	}
	if page.HasMore {
		t.Fatal("single bad result unexpectedly has lookahead")
	}

	all, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: fixture.lifecycle.instanceID, Window: 15 * time.Minute, Provider: "openai", Quality: "unknown", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 100 || !all.HasMore {
		t.Fatalf("unknown page=%d more=%v", len(all.Items), all.HasMore)
	}
	second, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: fixture.lifecycle.instanceID, Window: 15 * time.Minute, Provider: "openai", Quality: "unknown", AfterAccountKey: all.Items[len(all.Items)-1].AccountKey, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 9 || second.HasMore {
		t.Fatalf("second page=%d more=%v", len(second.Items), second.HasMore)
	}
}

func stringPtr(value string) *string { return &value }

func testDecimal(value int) string {
	return fmt.Sprintf("%03d", value)
}
