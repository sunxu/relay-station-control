package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func newCrossNodeDuplicateOwnershipOccurrenceRepository(t *testing.T, database *isolatedJobDatabase) *productstore.CrossNodeDuplicateOwnershipOccurrenceRepository {
	t.Helper()
	repository, err := productstore.NewCrossNodeDuplicateOwnershipOccurrenceRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

// TestCrossNodeDuplicateOwnershipOccurrenceReadModel covers Phase 5.1/5.2:
// the read-only occurrence list/detail/evidence store, built directly
// against the runtime pool (relay_control_runtime already holds SELECT on
// all three Phase 1B tables -- no new migration/grant needed).
func TestCrossNodeDuplicateOwnershipOccurrenceReadModel(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "read-model-env"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	reader := newCrossNodeDuplicateOwnershipOccurrenceRepository(t, database)

	group := newOwnershipNodeGroup(t, ctx, database, "readmodel", 3)
	accountKeyActive := fixtureProviderName + ":read-model-active@example.invalid"
	accountKeyResolved := fixtureProviderName + ":read-model-resolved@example.invalid"

	// Build one ACTIVE occurrence (A/B) and one that gets resolved (A/B then B absent).
	group.finalize(t, ctx, database, group.nodes[0], []string{"read-model-active@example.invalid"}, 1, false)
	group.finalize(t, ctx, database, group.nodes[1], []string{"read-model-active@example.invalid"}, 1, false)
	active, err := lifecycle.Evaluate(ctx, environmentID, accountKeyActive)
	if err != nil {
		t.Fatal(err)
	}

	group.finalize(t, ctx, database, group.nodes[0], []string{"read-model-resolved@example.invalid"}, 1, false)
	group.finalize(t, ctx, database, group.nodes[1], []string{"read-model-resolved@example.invalid"}, 1, false)
	resolved, err := lifecycle.Evaluate(ctx, environmentID, accountKeyResolved)
	if err != nil {
		t.Fatal(err)
	}
	group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
	if _, err := lifecycle.Evaluate(ctx, environmentID, accountKeyResolved); err != nil {
		t.Fatal(err)
	}

	t.Run("list filters by status", func(t *testing.T) {
		page, err := reader.ListOccurrences(ctx, productstore.CrossNodeDuplicateOccurrenceFilters{Status: "ACTIVE"}, nil, 50)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range page.Items {
			if item.OccurrenceID == active.OccurrenceID {
				found = true
				if item.Status != "ACTIVE" {
					t.Fatalf("status = %q, want ACTIVE", item.Status)
				}
			}
			if item.OccurrenceID == resolved.OccurrenceID {
				t.Fatal("RESOLVED occurrence must not appear in an ACTIVE-filtered page")
			}
		}
		if !found {
			t.Fatal("expected the ACTIVE occurrence in the ACTIVE-filtered page")
		}
	})

	t.Run("list filters by account_key", func(t *testing.T) {
		page, err := reader.ListOccurrences(ctx, productstore.CrossNodeDuplicateOccurrenceFilters{AccountKey: accountKeyResolved}, nil, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].OccurrenceID != resolved.OccurrenceID {
			t.Fatalf("page.Items = %+v, want exactly the resolved occurrence", page.Items)
		}
	})

	t.Run("list filters by instance_id", func(t *testing.T) {
		page, err := reader.ListOccurrences(ctx, productstore.CrossNodeDuplicateOccurrenceFilters{InstanceID: group.nodes[2]}, nil, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.OccurrenceID == active.OccurrenceID || item.OccurrenceID == resolved.OccurrenceID {
				t.Fatalf("Node %s was never an affected Node of either fixture occurrence, but it appeared in the filtered page", group.nodes[2])
			}
		}
	})

	t.Run("history uses evidence after current membership is removed", func(t *testing.T) {
		page, err := reader.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(
			ctx, group.nodes[1], "RESOLVED", nil, 50,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].OccurrenceID != resolved.OccurrenceID {
			t.Fatalf("history page = %+v, want resolved occurrence", page.Items)
		}
		for _, item := range page.Items[0].AffectedNodes {
			if item == group.nodes[1] {
				t.Fatal("history response must retain current affected_nodes semantics; absent Node must not be recreated")
			}
		}

		current, err := reader.ListOccurrences(ctx, productstore.CrossNodeDuplicateOccurrenceFilters{
			Status: "RESOLVED", InstanceID: group.nodes[1],
		}, nil, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(current.Items) != 0 {
			t.Fatalf("current membership query = %+v, want no rows after absence", current.Items)
		}
	})

	t.Run("history survives removal of every current member and poll retention", func(t *testing.T) {
		// The remaining owner also gets a fresh absence observation. The
		// lifecycle removes the final current membership, but evidence remains
		// the historical involvement source.
		group.finalize(t, ctx, database, group.nodes[0], nil, 0, false)
		if _, err := lifecycle.Evaluate(ctx, environmentID, accountKeyResolved); err != nil {
			t.Fatal(err)
		}
		// A retention-cleaned evidence row has a NULL source_poll_run_id but
		// keeps occurrence_id/instance_id. It must remain discoverable by
		// history without joining the poll tables.
		retainedOccurrenceID := uuid.New()
		retainedEvaluationID := uuid.New()
		retainedAt := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			occurrence_id, environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,$2,$3,'cross_node_duplicate_ownership','ACTIVE','Critical',$4,$4,'complete')`,
			retainedOccurrenceID, environmentID, fixtureProviderName+":retained@example.invalid", retainedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, source_poll_run_id, evaluation_id, evaluation_at
		) VALUES ($1,$2,'absence_confirmed','openai',$3,$3,NULL,$4,$3)`,
			retainedOccurrenceID, group.nodes[0], retainedAt, retainedEvaluationID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, source_poll_run_id, evaluation_id, evaluation_at
		) VALUES ($1,$2,'degraded','openai',$3,$3,NULL,$4,$3)`,
			retainedOccurrenceID, group.nodes[1], retainedAt.Add(time.Second), uuid.New()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `UPDATE cross_node_duplicate_occurrences
			SET status = 'RESOLVED', resolved_at = $2
			WHERE occurrence_id = $1`, retainedOccurrenceID, retainedAt); err != nil {
			t.Fatal(err)
		}
		freshReader := newCrossNodeDuplicateOwnershipOccurrenceRepository(t, database)
		for _, nodeID := range []uuid.UUID{group.nodes[0], group.nodes[1]} {
			page, err := freshReader.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(ctx, nodeID, "RESOLVED", nil, 50)
			if err != nil {
				t.Fatal(err)
			}
			foundResolved := false
			for _, item := range page.Items {
				if item.OccurrenceID == resolved.OccurrenceID || item.OccurrenceID == retainedOccurrenceID {
					foundResolved = true
					if item.OccurrenceID == retainedOccurrenceID && len(item.AffectedNodes) != 0 {
						t.Fatalf("node %s current affected set = %v, want empty after membership removal", nodeID, item.AffectedNodes)
					}
				}
			}
			if !foundResolved {
				t.Fatalf("node %s history = %+v, want retained history", nodeID, page.Items)
			}
			if page.ObservedAt == nil || page.ObservedAt.IsZero() {
				t.Fatal("history page must include a DB observed_at")
			}
		}
	})

	t.Run("history deduplicates multiple evidence rows and paginates at occurrence level", func(t *testing.T) {
		page, err := reader.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(
			ctx, group.nodes[0], "", nil, 1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || !page.HasMore {
			t.Fatalf("history page = %+v, hasMore=%v, want one item and another occurrence", page.Items, page.HasMore)
		}
		seen := map[uuid.UUID]bool{}
		for _, item := range page.Items {
			if seen[item.OccurrenceID] {
				t.Fatalf("occurrence %s repeated despite multiple evidence rows", item.OccurrenceID)
			}
			seen[item.OccurrenceID] = true
		}
		last := page.Items[len(page.Items)-1]
		next, err := reader.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(
			ctx, group.nodes[0], "", &productstore.CrossNodeDuplicateOccurrenceCursor{
				LastSeenAt: last.LastSeenAt, OccurrenceID: last.OccurrenceID,
			}, 50,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range next.Items {
			if item.OccurrenceID == last.OccurrenceID {
				t.Fatal("history cursor repeated the last occurrence")
			}
		}
	})

	t.Run("history rejects invalid target, status, limit, and cursor", func(t *testing.T) {
		cases := []struct {
			name   string
			node   uuid.UUID
			status string
			cursor *productstore.CrossNodeDuplicateOccurrenceCursor
			limit  int
		}{
			{"nil node", uuid.Nil, "", nil, 25},
			{"bad status", group.nodes[0], "bogus", nil, 25},
			{"bad limit", group.nodes[0], "", nil, 0},
			{"bad cursor", group.nodes[0], "", &productstore.CrossNodeDuplicateOccurrenceCursor{OccurrenceID: uuid.New()}, 25},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := reader.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(ctx, tc.node, tc.status, tc.cursor, tc.limit)
				if err != productstore.ErrCrossNodeDuplicateOccurrenceQuery {
					t.Fatalf("err = %v, want ErrCrossNodeDuplicateOccurrenceQuery", err)
				}
			})
		}
	})

	t.Run("get occurrence detail returns affected nodes", func(t *testing.T) {
		detail, err := reader.GetOccurrence(ctx, active.OccurrenceID)
		if err != nil {
			t.Fatal(err)
		}
		assertNodeSetEqual(t, detail.AffectedNodes, group.nodes[0], group.nodes[1])
		if detail.AccountKey != accountKeyActive {
			t.Fatalf("account_key = %q, want %q (plaintext, no masking per frozen non-goal)", detail.AccountKey, accountKeyActive)
		}
	})

	t.Run("get occurrence detail: unknown occurrence_id is not found", func(t *testing.T) {
		_, err := reader.GetOccurrence(ctx, uuid.New())
		if err != productstore.ErrCrossNodeDuplicateOccurrenceNotFound {
			t.Fatalf("err = %v, want ErrCrossNodeDuplicateOccurrenceNotFound", err)
		}
	})

	t.Run("get occurrence detail: nil occurrence_id is rejected without touching the database", func(t *testing.T) {
		_, err := reader.GetOccurrence(ctx, uuid.Nil)
		if err != productstore.ErrCrossNodeDuplicateOccurrenceQuery {
			t.Fatalf("err = %v, want ErrCrossNodeDuplicateOccurrenceQuery", err)
		}
	})

	t.Run("list occurrence evidence is bounded and independently paginated", func(t *testing.T) {
		page, err := reader.ListOccurrenceEvidence(ctx, resolved.OccurrenceID, nil, uuid.Nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("len(page.Items) = %d, want 1 (limit=1)", len(page.Items))
		}
		if !page.HasMore {
			t.Fatal("expected HasMore=true: the resolved occurrence has more than one evidence observation across detect+resolve")
		}
		first := page.Items[0]
		next, err := reader.ListOccurrenceEvidence(ctx, resolved.OccurrenceID, &first.RecordedAt, first.ObservationID, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range next.Items {
			if item.ObservationID == first.ObservationID {
				t.Fatal("keyset cursor must not repeat the last item from the previous page")
			}
		}
	})

	t.Run("list occurrence evidence: invalid limit is rejected", func(t *testing.T) {
		_, err := reader.ListOccurrenceEvidence(ctx, resolved.OccurrenceID, nil, uuid.Nil, 0)
		if err != productstore.ErrCrossNodeDuplicateOccurrenceQuery {
			t.Fatalf("err = %v, want ErrCrossNodeDuplicateOccurrenceQuery", err)
		}
	})

	t.Run("list occurrences: invalid status filter is rejected", func(t *testing.T) {
		_, err := reader.ListOccurrences(ctx, productstore.CrossNodeDuplicateOccurrenceFilters{Status: "bogus"}, nil, 50)
		if err != productstore.ErrCrossNodeDuplicateOccurrenceQuery {
			t.Fatalf("err = %v, want ErrCrossNodeDuplicateOccurrenceQuery", err)
		}
	})
}
