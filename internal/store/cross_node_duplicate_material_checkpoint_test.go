package store_test

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestCrossNodeDuplicateMaterialCheckpointPostgres(t *testing.T) {
	ctx := context.Background()
	db := newIsolatedJobDatabase(t)
	env := "material-checkpoint"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, db, env)
	group := newOwnershipNodeGroup(t, ctx, db, "material", 2)
	for _, n := range group.nodes {
		group.finalize(t, ctx, db, n, []string{"material@example.invalid"}, 1, false)
	}
	repo := newCrossNodeDuplicateOwnershipLifecycleRepository(t, db)
	first, err := repo.Evaluate(ctx, env, "openai:material@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	var legacy string
	if err := db.owner.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(e) ORDER BY observation_id)::text FROM cross_node_duplicate_occurrence_evidence e WHERE occurrence_id=$1`, first.OccurrenceID).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		result, err := repo.Evaluate(ctx, env, "openai:material@example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		var latest uuid.UUID
		var seen, verified time.Time
		if err := db.owner.QueryRow(ctx, `SELECT latest_evaluation_id,last_seen_at,last_fully_verified_at FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, first.OccurrenceID).Scan(&latest, &seen, &verified); err != nil {
			t.Fatal(err)
		}
		if result.EvaluationID == first.EvaluationID || !result.EvaluationAt.After(first.EvaluationAt) || !seen.Equal(result.EvaluationAt) || !verified.Equal(result.EvaluationAt) || latest != first.EvaluationID {
			t.Fatal("no-op skipped evaluation or changed checkpoint/time semantics")
		}
	}
	if n := duplicateEvidenceCount(t, ctx, db, first.OccurrenceID); n != 2 {
		t.Fatalf("100 noops evidence=%d", n)
	}
	// Simulate the existing retention transition of the nullable current pointer,
	// not an UPDATE of immutable evidence. Stable source metadata remains intact.
	if _, err := db.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard; UPDATE account_inventory_provider_states SET current_poll_run_id=NULL; ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Evaluate(ctx, env, "openai:material@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if n := duplicateEvidenceCount(t, ctx, db, first.OccurrenceID); n != 2 {
		t.Fatal("retention pointer loss invented source")
	}
	group.finalize(t, ctx, db, group.nodes[0], []string{"material@example.invalid"}, 2, false)
	next, err := repo.Evaluate(ctx, env, "openai:material@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	var latest uuid.UUID
	if err := db.owner.QueryRow(ctx, `SELECT latest_evaluation_id FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, first.OccurrenceID).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if duplicateEvidenceCount(t, ctx, db, first.OccurrenceID) != 4 || latest != next.EvaluationID {
		t.Fatal("new source did not write full checkpoint")
	}
	var preserved string
	if err := db.owner.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(e) ORDER BY observation_id)::text FROM cross_node_duplicate_occurrence_evidence e WHERE occurrence_id=$1 AND evaluation_id=$2`, first.OccurrenceID, first.EvaluationID).Scan(&preserved); err != nil || preserved != legacy {
		t.Fatal("legacy evidence changed")
	}
}

func TestCrossNodeDuplicateMaterialStaleAndRecoveryPostgres(t *testing.T) {
	ctx := context.Background()
	db := newIsolatedJobDatabase(t)
	env := "material-stale"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, db, env)
	g := newOwnershipNodeGroup(t, ctx, db, "matstale", 2)
	for _, n := range g.nodes {
		g.finalize(t, ctx, db, n, []string{"stale@example.invalid"}, 1, false)
	}
	// Establish fresh evidence just before the unchanged 15-minute threshold.
	backdateProviderState(t, ctx, db, g.nodes[1], "14 minutes 58 seconds")
	repo := newCrossNodeDuplicateOwnershipLifecycleRepository(t, db)
	first, err := repo.Evaluate(ctx, env, "openai:stale@example.invalid")
	if err != nil || first == nil || first.EvidenceState != "complete" {
		t.Fatalf("initial: %+v %v", first, err)
	}
	// No provider writes or new source between these evaluations: only DB time.
	if _, err := db.owner.Exec(ctx, `SELECT pg_sleep(2.1)`); err != nil {
		t.Fatal(err)
	}
	stale, err := repo.Evaluate(ctx, env, "openai:stale@example.invalid")
	if err != nil || stale.Status != "ACTIVE" || stale.EvidenceState != "degraded" || len(stale.Removed) != 0 {
		t.Fatalf("stale: %+v %v", stale, err)
	}
	if duplicateEvidenceCount(t, ctx, db, first.OccurrenceID) != 4 {
		t.Fatal("stale checkpoint missing")
	}
	for i := 0; i < 10; i++ {
		if _, err := repo.Evaluate(ctx, env, "openai:stale@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	if duplicateEvidenceCount(t, ctx, db, first.OccurrenceID) != 4 {
		t.Fatal("degraded noops appended")
	}
	hideProviderState(t, ctx, db, g.nodes[0])
	hideProviderState(t, ctx, db, g.nodes[1])
	for i := 0; i < 3; i++ {
		r, err := repo.Evaluate(ctx, env, "openai:stale@example.invalid")
		if err != nil || r.EvidenceState != "degraded" {
			t.Fatal("zero-source degraded changed")
		}
	}
	if duplicateEvidenceCount(t, ctx, db, first.OccurrenceID) != 4 {
		t.Fatal("zero source invented evidence")
	}
	if _, err := db.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard; UPDATE account_inventory_provider_states SET provider='openai' WHERE provider='openai-hidden'; ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	for _, n := range g.nodes {
		g.finalize(t, ctx, db, n, []string{"stale@example.invalid"}, 2, false)
	}
	restored, err := repo.Evaluate(ctx, env, "openai:stale@example.invalid")
	if err != nil || restored.EvidenceState != "complete" || duplicateEvidenceCount(t, ctx, db, first.OccurrenceID) != 6 {
		t.Fatalf("recovery: %+v %v", restored, err)
	}
}
