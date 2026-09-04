package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestCrossNodeDuplicateOwnershipEvidenceEvaluationSnapshotRace is the
// Phase 4 review item F regression (design.md §4/§5), and now also exercises
// migrations/00016's defense-in-depth guard directly: it calls
// control_evaluate_cross_node_duplicate_evidence_v1 (migrations/00015/00016)
// with an intentionally-earlier at_time than the Node's actual committed
// last_complete_at/health_scheduled_at -- exactly what the original
// two-statement SelectClockTimestamp-then-evaluate race window would have
// looked like from the function's point of view before the root cause was
// closed by EvaluateCrossNodeDuplicateEvidenceAtDatabaseNow's single-statement
// snapshot (cross_node_duplicate_ownership_lifecycle.go). This test proves
// the function itself still refuses to attribute future source truth to a
// past at_time, independent of how at_time was obtained.
func TestCrossNodeDuplicateOwnershipEvidenceEvaluationSnapshotRace(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	group := newOwnershipNodeGroup(t, ctx, database, "snapshotrace", 1)
	accountKey := fixtureProviderName + ":snapshot-race@example.invalid"
	group.finalize(t, ctx, database, group.nodes[0], []string{"snapshot-race@example.invalid"}, 1, false)

	var lastCompleteAt, healthScheduledAt time.Time
	if err := database.owner.QueryRow(ctx, `SELECT last_complete_at, health_scheduled_at
		FROM account_inventory_provider_states
		WHERE instance_id=$1 AND provider='openai'`, group.nodes[0]).Scan(&lastCompleteAt, &healthScheduledAt); err != nil {
		t.Fatal(err)
	}

	// atTime simulates an evaluation instant captured strictly before this
	// Node's promotion committed -- e.g. the earlier two-statement
	// SelectClockTimestamp ran, then a concurrent poll promotion committed,
	// then control_evaluate_cross_node_duplicate_evidence_v1 executed and
	// saw that now-committed row.
	atTime := lastCompleteAt.Add(-1 * time.Minute)

	rows, err := database.runtime.Query(ctx,
		`SELECT instance_id, observation_kind, source_completed_at, source_scheduled_at
		 FROM public.control_evaluate_cross_node_duplicate_evidence_v1($1, ARRAY[$2]::uuid[], $3)`,
		accountKey, group.nodes[0], atTime)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	sawRow := false
	for rows.Next() {
		sawRow = true
		var instanceID pgtype.UUID
		var observationKind string
		var sourceCompletedAt, sourceScheduledAt time.Time
		if err := rows.Scan(&instanceID, &observationKind, &sourceCompletedAt, &sourceScheduledAt); err != nil {
			t.Fatal(err)
		}
		// The invariant this migration must enforce: evidence produced for
		// a given at_time must never cite a source timestamp from after
		// at_time -- that would mean persisting
		// cross_node_duplicate_occurrence_evidence.source_completed_at >
		// evaluation_at, and would treat post-at_time source truth as if it
		// already existed at at_time.
		if sourceCompletedAt.After(atTime) {
			t.Fatalf("observation_kind=%s source_completed_at=%s is after at_time=%s: "+
				"future source truth must not be attributed to this evaluation instant",
				observationKind, sourceCompletedAt, atTime)
		}
		if sourceScheduledAt.After(atTime) {
			t.Fatalf("observation_kind=%s source_scheduled_at=%s is after at_time=%s: "+
				"future source truth must not be attributed to this evaluation instant",
				observationKind, sourceScheduledAt, atTime)
		}
		if observationKind == "owner_confirmed" {
			t.Fatalf("a Node whose only promotion is from after at_time must not be owner_confirmed "+
				"as of at_time=%s (last_complete_at=%s)", atTime, lastCompleteAt)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Either the row is entirely omitted (treated as "could not be
	// evaluated as of at_time", the same as no provider_state row at all)
	// or it is returned classified degraded -- both are safe; only
	// owner_confirmed/absence_confirmed citing a future timestamp is the
	// prohibited outcome, already checked above.
	_ = sawRow
}

// TestCrossNodeDuplicateOwnershipEvidenceEvaluationPolicyOnlyTimestampGuard
// is Phase 4 Final Review item two/three: last_complete_at and
// health_scheduled_at are held at their original (old) values, but a
// policy-only Provider scope transition (control_activate_provider_policy_with_lifecycle,
// migrations/00007) advances account_inventory_provider_states.updated_at
// *and* account_inventory.updated_at to "now" -- exactly the case the design
// comment warns about ("policy transition 可以推进 monitoring_status/updated_at
// 而不推进 last_complete_at"). migrations/00016's WHERE clause must
// independently bound state.updated_at and account.updated_at by at_time,
// or this Node's degraded observation would persist source_completed_at
// (the state.updated_at proxy) strictly after at_time.
func TestCrossNodeDuplicateOwnershipEvidenceEvaluationPolicyOnlyTimestampGuard(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	group := newOwnershipNodeGroup(t, ctx, database, "policyonly", 1)
	accountKey := fixtureProviderName + ":policy-only@example.invalid"
	group.finalize(t, ctx, database, group.nodes[0], []string{"policy-only@example.invalid"}, 1, false)

	var lastCompleteAt, healthScheduledAt time.Time
	if err := database.owner.QueryRow(ctx, `SELECT last_complete_at, health_scheduled_at
		FROM account_inventory_provider_states
		WHERE instance_id=$1 AND provider='openai'`, group.nodes[0]).Scan(&lastCompleteAt, &healthScheduledAt); err != nil {
		t.Fatal(err)
	}

	// atTime is captured right after the promotion above and strictly
	// before the policy-only transition below -- last_complete_at and
	// health_scheduled_at are both <= atTime, satisfying the pre-existing
	// freshness guards.
	var atTime time.Time
	if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()::timestamptz`).Scan(&atTime); err != nil {
		t.Fatal(err)
	}

	// A policy-only transition (no promotion, no new poll run): move
	// 'openai' out of scope for this group's node_type. This advances both
	// account_inventory_provider_states.updated_at and
	// account_inventory.updated_at to "now" (strictly after atTime) while
	// leaving last_complete_at/health_scheduled_at completely untouched.
	var activationID pgtype.UUID
	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY[]::text[],ARRAY['openai','legacy'],'integration-test','policy-only guard test',NULL)`,
		group.nodeType, group.contract).Scan(&activationID); err != nil {
		t.Fatal(err)
	}

	var stateUpdatedAt, accountUpdatedAt time.Time
	if err := database.owner.QueryRow(ctx, `SELECT last_complete_at, health_scheduled_at, updated_at
		FROM account_inventory_provider_states
		WHERE instance_id=$1 AND provider='openai'`, group.nodes[0]).Scan(&lastCompleteAt, &healthScheduledAt, &stateUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT updated_at
		FROM account_inventory WHERE instance_id=$1 AND account_key=$2`,
		group.nodes[0], accountKey).Scan(&accountUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if !lastCompleteAt.Before(atTime) && !lastCompleteAt.Equal(atTime) {
		t.Fatalf("fixture invariant broken: last_complete_at=%s must be <= atTime=%s", lastCompleteAt, atTime)
	}
	if !healthScheduledAt.Before(atTime) && !healthScheduledAt.Equal(atTime) {
		t.Fatalf("fixture invariant broken: health_scheduled_at=%s must be <= atTime=%s", healthScheduledAt, atTime)
	}
	if !stateUpdatedAt.After(atTime) {
		t.Fatalf("fixture invariant broken: state.updated_at=%s must be strictly after atTime=%s", stateUpdatedAt, atTime)
	}
	if !accountUpdatedAt.After(atTime) {
		t.Fatalf("fixture invariant broken: account.updated_at=%s must be strictly after atTime=%s", accountUpdatedAt, atTime)
	}

	rows, err := database.runtime.Query(ctx,
		`SELECT instance_id, observation_kind, source_completed_at, source_scheduled_at
		 FROM public.control_evaluate_cross_node_duplicate_evidence_v1($1, ARRAY[$2]::uuid[], $3)`,
		accountKey, group.nodes[0], atTime)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	rowCount := 0
	for rows.Next() {
		rowCount++
		var instanceID pgtype.UUID
		var observationKind string
		var sourceCompletedAt, sourceScheduledAt time.Time
		if err := rows.Scan(&instanceID, &observationKind, &sourceCompletedAt, &sourceScheduledAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The Node's only fresh source metadata (state.updated_at,
	// account.updated_at) is strictly after atTime: the row must be
	// entirely omitted -- migrations/00016's WHERE clause guard -- and no
	// degraded evidence citing that future updated_at may be returned as
	// if it existed "as of" atTime.
	if rowCount != 0 {
		t.Fatalf("expected the Node's row to be omitted (updated_at is after atTime), got %d row(s)", rowCount)
	}
}
