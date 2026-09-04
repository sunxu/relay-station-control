package store_test

import (
	"context"
	"testing"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

// newCrossNodeDuplicateOwnershipReconciler wires a reconciler against the
// same runtime pool the lifecycle/query repositories use, exercising the
// exact grants/EXECUTE privileges production wiring would use.
func newCrossNodeDuplicateOwnershipReconciler(t *testing.T, database *isolatedJobDatabase) *productstore.CrossNodeDuplicateOwnershipReconciler {
	t.Helper()
	reader := newCrossNodeDuplicateOwnershipRepository(t, database)
	lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	reconciler, err := productstore.NewCrossNodeDuplicateOwnershipReconciler(database.runtime, reader, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

// TestCrossNodeDuplicateOwnershipReconciliation covers Phase 4.1: recomputing
// occurrence state against current Account Inventory truth after a process
// restart or missed detection cycles, without any second copy of lifecycle
// logic (every key is delegated to the existing Phase 3
// CrossNodeDuplicateOwnershipLifecycleRepository.Evaluate transaction).
func TestCrossNodeDuplicateOwnershipReconciliation(t *testing.T) {
	ctx := context.Background()

	t.Run("restart with an existing duplicate: reconcile reuses the existing ACTIVE occurrence", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "reconcile-existing"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "reconcileexisting", 2)
		accountKey := fixtureProviderName + ":reconcile-existing@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"reconcile-existing@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"reconcile-existing@example.invalid"}, 1, false)

		// Simulate the detect pass that ran before the (simulated) restart.
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		reconciler := newCrossNodeDuplicateOwnershipReconciler(t, database)
		evaluations, err := reconciler.Reconcile(ctx, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(evaluations) != 1 {
			t.Fatalf("evaluations = %d, want 1", len(evaluations))
		}
		if evaluations[0].OccurrenceID != created.OccurrenceID {
			t.Fatal("reconcile must reuse the existing ACTIVE occurrence_id, not create a new one")
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("reconcile must not create a duplicate occurrence row")
		}
	})

	t.Run("restart with a duplicate that first appeared during downtime: reconcile creates the occurrence", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "reconcile-new"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		group := newOwnershipNodeGroup(t, ctx, database, "reconcilenew", 2)
		accountKey := fixtureProviderName + ":reconcile-new@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"reconcile-new@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"reconcile-new@example.invalid"}, 1, false)

		// No detect pass ran before this reconciliation -- the whole
		// duplicate appeared while the process was down.
		reconciler := newCrossNodeDuplicateOwnershipReconciler(t, database)
		evaluations, err := reconciler.Reconcile(ctx, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(evaluations) != 1 || !evaluations[0].Created {
			t.Fatalf("evaluations = %+v, want exactly one Created=true", evaluations)
		}
		assertNodeSetEqual(t, evaluations[0].AffectedNodes, group.nodes[0], group.nodes[1])
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("reconcile must create exactly one occurrence row")
		}
	})

	t.Run("ACTIVE occurrence with a Node fresh absent during downtime: reconcile resolves it", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "reconcile-resolve"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "reconcileresolve", 2)
		accountKey := fixtureProviderName + ":reconcile-resolve@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"reconcile-resolve@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"reconcile-resolve@example.invalid"}, 1, false)
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		// While the process was down, Node B's next fresh+complete
		// promoted snapshot no longer contains the account.
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)

		reconciler := newCrossNodeDuplicateOwnershipReconciler(t, database)
		evaluations, err := reconciler.Reconcile(ctx, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(evaluations) != 1 {
			t.Fatalf("evaluations = %d, want 1", len(evaluations))
		}
		if evaluations[0].OccurrenceID != created.OccurrenceID || evaluations[0].Status != "RESOLVED" {
			t.Fatalf("evaluations[0] = %+v, want Status=RESOLVED on the existing occurrence", evaluations[0])
		}
		if occurrenceStatus(t, ctx, database, created.OccurrenceID) != "RESOLVED" {
			t.Fatal("occurrence row must be RESOLVED in the database")
		}
	})

	t.Run("ACTIVE occurrence with a Node stale during downtime: reconcile degrades, does not resolve", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "reconcile-degrade"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "reconciledegrade", 2)
		accountKey := fixtureProviderName + ":reconcile-degrade@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"reconcile-degrade@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"reconcile-degrade@example.invalid"}, 1, false)
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		// While the process was down, Node B's provider state went stale
		// (no fresh evidence either way -- must never be treated as
		// absence).
		makeProviderStateStale(t, ctx, database, group.nodes[1])

		reconciler := newCrossNodeDuplicateOwnershipReconciler(t, database)
		evaluations, err := reconciler.Reconcile(ctx, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(evaluations) != 1 {
			t.Fatalf("evaluations = %d, want 1", len(evaluations))
		}
		result := evaluations[0]
		if result.OccurrenceID != created.OccurrenceID || result.Status != "ACTIVE" || result.EvidenceState != "degraded" {
			t.Fatalf("result = %+v, want ACTIVE/degraded on the existing occurrence", result)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1])
	})

	t.Run("ACTIVE A/B/C occurrence with C fresh absent during downtime: reconcile shrinks to A/B, still ACTIVE", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "reconcile-shrink"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		group := newOwnershipNodeGroup(t, ctx, database, "reconcileshrink", 3)
		accountKey := fixtureProviderName + ":reconcile-shrink@example.invalid"
		for _, nodeID := range group.nodes {
			group.finalize(t, ctx, database, nodeID, []string{"reconcile-shrink@example.invalid"}, 1, false)
		}
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		group.finalize(t, ctx, database, group.nodes[2], nil, 0, false)

		reconciler := newCrossNodeDuplicateOwnershipReconciler(t, database)
		evaluations, err := reconciler.Reconcile(ctx, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(evaluations) != 1 {
			t.Fatalf("evaluations = %d, want 1", len(evaluations))
		}
		result := evaluations[0]
		if result.OccurrenceID != created.OccurrenceID || result.Status != "ACTIVE" {
			t.Fatalf("result = %+v, want ACTIVE on the existing occurrence", result)
		}
		assertNodeSetEqual(t, result.AffectedNodes, group.nodes[0], group.nodes[1])
		assertNodeSetEqual(t, result.Removed, group.nodes[2])
	})

	t.Run("idempotent: repeated reconciliation against unchanged truth never duplicates state", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "reconcile-idempotent"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		group := newOwnershipNodeGroup(t, ctx, database, "reconcileidempotent", 3)
		accountKey := fixtureProviderName + ":reconcile-idempotent@example.invalid"
		for _, nodeID := range group.nodes {
			group.finalize(t, ctx, database, nodeID, []string{"reconcile-idempotent@example.invalid"}, 1, false)
		}

		reconciler := newCrossNodeDuplicateOwnershipReconciler(t, database)
		var occurrenceID [3]productstore.CrossNodeDuplicateOwnershipEvaluation
		for pass := 0; pass < 3; pass++ {
			evaluations, err := reconciler.Reconcile(ctx, environmentID)
			if err != nil {
				t.Fatal(err)
			}
			if len(evaluations) != 1 {
				t.Fatalf("pass %d: evaluations = %d, want 1", pass, len(evaluations))
			}
			occurrenceID[pass] = *evaluations[0]
			if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
				t.Fatalf("pass %d: occurrence row count must stay 1, never a pairwise/duplicate row", pass)
			}
			assertNodeSetEqual(t, occurrenceID[pass].AffectedNodes, group.nodes[0], group.nodes[1], group.nodes[2])
			if occurrenceID[pass].Status != "ACTIVE" {
				t.Fatalf("pass %d: status = %s, want ACTIVE", pass, occurrenceID[pass].Status)
			}
		}
		if occurrenceID[0].OccurrenceID != occurrenceID[1].OccurrenceID || occurrenceID[1].OccurrenceID != occurrenceID[2].OccurrenceID {
			t.Fatal("occurrence_id must stay identical across repeated reconciliation passes")
		}
		var nodeRowCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrence_nodes
			WHERE occurrence_id=$1`, occurrenceID[0].OccurrenceID).Scan(&nodeRowCount); err != nil {
			t.Fatal(err)
		}
		if nodeRowCount != 3 {
			t.Fatalf("occurrence_nodes row count = %d, want 3 (membership must never be duplicated across passes)", nodeRowCount)
		}
	})

	t.Run("invalid environment_id is rejected without touching the database", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		reconciler := newCrossNodeDuplicateOwnershipReconciler(t, database)
		if _, err := reconciler.Reconcile(ctx, ""); err == nil {
			t.Fatal("expected an error for an empty environment_id")
		}
	})
}
