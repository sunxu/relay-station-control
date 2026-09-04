package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

// isolatedCrossNodeDuplicateOwnershipDatabase gives the production-wiring
// acceptance tests their own migrated database, mirroring the
// isolatedRuntimeDatabaseURLs helper in internal/api and isolatedJobDatabase
// in internal/store (neither is exported across package boundaries, so this
// is a minimal cmd/control-local copy of the same pattern).
func isolatedCrossNodeDuplicateOwnershipDatabase(t *testing.T) (owner, runtime *pgxpool.Pool) {
	t.Helper()
	ownerBase := os.Getenv("CONTROL_DATABASE_TEST_URL")
	runtimeBase := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if ownerBase == "" || runtimeBase == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL")
	}
	ownerParsed, err := url.Parse(ownerBase)
	if err != nil || ownerParsed.Scheme == "" {
		t.Fatalf("parse owner database URL: %v", err)
	}
	runtimeParsed, err := url.Parse(runtimeBase)
	if err != nil || runtimeParsed.Scheme == "" {
		t.Fatalf("parse runtime database URL: %v", err)
	}
	admin, err := pgx.Connect(context.Background(), ownerBase)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "control_duplicate_wiring_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err = admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		_ = admin.Close(context.Background())
		t.Fatalf("create isolated database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	ownerParsed.Path = "/" + databaseName
	runtimeParsed.Path = "/" + databaseName

	repositoryRoot := crossNodeDuplicateOwnershipRepositoryRoot(t)
	command := exec.Command("go", "tool", "goose", "-dir", "../migrations", "postgres", ownerParsed.String(), "up")
	command.Dir = filepath.Join(repositoryRoot, "tools")
	if output, migrationErr := command.CombinedOutput(); migrationErr != nil {
		t.Fatalf("migrate isolated database: %v\n%s", migrationErr, output)
	}
	owner, err = pgxpool.New(context.Background(), ownerParsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	runtime, err = pgxpool.New(context.Background(), runtimeParsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return owner, runtime
}

func crossNodeDuplicateOwnershipRepositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for candidate := workingDirectory; ; candidate = filepath.Dir(candidate) {
		if _, statErr := os.Stat(filepath.Join(candidate, "go.mod")); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			t.Fatal("repository root not found")
		}
	}
}

// seedCrossNodeDuplicateOwnershipFixture provisions two Nodes sharing one
// node_type/provider policy plus one environments row, mirroring
// newOwnershipNodeGroup in internal/store closely enough to drive
// control_finalize_account_inventory_poll_run_with_lifecycle, so the
// production reconciliation trigger built in cmd/control can be exercised
// against real Account Inventory truth end to end.
type crossNodeDuplicateOwnershipFixture struct {
	nodeType string
	contract string
	baseSlot time.Time
	nextPoll map[uuid.UUID]int
	nodes    []uuid.UUID
}

func seedCrossNodeDuplicateOwnershipFixture(t *testing.T, ctx context.Context, owner *pgxpool.Pool, environmentID, tag string) *crossNodeDuplicateOwnershipFixture {
	t.Helper()
	fixture := &crossNodeDuplicateOwnershipFixture{nodeType: "ownership-" + tag, contract: "v1", nextPoll: map[uuid.UUID]int{}}
	if _, err := owner.Exec(ctx, `INSERT INTO environments(environment_id, name, environment_type)
		VALUES ($1, 'Cross-node Duplicate Production Wiring Test', 'dev')`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Ownership Production Wiring Driver')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	var policyID uuid.UUID
	if err := owner.QueryRow(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES (gen_random_uuid(),$1,$2,ARRAY['openai'],ARRAY['legacy'],'integration-test')
	RETURNING policy_version_id`, fixture.nodeType, fixture.contract).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES ($1,$2,$3,'integration-test',clock_timestamp())`,
		fixture.nodeType, fixture.contract, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		activated_by,created_at
	) VALUES ($1,$2,$3,clock_timestamp(),'integration-test',CURRENT_TIMESTAMP)`,
		fixture.nodeType, fixture.contract, policyID); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT
		date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01')
		- interval '30 minutes'`).Scan(&fixture.baseSlot); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		nodeID := uuid.New()
		if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,
			management_endpoint,reader_secret_ref
		) VALUES ($1,'Ownership Production Wiring Node',$2,$3,$4,NULL)`,
			nodeID, fixture.nodeType, fixture.contract, "https://node-"+nodeID.String()+".test"); err != nil {
			t.Fatal(err)
		}
		fixture.nodes = append(fixture.nodes, nodeID)
	}
	return fixture
}

func (fixture *crossNodeDuplicateOwnershipFixture) finalize(
	t *testing.T, ctx context.Context, owner, runtime *pgxpool.Pool, instanceID uuid.UUID, emails []string,
) {
	t.Helper()
	pollID, fence := uuid.New(), uuid.New()
	var policyID uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).
		Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	scheduledAt := fixture.baseSlot.Add(time.Duration(fixture.nextPoll[instanceID]) * 5 * time.Minute)
	fixture.nextPoll[instanceID]++
	if _, err := owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`,
		pollID, instanceID, fixture.nodeType, fixture.contract, scheduledAt, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, pollID, fence); err != nil {
		t.Fatal(err)
	}
	providerJSON, err := json.Marshal([]map[string]any{{
		"provider": "openai", "identifiable_count": len(emails),
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make([]map[string]any, 0, len(emails))
	for _, email := range emails {
		snapshot = append(snapshot, map[string]any{
			"provider": "openai", "account_key": "openai:" + email,
			"email": email, "basic_status": "active",
			"success_count": int64(1), "failed_count": int64(0),
			"recent_request_count": int64(0), "last_refresh_unix": nil,
			"next_retry_unix": nil, "updated_at_unix": nil,
		})
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var finalized int
	if err := runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			$3,$3,0,0,0,'v1.0.0','abcdef1',$4::jsonb,$5::jsonb,'[]'::jsonb
		)`, pollID, fence, len(emails), providerJSON, snapshotJSON).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("finalized rows = %d, want 1", finalized)
	}
}

// recordingSlogHandler is a minimal slog.Handler double that keeps every
// emitted record's attributes, so tests can prove the production alert
// observer actually reached the logger (i.e. is not the default noop)
// without asserting on exact log text.
type recordingSlogHandler struct {
	records *[]map[string]string
}

func (handler recordingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler recordingSlogHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := map[string]string{"msg": record.Message}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.String()
		return true
	})
	*handler.records = append(*handler.records, attrs)
	return nil
}

func (handler recordingSlogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return handler }
func (handler recordingSlogHandler) WithGroup(_ string) slog.Handler      { return handler }

// TestCrossNodeDuplicateOwnershipProductionWiring covers the Final Overall
// Review's P1 acceptance criteria: the production reconciliation trigger
// (newCrossNodeDuplicateOwnershipReconciliationTrigger, the exact closure
// wired into inventorypoll.Config.LifecycleObserver in main()) and the
// concrete production alert observer (crossNodeDuplicateOwnershipSlogAlertObserver,
// the exact type wired via SetAlertObserver in main()) both actually reach
// the database/logger -- proving neither is dead code and neither is the
// default no-op.
func TestCrossNodeDuplicateOwnershipProductionWiring(t *testing.T) {
	ctx := context.Background()
	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	environmentID := "duplicate-wiring"
	fixture := seedCrossNodeDuplicateOwnershipFixture(t, ctx, owner, environmentID, "wiring")

	reader, err := assetstore.NewCrossNodeDuplicateOwnershipRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := assetstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]string
	testLogger := slog.New(recordingSlogHandler{records: &records})
	lifecycle.SetAlertObserver(crossNodeDuplicateOwnershipSlogAlertObserver{logger: testLogger})
	reconciler, err := assetstore.NewCrossNodeDuplicateOwnershipReconciler(runtime, reader, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	trigger := newCrossNodeDuplicateOwnershipReconciliationTrigger(reconciler, environmentID, testLogger)

	// Restart/startup path: Account Inventory truth already contains a
	// duplicate before the production trigger ever runs (as it would after
	// a Control restart with pre-existing database state).
	fixture.finalize(t, ctx, owner, runtime, fixture.nodes[0], []string{"wiring@example.invalid"})
	fixture.finalize(t, ctx, owner, runtime, fixture.nodes[1], []string{"wiring@example.invalid"})

	trigger(ctx)

	var occurrenceID uuid.UUID
	var status string
	if err := owner.QueryRow(ctx, `SELECT occurrence_id, status FROM cross_node_duplicate_occurrences
		WHERE environment_id=$1 AND account_key='openai:wiring@example.invalid'`, environmentID).
		Scan(&occurrenceID, &status); err != nil {
		t.Fatalf("expected the production trigger to create an occurrence: %v", err)
	}
	if status != "ACTIVE" {
		t.Fatalf("status = %s, want ACTIVE", status)
	}
	if len(records) != 1 || records[0]["transition"] != "active" || records[0]["severity"] != "Critical" ||
		records[0]["occurrence_id"] != occurrenceID.String() || records[0]["account_key"] != "openai:wiring@example.invalid" {
		t.Fatalf("alert records after create = %+v, want exactly one active record for %s", records, occurrenceID)
	}

	// Ongoing reconciliation: the production trigger runs again against
	// unchanged truth (as it would after every subsequent successfully
	// finalized poll run) -- this must not duplicate the occurrence or the
	// alert.
	trigger(ctx)
	var occurrenceCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrences
		WHERE environment_id=$1 AND account_key='openai:wiring@example.invalid'`, environmentID).Scan(&occurrenceCount); err != nil {
		t.Fatal(err)
	}
	if occurrenceCount != 1 {
		t.Fatalf("occurrence count after unchanged-truth refresh = %d, want 1", occurrenceCount)
	}
	if len(records) != 1 {
		t.Fatalf("alert records after unchanged-truth refresh = %+v, want still exactly one (no alert-per-refresh)", records)
	}

	// Subsequent truth change: Node B's next fresh+complete promoted
	// snapshot no longer contains the account -- the next existing
	// Inventory reconciliation cycle (i.e. the next production trigger
	// call) must resolve the occurrence and fire exactly one resolved
	// alert.
	fixture.finalize(t, ctx, owner, runtime, fixture.nodes[1], nil)
	trigger(ctx)

	if err := owner.QueryRow(ctx, `SELECT status FROM cross_node_duplicate_occurrences
		WHERE occurrence_id=$1`, occurrenceID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "RESOLVED" {
		t.Fatalf("status after subsequent truth change = %s, want RESOLVED", status)
	}
	if len(records) != 2 || records[1]["transition"] != "resolved" || records[1]["occurrence_id"] != occurrenceID.String() {
		t.Fatalf("alert records after resolve = %+v, want a second resolved record for %s", records, occurrenceID)
	}
}

// backdateCrossNodeDuplicateOwnershipProviderState rewinds instance_id's
// account_inventory_provider_states.last_complete_at/source_observed_at past
// the frozen 15-minute freshness window, bypassing the guard trigger the
// same way internal/store's cross-node duplicate ownership integration
// tests do (see internal/store/cross_node_duplicate_ownership_query_schema_integration_test.go
// backdateProviderState), so a "purely time-elapsed, no new finalize"
// staleness transition can be constructed deterministically.
func backdateCrossNodeDuplicateOwnershipProviderState(t *testing.T, ctx context.Context, owner *pgxpool.Pool, instanceID uuid.UUID) {
	t.Helper()
	if _, err := owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states
		DISABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states
			ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := owner.Exec(ctx, `UPDATE account_inventory_provider_states
		SET last_complete_at=statement_timestamp()-interval '16 minutes',
		source_observed_at=statement_timestamp()-interval '16 minutes'
		WHERE instance_id=$1 AND provider='openai'`, instanceID); err != nil {
		t.Fatal(err)
	}
}

// TestCrossNodeDuplicateOwnershipProductionTriggerCatchesTimeOnlyStaleness
// covers acceptance D: the production trigger must react to duplicate owner
// freshness/staleness purely evolving with wall-clock time -- no new
// FinalizeFenced call at all -- because it is now installed as
// inventorypoll's periodic Reconciler.ReconcileOnce success hook (see
// reconciler.go), not only the low-latency post-finalize hook.
func TestCrossNodeDuplicateOwnershipProductionTriggerCatchesTimeOnlyStaleness(t *testing.T) {
	ctx := context.Background()
	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	environmentID := "duplicate-time-stale"
	fixture := seedCrossNodeDuplicateOwnershipFixture(t, ctx, owner, environmentID, "timestale")

	reader, err := assetstore.NewCrossNodeDuplicateOwnershipRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := assetstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	testLogger := slog.New(recordingSlogHandler{records: &[]map[string]string{}})
	reconciler, err := assetstore.NewCrossNodeDuplicateOwnershipReconciler(runtime, reader, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	trigger := newCrossNodeDuplicateOwnershipReconciliationTrigger(reconciler, environmentID, testLogger)

	fixture.finalize(t, ctx, owner, runtime, fixture.nodes[0], []string{"timestale@example.invalid"})
	fixture.finalize(t, ctx, owner, runtime, fixture.nodes[1], []string{"timestale@example.invalid"})
	trigger(ctx)

	var occurrenceID uuid.UUID
	var status string
	if err := owner.QueryRow(ctx, `SELECT occurrence_id, status FROM cross_node_duplicate_occurrences
		WHERE environment_id=$1 AND account_key='openai:timestale@example.invalid'`, environmentID).
		Scan(&occurrenceID, &status); err != nil {
		t.Fatalf("expected the production trigger to create an occurrence: %v", err)
	}
	if status != "ACTIVE" {
		t.Fatalf("status = %s, want ACTIVE", status)
	}

	// No new finalize at all -- only wall-clock time elapsing past Node B's
	// freshness window. A real production trigger call (simulating the
	// existing Inventory poll ReconcileOnce success path) must degrade the
	// occurrence, not resolve it, and must not lose membership.
	backdateCrossNodeDuplicateOwnershipProviderState(t, ctx, owner, fixture.nodes[1])
	trigger(ctx)

	var evidenceState string
	if err := owner.QueryRow(ctx, `SELECT status, evidence_state FROM cross_node_duplicate_occurrences
		WHERE occurrence_id=$1`, occurrenceID).Scan(&status, &evidenceState); err != nil {
		t.Fatal(err)
	}
	if status != "ACTIVE" || evidenceState != "degraded" {
		t.Fatalf("status/evidence_state after time-only staleness = %s/%s, want ACTIVE/degraded", status, evidenceState)
	}
	var memberCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrence_nodes
		WHERE occurrence_id=$1`, occurrenceID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if memberCount != 2 {
		t.Fatalf("membership count after time-only staleness = %d, want 2 (A/B must both remain)", memberCount)
	}
}

// TestCrossNodeDuplicateOwnershipTriggerContextRespectsParentCancellation
// covers the P2 fix: crossNodeDuplicateOwnershipTriggerContext (the exact
// context derivation newCrossNodeDuplicateOwnershipReconciliationTrigger
// uses to bound each Reconcile call) must stay attached to parent
// (context.WithTimeout(parent, ...)), not detached via
// context.WithoutCancel(parent, ...). This is proven deterministically by
// canceling parent and observing the derived context's Done channel fire
// immediately with context.Canceled, with no sleep-based timing assertion
// and no dependency on any real reconciliation work.
func TestCrossNodeDuplicateOwnershipTriggerContextRespectsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	derived, cancelDerived := crossNodeDuplicateOwnershipTriggerContext(parent)
	defer cancelDerived()

	cancelParent()

	select {
	case <-derived.Done():
	case <-time.After(time.Second):
		t.Fatal("derived context did not observe parent cancellation")
	}
	if !errors.Is(derived.Err(), context.Canceled) {
		t.Fatalf("derived context error = %v, want context.Canceled", derived.Err())
	}
}
