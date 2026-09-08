package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	requestquality "github.com/sunxu/relay-station-control/internal/requestquality"
	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountQualityIncidentsPostgres(t *testing.T) {
	ctx := context.Background()
	accounts := make([]lifecycleAccount, 110)
	for i := range accounts {
		accounts[i] = lifecycleAccount{email: "incident" + testDecimal(i) + "@example.invalid"}
	}
	fixture := newReadonlyQueryFixture(t, ctx, accounts)
	repo, err := productstore.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	events := make([]requestquality.Event, 0, 20)
	classes := []string{"auth", "quota", "rate_limit", "upstream"}
	for _, class := range classes {
		for i := 0; i < 3; i++ {
			events = append(events, incidentEvent(fixture.lifecycle.instanceID, "openai:"+accounts[0].email, "a-"+class+testDecimal(i), class, now.Add(-time.Duration(i+1)*time.Minute)))
		}
	}
	// The second qualifying account is after the first 101 inventory rows.
	for i := 0; i < 3; i++ {
		events = append(events, incidentEvent(fixture.lifecycle.instanceID, "openai:"+accounts[109].email, "late-"+testDecimal(i), "auth", now.Add(-time.Duration(i+1)*time.Minute)))
	}
	// Two failures do not reach the threshold; a success does not reduce hits.
	for i := 0; i < 2; i++ {
		events = append(events, incidentEvent(fixture.lifecycle.instanceID, "openai:"+accounts[1].email, "two-"+testDecimal(i), "auth", now.Add(-time.Duration(i+1)*time.Minute)))
	}
	for i := 0; i < 3; i++ {
		events = append(events, incidentEvent(fixture.lifecycle.instanceID, "openai:"+accounts[2].email, "older-"+testDecimal(i), "auth", now.Add(-time.Duration(i+6)*time.Minute)))
	}
	account0 := "openai:" + accounts[0].email
	events = append(events,
		requestquality.Event{EventHash: "success-7d", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &account0, OccurredAt: now.Add(-30 * time.Second), Success: true},
		requestquality.Event{EventHash: "success-future", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &account0, OccurredAt: now.Add(time.Minute), Success: true},
		requestquality.Event{EventHash: "success-old", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &account0, OccurredAt: now.Add(-8 * 24 * time.Hour), Success: true},
	)
	lateKey := "openai:" + accounts[109].email
	events = append(events,
		requestquality.Event{EventHash: "late-old-success", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &lateKey, OccurredAt: now.Add(-8 * 24 * time.Hour), Success: true},
		requestquality.Event{EventHash: "late-future-success", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &lateKey, OccurredAt: now.Add(24 * time.Hour), Success: true})
	for i := 0; i < 3; i++ {
		events = append(events, incidentEvent(fixture.lifecycle.instanceID, account0, "unknown-"+testDecimal(i), "unknown", now.Add(-time.Duration(i+1)*time.Minute)))
		events = append(events, requestquality.Event{EventHash: "null-" + testDecimal(i), NodeID: fixture.lifecycle.instanceID, Provider: "openai", OccurredAt: now.Add(-time.Duration(i+1) * time.Minute), Success: false, FailureClass: stringPtr("auth")})
	}
	otherNode := uuid.New()
	for i := 0; i < 3; i++ {
		events = append(events, incidentEvent(otherNode, account0, "other-"+testDecimal(i), "auth", now.Add(-time.Duration(i+1)*time.Minute)))
	}
	for i := 0; i < 3; i++ {
		events = append(events, incidentEvent(fixture.lifecycle.instanceID, "openai:event-only@example.invalid", "event-only-"+testDecimal(i), "auth", now.Add(-time.Duration(i+1)*time.Minute)))
	}
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListAccountQualityIncidents(ctx, productstore.AccountQualityIncidentQuery{InstanceID: fixture.lifecycle.instanceID, Provider: "openai", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 6 || page.HasMore {
		t.Fatalf("incidents=%d more=%v", len(page.Items), page.HasMore)
	}

	wantKeys := []string{account0, account0, account0, account0, "openai:" + accounts[109].email, "openai:" + accounts[2].email}
	wantClasses := []string{"auth", "quota", "rate_limit", "upstream", "auth", "auth"}
	for i, item := range page.Items {
		first, last := now.Add(-3*time.Minute), now.Add(-time.Minute)
		if i == 5 {
			first, last = now.Add(-8*time.Minute), now.Add(-6*time.Minute)
		}
		if item.NodeID != fixture.lifecycle.instanceID || item.AccountKey != wantKeys[i] || item.FailureClass != wantClasses[i] || item.Provider != "openai" || item.Status != "active" || item.HitCount != 3 || !item.FirstSeen.Equal(first) || !item.LastSeen.Equal(last) {
			t.Fatalf("row %d: %+v", i, item)
		}
		if i < 4 {
			if item.LastSuccessAt == nil || !item.LastSuccessAt.Equal(now.Add(-30*time.Second)) {
				t.Fatalf("wrong last success: %+v", item)
			}
		} else if item.LastSuccessAt != nil {
			t.Fatal("success crossed account")
		}
	}
	q := productstore.AccountQualityIncidentQuery{InstanceID: fixture.lifecycle.instanceID, Limit: 2}
	var paged []productstore.AccountQualityIncident
	for count := 0; count < 4; count++ {
		result, err := repo.ListAccountQualityIncidents(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, result.Items...)
		if !result.HasMore {
			break
		}
		if len(result.Items) != 2 {
			t.Fatal("wrong page length")
		}
		last := result.Items[1]
		q.AfterLastSeen = &last.LastSeen
		q.AfterAccountKey = last.AccountKey
		q.AfterFailureClass = last.FailureClass
	}
	if !reflect.DeepEqual(paged, page.Items) {
		t.Fatalf("keyset lost/duplicated/reordered rows: %+v", paged)
	}
	filtered, err := repo.ListAccountQualityIncidents(ctx, productstore.AccountQualityIncidentQuery{InstanceID: fixture.lifecycle.instanceID, FailureClass: "quota", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].FailureClass != "quota" {
		t.Fatalf("filtered=%+v", filtered.Items)
	}
}

func incidentEvent(nodeID uuid.UUID, account, hash, class string, occurred time.Time) requestquality.Event {
	key := account
	return requestquality.Event{EventHash: hash, NodeID: nodeID, Provider: "openai", AccountKey: &key, OccurredAt: occurred, Success: false, FailureClass: &class}
}

func TestAccountQualityIncidentsBoundaryPostgres(t *testing.T) {
	ctx := context.Background()
	f := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "boundary@example.invalid"}})
	statement := fmt.Sprintf(`DO $test$
 DECLARE result record; total integer;
 BEGIN
 INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success,failure_class)
 SELECT 'boundary-'||i,'%s'::uuid,'openai','openai:boundary@example.invalid',statement_timestamp()-interval '15 minutes',false,'auth' FROM generate_series(1,3)i;
 INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success,failure_class)
 SELECT 'expired-'||i,'%s'::uuid,'openai','openai:boundary@example.invalid',statement_timestamp()-interval '15 minutes 1 microsecond',false,'quota' FROM generate_series(1,3)i;
 INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success,failure_class)
 SELECT 'future-'||i,'%s'::uuid,'openai','openai:boundary@example.invalid',statement_timestamp()+interval '1 day',false,'upstream' FROM generate_series(1,3)i;
 INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success)
 VALUES ('success-7d','%s'::uuid,'openai','openai:boundary@example.invalid',statement_timestamp()-interval '7 days',true);
 SELECT count(*) INTO total FROM control_query_node_account_quality_incidents_v1('%s'::uuid,'','',NULL,'','',25);
 IF total<>1 THEN RAISE EXCEPTION 'unexpected incident count %%',total; END IF;
 SELECT * INTO result FROM control_query_node_account_quality_incidents_v1('%s'::uuid,'','',NULL,'','',25);
 IF result.account_key IS DISTINCT FROM 'openai:boundary@example.invalid' OR result.failure_class IS DISTINCT FROM 'auth' OR result.hit_count IS DISTINCT FROM 3::bigint OR result.first_seen IS DISTINCT FROM statement_timestamp()-interval '15 minutes' OR result.last_seen IS DISTINCT FROM statement_timestamp()-interval '15 minutes' OR result.last_success_at IS DISTINCT FROM statement_timestamp()-interval '7 days' THEN RAISE EXCEPTION 'wrong exact boundary incident'; END IF;
 END $test$`, f.lifecycle.instanceID, f.lifecycle.instanceID, f.lifecycle.instanceID, f.lifecycle.instanceID, f.lifecycle.instanceID, f.lifecycle.instanceID)
	if _, err := f.database.owner.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
	repo, err := productstore.NewAccountRequestQualityRepository(f.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListAccountQualityIncidents(ctx, productstore.AccountQualityIncidentQuery{InstanceID: f.lifecycle.instanceID, Limit: 25})
	if err != nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("expired threshold must disappear, never recovered: %+v %v", page, err)
	}
}

func TestAccountQualityIncidentsErrorsPostgres(t *testing.T) {
	ctx := context.Background()
	f := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "empty@example.invalid"}})
	repo, err := productstore.NewAccountRequestQualityRepository(f.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	q := productstore.AccountQualityIncidentQuery{InstanceID: f.lifecycle.instanceID, Limit: 25}
	page, err := repo.ListAccountQualityIncidents(ctx, q)
	if err != nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("empty: %+v %v", page, err)
	}
	for _, bad := range []productstore.AccountQualityIncidentQuery{
		{InstanceID: q.InstanceID, Limit: 0}, {InstanceID: q.InstanceID, Limit: 101}, {InstanceID: q.InstanceID, Limit: 25, FailureClass: "unknown"}, {InstanceID: q.InstanceID, Limit: 25, Provider: "UPPER"}, {InstanceID: q.InstanceID, Limit: 25, AfterAccountKey: "orphan"},
	} {
		if _, err := repo.ListAccountQualityIncidents(ctx, bad); !errors.Is(err, productstore.ErrInvalidAccountInventoryQuery) {
			t.Fatalf("invalid query: %+v %v", bad, err)
		}
	}
	missing := q
	missing.InstanceID = uuid.New()
	if _, err := repo.ListAccountQualityIncidents(ctx, missing); !errors.Is(err, productstore.ErrAccountInventoryInstanceNotFound) {
		t.Fatalf("missing Node %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.ListAccountQualityIncidents(cancelled, q); err == nil {
		t.Fatal("cancel returned empty")
	}
	if _, err := f.database.owner.Exec(ctx, `DELETE FROM node_capabilities WHERE instance_id=$1 AND capability='management_account_inventory_read'`, q.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListAccountQualityIncidents(ctx, q); !errors.Is(err, productstore.ErrAccountInventoryCapabilityUnsupported) {
		t.Fatalf("capability failure returned empty: %v", err)
	}
}
