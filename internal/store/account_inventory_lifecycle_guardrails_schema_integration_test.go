package store_test

import (
	"context"
	"encoding/json"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type lifecycleGuardrailEvidence struct {
	transportSuccess     bool
	responseShapeValid   bool
	contractValid        bool
	inventoryMode        *string
	nodeIdentityComplete bool
	snapshotComplete     bool
	degraded             bool
	result               string
	reason               string
	sourceCount          int
	identifiableCount    int
	unidentifiedCount    int
	unsupportedCount     int
	outOfScopeCount      int
	providerResults      []map[string]any
	snapshotItems        []map[string]any
	duplicateEvidence    []map[string]any
}

type lifecycleGuardrailState struct {
	lifecycle   string
	missing     int
	currentPoll uuid.UUID
	updatedAt   time.Time
}

func lifecycleGuardrailCompleteEmptyEvidence() lifecycleGuardrailEvidence {
	mode := "runtime"
	return lifecycleGuardrailEvidence{
		transportSuccess: true, responseShapeValid: true, contractValid: true,
		inventoryMode: &mode, nodeIdentityComplete: true, snapshotComplete: true,
		result: "success", reason: "none",
		providerResults: []map[string]any{{
			"provider": fixtureProviderName, "identifiable_count": 0,
			"missing_identity_count": 0, "duplicate_identity_count": 0,
			"identity_complete": true, "snapshot_complete": true,
			"degraded": false, "reason": "complete",
		}},
		snapshotItems: []map[string]any{}, duplicateEvidence: []map[string]any{},
	}
}

func insertLifecycleGuardrailPoll(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
	fixture *lifecycleSchemaFixture,
	policyID uuid.UUID,
	expired bool,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	if policyID == uuid.Nil {
		if err := database.owner.QueryRow(ctx, `SELECT policy_version_id
			FROM provider_inventory_policy_bindings
			WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).
			Scan(&policyID); err != nil {
			t.Fatal(err)
		}
	}
	pollID, fence := uuid.New(), uuid.New()
	scheduledAt := fixture.baseSlot.Add(time.Duration(fixture.nextPoll) * 5 * time.Minute)
	fixture.nextPoll++
	if expired {
		if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
			created_at,first_started_at,last_started_at,lease_expires_at,lease_fencing_token
		) VALUES ($1,$2,$3,$4,$5::timestamptz,$6,'running',1,2,299,$5::timestamptz,
			$5::timestamptz+interval '1 second',$5::timestamptz+interval '1 second',
			$5::timestamptz+interval '2 seconds',$7)`, pollID, fixture.instanceID,
			fixture.nodeType, fixture.contract, scheduledAt, policyID, fence); err != nil {
			t.Fatal(err)
		}
		return pollID, fence
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, pollID, fixture.instanceID,
		fixture.nodeType, fixture.contract, scheduledAt, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, pollID, fence); err != nil {
		t.Fatal(err)
	}
	return pollID, fence
}

func finalizeLifecycleGuardrailPoll(
	ctx context.Context,
	database *isolatedJobDatabase,
	pollID uuid.UUID,
	fence uuid.UUID,
	evidence lifecycleGuardrailEvidence,
) (int, error) {
	providers, err := json.Marshal(evidence.providerResults)
	if err != nil {
		return 0, err
	}
	items, err := json.Marshal(evidence.snapshotItems)
	if err != nil {
		return 0, err
	}
	duplicates, err := json.Marshal(evidence.duplicateEvidence)
	if err != nil {
		return 0, err
	}
	var finalized int
	err = database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,
			'v1.0.0','abcdef1',$17::jsonb,$18::jsonb,$19::jsonb
		)`, pollID, fence, evidence.transportSuccess, evidence.responseShapeValid,
		evidence.contractValid, evidence.inventoryMode, evidence.nodeIdentityComplete,
		evidence.snapshotComplete, evidence.degraded, evidence.result, evidence.reason,
		evidence.sourceCount, evidence.identifiableCount, evidence.unidentifiedCount,
		evidence.unsupportedCount, evidence.outOfScopeCount, providers, items, duplicates).
		Scan(&finalized)
	return finalized, err
}

func readLifecycleGuardrailState(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
	fixture *lifecycleSchemaFixture,
	email string,
) lifecycleGuardrailState {
	t.Helper()
	var state lifecycleGuardrailState
	if err := database.owner.QueryRow(ctx, `SELECT lifecycle,consecutive_missing_count,
		current_poll_run_id,updated_at FROM account_inventory
		WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID,
		fixtureProviderName+":"+email).Scan(&state.lifecycle, &state.missing,
		&state.currentPoll, &state.updatedAt); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestAccountInventoryLifecycleActualRolePermissionsAndControlledRead(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	fixture.finalize(t, ctx, database, []lifecycleAccount{{
		email: "permission-guardrail@example.invalid", successCount: 1,
	}})

	registrarURL, err := url.Parse(database.runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	registrarURL.User = url.UserPassword(
		"relay_control_asset_registrar_dev", "relay_control_asset_registrar_dev_only",
	)
	registrar, err := pgx.Connect(ctx, registrarURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrar.Close(context.Background()) })

	roles := []struct {
		name string
		exec func(context.Context, string, ...any) (pgconn.CommandTag, error)
	}{
		{name: "runtime", exec: database.runtime.Exec},
		{name: "registrar", exec: registrar.Exec},
	}
	statements := []struct {
		name string
		sql  string
	}{
		{name: "lifecycle select", sql: `SELECT * FROM account_inventory LIMIT 1`},
		{name: "lifecycle insert", sql: `INSERT INTO account_inventory DEFAULT VALUES`},
		{name: "lifecycle update", sql: `UPDATE account_inventory SET success_count=success_count`},
		{name: "lifecycle delete", sql: `DELETE FROM account_inventory`},
		{name: "lifecycle truncate", sql: `TRUNCATE account_inventory`},
		{name: "audit select", sql: `SELECT * FROM account_inventory_scope_transition_audits LIMIT 1`},
		{name: "audit insert", sql: `INSERT INTO account_inventory_scope_transition_audits DEFAULT VALUES`},
		{name: "audit update", sql: `UPDATE account_inventory_scope_transition_audits SET reason=reason`},
		{name: "audit delete", sql: `DELETE FROM account_inventory_scope_transition_audits`},
		{name: "audit truncate", sql: `TRUNCATE account_inventory_scope_transition_audits`},
	}
	for _, role := range roles {
		for _, statement := range statements {
			_, err := role.exec(ctx, statement.sql)
			requirePostgresCode(t, err, "42501")
		}
	}

	var rows int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_list_current_account_inventory_lifecycle($1,'','','',10)`,
		fixture.instanceID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("runtime controlled lifecycle read rows=%d err=%v", rows, err)
	}
	if err := registrar.QueryRow(ctx, `SELECT count(*)
		FROM public.control_list_current_account_inventory_lifecycle($1,'','','',10)`,
		fixture.instanceID).Scan(&rows); err == nil {
		t.Fatal("unauthorized registrar used the runtime lifecycle read")
	} else {
		requirePostgresCode(t, err, "42501")
	}
}

func TestAccountInventoryLifecycleSkipMatrixPreservesMissingProgress(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	// This matrix uses more than six slots; keep every observation behind the
	// database clock so finalized_at/observed_at remain ordered by construction.
	fixture.baseSlot = fixture.baseSlot.Add(-2 * time.Hour)
	account := lifecycleAccount{email: "skip-matrix@example.invalid", successCount: 7}
	fixture.finalize(t, ctx, database, []lifecycleAccount{account})
	fixture.finalize(t, ctx, database, nil)
	baseline := readLifecycleGuardrailState(t, ctx, database, fixture, account.email)
	if baseline.lifecycle != "suspected_missing" || baseline.missing != 1 {
		t.Fatal("skip matrix baseline is not a partially advanced missing lifecycle")
	}

	runtimeMode, diskMode := "runtime", "disk_fallback"
	cases := []struct {
		name       string
		wantReason string
		evidence   lifecycleGuardrailEvidence
	}{
		{
			name: "contract_invalid", wantReason: "contract_invalid",
			evidence: lifecycleGuardrailEvidence{
				transportSuccess: true, responseShapeValid: true, degraded: true,
				nodeIdentityComplete: true, result: "failed", reason: "contract_invalid",
				providerResults: []map[string]any{{
					"provider": fixtureProviderName, "identifiable_count": 0,
					"missing_identity_count": 0, "duplicate_identity_count": 0,
					"identity_complete": true, "snapshot_complete": false,
					"degraded": true, "reason": "contract_invalid",
				}}, snapshotItems: []map[string]any{}, duplicateEvidence: []map[string]any{},
			},
		},
		{
			name: "disk_fallback", wantReason: "disk_fallback",
			evidence: lifecycleGuardrailEvidence{
				transportSuccess: true, responseShapeValid: true, contractValid: true,
				inventoryMode: &diskMode, nodeIdentityComplete: true, snapshotComplete: true,
				degraded: true, result: "degraded", reason: "none",
				providerResults: []map[string]any{{
					"provider": fixtureProviderName, "identifiable_count": 0,
					"missing_identity_count": 0, "duplicate_identity_count": 0,
					"identity_complete": true, "snapshot_complete": true,
					"degraded": false, "reason": "complete",
				}}, snapshotItems: []map[string]any{}, duplicateEvidence: []map[string]any{},
			},
		},
		{
			name: "identity_incomplete", wantReason: "provider_identity_incomplete",
			evidence: lifecycleGuardrailEvidence{
				transportSuccess: true, responseShapeValid: true, contractValid: true,
				inventoryMode: &runtimeMode, degraded: true, result: "degraded", reason: "none",
				sourceCount: 1, unidentifiedCount: 1,
				providerResults: []map[string]any{{
					"provider": fixtureProviderName, "identifiable_count": 0,
					"missing_identity_count": 0, "duplicate_identity_count": 0,
					"identity_complete": true, "snapshot_complete": false,
					"degraded": true, "reason": "node_identity_incomplete",
				}}, snapshotItems: []map[string]any{}, duplicateEvidence: []map[string]any{},
			},
		},
		{
			name: "duplicate", wantReason: "provider_duplicate",
			evidence: lifecycleGuardrailEvidence{
				transportSuccess: true, responseShapeValid: true, contractValid: true,
				inventoryMode: &runtimeMode, nodeIdentityComplete: true, degraded: true,
				result: "degraded", reason: "none", sourceCount: 2, identifiableCount: 2,
				providerResults: []map[string]any{{
					"provider": fixtureProviderName, "identifiable_count": 2,
					"missing_identity_count": 0, "duplicate_identity_count": 1,
					"identity_complete": false, "snapshot_complete": false,
					"degraded": true, "reason": "identity_incomplete",
				}}, snapshotItems: []map[string]any{}, duplicateEvidence: []map[string]any{{
					"provider":         fixtureProviderName,
					"account_key":      fixtureProviderName + ":duplicate@example.invalid",
					"occurrence_count": 2,
				}},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			pollID, fence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, false)
			finalized, err := finalizeLifecycleGuardrailPoll(ctx, database, pollID, fence, testCase.evidence)
			if err != nil || finalized != 1 {
				t.Fatalf("finalize rows=%d err=%v", finalized, err)
			}
			var applied bool
			var reason string
			if err := database.owner.QueryRow(ctx, `SELECT promotion_applied,promotion_skipped_reason
				FROM account_inventory_poll_provider_results
				WHERE poll_run_id=$1 AND provider=$2`, pollID, fixtureProviderName).
				Scan(&applied, &reason); err != nil {
				t.Fatal(err)
			}
			if applied || reason != testCase.wantReason {
				t.Fatalf("promotion=%t reason=%q, want false/%q", applied, reason, testCase.wantReason)
			}
			if actual := readLifecycleGuardrailState(t, ctx, database, fixture, account.email); actual != baseline {
				t.Fatalf("skipped observation changed lifecycle: got=%+v want=%+v", actual, baseline)
			}
		})
	}

	oldPolicy := fixture.policyID
	policyPoll, policyFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, oldPolicy, false)
	newPolicy := uuid.New()
	transition, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var boundary time.Time
	if err := transition.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&boundary); err != nil {
		_ = transition.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := transition.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES ($1,$2,$3,ARRAY['openai'],ARRAY['legacy','other'],'guardrail-test')`,
		newPolicy, fixture.nodeType, fixture.contract); err != nil {
		_ = transition.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := transition.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_to=$1 WHERE node_type=$2 AND driver_contract_version=$3
		AND effective_to IS NULL`, boundary, fixture.nodeType, fixture.contract); err != nil {
		_ = transition.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := transition.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		activated_by,created_at
	) VALUES ($2,$3,$1,$4,'guardrail-test',$4)`, newPolicy,
		fixture.nodeType, fixture.contract, boundary); err != nil {
		_ = transition.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := transition.Exec(ctx, `UPDATE provider_inventory_policy_bindings
		SET policy_version_id=$1,bound_at=$4 WHERE node_type=$2 AND driver_contract_version=$3`,
		newPolicy, fixture.nodeType, fixture.contract, boundary); err != nil {
		_ = transition.Rollback(ctx)
		t.Fatal(err)
	}
	if err := transition.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	finalized, err := finalizeLifecycleGuardrailPoll(
		ctx, database, policyPoll, policyFence, lifecycleGuardrailCompleteEmptyEvidence(),
	)
	if err != nil || finalized != 1 {
		t.Fatalf("policy-changed finalize rows=%d err=%v", finalized, err)
	}
	var policyReason string
	if err := database.owner.QueryRow(ctx, `SELECT promotion_skipped_reason
		FROM account_inventory_poll_provider_results WHERE poll_run_id=$1 AND provider=$2`,
		policyPoll, fixtureProviderName).Scan(&policyReason); err != nil || policyReason != "policy_changed" {
		t.Fatalf("policy-changed reason=%q err=%v", policyReason, err)
	}
	if actual := readLifecycleGuardrailState(t, ctx, database, fixture, account.email); actual != baseline {
		t.Fatalf("policy change altered lifecycle: got=%+v want=%+v", actual, baseline)
	}

	abandonedPoll, abandonedFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, false)
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='abandoned',lease_expires_at=NULL,lease_fencing_token=NULL,
		abandoned_at=clock_timestamp(),execution_reason='max_attempts_exhausted'
		WHERE poll_run_id=$1`, abandonedPoll); err != nil {
		t.Fatal(err)
	}
	finalized, err = finalizeLifecycleGuardrailPoll(
		ctx, database, abandonedPoll, abandonedFence, lifecycleGuardrailCompleteEmptyEvidence(),
	)
	if err != nil || finalized != 0 {
		t.Fatalf("abandoned finalize rows=%d err=%v", finalized, err)
	}
	if actual := readLifecycleGuardrailState(t, ctx, database, fixture, account.email); actual != baseline {
		t.Fatalf("abandoned observation altered lifecycle: got=%+v want=%+v", actual, baseline)
	}
}

func TestAccountInventoryLifecycleFencingExpiryAndReplayAreIdempotent(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	account := lifecycleAccount{email: "fencing-replay@example.invalid", successCount: 5}
	baselinePoll := fixture.finalize(t, ctx, database, []lifecycleAccount{account})
	baseline := readLifecycleGuardrailState(t, ctx, database, fixture, account.email)
	if baseline.currentPoll != baselinePoll {
		t.Fatal("unexpected fencing baseline")
	}

	firstPoll, currentFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, false)
	finalized, err := finalizeLifecycleGuardrailPoll(
		ctx, database, firstPoll, uuid.New(), lifecycleGuardrailCompleteEmptyEvidence(),
	)
	if err != nil || finalized != 0 {
		t.Fatalf("old fencing token finalize rows=%d err=%v", finalized, err)
	}
	if actual := readLifecycleGuardrailState(t, ctx, database, fixture, account.email); actual != baseline {
		t.Fatal("old fencing token changed lifecycle")
	}
	finalized, err = finalizeLifecycleGuardrailPoll(
		ctx, database, firstPoll, currentFence, lifecycleGuardrailCompleteEmptyEvidence(),
	)
	if err != nil || finalized != 1 {
		t.Fatalf("current fencing token finalize rows=%d err=%v", finalized, err)
	}
	suspected := readLifecycleGuardrailState(t, ctx, database, fixture, account.email)
	if suspected.lifecycle != "suspected_missing" || suspected.missing != 1 || suspected.currentPoll != baselinePoll {
		t.Fatalf("first complete miss state=%+v", suspected)
	}
	for replay := 0; replay < 2; replay++ {
		finalized, err = finalizeLifecycleGuardrailPoll(
			ctx, database, firstPoll, currentFence, lifecycleGuardrailCompleteEmptyEvidence(),
		)
		if err != nil || finalized != 0 {
			t.Fatalf("finalized replay %d rows=%d err=%v", replay, finalized, err)
		}
		if actual := readLifecycleGuardrailState(t, ctx, database, fixture, account.email); actual != suspected {
			t.Fatal("finalized replay advanced lifecycle a second time")
		}
	}

	expiredPoll, expiredFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, true)
	finalized, err = finalizeLifecycleGuardrailPoll(
		ctx, database, expiredPoll, expiredFence, lifecycleGuardrailCompleteEmptyEvidence(),
	)
	if err != nil || finalized != 0 {
		t.Fatalf("expired lease finalize rows=%d err=%v", finalized, err)
	}
	if actual := readLifecycleGuardrailState(t, ctx, database, fixture, account.email); actual != suspected {
		t.Fatal("expired lease advanced lifecycle")
	}

	secondPoll, secondFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, false)
	finalized, err = finalizeLifecycleGuardrailPoll(
		ctx, database, secondPoll, secondFence, lifecycleGuardrailCompleteEmptyEvidence(),
	)
	if err != nil || finalized != 1 {
		t.Fatalf("second complete miss rows=%d err=%v", finalized, err)
	}
	missing := readLifecycleGuardrailState(t, ctx, database, fixture, account.email)
	if missing.lifecycle != "missing" || missing.missing != 2 || missing.currentPoll != baselinePoll {
		t.Fatalf("second complete miss state=%+v", missing)
	}
	finalized, err = finalizeLifecycleGuardrailPoll(
		ctx, database, secondPoll, secondFence, lifecycleGuardrailCompleteEmptyEvidence(),
	)
	if err != nil || finalized != 0 {
		t.Fatalf("duplicate finalization rows=%d err=%v", finalized, err)
	}
	if actual := readLifecycleGuardrailState(t, ctx, database, fixture, account.email); actual != missing {
		t.Fatal("duplicate finalization advanced saturated lifecycle")
	}
}

func TestAccountInventoryLifecycleConcurrentOldAndNewSlotsAvoidDeadlock(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	account := lifecycleAccount{email: "concurrent-slots@example.invalid", successCount: 3}
	fixture.finalize(t, ctx, database, []lifecycleAccount{account})
	oldPoll, oldFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, false)
	newPoll, newFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, false)

	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	type outcome struct {
		pollID    uuid.UUID
		finalized int
		err       error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var workers sync.WaitGroup
	for _, item := range []struct {
		pollID uuid.UUID
		fence  uuid.UUID
	}{{oldPoll, oldFence}, {newPoll, newFence}} {
		item := item
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			finalized, err := finalizeLifecycleGuardrailPoll(
				testCtx, database, item.pollID, item.fence, lifecycleGuardrailCompleteEmptyEvidence(),
			)
			outcomes <- outcome{pollID: item.pollID, finalized: finalized, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(outcomes)
	for result := range outcomes {
		if result.err != nil || result.finalized != 1 {
			t.Fatalf("concurrent finalize poll=%s rows=%d err=%v", result.pollID, result.finalized, result.err)
		}
	}

	var currentPoll uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT current_poll_run_id
		FROM account_inventory_provider_states WHERE instance_id=$1 AND provider=$2`,
		fixture.instanceID, fixtureProviderName).Scan(&currentPoll); err != nil {
		t.Fatal(err)
	}
	if currentPoll != newPoll {
		t.Fatalf("concurrent slots left current poll=%s, want newer %s", currentPoll, newPoll)
	}
	state := readLifecycleGuardrailState(t, ctx, database, fixture, account.email)
	if !((state.lifecycle == "suspected_missing" && state.missing == 1) ||
		(state.lifecycle == "missing" && state.missing == 2)) {
		t.Fatalf("concurrent slots produced invalid lifecycle state=%+v", state)
	}
	var finalizedRuns int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_poll_runs
		WHERE poll_run_id=ANY($1) AND status='finalized'`, []uuid.UUID{oldPoll, newPoll}).
		Scan(&finalizedRuns); err != nil || finalizedRuns != 2 {
		t.Fatalf("concurrent finalized runs=%d err=%v", finalizedRuns, err)
	}
}
