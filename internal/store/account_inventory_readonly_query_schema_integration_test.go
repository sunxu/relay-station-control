package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountInventoryReadonlyQueryMigrationEmptyDownUpRestoresCompatibility(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t, "up-to", "9")
	repository, err := productstore.NewAccountInventoryRepository(database.runtime)
	if err != nil {
		t.Fatal("create readonly query repository")
	}
	if err := repository.CheckCompatibility(ctx); err != nil {
		t.Fatal("readonly query compatibility is unavailable before protected down")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 9)

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal("prepare isolated Migration 8 database")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 8)

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal("empty readonly query Migration 8 down failed")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 7)
	if err := repository.CheckCompatibility(ctx); !errors.Is(err, productstore.ErrAccountInventoryInconsistent) {
		t.Fatal("readonly query compatibility remained available after Migration 8 down")
	}

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-to", "9"); err != nil {
		t.Fatal("readonly query Migration 8 up after protected down failed")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 9)
	if err := repository.CheckCompatibility(ctx); err != nil {
		t.Fatal("readonly query compatibility did not recover after Migration 8 up")
	}
}

func requireAccountInventoryReadonlyQueryMigrationVersion(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, want int64,
) {
	t.Helper()
	var version int64
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id)
		FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != want {
		t.Fatalf("readonly query Migration version = %d, want %d", version, want)
	}
}

func TestAccountInventoryReadonlyQueryStoreAndPermissionMatrix(t *testing.T) {
	ctx := context.Background()
	// Preserve this historical rollback fixture before the forward-only 00028 boundary.
	database := newIsolatedJobDatabase(t, "up-to", "27")
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES ($1,$2,'management_account_inventory_read')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES ($1,$2,$3,'management_account_inventory_read')`,
		fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(
		admin_id,login_name,display_name
	) VALUES ($1,$2,'Account Inventory Operator')`, actorID, "inventory_"+assetFixtureSuffix(t)); err != nil {
		t.Fatal(err)
	}
	fixture.finalize(t, ctx, database, []lifecycleAccount{
		{email: "a@example.invalid", successCount: 1},
		{email: "b@example.invalid", successCount: 2},
		{email: "c@example.invalid", successCount: 3},
	})

	repository, err := productstore.NewAccountInventoryRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CheckCompatibility(ctx); err != nil {
		t.Fatalf("compatibility: %v", err)
	}
	fingerprint := sha256.Sum256([]byte("account-inventory-store-integration"))
	audit := productstore.AccountInventoryViewAudit{
		ActorAdminID: actorID, SourceFingerprint: fingerprint[:], RequestID: "inventory-query-test",
	}
	first, err := repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: fixture.instanceID, Limit: 2,
	}, audit)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Items) != 2 || first.ContinuationAccountKey != "openai:b@example.invalid" ||
		first.Items[0].Email != "a@example.invalid" ||
		first.Items[0].BasicStatus != productstore.AccountInventoryBasicStatusReportedActive ||
		first.Items[0].SnapshotFreshness != productstore.AccountInventorySnapshotFreshnessFresh {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: fixture.instanceID, AfterAccountKey: first.ContinuationAccountKey, Limit: 2,
	}, audit)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Items) != 1 || second.ContinuationAccountKey != "" ||
		second.Items[0].Email != "c@example.invalid" {
		t.Fatalf("unexpected second page: %+v", second)
	}
	filtered, err := repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: fixture.instanceID, Limit: 100,
		Filters: productstore.AccountInventoryQueryFilters{
			Provider: "openai", Lifecycle: productstore.AccountInventoryPresent,
			BasicStatus: productstore.AccountInventoryBasicStatusReportedActive,
			Email:       "b@example.invalid",
		},
	}, audit)
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Email != "b@example.invalid" {
		t.Fatalf("combined filter: page=%+v err=%v", filtered, err)
	}
	var currentPollID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT current_poll_run_id
		FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='openai'`,
		fixture.instanceID).Scan(&currentPollID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		DISABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_provider_results
		SET degraded=true,reason='disk_fallback'
		WHERE poll_run_id=$1 AND provider='openai'`, currentPollID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	degraded, err := repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: fixture.instanceID, Limit: 10,
	}, audit)
	if err != nil || len(degraded.Items) != 3 || degraded.Items[0].ProviderDegraded ||
		degraded.Items[0].SnapshotFreshness != productstore.AccountInventorySnapshotFreshnessFresh {
		t.Fatalf("historical source mutation changed denormalized health: page=%+v err=%v", degraded, err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states
		DISABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states
		SET last_complete_at=statement_timestamp()-interval '16 minutes',
		source_observed_at=statement_timestamp()-interval '16 minutes'
		WHERE instance_id=$1 AND provider='openai'`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states
		ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	stale, err := repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: fixture.instanceID, Limit: 10,
	}, audit)
	if err != nil || len(stale.Items) != 3 || stale.Items[0].ProviderDegraded ||
		stale.Items[0].SnapshotFreshness != productstore.AccountInventorySnapshotFreshnessStale {
		t.Fatalf("stale denormalized health projection: page=%+v err=%v", stale, err)
	}
	// Restore the immutable source fixture before exercising Migration 9
	// down/up. Otherwise its safe backfill would intentionally import this
	// owner-only corruption and obscure the current-source retention check.
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		DISABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_provider_results
		SET degraded=false,reason='complete'
		WHERE poll_run_id=$1 AND provider='openai'`, currentPollID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}

	var auditCount int
	var detailsJSON []byte
	if err := database.owner.QueryRow(ctx, `SELECT count(*),
		((array_agg(details ORDER BY occurred_at DESC))[1])::text::bytea
		FROM audit_logs WHERE category='account_inventory' AND action='account_inventory.view'
		AND actor_admin_id=$1`, actorID).Scan(&auditCount, &detailsJSON); err != nil {
		t.Fatal(err)
	}
	if auditCount != 5 {
		t.Fatalf("view audit count=%d, want 5", auditCount)
	}
	var details map[string]any
	if err := json.Unmarshal(detailsJSON, &details); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"email", "account_key", "cursor", "filter_hash"} {
		if _, present := details[forbidden]; present {
			t.Fatalf("audit contains forbidden key %q", forbidden)
		}
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down-to", "7"); err == nil {
		t.Fatal("protected down removed a query schema with immutable view audits")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 8)
	if err := repository.CheckCompatibility(ctx); err != nil {
		t.Fatalf("failed protected down changed compatibility: %v", err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal("restore Migration 9 after protected Migration 8 down")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 9)

	if _, err := database.runtime.Exec(ctx, `SELECT * FROM account_inventory LIMIT 1`); err == nil {
		t.Fatal("runtime role directly selected account inventory")
	}
	if _, err := database.runtime.Exec(ctx, `UPDATE account_inventory SET lifecycle=lifecycle`); err == nil {
		t.Fatal("runtime role directly updated account inventory")
	}

	_, err = repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: uuid.New(), Limit: 10,
	}, audit)
	if !errors.Is(err, productstore.ErrAccountInventoryInstanceNotFound) {
		t.Fatalf("unknown instance error=%v", err)
	}
	unsupportedID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,
		management_endpoint,reader_secret_ref
	) VALUES ($1,'Unsupported Inventory Node',$2,$3,
		'http://unsupported-inventory.example','docker-secret://synthetic/unsupported-reader')`,
		unsupportedID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	_, err = repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: unsupportedID, Limit: 10,
	}, audit)
	if !errors.Is(err, productstore.ErrAccountInventoryCapabilityUnsupported) {
		t.Fatalf("unsupported instance error=%v", err)
	}

	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states
		SET current_poll_run_id=NULL WHERE instance_id=$1 AND provider='openai'`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory
		SET current_poll_run_id=NULL WHERE instance_id=$1`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	retained, err := repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: fixture.instanceID, Limit: 10,
	}, audit)
	if err != nil || len(retained.Items) != 3 || retained.Items[0].ProviderDegraded ||
		retained.Items[0].SnapshotFreshness != productstore.AccountInventorySnapshotFreshnessStale {
		t.Fatalf("retention-cleared source changed current projection: page=%+v err=%v", retained, err)
	}
}
