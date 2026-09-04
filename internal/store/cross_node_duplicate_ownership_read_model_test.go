package store_test

import (
	"context"
	"testing"

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
