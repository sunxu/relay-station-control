package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

// ownershipNodeGroup provisions N relay_node_assets sharing one node_type /
// driver_contract_version / active provider policy, so that
// control_finalize_account_inventory_poll_run_with_lifecycle can be driven
// independently per instance_id while all Nodes in the group can carry the
// same account_key (email) to exercise cross-node duplicate ownership.
// Each group uses its own node_type so that a provider-policy-scope change
// (out_of_scope) made against one group never affects a different group's
// Nodes in the same test.
type ownershipNodeGroup struct {
	nodeType string
	contract string
	baseSlot time.Time
	nextPoll map[uuid.UUID]int
	nodes    []uuid.UUID
}

func newOwnershipNodeGroup(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, tag string, nodeCount int,
) *ownershipNodeGroup {
	t.Helper()
	group := &ownershipNodeGroup{nodeType: "ownership-" + tag, contract: "v1", nextPoll: map[uuid.UUID]int{}}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Ownership Test Driver')`, group.nodeType, group.contract); err != nil {
		t.Fatal(err)
	}
	var policyID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES (gen_random_uuid(),$1,$2,ARRAY['openai'],ARRAY['legacy'],'integration-test')
	RETURNING policy_version_id`, group.nodeType, group.contract).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES ($1,$2,$3,'integration-test',clock_timestamp())`,
		group.nodeType, group.contract, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		activated_by,created_at
	) VALUES ($1,$2,$3,clock_timestamp(),'integration-test',CURRENT_TIMESTAMP)`,
		group.nodeType, group.contract, policyID); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT
		date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01')
		- interval '30 minutes'`).Scan(&group.baseSlot); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nodeCount; i++ {
		nodeID := uuid.New()
		if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,
			management_endpoint,reader_secret_ref
		) VALUES ($1,'Ownership Test Node',$2,$3,$4,NULL)`,
			nodeID, group.nodeType, group.contract, "https://node-"+nodeID.String()+".test"); err != nil {
			t.Fatal(err)
		}
		group.nodes = append(group.nodes, nodeID)
	}
	return group
}

// finalize promotes (or, when emails is empty, advances the absence
// transition for) the given instance's account snapshot. degraded only
// affects the health-only projection (account_inventory_provider_states.health_degraded),
// which is decoupled from promotion/lifecycle per design.md Phase 1A.
func (group *ownershipNodeGroup) finalize(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
	instanceID uuid.UUID, emails []string, successCount int64, degraded bool,
) uuid.UUID {
	t.Helper()
	pollID, fence := uuid.New(), uuid.New()
	var policyID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2`, group.nodeType, group.contract).
		Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	scheduledAt := group.baseSlot.Add(time.Duration(group.nextPoll[instanceID]) * 5 * time.Minute)
	group.nextPoll[instanceID]++
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`,
		pollID, instanceID, group.nodeType, group.contract, scheduledAt, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, pollID, fence); err != nil {
		t.Fatal(err)
	}

	// account_inventory_poll_provider_result_consistent requires
	// reason='complete' only when NOT degraded; identity_complete/
	// snapshot_complete stay true either way so per-provider promotion is
	// unaffected (promotion never reads this provider-level reason field).
	reason := "complete"
	if degraded {
		reason = "identity_incomplete"
	}
	providerJSON, err := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "identifiable_count": len(emails),
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": degraded, "reason": reason,
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make([]map[string]any, 0, len(emails))
	for _, email := range emails {
		snapshot = append(snapshot, map[string]any{
			"provider": fixtureProviderName, "account_key": fixtureProviderName + ":" + email,
			"email": email, "basic_status": "active",
			"success_count": successCount, "failed_count": int64(0),
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
		)`, pollID, fence, len(emails), providerJSON, snapshotJSON).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("finalized rows = %d, want 1", finalized)
	}
	return pollID
}

// makeStale rewinds instance_id's account_inventory_provider_states.last_complete_at
// (and source_observed_at) past the frozen 15-minute freshness window, using
// the same disable/UPDATE/enable-trigger pattern as the existing Account
// Inventory readonly-query schema tests.
func makeProviderStateStale(t *testing.T, ctx context.Context, database *isolatedJobDatabase, instanceID uuid.UUID) {
	t.Helper()
	backdateProviderState(t, ctx, database, instanceID, "16 minutes")
}

// backdateProviderState directly rewrites last_complete_at/source_observed_at
// past the frozen 15-minute freshness window, bypassing the guard trigger the
// same way the existing Account Inventory readonly-query schema tests do.
// statement_timestamp() (not clock_timestamp()) is used so both columns
// receive the exact same value within one statement, satisfying
// account_inventory_provider_state_times_ordered. The guard trigger is
// re-enabled via defer so a failing UPDATE never leaves it disabled for
// later subtests sharing the same database.
func backdateProviderState(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, instanceID uuid.UUID, age string,
) {
	t.Helper()
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states
		DISABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states
			ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states
		SET last_complete_at=statement_timestamp()-$2::interval,
		source_observed_at=statement_timestamp()-$2::interval
		WHERE instance_id=$1 AND provider='openai'`, instanceID, age); err != nil {
		t.Fatal(err)
	}
}

func newCrossNodeDuplicateOwnershipRepository(t *testing.T, database *isolatedJobDatabase) *productstore.CrossNodeDuplicateOwnershipRepository {
	t.Helper()
	// relay_control_runtime has EXECUTE on the two SECURITY DEFINER readonly
	// functions (migrations/00014_cross_node_duplicate_ownership_query_access.sql)
	// but no direct SELECT on account_inventory / account_inventory_provider_states.
	// Using the runtime pool here exercises the same path production wiring
	// would use.
	repository, err := productstore.NewCrossNodeDuplicateOwnershipRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestCrossNodeDuplicateOwnershipQuery(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	repository := newCrossNodeDuplicateOwnershipRepository(t, database)

	t.Run("A/B fresh present are both eligible: one duplicate candidate", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "ab", 2)
		accountKey := fixtureProviderName + ":ab-duplicate@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"ab-duplicate@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"ab-duplicate@example.invalid"}, 1, false)

		owners, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertOwnersEqual(t, owners, group.nodes[0], group.nodes[1])

		candidates, err := repository.ListCrossNodeDuplicateCandidates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertHasCandidate(t, candidates, accountKey, group.nodes[0], group.nodes[1])
	})

	t.Run("A fresh + B stale: only A is eligible, no duplicate candidate", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "afreshbstale", 2)
		accountKey := fixtureProviderName + ":a-fresh-b-stale@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"a-fresh-b-stale@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"a-fresh-b-stale@example.invalid"}, 1, false)
		makeProviderStateStale(t, ctx, database, group.nodes[1])

		owners, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertOwnersEqual(t, owners, group.nodes[0])

		candidates, err := repository.ListCrossNodeDuplicateCandidates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertNoCandidate(t, candidates, accountKey)
	})

	t.Run("degraded but fresh/current/present is still eligible", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "degraded", 1)
		accountKey := fixtureProviderName + ":degraded-owner@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"degraded-owner@example.invalid"}, 1, true)

		var healthDegraded bool
		if err := database.owner.QueryRow(ctx, `SELECT health_degraded
			FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='openai'`,
			group.nodes[0]).Scan(&healthDegraded); err != nil {
			t.Fatal(err)
		}
		if !healthDegraded {
			t.Fatal("expected health_degraded=true fixture setup to take effect")
		}

		owners, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertOwnersEqual(t, owners, group.nodes[0])
	})

	t.Run("suspected_missing/missing/out_of_scope are not eligible", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "absencestates", 3)
		suspectedEmail, missingEmail, scopeEmail :=
			"suspected@example.invalid", "missing@example.invalid", "scoped@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{suspectedEmail}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{missingEmail}, 1, false)
		group.finalize(t, ctx, database, group.nodes[2], []string{scopeEmail}, 1, false)

		// suspected_missing: one complete promoted poll without the account.
		group.finalize(t, ctx, database, group.nodes[0], nil, 0, false)
		// missing: two consecutive complete promoted polls without the account.
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
		// out_of_scope: move this group's active provider out of scope.
		var activationID uuid.UUID
		if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
			$1,$2,ARRAY['legacy'],ARRAY['openai'],'integration-test','scope removal',NULL)`,
			group.nodeType, group.contract).Scan(&activationID); err != nil || activationID == uuid.Nil {
			t.Fatalf("move provider out of scope failed: %v", err)
		}

		for _, email := range []string{suspectedEmail, missingEmail, scopeEmail} {
			owners, err := repository.ListEligibleOwnersByAccountKey(ctx, fixtureProviderName+":"+email)
			if err != nil {
				t.Fatal(err)
			}
			if len(owners) != 0 {
				t.Fatalf("email=%s eligible owners=%v, want none", email, owners)
			}
		}
	})

	t.Run("retention-cleared current_poll_run_id does not affect eligibility", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "retention", 1)
		accountKey := fixtureProviderName + ":retention-owner@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"retention-owner@example.invalid"}, 1, false)

		if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states
			SET current_poll_run_id=NULL WHERE instance_id=$1 AND provider='openai'`,
			group.nodes[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `UPDATE account_inventory
			SET current_poll_run_id=NULL WHERE instance_id=$1`, group.nodes[0]); err != nil {
			t.Fatal(err)
		}

		owners, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertOwnersEqual(t, owners, group.nodes[0])
	})

	t.Run("15-minute boundary: fresh and stale rows in the same evaluation are judged consistently", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "boundary", 2)
		accountKey := fixtureProviderName + ":boundary-owner@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"boundary-owner@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"boundary-owner@example.invalid"}, 1, false)

		backdateProviderState(t, ctx, database, group.nodes[0], "14 minutes")
		backdateProviderState(t, ctx, database, group.nodes[1], "16 minutes")

		owners, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertOwnersEqual(t, owners, group.nodes[0])

		candidates, err := repository.ListCrossNodeDuplicateCandidates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertNoCandidate(t, candidates, accountKey)
	})

	t.Run("3 Nodes same account_key: one candidate with 3 owners", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "three", 3)
		accountKey := fixtureProviderName + ":three-way@example.invalid"
		for _, nodeID := range group.nodes {
			group.finalize(t, ctx, database, nodeID, []string{"three-way@example.invalid"}, 1, false)
		}

		owners, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertOwnersEqual(t, owners, group.nodes[0], group.nodes[1], group.nodes[2])

		candidates, err := repository.ListCrossNodeDuplicateCandidates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertHasCandidate(t, candidates, accountKey, group.nodes[0], group.nodes[1], group.nodes[2])
	})

	t.Run("Gateway Directory/Binding mutation does not affect ownership query results", func(t *testing.T) {
		group := newOwnershipNodeGroup(t, ctx, database, "gwbinding", 2)
		accountKey := fixtureProviderName + ":gw-binding@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"gw-binding@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"gw-binding@example.invalid"}, 1, false)

		before, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		gatewayID := uuid.New()
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "https://ownership-gw-binding.test", "file://ownership-gw-binding-reader")
		var snapshotID uuid.UUID
		if err := database.owner.QueryRow(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count
		) VALUES (gen_random_uuid(),$1,$2,1,1) RETURNING snapshot_id`,
			gatewayID, make([]byte, 32)).Scan(&snapshotID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1,901,'Account','linux','apikey',NULL,'active')`, snapshotID); err != nil {
			t.Fatal(err)
		}
		var adminID uuid.UUID
		if err := database.owner.QueryRow(ctx, `INSERT INTO control_admin_users(
			admin_id, login_name, display_name
		) VALUES (gen_random_uuid(),'ownership_gw_admin','Ownership GW Admin') RETURNING admin_id`).
			Scan(&adminID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_gateway_account_bindings(
			relay_node_id, gateway_instance_id, gateway_account_id,
			evidence_snapshot_id, bound_by, bind_reason
		) VALUES ($1,$2,901,$3,$4,'administrator_bind')`,
			group.nodes[0], gatewayID, snapshotID, adminID); err != nil {
			t.Fatal(err)
		}

		after, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if !uuidSlicesEqual(before, after) {
			t.Fatalf("Gateway Binding mutation changed ownership query result: before=%v after=%v", before, after)
		}
	})

	t.Run("unknown/nonexistent account_key returns empty", func(t *testing.T) {
		owners, err := repository.ListEligibleOwnersByAccountKey(ctx, fixtureProviderName+":never-existed@example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		if len(owners) != 0 {
			t.Fatalf("owners=%v, want empty for unknown account_key", owners)
		}
	})

	t.Run("canonical account_key input validation", func(t *testing.T) {
		for _, invalid := range []string{
			"", "no-colon-here", ":missing-provider@example.invalid",
			"OPENAI:uppercase-provider@example.invalid", "openai:",
			"openai:Not-Lowercase@example.invalid",
		} {
			_, err := repository.ListEligibleOwnersByAccountKey(ctx, invalid)
			if !errors.Is(err, productstore.ErrInvalidCrossNodeDuplicateOwnershipQuery) {
				t.Fatalf("account_key=%q err=%v, want ErrInvalidCrossNodeDuplicateOwnershipQuery", invalid, err)
			}
		}
	})
}

func assertOwnersEqual(t *testing.T, actual []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	expected := append([]uuid.UUID{}, want...)
	sortUUIDs(expected)
	if !uuidSlicesEqual(actual, expected) {
		t.Fatalf("owners = %v, want %v", actual, expected)
	}
}

func assertHasCandidate(t *testing.T, candidates []productstore.CrossNodeDuplicateOwnershipCandidate, accountKey string, owners ...uuid.UUID) {
	t.Helper()
	expected := append([]uuid.UUID{}, owners...)
	sortUUIDs(expected)
	for _, candidate := range candidates {
		if candidate.AccountKey != accountKey {
			continue
		}
		if !uuidSlicesEqual(candidate.OwnerInstanceIDs, expected) {
			t.Fatalf("candidate %s owners = %v, want %v", accountKey, candidate.OwnerInstanceIDs, expected)
		}
		return
	}
	t.Fatalf("expected a candidate for account_key=%s, got %+v", accountKey, candidates)
}

func assertNoCandidate(t *testing.T, candidates []productstore.CrossNodeDuplicateOwnershipCandidate, accountKey string) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.AccountKey == accountKey {
			t.Fatalf("did not expect a candidate for account_key=%s, got %+v", accountKey, candidate)
		}
	}
}

// TestCrossNodeDuplicateOwnershipQueryRuntimePrivileges proves the intended
// production privilege boundary end to end: relay_control_runtime can EXECUTE
// the two SECURITY DEFINER readonly functions and get correct results, but
// still cannot SELECT the underlying account_inventory* tables directly, and
// PUBLIC/an unauthorized role has EXECUTE on neither function.
func TestCrossNodeDuplicateOwnershipQueryRuntimePrivileges(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	group := newOwnershipNodeGroup(t, ctx, database, "runtimeperm", 2)
	accountKey := fixtureProviderName + ":runtime-perm@example.invalid"
	group.finalize(t, ctx, database, group.nodes[0], []string{"runtime-perm@example.invalid"}, 1, false)
	group.finalize(t, ctx, database, group.nodes[1], []string{"runtime-perm@example.invalid"}, 1, false)

	repository := newCrossNodeDuplicateOwnershipRepository(t, database)
	owners, err := repository.ListEligibleOwnersByAccountKey(ctx, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	assertOwnersEqual(t, owners, group.nodes[0], group.nodes[1])

	candidates, err := repository.ListCrossNodeDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertHasCandidate(t, candidates, accountKey, group.nodes[0], group.nodes[1])

	var databaseError *pgconn.PgError
	if _, err := database.runtime.Exec(ctx, `SELECT 1 FROM account_inventory LIMIT 1`); !errors.As(err, &databaseError) || databaseError.Code != "42501" {
		t.Fatalf("runtime direct SELECT account_inventory SQLSTATE = %v", err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT 1 FROM account_inventory_provider_states LIMIT 1`); !errors.As(err, &databaseError) || databaseError.Code != "42501" {
		t.Fatalf("runtime direct SELECT account_inventory_provider_states SQLSTATE = %v", err)
	}

	type permission struct {
		name      string
		arguments int
		role      string
		allowed   bool
	}
	wanted := []permission{
		{"control_list_eligible_cross_node_owners_v1", 1, "relay_control_runtime", true},
		{"control_list_cross_node_duplicate_candidates_v1", 0, "relay_control_runtime", true},
		{"control_list_eligible_cross_node_owners_v1", 1, "public", false},
		{"control_list_cross_node_duplicate_candidates_v1", 0, "public", false},
		{"control_list_eligible_cross_node_owners_v1", 1, "relay_control_asset_registrar", false},
		{"control_list_cross_node_duplicate_candidates_v1", 0, "relay_control_asset_registrar", false},
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
}

func TestCrossNodeDuplicateOwnershipQueryExplain(t *testing.T) {
	if testing.Short() {
		t.Skip("EXPLAIN evidence test skipped in -short mode")
	}
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	group := newOwnershipNodeGroup(t, ctx, database, "explain", 260)

	// Representative fixture: ~250 single-owner accounts (the common case)
	// plus a handful of duplicate-owner account_keys, to let the planner
	// choose a real plan rather than trivially returning zero/one row.
	for i := 0; i < 250; i++ {
		email := fmt.Sprintf("solo-owner-%03d@example.invalid", i)
		group.finalize(t, ctx, database, group.nodes[i], []string{email}, 1, false)
	}
	duplicateAccountKey := fixtureProviderName + ":explain-duplicate@example.invalid"
	for i := 250; i < 260; i++ {
		group.finalize(t, ctx, database, group.nodes[i], []string{"explain-duplicate@example.invalid"}, 1, false)
	}

	explainOwners := `EXPLAIN (ANALYZE, BUFFERS) WITH evaluation AS (SELECT clock_timestamp() AS database_now)
		SELECT account.instance_id
		FROM account_inventory AS account
		JOIN account_inventory_provider_states AS state
		  ON state.instance_id = account.instance_id AND state.provider = account.provider
		CROSS JOIN evaluation
		WHERE account.account_key = $1
		  AND account.lifecycle = 'present'
		  AND state.state = 'current'
		  AND state.last_complete_at IS NOT NULL
		  AND (evaluation.database_now - state.last_complete_at) <= interval '15 minutes'
		  AND (account.lifecycle = 'out_of_scope') = (state.monitoring_status = 'out_of_scope')
		ORDER BY account.instance_id`
	logExplain(t, ctx, database, explainOwners, duplicateAccountKey)

	explainCandidates := `EXPLAIN (ANALYZE, BUFFERS) WITH evaluation AS (SELECT clock_timestamp() AS database_now)
		SELECT account.account_key AS account_key,
		       array_agg(DISTINCT account.instance_id ORDER BY account.instance_id)::uuid[] AS owner_instance_ids
		FROM account_inventory AS account
		JOIN account_inventory_provider_states AS state
		  ON state.instance_id = account.instance_id AND state.provider = account.provider
		CROSS JOIN evaluation
		WHERE account.lifecycle = 'present'
		  AND state.state = 'current'
		  AND state.last_complete_at IS NOT NULL
		  AND (evaluation.database_now - state.last_complete_at) <= interval '15 minutes'
		  AND (account.lifecycle = 'out_of_scope') = (state.monitoring_status = 'out_of_scope')
		GROUP BY account.account_key
		HAVING count(DISTINCT account.instance_id) >= 2
		ORDER BY account.account_key`
	logExplain(t, ctx, database, explainCandidates)

	repository := newCrossNodeDuplicateOwnershipRepository(t, database)
	candidates, err := repository.ListCrossNodeDuplicateCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertHasCandidate(t, candidates, duplicateAccountKey, group.nodes[250:]...)
}

func logExplain(t *testing.T, ctx context.Context, database *isolatedJobDatabase, query string, args ...any) {
	t.Helper()
	rows, err := database.owner.Query(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	t.Log("EXPLAIN (ANALYZE, BUFFERS):")
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func sortUUIDs(values []uuid.UUID) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1].String() > values[j].String(); j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

func uuidSlicesEqual(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
