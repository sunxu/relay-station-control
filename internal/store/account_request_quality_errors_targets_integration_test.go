package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	"github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountRequestQualityRepositoryErrorsAndTargets(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	repo, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	node := uuid.New()
	account := "openai:user:with:colon"
	failure := "upstream"
	now := time.Now().UTC()
	ms := func(v int64) *int64 { return &v }
	if err := repo.InsertRequestEvents(ctx, []requestquality.Event{
		{EventHash: "edge-success", NodeID: node, Provider: "openai", AccountKey: &account, OccurredAt: now.Add(-2 * time.Minute), DurationMS: ms(100), Success: true},
		{EventHash: "edge-failure", NodeID: node, Provider: "openai", AccountKey: &account, OccurredAt: now.Add(-20 * time.Minute), DurationMS: ms(300), Success: false, FailureClass: &failure},
		{EventHash: "future", NodeID: node, Provider: "openai", AccountKey: &account, OccurredAt: now.Add(2 * time.Minute), DurationMS: ms(999), Success: true},
		{EventHash: "unresolved", NodeID: node, Provider: "openai", OccurredAt: now.Add(-2 * time.Minute), DurationMS: ms(500), Success: false, FailureClass: &failure},
	}); err != nil {
		t.Fatal(err)
	}
	quality, err := repo.AccountQuality(ctx, node, account, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if quality.RequestCount != 2 || quality.SuccessCount != 1 || quality.FailureCount != 1 || quality.LastFailureAt == nil || quality.LastFailureClass == nil || *quality.LastFailureClass != failure {
		t.Fatalf("quality=%+v", quality)
	}
	short, err := repo.AccountQuality(ctx, node, account, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if short.RequestCount != 1 || short.LastSuccessAt == nil || short.LastFailureAt != nil {
		t.Fatalf("15m quality=%+v", short)
	}
	nodeQuality, err := repo.NodeProviderQuality(ctx, node, "openai", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if nodeQuality.RequestCount != 3 || nodeQuality.UnresolvedRequestCount != 1 {
		t.Fatalf("node quality=%+v", nodeQuality)
	}
	if _, err := repo.AccountQuality(ctx, node, account, 2*time.Hour); err == nil {
		t.Fatal("invalid window accepted")
	}

	// Invalid rows fail atomically: the valid sibling is not committed.
	badNode := uuid.New()
	bad := "invalid"
	err = repo.InsertRequestEvents(ctx, []requestquality.Event{
		{EventHash: "atomic-good", NodeID: badNode, Provider: "openai", AccountKey: &account, OccurredAt: now, Success: true},
		{EventHash: "atomic-bad", NodeID: badNode, Provider: "openai", AccountKey: &account, OccurredAt: now, Success: false, FailureClass: &bad},
	})
	if err == nil {
		t.Fatal("invalid mixed batch accepted")
	}
	var count int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM account_request_quality_events WHERE node_id=$1`, badNode).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("atomic batch wrote %d rows", count)
	}

	if _, err := repo.NodeProviderQuality(ctx, node, "openai", time.Hour); err != nil {
		t.Fatal(err)
	}
	db.runtime.Close()
	if _, err := repo.NodeProviderQuality(ctx, node, "openai", time.Hour); err == nil {
		t.Fatal("closed database returned quality")
	}

}

func TestAccountRequestQualityTargetsUseActiveCapabilityTruth(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	repo, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	nodeType, contract := "cliproxyapi", "cliproxyapi.auth-files.v1"
	if _, err := db.owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES($1,$2,'quality target')`, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := db.owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES($1,$2,'management_account_inventory_read')`, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	active, noCapability, inactive := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{active, noCapability, inactive} {
		if _, err := db.owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref) VALUES($1,'quality target',$2,$3,'http://quality.example','docker-secret://quality/reader')`, id, nodeType, contract); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{active, inactive} {
		if _, err := db.owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,$2,$3,'management_account_inventory_read')`, id, nodeType, contract); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{active, noCapability} {
		if _, err := db.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor) VALUES($1,CURRENT_TIMESTAMP,'deployment_enable','quality-test')`, id); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := repo.ListRequestQualityTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].InstanceID != active {
		t.Fatalf("targets=%v", targets)
	}
	if targets[0].ManagementEndpoint != "http://quality.example" || len(targets[0].Capabilities) != 1 {
		t.Fatalf("target projection=%v", targets[0])
	}
	if _, err := db.runtime.Exec(ctx, `SELECT * FROM relay_node_assets`); err == nil {
		t.Fatal("runtime unexpectedly has direct asset access")
	}
}
