package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	requestquality "github.com/sunxu/relay-station-control/internal/requestquality"
	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountRequestHistoryPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "history-a@example.invalid"}, {email: "history-b@example.invalid"}, {email: "history-empty@example.invalid"}})
	repo, err := productstore.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	failure := "upstream"
	accountA := "openai:history-a@example.invalid"
	accountB := "openai:history-b@example.invalid"
	accountEmpty := "openai:history-empty@example.invalid"
	otherNode := uuid.New()
	events := []requestquality.Event{
		{EventHash: "a-old", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &accountA, OccurredAt: now.Add(-8 * 24 * time.Hour), Success: true},
		{EventHash: "a-new", RequestID: "request-a", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &accountA, Model: "gpt", OccurredAt: now.Add(-time.Minute), DurationMS: int64Ptr(123), Success: false, FailureClass: &failure},
		{EventHash: "a-null", NodeID: fixture.lifecycle.instanceID, Provider: "openai", OccurredAt: now.Add(-time.Minute), Success: false, FailureClass: &failure},
		{EventHash: "a-other-node", NodeID: otherNode, Provider: "openai", AccountKey: &accountA, OccurredAt: now.Add(-time.Minute), Success: true},
		{EventHash: "b-new", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &accountB, OccurredAt: now.Add(-2 * time.Minute), Success: true},
		{EventHash: "event-only", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: stringPtr("openai:event-only@example.invalid"), OccurredAt: now.Add(-time.Minute), Success: true},
	}
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	page, err := repo.ListAccountRequestHistory(ctx, productstore.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: accountA, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].EventHash != "a-new" || page.Items[0].RequestID != "request-a" || page.Items[0].DurationMS == nil || *page.Items[0].DurationMS != 123 || page.Items[0].Success || page.Items[0].FailureClass == nil {
		t.Fatalf("history=%+v", page)
	}
	if page.HasMore {
		t.Fatal("history unexpectedly has more")
	}
	empty, err := repo.ListAccountRequestHistory(ctx, productstore.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: accountEmpty, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("empty account history=%+v", empty)
	}

	_, err = repo.ListAccountRequestHistory(ctx, productstore.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: "openai:event-only@example.invalid", Limit: 10})
	if !errors.Is(err, productstore.ErrAccountInventoryInstanceNotFound) {
		t.Fatalf("event-only error=%v", err)
	}
	_, err = repo.ListAccountRequestHistory(ctx, productstore.AccountRequestHistoryQuery{InstanceID: otherNode, AccountKey: accountA, Limit: 10})
	if !errors.Is(err, productstore.ErrAccountInventoryInstanceNotFound) {
		t.Fatalf("other node error=%v", err)
	}
	_, err = repo.ListAccountRequestHistory(ctx, productstore.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: "openai:history-a@example.invalid", AfterEventHash: "orphan", Limit: 10})
	if !errors.Is(err, productstore.ErrInvalidAccountInventoryQuery) {
		t.Fatalf("invalid cursor error=%v", err)
	}
}

func TestAccountRequestHistoryBoundaryAndStableKeysetPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "boundary@example.invalid"}})
	repo, err := productstore.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	account := "openai:boundary@example.invalid"
	node := fixture.lifecycle.instanceID
	// These rows use statement_timestamp in one SQL statement. The DO block
	// invokes the new function in that same statement, proving the inclusive
	// seven-day lower bound without relying on host clock timing.
	boundarySQL := fmt.Sprintf(`DO $test$
DECLARE hashes text[];
BEGIN
  INSERT INTO public.account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success)
    VALUES ('exact-7d','%s'::uuid,'openai','%s',statement_timestamp()-interval '7 days',true);
  INSERT INTO public.account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success)
    VALUES ('future','%s'::uuid,'openai','%s',statement_timestamp()+interval '1 day',true);
  INSERT INTO public.account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success)
    VALUES ('expired','%s'::uuid,'openai','%s',statement_timestamp()-interval '7 days 1 microsecond',true);
  SELECT array_agg(event_hash ORDER BY event_hash) INTO hashes FROM public.control_query_account_request_history_v1('%s'::uuid,'%s',NULL,NULL,100);
  IF hashes IS DISTINCT FROM ARRAY['exact-7d']::text[] THEN RAISE EXCEPTION 'seven-day boundary mismatch: %%', hashes; END IF;

END $test$`, node, account, node, account, node, account, node, account)
	if _, err := fixture.database.owner.Exec(ctx, boundarySQL); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.owner.Exec(ctx, `INSERT INTO public.account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success) VALUES ('z-tie',$1,'openai',$2,statement_timestamp()-interval '1 minute',true),('a-tie',$1,'openai',$2,statement_timestamp()-interval '1 minute',true)`, node, account); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ListAccountRequestHistory(ctx, productstore.AccountRequestHistoryQuery{InstanceID: node, AccountKey: account, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].EventHash != "z-tie" || !first.HasMore {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := repo.ListAccountRequestHistory(ctx, productstore.AccountRequestHistoryQuery{InstanceID: node, AccountKey: account, AfterOccurredAt: &first.Items[0].OccurredAt, AfterEventHash: first.Items[0].EventHash, Limit: 1})
	if err != nil || len(second.Items) != 1 || second.Items[0].EventHash != "a-tie" || second.HasMore {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if !second.Items[0].OccurredAt.Equal(first.Items[0].OccurredAt) {
		t.Fatal("fixture timestamps are not tied")
	}
	if second.Items[0].EventHash == first.Items[0].EventHash {
		t.Fatal("keyset repeated event")
	}
}

func TestAccountRequestHistoryReadFailuresPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "failure@example.invalid"}})
	repo, err := productstore.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	q := productstore.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: "openai:failure@example.invalid", Limit: 25}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.ListAccountRequestHistory(canceled, q); err == nil {
		t.Fatal("cancelled read returned successful empty")
	}
	if _, err := fixture.database.owner.Exec(ctx, `DELETE FROM node_capabilities WHERE instance_id=$1 AND capability='management_account_inventory_read'`, q.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListAccountRequestHistory(ctx, q); !errors.Is(err, productstore.ErrAccountInventoryCapabilityUnsupported) {
		t.Fatalf("Inventory capability failure: %v", err)
	}
}

// Hash is opaque text, not a hex-only or trimmed identifier. Preserve the
// existing event table's 1..256-byte identity contract during pagination.
func TestAccountRequestHistoryOpaqueHashPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "opaque@example.invalid"}})
	repo, err := productstore.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	q := productstore.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: "openai:opaque@example.invalid", Limit: 1}
	if _, err := fixture.database.owner.Exec(ctx, `INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success) VALUES ('  ',$1,'openai',$2,statement_timestamp()-interval '1 minute',true),(' ',$1,'openai',$2,statement_timestamp()-interval '1 minute',true)`, q.InstanceID, q.AccountKey); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ListAccountRequestHistory(ctx, q)
	if err != nil || len(first.Items) != 1 || first.Items[0].EventHash != "  " || !first.HasMore {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	q.AfterOccurredAt = &first.Items[0].OccurredAt
	q.AfterEventHash = first.Items[0].EventHash
	second, err := repo.ListAccountRequestHistory(ctx, q)
	if err != nil || len(second.Items) != 1 || second.Items[0].EventHash != " " || second.HasMore {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}
