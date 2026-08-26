package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type lifecycleSchemaFixture struct {
	instanceID uuid.UUID
	policyID   uuid.UUID
	nodeType   string
	contract   string
	baseSlot   time.Time
	nextPoll   int
}

type lifecycleAccount struct {
	email        string
	successCount int64
}

func newLifecycleSchemaFixture(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
) *lifecycleSchemaFixture {
	t.Helper()
	fixture := &lifecycleSchemaFixture{
		instanceID: uuid.New(), policyID: uuid.New(),
		nodeType: "lifecycle-test", contract: "v1",
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Lifecycle Test Driver')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,
		management_endpoint,reader_secret_ref
	) VALUES ($1,'Lifecycle Test Node',$2,$3,'http://lifecycle.example',
		'docker-secret://synthetic/lifecycle-reader')`, fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES ($1,$2,$3,ARRAY['openai'],ARRAY['legacy'],'integration-test')`,
		fixture.policyID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES ($1,$2,$3,'integration-test',clock_timestamp())`,
		fixture.nodeType, fixture.contract, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		activated_by,created_at
	) VALUES ($1,$2,$3,clock_timestamp(),'integration-test',CURRENT_TIMESTAMP)`,
		fixture.nodeType, fixture.contract, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT
		date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01')
		- interval '30 minutes'`).Scan(&fixture.baseSlot); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *lifecycleSchemaFixture) finalize(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, accounts []lifecycleAccount,
) uuid.UUID {
	t.Helper()
	pollID, fence := uuid.New(), uuid.New()
	var policyID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).
		Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	scheduledAt := fixture.baseSlot.Add(time.Duration(fixture.nextPoll) * 5 * time.Minute)
	fixture.nextPoll++
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`,
		pollID, fixture.instanceID, fixture.nodeType, fixture.contract, scheduledAt, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, pollID, fence); err != nil {
		t.Fatal(err)
	}

	providerJSON, err := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "identifiable_count": len(accounts),
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		snapshot = append(snapshot, map[string]any{
			"provider": fixtureProviderName, "account_key": fixtureProviderName + ":" + account.email,
			"email": account.email, "basic_status": "active",
			"success_count": account.successCount, "failed_count": int64(0),
			"recent_request_count": int64(0), "last_refresh_unix": nil,
			"next_retry_unix": nil, "updated_at_unix": nil,
		})
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			$3,$3,0,0,0,'v1.0.0','abcdef1',$4::jsonb,$5::jsonb,'[]'::jsonb
		)`, pollID, fence, len(accounts), providerJSON, snapshotJSON).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("finalized rows = %d, want 1", finalized)
	}
	return pollID
}

const fixtureProviderName = "openai"

func TestAccountInventoryLifecycleConsecutiveMissingRecoveryAndSaturation(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	first := lifecycleAccount{email: "first@example.invalid", successCount: 11}
	second := lifecycleAccount{email: "second@example.invalid", successCount: 12}
	fixture.finalize(t, ctx, database, []lifecycleAccount{first, second})

	type state struct {
		lifecycle   string
		missing     int
		missingAt   *time.Time
		firstSeen   time.Time
		lastSeen    time.Time
		updatedAt   time.Time
		success     int64
		currentPoll uuid.UUID
	}
	read := func(email string) state {
		t.Helper()
		var result state
		if err := database.owner.QueryRow(ctx, `SELECT lifecycle,consecutive_missing_count,
			missing_since,first_seen_at,last_seen_at,updated_at,success_count,current_poll_run_id
			FROM account_inventory WHERE instance_id=$1 AND account_key=$2`,
			fixture.instanceID, fixtureProviderName+":"+email).Scan(
			&result.lifecycle, &result.missing, &result.missingAt, &result.firstSeen,
			&result.lastSeen, &result.updatedAt, &result.success, &result.currentPoll,
		); err != nil {
			t.Fatal(err)
		}
		return result
	}
	baseline := read(first.email)
	if baseline.lifecycle != "present" || baseline.missing != 0 || baseline.missingAt != nil ||
		baseline.firstSeen != baseline.lastSeen || baseline.success != 11 {
		t.Fatal("initial promotion did not create the present lifecycle baseline")
	}

	fixture.finalize(t, ctx, database, nil)
	suspected := read(first.email)
	if suspected.lifecycle != "suspected_missing" || suspected.missing != 1 ||
		suspected.missingAt != nil || suspected.firstSeen != baseline.firstSeen ||
		suspected.lastSeen != baseline.lastSeen {
		t.Fatal("first complete miss did not create suspected_missing")
	}
	fixture.finalize(t, ctx, database, nil)
	missing := read(first.email)
	if missing.lifecycle != "missing" || missing.missing != 2 || missing.missingAt == nil ||
		missing.firstSeen != baseline.firstSeen || missing.lastSeen != baseline.lastSeen {
		t.Fatal("second complete miss did not create missing")
	}
	fixture.finalize(t, ctx, database, nil)
	saturated := read(first.email)
	if saturated.lifecycle != "missing" || saturated.missing != 2 || saturated.missingAt == nil ||
		!saturated.missingAt.Equal(*missing.missingAt) || saturated.updatedAt != missing.updatedAt {
		t.Fatal("later complete miss did not preserve saturated missing state")
	}

	recoveryPoll := fixture.finalize(t, ctx, database, []lifecycleAccount{{email: first.email, successCount: 21}})
	recovered := read(first.email)
	if recovered.lifecycle != "present" || recovered.missing != 0 || recovered.missingAt != nil ||
		recovered.firstSeen != baseline.firstSeen || !recovered.lastSeen.After(baseline.lastSeen) ||
		recovered.success != 21 || recovered.currentPoll != recoveryPoll {
		t.Fatal("reappearance did not restore present while retaining first_seen")
	}
}

func TestAccountInventoryLifecycleProviderOutOfScopeAndPartialReactivation(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	first := lifecycleAccount{email: "scope-first@example.invalid", successCount: 1}
	second := lifecycleAccount{email: "scope-second@example.invalid", successCount: 2}
	fixture.finalize(t, ctx, database, []lifecycleAccount{first, second})

	var activationID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['legacy'],ARRAY['openai'],'integration-test','planned scope removal',NULL)`,
		fixture.nodeType, fixture.contract).Scan(&activationID); err != nil || activationID == uuid.Nil {
		t.Fatalf("move Provider out of scope failed: %v", err)
	}
	var providerStatus string
	var providerOutAt time.Time
	if err := database.owner.QueryRow(ctx, `SELECT monitoring_status,out_of_scope_since
		FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='openai'`,
		fixture.instanceID).Scan(&providerStatus, &providerOutAt); err != nil {
		t.Fatal(err)
	}
	if providerStatus != "out_of_scope" {
		t.Fatal("Provider monitoring status did not move out of scope")
	}
	var auditActor, auditReason string
	var movedOut, reactivated []string
	var auditTime time.Time
	if err := database.owner.QueryRow(ctx, `SELECT actor,reason,moved_out_providers,
		reactivated_providers,transitioned_at
		FROM account_inventory_scope_transition_audits WHERE activation_id=$1`, activationID).
		Scan(&auditActor, &auditReason, &movedOut, &reactivated, &auditTime); err != nil {
		t.Fatal(err)
	}
	if auditActor != "integration-test" || auditReason != "planned scope removal" ||
		len(movedOut) != 1 || movedOut[0] != "openai" || len(reactivated) != 1 ||
		reactivated[0] != "legacy" || !auditTime.Equal(providerOutAt) {
		t.Fatal("scope transition audit did not match the atomic transition")
	}
	for _, statement := range []string{
		`UPDATE account_inventory_scope_transition_audits SET reason='changed'`,
		`DELETE FROM account_inventory_scope_transition_audits`,
		`TRUNCATE account_inventory_scope_transition_audits`,
	} {
		_, err := database.owner.Exec(ctx, statement)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "42501" {
			t.Fatal("scope transition audit mutation was not rejected")
		}
	}
	rows, err := database.owner.Query(ctx, `SELECT lifecycle,consecutive_missing_count,
		missing_since,out_of_scope_since FROM account_inventory
		WHERE instance_id=$1 ORDER BY account_key`, fixture.instanceID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var lifecycle string
		var missing int
		var missingAt *time.Time
		var outAt time.Time
		if err := rows.Scan(&lifecycle, &missing, &missingAt, &outAt); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if lifecycle != "out_of_scope" || missing != 0 || missingAt != nil || !outAt.Equal(providerOutAt) {
			rows.Close()
			t.Fatal("Provider and account out-of-scope transition was not atomic")
		}
	}
	rows.Close()

	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['openai'],ARRAY['legacy'],'integration-test','scope restoration',NULL)`,
		fixture.nodeType, fixture.contract).Scan(&activationID); err != nil {
		t.Fatal(err)
	}
	var outAt *time.Time
	if err := database.owner.QueryRow(ctx, `SELECT monitoring_status,out_of_scope_since
		FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='openai'`,
		fixture.instanceID).Scan(&providerStatus, &outAt); err != nil {
		t.Fatal(err)
	}
	if providerStatus != "active" || outAt != nil {
		t.Fatal("Provider did not reactivate independently of accounts")
	}
	var stillOut int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory
		WHERE instance_id=$1 AND lifecycle='out_of_scope'`, fixture.instanceID).Scan(&stillOut); err != nil || stillOut != 2 {
		t.Fatalf("reactivation restored accounts prematurely: count=%d err=%v", stillOut, err)
	}

	fixture.finalize(t, ctx, database, []lifecycleAccount{{email: first.email, successCount: 9}})
	var firstLifecycle, secondLifecycle string
	if err := database.owner.QueryRow(ctx, `SELECT lifecycle FROM account_inventory
		WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID,
		fixtureProviderName+":"+first.email).Scan(&firstLifecycle); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT lifecycle FROM account_inventory
		WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID,
		fixtureProviderName+":"+second.email).Scan(&secondLifecycle); err != nil {
		t.Fatal(err)
	}
	if firstLifecycle != "present" || secondLifecycle != "out_of_scope" {
		t.Fatal("partial Provider snapshot did not restore only the appearing account")
	}
}

func TestAccountInventoryLifecycleAllowsOneEmptyProviderPolicySet(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	account := lifecycleAccount{email: "empty-set@example.invalid", successCount: 1}
	fixture.finalize(t, ctx, database, []lifecycleAccount{account})

	var movedOutActivation uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY[]::text[],ARRAY['legacy','openai'],
		'integration-test','all Providers paused',NULL)`, fixture.nodeType, fixture.contract).
		Scan(&movedOutActivation); err != nil {
		t.Fatal(err)
	}
	var activeProviders, outOfScopeProviders, movedOut, reactivated []string
	var providerStatus, lifecycle string
	if err := database.owner.QueryRow(ctx, `SELECT
		policy.active_providers,policy.out_of_scope_providers,
		audit.moved_out_providers,audit.reactivated_providers,
		state.monitoring_status,account.lifecycle
		FROM provider_inventory_policy_activations AS activation
		JOIN provider_inventory_policy_versions AS policy USING (policy_version_id)
		JOIN account_inventory_scope_transition_audits AS audit USING (activation_id)
		JOIN account_inventory_provider_states AS state
		  ON state.instance_id=$2 AND state.provider='openai'
		JOIN account_inventory AS account
		  ON account.instance_id=state.instance_id AND account.provider=state.provider
		WHERE activation.activation_id=$1`, movedOutActivation, fixture.instanceID).Scan(
		&activeProviders, &outOfScopeProviders, &movedOut, &reactivated,
		&providerStatus, &lifecycle,
	); err != nil {
		t.Fatal(err)
	}
	if len(activeProviders) != 0 || len(outOfScopeProviders) != 2 ||
		outOfScopeProviders[0] != "legacy" || outOfScopeProviders[1] != "openai" ||
		len(movedOut) != 1 || movedOut[0] != "openai" || len(reactivated) != 0 ||
		providerStatus != "out_of_scope" || lifecycle != "out_of_scope" {
		t.Fatal("empty active Provider set did not produce a canonical all-out-of-scope transition")
	}

	var readdActivation uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['openai','legacy','openai'],ARRAY[]::text[],
		'integration-test','all Providers restored',NULL)`, fixture.nodeType, fixture.contract).
		Scan(&readdActivation); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT
		policy.active_providers,policy.out_of_scope_providers,
		audit.moved_out_providers,audit.reactivated_providers,
		state.monitoring_status,account.lifecycle
		FROM provider_inventory_policy_activations AS activation
		JOIN provider_inventory_policy_versions AS policy USING (policy_version_id)
		JOIN account_inventory_scope_transition_audits AS audit USING (activation_id)
		JOIN account_inventory_provider_states AS state
		  ON state.instance_id=$2 AND state.provider='openai'
		JOIN account_inventory AS account
		  ON account.instance_id=state.instance_id AND account.provider=state.provider
		WHERE activation.activation_id=$1`, readdActivation, fixture.instanceID).Scan(
		&activeProviders, &outOfScopeProviders, &movedOut, &reactivated,
		&providerStatus, &lifecycle,
	); err != nil {
		t.Fatal(err)
	}
	if len(activeProviders) != 2 || activeProviders[0] != "legacy" || activeProviders[1] != "openai" ||
		len(outOfScopeProviders) != 0 || len(movedOut) != 0 || len(reactivated) != 2 ||
		providerStatus != "active" || lifecycle != "out_of_scope" {
		t.Fatal("empty out-of-scope Provider set did not canonicalize or reactivate atomically")
	}

	for _, providerSets := range []struct {
		active     []string
		outOfScope []string
	}{
		{active: []string{}, outOfScope: []string{}},
		{active: []string{"openai"}, outOfScope: []string{"openai", "legacy"}},
		{active: []string{"OpenAI", "legacy"}, outOfScope: []string{}},
	} {
		if _, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
			$1,$2,$3::text[],$4::text[],'integration-test','invalid Provider set',NULL)`,
			fixture.nodeType, fixture.contract, providerSets.active, providerSets.outOfScope); err == nil {
			t.Fatal("invalid empty, overlapping, or noncanonical Provider set was accepted")
		}
	}
}

func TestAccountInventoryLifecyclePermissionsAndProtectedWrites(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	fixture.finalize(t, ctx, database, []lifecycleAccount{{email: "protected@example.invalid", successCount: 1}})

	for _, statement := range []string{
		`UPDATE account_inventory SET success_count=2 WHERE instance_id=$1`,
		`DELETE FROM account_inventory WHERE instance_id=$1`,
		`TRUNCATE account_inventory`,
	} {
		var err error
		if statement == `TRUNCATE account_inventory` {
			_, err = database.owner.Exec(ctx, statement)
		} else {
			_, err = database.owner.Exec(ctx, statement, fixture.instanceID)
		}
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "42501" {
			t.Fatalf("protected lifecycle statement SQLSTATE = %v", err)
		}
	}

	type permission struct {
		name      string
		arguments int
		role      string
		allowed   bool
	}
	wanted := []permission{
		{"control_finalize_account_inventory_poll_run", 21, "relay_control_runtime", false},
		{"control_finalize_account_inventory_poll_run_with_lifecycle", 21, "relay_control_runtime", true},
		{"control_finalize_account_inventory_poll_run_with_lifecycle", 21, "relay_control_asset_registrar", false},
		{"control_activate_provider_policy", 6, "relay_control_asset_registrar", false},
		{"control_activate_provider_policy_with_lifecycle", 7, "relay_control_asset_registrar", true},
		{"control_activate_provider_policy_with_lifecycle", 7, "relay_control_runtime", false},
	}
	for _, expected := range wanted {
		var allowed bool
		if err := database.owner.QueryRow(ctx, `SELECT has_function_privilege($3,p.oid,'EXECUTE')
			FROM pg_proc AS p JOIN pg_namespace AS n ON n.oid=p.pronamespace
			WHERE n.nspname='public' AND p.proname=$1 AND p.pronargs=$2`,
			expected.name, expected.arguments, expected.role).Scan(&allowed); err != nil {
			t.Fatal(err)
		}
		if allowed != expected.allowed {
			t.Fatalf("%s/%s execute = %t, want %t", expected.name, expected.role, allowed, expected.allowed)
		}
	}
	var auditTablePrivilege bool
	if err := database.owner.QueryRow(ctx, `SELECT bool_or(has_table_privilege(
		role_name,'public.account_inventory_scope_transition_audits',privilege_name
	)) FROM unnest(ARRAY['relay_control_runtime','relay_control_asset_registrar']) AS roles(role_name)
	CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE']) AS privileges(privilege_name)`).
		Scan(&auditTablePrivilege); err != nil {
		t.Fatal(err)
	}
	if auditTablePrivilege {
		t.Fatal("runtime or registrar received direct scope transition audit table privileges")
	}

	var rows int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_list_current_account_inventory_lifecycle($1,'','','',10)`,
		fixture.instanceID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("bounded lifecycle read rows=%d err=%v", rows, err)
	}
	var metricRows int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_list_account_inventory_lifecycle_metrics()`).Scan(&metricRows); err != nil || metricRows != 1 {
		t.Fatalf("lifecycle metric rows=%d err=%v", metricRows, err)
	}

	if _, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['openai'],ARRAY['legacy'],'integration-test','future change',
		clock_timestamp()+interval '1 minute')`,
		fixture.nodeType, fixture.contract); err == nil {
		t.Fatal("future lifecycle-aware activation was accepted")
	}
	for _, reason := range []any{nil, "", " surrounding ", "line\nbreak"} {
		_, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
			$1,$2,ARRAY['openai'],ARRAY['legacy'],'integration-test',$3,NULL)`,
			fixture.nodeType, fixture.contract, reason)
		if err == nil {
			t.Fatal("missing or invalid scope transition reason was accepted")
		}
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err == nil {
		t.Fatal("protected lifecycle migration down accepted nonempty state")
	}
}

func TestAccountInventoryLifecycleMigrationDoesNotBackfillSnapshotHistory(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal("empty lifecycle migration down failed")
	}
	fixture := insertSnapshotPollFixture(t, ctx, database)
	fence := uuid.New()
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '30 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, fixture.pollRunID, fence); err != nil {
		t.Fatal(err)
	}
	email := "pre-lifecycle@example.invalid"
	providerJSON, _ := json.Marshal([]map[string]any{{
		"provider": fixture.provider, "identifiable_count": 1,
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	snapshotJSON, _ := json.Marshal([]map[string]any{{
		"provider": fixture.provider, "account_key": fixture.provider + ":" + email,
		"email": email, "basic_status": "active", "success_count": 0,
		"failed_count": 0, "recent_request_count": 0, "last_refresh_unix": nil,
		"next_retry_unix": nil, "updated_at_unix": nil,
	}})
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			1,1,0,0,0,'v1.0.0','abcdef1',$3::jsonb,$4::jsonb,'[]'::jsonb
		)`, fixture.pollRunID, fence, providerJSON, snapshotJSON).Scan(&finalized); err != nil || finalized != 1 {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal("lifecycle migration up over snapshot history failed")
	}
	var lifecycleRows, snapshotRows int
	var monitoringStatus string
	var promotionApplied bool
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT monitoring_status FROM account_inventory_provider_states
		 WHERE instance_id=$2 AND provider=$3),
		(SELECT promotion_applied FROM account_inventory_poll_provider_results
		 WHERE poll_run_id=$1 AND provider=$3)`, fixture.pollRunID, fixture.instanceID,
		fixture.provider).Scan(&lifecycleRows, &snapshotRows, &monitoringStatus, &promotionApplied); err != nil {
		t.Fatal(err)
	}
	if lifecycleRows != 0 || snapshotRows != 1 || monitoringStatus != "active" || !promotionApplied {
		t.Fatal("lifecycle migration backfilled accounts or changed historical promotion")
	}
}

func TestAccountInventoryLifecycleFailedPromotionAndAtomicRollbackDoNotChangeAccounts(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	account := lifecycleAccount{email: "atomic@example.invalid", successCount: 7}
	baselinePoll := fixture.finalize(t, ctx, database, []lifecycleAccount{account})

	insertRunningAt := func(scheduledAt time.Time) (uuid.UUID, uuid.UUID) {
		t.Helper()
		pollID, fence := uuid.New(), uuid.New()
		var policyID uuid.UUID
		if err := database.owner.QueryRow(ctx, `SELECT policy_version_id
			FROM provider_inventory_policy_bindings
			WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).
			Scan(&policyID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
		) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, pollID,
			fixture.instanceID, fixture.nodeType, fixture.contract, scheduledAt, policyID); err != nil {
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
	insertRunning := func() (uuid.UUID, uuid.UUID) {
		scheduledAt := fixture.baseSlot.Add(time.Duration(fixture.nextPoll) * 5 * time.Minute)
		fixture.nextPoll++
		return insertRunningAt(scheduledAt)
	}

	completeProviders, _ := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "identifiable_count": 1,
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	completeItems, _ := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "account_key": fixtureProviderName + ":" + account.email,
		"email": account.email, "basic_status": "active", "success_count": 99,
		"failed_count": 0, "recent_request_count": 0, "last_refresh_unix": nil,
		"next_retry_unix": nil, "updated_at_unix": nil,
	}})

	stalePoll, staleFence := insertRunningAt(fixture.baseSlot.Add(-5 * time.Minute))
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			1,1,0,0,0,'v1.0.0','abcdef1',$3::jsonb,$4::jsonb,'[]'::jsonb
		)`, stalePoll, staleFence, completeProviders, completeItems).Scan(&finalized); err != nil || finalized != 1 {
		t.Fatal("stale poll did not finalize with a fixed skip result")
	}
	var staleReason string
	if err := database.owner.QueryRow(ctx, `SELECT promotion_skipped_reason
		FROM account_inventory_poll_provider_results WHERE poll_run_id=$1 AND provider=$2`,
		stalePoll, fixtureProviderName).Scan(&staleReason); err != nil || staleReason != "stale_poll" {
		t.Fatal("older poll was not classified stale")
	}

	failedPoll, failedFence := insertRunning()
	failedProviders, _ := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "identifiable_count": 0,
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": false,
		"degraded": true, "reason": "transport_failed",
	}})
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,false,false,false,NULL,false,false,true,'failed','network_unavailable',
			0,0,0,0,0,'unknown','unknown',$3::jsonb,'[]'::jsonb,'[]'::jsonb
		)`, failedPoll, failedFence, failedProviders).Scan(&finalized); err != nil || finalized != 1 {
		t.Fatal("failed observation did not finalize through the controlled function")
	}
	var lifecycle string
	var missing int
	var currentPoll uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT lifecycle,consecutive_missing_count,current_poll_run_id
		FROM account_inventory WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID,
		fixtureProviderName+":"+account.email).Scan(&lifecycle, &missing, &currentPoll); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "present" || missing != 0 || currentPoll != baselinePoll {
		t.Fatal("non-promoted transport failure changed lifecycle")
	}

	transition, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transition.Exec(ctx, `SELECT set_config('relay_control.lifecycle_write','policy',true)`); err != nil {
		_ = transition.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := transition.Exec(ctx, `UPDATE account_inventory_provider_states
		SET monitoring_status='out_of_scope',out_of_scope_since=clock_timestamp(),
		updated_at=clock_timestamp() WHERE instance_id=$1 AND provider=$2`,
		fixture.instanceID, fixtureProviderName); err != nil {
		_ = transition.Rollback(ctx)
		t.Fatal(err)
	}
	if err := transition.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	rollbackPoll, rollbackFence := insertRunning()
	err = database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			1,1,0,0,0,'v1.0.0','abcdef1',$3::jsonb,$4::jsonb,'[]'::jsonb
		)`, rollbackPoll, rollbackFence, completeProviders, completeItems).Scan(&finalized)
	if err == nil {
		t.Fatal("inconsistent Provider monitoring state accepted promotion")
	}
	var status string
	var providerRows, snapshotRows int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, rollbackPoll).
		Scan(&status, &providerRows, &snapshotRows); err != nil {
		t.Fatal(err)
	}
	if status != "running" || providerRows != 0 || snapshotRows != 0 {
		t.Fatal("failed lifecycle promotion left partial poll or snapshot state")
	}
	var pointer uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT current_poll_run_id
		FROM account_inventory_provider_states WHERE instance_id=$1 AND provider=$2`,
		fixture.instanceID, fixtureProviderName).Scan(&pointer); err != nil {
		t.Fatal(err)
	}
	if pointer != baselinePoll {
		t.Fatal("failed lifecycle promotion moved the Provider pointer")
	}
}

func TestAccountInventoryLifecycleMigrationRejectsPreexistingFuturePolicyBinding(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal("empty lifecycle migration down failed")
	}
	nodeType, contract := "future-policy-test", "v1"
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Future Policy Test Driver')`, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	var activationID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy(
		$1,$2,ARRAY['openai'],ARRAY['legacy'],'integration-test',NULL)`, nodeType, contract).
		Scan(&activationID); err != nil {
		t.Fatal(err)
	}
	var futureEffectiveAt time.Time
	if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()+interval '1 second'`).
		Scan(&futureEffectiveAt); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy(
		$1,$2,ARRAY['legacy'],ARRAY['openai'],'integration-test',
		$3)`, nodeType, contract, futureEffectiveAt).Scan(&activationID); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err == nil {
		t.Fatal("lifecycle migration accepted a binding that points at a future policy")
	}
	var appliedVersion int64
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion); err != nil || appliedVersion != 6 {
		t.Fatal("failed lifecycle preflight did not leave schema at version 6")
	}
	var lifecycleTable *string
	if err := database.owner.QueryRow(ctx, `SELECT to_regclass('public.account_inventory')::text`).
		Scan(&lifecycleTable); err != nil || lifecycleTable != nil {
		t.Fatal("failed lifecycle preflight left partial schema")
	}
	if _, err := database.owner.Exec(ctx, `SELECT pg_sleep(
		greatest(0,extract(epoch FROM ($1::timestamptz-clock_timestamp())))+0.05
	)`, futureEffectiveAt); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal("lifecycle migration remained blocked after the scheduled policy became effective")
	}
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion); err != nil || appliedVersion != 7 {
		t.Fatal("lifecycle migration did not apply after the scheduled policy became effective")
	}
}

func TestAccountInventoryLifecycleScopeAuditAloneProtectsDown(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['legacy'],ARRAY['openai'],'integration-test','audit-only transition',NULL)`,
		fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	var lifecycleRows, providerRows, auditRows int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory),
		(SELECT count(*) FROM account_inventory_provider_states),
		(SELECT count(*) FROM account_inventory_scope_transition_audits)`).
		Scan(&lifecycleRows, &providerRows, &auditRows); err != nil {
		t.Fatal(err)
	}
	if lifecycleRows != 0 || providerRows != 0 || auditRows != 1 {
		t.Fatal("audit-only fixture unexpectedly created lifecycle state")
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err == nil {
		t.Fatal("protected lifecycle down accepted a nonempty scope audit")
	}
}

func TestAccountInventoryLifecycleScopeTransitionAuditRollsBackWithState(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	account := lifecycleAccount{email: "scope-rollback@example.invalid", successCount: 1}
	fixture.finalize(t, ctx, database, []lifecycleAccount{account})
	var originalPolicy uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).
		Scan(&originalPolicy); err != nil {
		t.Fatal(err)
	}

	fault, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fault.Exec(ctx, `SELECT set_config('relay_control.lifecycle_write','finalize',true)`); err != nil {
		_ = fault.Rollback(ctx)
		t.Fatal(err)
	}
	var futureObservation time.Time
	if err := fault.QueryRow(ctx, `SELECT clock_timestamp()+interval '1 hour'`).Scan(&futureObservation); err != nil {
		_ = fault.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := fault.Exec(ctx, `UPDATE account_inventory
		SET last_seen_at=$2, source_observed_at=$2, updated_at=$2
		WHERE instance_id=$1`, fixture.instanceID, futureObservation); err != nil {
		_ = fault.Rollback(ctx)
		t.Fatal(err)
	}
	if err := fault.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['legacy'],ARRAY['openai'],'integration-test','rollback injection',NULL)`,
		fixture.nodeType, fixture.contract); err == nil {
		t.Fatal("fault-injected scope transition unexpectedly committed")
	}
	var boundPolicy uuid.UUID
	var auditRows int
	var monitoringStatus, lifecycle string
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT policy_version_id FROM provider_inventory_policy_bindings
		 WHERE node_type=$2 AND driver_contract_version=$3),
		(SELECT count(*) FROM account_inventory_scope_transition_audits),
		(SELECT monitoring_status FROM account_inventory_provider_states
		 WHERE instance_id=$1 AND provider='openai'),
		(SELECT lifecycle FROM account_inventory WHERE instance_id=$1)`,
		fixture.instanceID, fixture.nodeType, fixture.contract).
		Scan(&boundPolicy, &auditRows, &monitoringStatus, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if boundPolicy != originalPolicy || auditRows != 0 || monitoringStatus != "active" || lifecycle != "present" {
		t.Fatal("failed scope transition left policy, audit, Provider, or account changes")
	}
}
