package store_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	productstore "github.com/sunxu/relay-station-control/internal/store"
	"testing"
	"time"
)

// Antigravity fixture follows the existing Phase 2 promotion fixture without
// changing its historical OpenAI defaults or the ownership evaluator.
type problemOwnershipNodeGroup struct {
	nodeType string
	contract string
	baseSlot time.Time
	nextPoll map[uuid.UUID]int
	nodes    []uuid.UUID
}

func newProblemOwnershipNodeGroup(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, tag string, nodeCount int,
) *problemOwnershipNodeGroup {
	t.Helper()
	group := &problemOwnershipNodeGroup{nodeType: "ownership-" + tag, contract: "v1", nextPoll: map[uuid.UUID]int{}}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Ownership Test Driver')`, group.nodeType, group.contract); err != nil {
		t.Fatal(err)
	}
	var policyID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES (gen_random_uuid(),$1,$2,ARRAY['antigravity'],ARRAY['legacy'],'integration-test')
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
func (group *problemOwnershipNodeGroup) finalize(
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
		"provider": "antigravity", "identifiable_count": len(emails),
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
			"provider": "antigravity", "account_key": "antigravity" + ":" + email,
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

func TestProblemAccountsDuplicateMembershipPostgres(t *testing.T) {
	ctx := context.Background()
	db := newIsolatedJobDatabase(t)
	env := "problem-membership"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, db, env)
	group := newProblemOwnershipNodeGroup(t, ctx, db, "problems", 3)
	email := "duplicate@example.invalid"
	key := "antigravity:" + email
	for _, node := range group.nodes {
		group.finalize(t, ctx, db, node, []string{email}, 1, false)
	}
	domain := newCrossNodeDuplicateOwnershipLifecycleRepository(t, db)
	created, err := domain.Evaluate(ctx, env, key)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := productstore.NewProblemAccountRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want ...uuid.UUID) {
		t.Helper()
		page, err := reader.ListProblemAccounts(ctx, productstore.ProblemAccountQuery{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var nodes []uuid.UUID
		for _, row := range page.Items {
			if row.AccountKey != key || len(row.Issues) != 1 || row.Issues[0].Type != "CROSS_NODE_DUPLICATE_OWNERSHIP" || row.Issues[0].OccurrenceID != created.OccurrenceID || row.HighestSeverity != "Critical" {
				t.Fatalf("invalid row: %+v", row)
			}
			nodes = append(nodes, row.InstanceID)
		}
		assertNodeSetEqual(t, nodes, want...)
	}
	check(group.nodes...)
	// The same occurrence gives all three rows identical severity, since and
	// email. instance_id must provide the stable final ordering key.
	var pagedNodes []uuid.UUID
	var after *productstore.ProblemAccountCursor
	for i := 0; i < 4; i++ {
		page, err := reader.ListProblemAccounts(ctx, productstore.ProblemAccountQuery{Limit: 1, After: after})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("tie page: %+v", page)
		}
		row := page.Items[0]
		pagedNodes = append(pagedNodes, row.InstanceID)
		if !page.HasMore {
			break
		}
		after = &productstore.ProblemAccountCursor{Severity: row.HighestSeverity, Since: row.OldestActiveSince, Email: row.Email, NodeID: row.InstanceID}
	}
	assertNodeSetEqual(t, pagedNodes, group.nodes...)
	for i := 1; i < len(pagedNodes); i++ {
		if pagedNodes[i-1].String() >= pagedNodes[i].String() {
			t.Fatalf("unstable node tie-break: %v", pagedNodes)
		}
	}
	// Both the domain transition and the read projection use current membership;
	// no notification integration is installed in this slice.
	group.finalize(t, ctx, db, group.nodes[0], nil, 0, false)
	second, err := domain.Evaluate(ctx, env, key)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "ACTIVE" || second.OccurrenceID != created.OccurrenceID {
		t.Fatalf("membership-only transition: %+v", second)
	}
	assertNodeSetEqual(t, second.Removed, group.nodes[0])
	check(group.nodes[1:]...)
	group.finalize(t, ctx, db, group.nodes[1], nil, 0, false)
	third, err := domain.Evaluate(ctx, env, key)
	if err != nil {
		t.Fatal(err)
	}
	if third.Status != "RESOLVED" || third.OccurrenceID != created.OccurrenceID {
		t.Fatalf("resolution: %+v", third)
	}
	check()
	var jobs int
	if err := db.owner.QueryRow(ctx, "SELECT count(*) FROM async_jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("read-only slice unexpectedly enqueued %d jobs", jobs)
	}
}

func TestProblemAccountsDuplicateExitKeepsAvailabilityIssue(t *testing.T) {
	ctx := context.Background()
	db := newIsolatedJobDatabase(t)
	env := "problem-mixed-exit"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, db, env)
	group := newProblemOwnershipNodeGroup(t, ctx, db, "mixed-exit", 3)
	email := "mixed@example.invalid"
	key := "antigravity:" + email
	for _, node := range group.nodes {
		group.finalize(t, ctx, db, node, []string{email}, 1, false)
	}
	domain := newCrossNodeDuplicateOwnershipLifecycleRepository(t, db)
	if _, err := domain.Evaluate(ctx, env, key); err != nil {
		t.Fatal(err)
	}
	// Existing confirmed Availability truth is independent of duplicate absence.
	if _, err := db.owner.Exec(ctx, `INSERT INTO account_availability_occurrences(node_id,account_key,reason,severity,first_seen_at,last_failure_at,confirmed_at) VALUES($1,$2,'token_invalid','Critical',statement_timestamp(),statement_timestamp(),statement_timestamp())`, group.nodes[0], key); err != nil {
		t.Fatal(err)
	}
	group.finalize(t, ctx, db, group.nodes[0], nil, 0, false)
	result, err := domain.Evaluate(ctx, env, key)
	if err != nil || result.Status != "ACTIVE" {
		t.Fatalf("membership removal: %+v %v", result, err)
	}
	reader, err := productstore.NewProblemAccountRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reader.ListProblemAccounts(ctx, productstore.ProblemAccountQuery{Limit: 100})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("mixed page: %+v %v", page, err)
	}
	for _, row := range page.Items {
		if row.InstanceID == group.nodes[0] && (len(row.Issues) != 1 || row.Issues[0].Type != "TOKEN_INVALID") {
			t.Fatalf("A lost Availability truth: %+v", row)
		}
	}
	group.finalize(t, ctx, db, group.nodes[1], nil, 0, false)
	result, err = domain.Evaluate(ctx, env, key)
	if err != nil || result.Status != "RESOLVED" {
		t.Fatalf("resolution: %+v %v", result, err)
	}
	page, err = reader.ListProblemAccounts(ctx, productstore.ProblemAccountQuery{Limit: 100})
	if err != nil || len(page.Items) != 1 || page.Items[0].InstanceID != group.nodes[0] || len(page.Items[0].Issues) != 1 || page.Items[0].Issues[0].Type != "TOKEN_INVALID" {
		t.Fatalf("resolution cleared independent issue: %+v %v", page, err)
	}
}

func TestProblemAccountsDuplicateConservativePostgres(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"stale", "UPDATE account_inventory_provider_states SET last_complete_at=statement_timestamp()-interval '16 minutes',source_observed_at=statement_timestamp()-interval '16 minutes' WHERE instance_id=$1"},
		{"unavailable", "UPDATE account_inventory_provider_states SET health_degraded=true,health_reason='transport_failed' WHERE instance_id=$1"},
		{"unverifiable", "UPDATE account_inventory_provider_states SET monitoring_status='out_of_scope',out_of_scope_since=statement_timestamp() WHERE instance_id=$1"},
		{"degraded", "UPDATE account_inventory_provider_states SET health_degraded=true,health_reason='identity_incomplete' WHERE instance_id=$1"},
		{"incomplete", "UPDATE account_inventory_provider_states SET health_degraded=true,health_reason='contract_invalid' WHERE instance_id=$1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := newIsolatedJobDatabase(t)
			env := "problem-conservative"
			newCrossNodeDuplicateLifecycleEnvironment(t, ctx, db, env)
			group := newProblemOwnershipNodeGroup(t, ctx, db, "conservative", 3)
			email := "conservative@example.invalid"
			key := "antigravity:" + email
			for _, node := range group.nodes {
				group.finalize(t, ctx, db, node, []string{email}, 1, false)
			}
			domain := newCrossNodeDuplicateOwnershipLifecycleRepository(t, db)
			created, err := domain.Evaluate(ctx, env, key)
			if err != nil {
				t.Fatal(err)
			}
			execInventoryOwnerMutation(t, db, tc.sql, group.nodes[0])
			result, err := domain.Evaluate(ctx, env, key)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "ACTIVE" || len(result.Removed) != 0 {
				t.Fatalf("degraded evidence removed membership: %+v", result)
			}
			assertNodeSetEqual(t, result.AffectedNodes, group.nodes...)
			reader, err := productstore.NewProblemAccountRepository(db.runtime)
			if err != nil {
				t.Fatal(err)
			}
			page, err := reader.ListProblemAccounts(ctx, productstore.ProblemAccountQuery{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 3 {
				t.Fatalf("lost confirmed rows: %+v", page)
			}
			for _, row := range page.Items {
				if len(row.Issues) != 1 || row.Issues[0].OccurrenceID != created.OccurrenceID {
					t.Fatalf("wrong issue %+v", row)
				}
			}
		})
	}
}
