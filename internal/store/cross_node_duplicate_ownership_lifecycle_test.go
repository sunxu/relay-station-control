package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

// newCrossNodeDuplicateLifecycleEnvironment inserts a minimal environments
// row so cross_node_duplicate_occurrences.environment_id (FK to
// environments.environment_id, migrations/00013) can be populated. Node
// groups (with their own provider policy) are provisioned separately via
// newOwnershipNodeGroup, exactly as the Phase 2 query tests do.
func newCrossNodeDuplicateLifecycleEnvironment(t *testing.T, ctx context.Context, database *isolatedJobDatabase, environmentID string) {
	t.Helper()
	if _, err := database.owner.Exec(ctx, `INSERT INTO environments(
		environment_id, name, environment_type
	) VALUES ($1, 'Cross-node Duplicate Lifecycle Test', 'dev')`, environmentID); err != nil {
		t.Fatal(err)
	}
}

func newCrossNodeDuplicateOwnershipLifecycleRepository(t *testing.T, database *isolatedJobDatabase) *productstore.CrossNodeDuplicateOwnershipLifecycleRepository {
	t.Helper()
	// relay_control_runtime already holds every table grant this repository
	// needs (migrations/00013 grants section) plus EXECUTE on the two
	// SECURITY DEFINER readonly functions (migrations/00014, 00015); using
	// the runtime pool here exercises the same path production wiring
	// would use.
	repository, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func occurrenceRowCount(t *testing.T, ctx context.Context, database *isolatedJobDatabase, environmentID, accountKey string) int {
	t.Helper()
	var count int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrences
		WHERE environment_id=$1 AND account_key=$2`, environmentID, accountKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func occurrenceStatus(t *testing.T, ctx context.Context, database *isolatedJobDatabase, occurrenceID uuid.UUID) string {
	t.Helper()
	var status string
	if err := database.owner.QueryRow(ctx, `SELECT status FROM cross_node_duplicate_occurrences
		WHERE occurrence_id=$1`, occurrenceID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func assertNodeSetEqual(t *testing.T, actual []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	if len(actual) != len(want) {
		t.Fatalf("affected nodes = %v, want %v", actual, want)
	}
	wantSet := map[uuid.UUID]bool{}
	for _, id := range want {
		wantSet[id] = true
	}
	for _, id := range actual {
		if !wantSet[id] {
			t.Fatalf("affected nodes = %v, want %v", actual, want)
		}
	}
}

// hideProviderState renames instance_id's account_inventory_provider_states
// row for provider 'openai' to a distinct provider value, making it
// deterministically invisible to
// control_evaluate_cross_node_duplicate_evidence_v1's join on
// provider = split_part(account_key, ':', 1) -- i.e. simulating "no
// account_inventory_provider_states row at all for this account_key's
// provider" without any timing race, using the same guard-trigger-disable
// pattern as makeProviderStateStale/backdateProviderState.
func hideProviderState(t *testing.T, ctx context.Context, database *isolatedJobDatabase, instanceID uuid.UUID) {
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
		SET provider='openai-hidden' WHERE instance_id=$1 AND provider='openai'`, instanceID); err != nil {
		t.Fatal(err)
	}
}

// setDistinctHealthScheduledAt advances instance_id's
// account_inventory_provider_states.health_scheduled_at by exactly 5
// minutes (preserving the required 5-minute slot alignment and the
// health_scheduled_at >= current_scheduled_at invariant), bypassing the
// guard trigger the same way makeProviderStateStale/backdateProviderState
// do, so a degraded evidence observation can be proven to cite a
// health_scheduled_at value distinct from current_scheduled_at.
func setDistinctHealthScheduledAt(t *testing.T, ctx context.Context, database *isolatedJobDatabase, instanceID uuid.UUID) time.Time {
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
	var distinct time.Time
	if err := database.owner.QueryRow(ctx, `UPDATE account_inventory_provider_states
		SET health_scheduled_at = health_scheduled_at + interval '5 minutes'
		WHERE instance_id=$1 AND provider='openai'
		RETURNING health_scheduled_at`, instanceID).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	return distinct
}

func TestCrossNodeDuplicateOwnershipLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("detect: first duplicate creates ACTIVE occurrence with full affected set", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-detect"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "detect", 2)
		accountKey := fixtureProviderName + ":detect@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"detect@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"detect@example.invalid"}, 1, false)

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil {
			t.Fatal("result = nil, want a created occurrence")
		}
		if !result.Created || result.Status != "ACTIVE" || result.EvidenceState != "complete" {
			t.Fatalf("result = %+v, want Created=true Status=ACTIVE EvidenceState=complete", result)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1])
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("want exactly one occurrence row")
		}
	})

	t.Run("no candidate: single owner produces no occurrence", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-none"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "none", 1)
		accountKey := fixtureProviderName + ":single-owner@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"single-owner@example.invalid"}, 1, false)

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			t.Fatalf("result = %+v, want nil (no candidate)", result)
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 0 {
			t.Fatal("want zero occurrence rows")
		}
	})

	t.Run("refresh: membership unchanged appends evidence without creating a new occurrence", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-refresh"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "refresh", 2)
		accountKey := fixtureProviderName + ":refresh@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"refresh@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"refresh@example.invalid"}, 1, false)

		first, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		// Re-finalize the same present state (simulates the next poll cycle
		// still proving both Nodes present), then re-evaluate.
		group.finalize(t, ctx, database, group.nodes[0], []string{"refresh@example.invalid"}, 2, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"refresh@example.invalid"}, 2, false)

		second, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if second.Created {
			t.Fatal("refresh pass must not report Created")
		}
		if second.OccurrenceID != first.OccurrenceID {
			t.Fatal("refresh pass must reuse the existing occurrence_id")
		}
		if second.Status != "ACTIVE" || second.EvidenceState != "complete" {
			t.Fatalf("result = %+v, want Status=ACTIVE EvidenceState=complete", second)
		}
		assertNodeSetEqual(t, second.AffectedNodes, group.nodes[0], group.nodes[1])
		if len(second.Added) != 0 || len(second.Removed) != 0 {
			t.Fatalf("refresh must not add/remove members, got Added=%v Removed=%v", second.Added, second.Removed)
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("want exactly one occurrence row after refresh")
		}
	})

	t.Run("A/B duplicate + B stale: degrade, no resolve, affected set unchanged", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-degrade-ab"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "degradeab", 2)
		accountKey := fixtureProviderName + ":degrade-ab@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"degrade-ab@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"degrade-ab@example.invalid"}, 1, false)

		if _, err := repository.Evaluate(ctx, environmentID, accountKey); err != nil {
			t.Fatal(err)
		}
		makeProviderStateStale(t, ctx, database, group.nodes[1])

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "ACTIVE" || result.EvidenceState != "degraded" {
			t.Fatalf("result = %+v, want Status=ACTIVE EvidenceState=degraded", result)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1])
		if len(result.Removed) != 0 {
			t.Fatalf("stale Node must not be removed, got Removed=%v", result.Removed)
		}
	})

	t.Run("A/B duplicate + B fresh absent: remove then resolve", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-resolve-ab"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "resolveab", 2)
		accountKey := fixtureProviderName + ":resolve-ab@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"resolve-ab@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"resolve-ab@example.invalid"}, 1, false)

		created, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		// B's next fresh+complete promoted snapshot no longer contains the
		// account: fresh+complete absence evidence (design.md absence
		// matrix: suspected_missing already counts).
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.OccurrenceID != created.OccurrenceID {
			t.Fatal("resolve must operate on the same occurrence_id")
		}
		if result.Status != "RESOLVED" {
			t.Fatalf("result = %+v, want Status=RESOLVED", result)
		}
		assertNodeSetEqual(t, result.Removed, group.nodes[1])
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0])
		if occurrenceStatus(t, ctx, database, created.OccurrenceID) != "RESOLVED" {
			t.Fatal("occurrence row must be RESOLVED in the database")
		}
	})

	t.Run("reopen: RESOLVED history does not block a brand-new occurrence", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-reopen"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "reopen", 2)
		accountKey := fixtureProviderName + ":reopen@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"reopen@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"reopen@example.invalid"}, 1, false)

		firstOccurrence, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
		resolved, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Status != "RESOLVED" {
			t.Fatal("setup: expected resolve before reopen")
		}

		// B becomes present again on both Nodes: a brand-new duplicate.
		group.finalize(t, ctx, database, group.nodes[1], []string{"reopen@example.invalid"}, 1, false)
		reopened, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if !reopened.Created {
			t.Fatal("reopen must report Created=true (a new occurrence_id)")
		}
		if reopened.OccurrenceID == firstOccurrence.OccurrenceID {
			t.Fatal("reopen must not reuse the RESOLVED occurrence_id")
		}
		if occurrenceStatus(t, ctx, database, firstOccurrence.OccurrenceID) != "RESOLVED" {
			t.Fatal("original occurrence must remain RESOLVED, untouched")
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 2 {
			t.Fatal("want exactly two occurrence rows: original RESOLVED + reopened ACTIVE")
		}
	})

	t.Run("3+ Node duplicate is one occurrence, not pairwise", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-three"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "three", 3)
		accountKey := fixtureProviderName + ":three-lifecycle@example.invalid"
		for _, nodeID := range group.nodes {
			group.finalize(t, ctx, database, nodeID, []string{"three-lifecycle@example.invalid"}, 1, false)
		}

		var nodeLocalDuplicatesBefore int
		if err := database.owner.QueryRow(ctx,
			`SELECT count(*) FROM account_inventory_poll_duplicates`).Scan(&nodeLocalDuplicatesBefore); err != nil {
			t.Fatal(err)
		}

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "ACTIVE" {
			t.Fatalf("result = %+v, want Status=ACTIVE", result)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1], group.nodes[2])
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("3+ Node duplicate must produce exactly one occurrence, never a pairwise set")
		}

		// Node-local duplicate detection (account_inventory_poll_duplicates)
		// is an entirely separate mechanism; cross-node lifecycle evaluation
		// must never write to it.
		var nodeLocalDuplicatesAfter int
		if err := database.owner.QueryRow(ctx,
			`SELECT count(*) FROM account_inventory_poll_duplicates`).Scan(&nodeLocalDuplicatesAfter); err != nil {
			t.Fatal(err)
		}
		if nodeLocalDuplicatesAfter != nodeLocalDuplicatesBefore {
			t.Fatal("cross-node duplicate lifecycle evaluation must not write to account_inventory_poll_duplicates")
		}
	})

	t.Run("add owner: A/B duplicate then C becomes fresh present joins the same occurrence", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-add"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "add", 3)
		accountKey := fixtureProviderName + ":add-owner@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"add-owner@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"add-owner@example.invalid"}, 1, false)

		created, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertNodeSetEqual(t, created.AffectedNodes, group.nodes[0], group.nodes[1])

		group.finalize(t, ctx, database, group.nodes[2], []string{"add-owner@example.invalid"}, 1, false)
		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.OccurrenceID != created.OccurrenceID {
			t.Fatal("add must reuse the existing occurrence_id, not create a second one")
		}
		assertNodeSetEqual(t, result.Added, group.nodes[2])
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1], group.nodes[2])
	})

	t.Run("A/B/C duplicate + C stale: no removal, evidence_state degraded, still ACTIVE", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-degrade-abc"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "degradeabc", 3)
		accountKey := fixtureProviderName + ":degrade-abc@example.invalid"
		for _, nodeID := range group.nodes {
			group.finalize(t, ctx, database, nodeID, []string{"degrade-abc@example.invalid"}, 1, false)
		}
		if _, err := repository.Evaluate(ctx, environmentID, accountKey); err != nil {
			t.Fatal(err)
		}
		makeProviderStateStale(t, ctx, database, group.nodes[2])

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "ACTIVE" || result.EvidenceState != "degraded" {
			t.Fatalf("result = %+v, want Status=ACTIVE EvidenceState=degraded", result)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1], group.nodes[2])
		if len(result.Removed) != 0 {
			t.Fatalf("stale Node must not be removed, got Removed=%v", result.Removed)
		}
	})

	t.Run("A/B/C duplicate + C fresh absent: removed but still ACTIVE (2 remain)", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-remove-abc"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "removeabc", 3)
		accountKey := fixtureProviderName + ":remove-abc@example.invalid"
		for _, nodeID := range group.nodes {
			group.finalize(t, ctx, database, nodeID, []string{"remove-abc@example.invalid"}, 1, false)
		}
		if _, err := repository.Evaluate(ctx, environmentID, accountKey); err != nil {
			t.Fatal(err)
		}
		group.finalize(t, ctx, database, group.nodes[2], nil, 0, false)

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "ACTIVE" || result.EvidenceState != "complete" {
			t.Fatalf("result = %+v, want Status=ACTIVE EvidenceState=complete", result)
		}
		assertNodeSetEqual(t, result.Removed, group.nodes[2])
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1])
	})

	t.Run("zero-evidence degraded pass: retained Node unclassifiable this pass, latest_evaluation_id preserved", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-zero-evidence"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "zeroevidence", 2)
		accountKey := fixtureProviderName + ":zero-evidence@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"zero-evidence@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"zero-evidence@example.invalid"}, 1, false)

		created, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		var latestEvaluationIDBefore uuid.UUID
		if err := database.owner.QueryRow(ctx, `SELECT latest_evaluation_id
			FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, created.OccurrenceID).
			Scan(&latestEvaluationIDBefore); err != nil {
			t.Fatal(err)
		}
		if latestEvaluationIDBefore != created.EvaluationID {
			t.Fatalf("latest_evaluation_id after detect = %v, want %v", latestEvaluationIDBefore, created.EvaluationID)
		}

		// Both retained Nodes become entirely unclassifiable this pass (no
		// account_inventory_provider_states row visible for this
		// account_key's provider at all): evaluateEvidenceTx returns zero
		// rows, so zero evidence can be written this pass.
		hideProviderState(t, ctx, database, group.nodes[0])
		hideProviderState(t, ctx, database, group.nodes[1])

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "ACTIVE" || result.EvidenceState != "degraded" {
			t.Fatalf("result = %+v, want Status=ACTIVE EvidenceState=degraded", result)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1])
		if len(result.Added) != 0 || len(result.Removed) != 0 {
			t.Fatalf("membership must be unchanged on a zero-evidence pass, got Added=%v Removed=%v", result.Added, result.Removed)
		}

		var evidenceCountThisEvaluation int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrence_evidence
			WHERE occurrence_id=$1 AND evaluation_id=$2`, created.OccurrenceID, result.EvaluationID).
			Scan(&evidenceCountThisEvaluation); err != nil {
			t.Fatal(err)
		}
		if evidenceCountThisEvaluation != 0 {
			t.Fatalf("evidence rows for this pass's evaluation_id = %d, want 0", evidenceCountThisEvaluation)
		}

		var latestEvaluationIDAfter uuid.UUID
		var evidenceState string
		var lastSeenAt, lastFullyVerifiedAt time.Time
		if err := database.owner.QueryRow(ctx, `SELECT latest_evaluation_id, evidence_state, last_seen_at, last_fully_verified_at
			FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, created.OccurrenceID).
			Scan(&latestEvaluationIDAfter, &evidenceState, &lastSeenAt, &lastFullyVerifiedAt); err != nil {
			t.Fatal(err)
		}
		if latestEvaluationIDAfter != latestEvaluationIDBefore {
			t.Fatalf("latest_evaluation_id after zero-evidence pass = %v, want unchanged %v", latestEvaluationIDAfter, latestEvaluationIDBefore)
		}
		if evidenceState != "degraded" {
			t.Fatalf("evidence_state = %q, want degraded", evidenceState)
		}
		if !lastSeenAt.Equal(result.EvaluationAt.UTC()) && lastSeenAt.Before(result.EvaluationAt.UTC()) {
			t.Fatalf("last_seen_at = %v, want advanced to this pass's evaluationAt %v", lastSeenAt, result.EvaluationAt)
		}
	})

	t.Run("new eligible Node not owner_confirmed at evaluationAt must not join the affected set", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-new-eligible-not-confirmed"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "neweligible", 3)
		accountKey := fixtureProviderName + ":new-eligible@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"new-eligible@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"new-eligible@example.invalid"}, 1, false)

		created, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		assertNodeSetEqual(t, created.AffectedNodes, group.nodes[0], group.nodes[1])

		// Node C is present per discovery's own snapshot, but becomes stale
		// by the time evaluateEvidenceTx authoritatively reconfirms it this
		// pass: it must not join the affected set.
		group.finalize(t, ctx, database, group.nodes[2], []string{"new-eligible@example.invalid"}, 1, false)
		makeProviderStateStale(t, ctx, database, group.nodes[2])

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Added) != 0 {
			t.Fatalf("Added = %v, want empty: a stale/degraded Node must never join the affected set", result.Added)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1])
	})

	t.Run("degraded observation cites health_scheduled_at and a NULL source_poll_run_id", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-degraded-evidence"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "degevid", 2)
		accountKey := fixtureProviderName + ":degraded-evidence@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"degraded-evidence@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"degraded-evidence@example.invalid"}, 1, false)

		created, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		makeProviderStateStale(t, ctx, database, group.nodes[1])
		distinctHealthScheduledAt := setDistinctHealthScheduledAt(t, ctx, database, group.nodes[1])

		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.EvidenceState != "degraded" {
			t.Fatalf("result = %+v, want EvidenceState=degraded", result)
		}

		var sourcePollRunID *uuid.UUID
		var sourceScheduledAt time.Time
		if err := database.owner.QueryRow(ctx, `SELECT source_poll_run_id, source_scheduled_at
			FROM cross_node_duplicate_occurrence_evidence
			WHERE occurrence_id=$1 AND instance_id=$2 AND observation_kind='degraded'
			ORDER BY recorded_at DESC LIMIT 1`, created.OccurrenceID, group.nodes[1]).
			Scan(&sourcePollRunID, &sourceScheduledAt); err != nil {
			t.Fatal(err)
		}
		if sourcePollRunID != nil {
			t.Fatalf("degraded evidence source_poll_run_id = %v, want NULL", *sourcePollRunID)
		}
		if !sourceScheduledAt.Equal(distinctHealthScheduledAt) {
			t.Fatalf("degraded evidence source_scheduled_at = %v, want health_scheduled_at %v", sourceScheduledAt, distinctHealthScheduledAt)
		}
	})

	t.Run("invalid input is rejected without touching the database", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		if _, err := repository.Evaluate(ctx, "", fixtureProviderName+":x@example.invalid"); err == nil {
			t.Fatal("want error for empty environment_id")
		}
		if _, err := repository.Evaluate(ctx, "lifecycle-invalid", "not-a-valid-account-key"); err == nil {
			t.Fatal("want error for invalid account_key")
		}
	})

	t.Run("rollback: a mid-transaction failure leaves no partial occurrence/evidence row", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "lifecycle-rollback"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		accountKey := fixtureProviderName + ":rollback@example.invalid"

		tx, err := database.runtime.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var occurrenceID uuid.UUID
		now := "clock_timestamp()"
		if err := tx.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,$2,'cross_node_duplicate_ownership','ACTIVE','Critical',`+now+`,`+now+`,'complete')
		RETURNING occurrence_id`, environmentID, accountKey).Scan(&occurrenceID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ($1, gen_random_uuid(), 'not_a_valid_kind', 'openai', `+now+`, `+now+`, gen_random_uuid(), `+now+`)`,
			occurrenceID); err == nil {
			t.Fatal("want CHECK violation for invalid observation_kind, got nil error")
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 0 {
			t.Fatal("rolled-back transaction must leave zero occurrence rows")
		}
	})
}
