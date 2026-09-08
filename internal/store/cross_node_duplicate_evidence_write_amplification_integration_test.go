package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func duplicateEvidenceCount(t *testing.T, ctx context.Context, database *isolatedJobDatabase, occurrenceID uuid.UUID) int {
	t.Helper()
	var count int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrence_evidence WHERE occurrence_id=$1`, occurrenceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCrossNodeDuplicateEvidenceWriteAmplificationRegressionPostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "evidence-write-amplification"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	group := newOwnershipNodeGroup(t, ctx, database, "evidence-amplification", 2)
	repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	accountKey := fixtureProviderName + ":evidence-amplification@example.invalid"
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"evidence-amplification@example.invalid"}, 1, false)
	}

	created, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || created == nil || !created.Created {
		t.Fatalf("initial evaluation=%+v err=%v", created, err)
	}
	if got := duplicateEvidenceCount(t, ctx, database, created.OccurrenceID); got != 2 {
		t.Fatalf("initial evidence=%d want 2", got)
	}

	for i := 0; i < 3; i++ {
		result, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil || result == nil || result.OccurrenceID != created.OccurrenceID {
			t.Fatalf("no-op evaluation %d result=%+v err=%v", i, result, err)
		}
		if got := duplicateEvidenceCount(t, ctx, database, created.OccurrenceID); got != 2 {
			t.Fatalf("no-op evaluation %d evidence=%d want 2", i, got)
		}
	}
	// A genuinely new promoted source is material even though the owner
	// membership and conclusion are unchanged. It must create exactly one
	// new full checkpoint before concurrent retries converge on it.
	group.finalize(t, ctx, database, group.nodes[0], []string{"evidence-amplification@example.invalid"}, 1, false)

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := repository.Evaluate(ctx, environmentID, accountKey)
			if err != nil {
				results <- err
				return
			}
			if result == nil || result.OccurrenceID != created.OccurrenceID || result.Status != "ACTIVE" {
				results <- fmt.Errorf("unexpected concurrent result: %+v", result)
			}
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if got := duplicateEvidenceCount(t, ctx, database, created.OccurrenceID); got != 4 {
		t.Fatalf("concurrent material refresh evidence=%d want 4", got)
	}

	restarted, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := restarted.Evaluate(ctx, environmentID, accountKey); err != nil || result == nil || result.OccurrenceID != created.OccurrenceID {
		t.Fatalf("restart evaluation=%+v err=%v", result, err)
	}
	if got := duplicateEvidenceCount(t, ctx, database, created.OccurrenceID); got != 4 {
		t.Fatalf("restart refresh evidence=%d want 4", got)
	}
}

func TestCrossNodeDuplicateAbsenceResolveReopenHistoryRegressionPostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "evidence-absence-history"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	group := newOwnershipNodeGroup(t, ctx, database, "absence-history", 2)
	repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	accountKey := fixtureProviderName + ":absence-history@example.invalid"
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"absence-history@example.invalid"}, 1, false)
	}
	created, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || created == nil {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, nil, 0, false)
	}
	resolved, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || resolved == nil || resolved.OccurrenceID != created.OccurrenceID || resolved.Status != "RESOLVED" {
		t.Fatalf("resolve=%+v err=%v", resolved, err)
	}
	if len(resolved.AffectedNodes) != 0 {
		t.Fatalf("resolved current membership=%v want empty", resolved.AffectedNodes)
	}
	reader, err := productstore.NewCrossNodeDuplicateOwnershipOccurrenceRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range group.nodes {
		page, err := reader.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(ctx, node, "RESOLVED", nil, 25)
		if err != nil || len(page.Items) != 1 || page.Items[0].OccurrenceID != created.OccurrenceID || page.Items[0].Status != "RESOLVED" || page.Items[0].AccountKey != accountKey {
			t.Fatalf("node=%s history=%+v err=%v", node, page, err)
		}
	}
	var currentMembership int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrence_nodes WHERE occurrence_id=$1`, created.OccurrenceID).Scan(&currentMembership); err != nil {
		t.Fatal(err)
	}
	if currentMembership != 0 {
		t.Fatalf("current membership=%d want 0", currentMembership)
	}
	var absenceEvidence, absenceEvaluations int
	if err := database.owner.QueryRow(ctx, `SELECT count(*), count(DISTINCT evaluation_id)
		FROM cross_node_duplicate_occurrence_evidence
		WHERE occurrence_id=$1 AND observation_kind='absence_confirmed'`, created.OccurrenceID).Scan(&absenceEvidence, &absenceEvaluations); err != nil {
		t.Fatal(err)
	}
	if absenceEvidence != 2 || absenceEvaluations != 1 {
		t.Fatalf("absence evidence rows=%d evaluations=%d want 2/1", absenceEvidence, absenceEvaluations)
	}
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"absence-history@example.invalid"}, 1, false)
	}
	reopened, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || reopened == nil || !reopened.Created || reopened.OccurrenceID == created.OccurrenceID || reopened.Status != "ACTIVE" {
		t.Fatalf("reopen=%+v err=%v", reopened, err)
	}
	if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 2 {
		t.Fatal("expected one resolved and one reopened occurrence")
	}
}
