package store

import (
	"testing"

	"github.com/google/uuid"
)

// SetCrossNodeDuplicateOwnershipInsertRaceHookForTest installs fn to be
// called synchronously every time create() loses the ON CONFLICT DO
// NOTHING RETURNING race and falls back to re-selecting the existing row
// FOR UPDATE. It restores the previous (no-op) hook when the returned
// restore func runs. Exported so the black-box concurrency regression in
// package store_test (cross_node_duplicate_ownership_concurrency_test.go)
// can deterministically prove the fallback path executed; this symbol only
// exists in the test binary (defined in a _test.go file) and is never part
// of the production build.
func SetCrossNodeDuplicateOwnershipInsertRaceHookForTest(fn func()) (restore func()) {
	previous := crossNodeDuplicateOwnershipInsertRaceObserved
	if fn == nil {
		fn = func() {}
	}
	crossNodeDuplicateOwnershipInsertRaceObserved = fn
	return func() { crossNodeDuplicateOwnershipInsertRaceObserved = previous }
}

// TestFilterOwnerConfirmedCandidates exercises the "discovery found >= 2
// candidates but the authoritative reconfirm at evaluationAt confirms fewer
// than 2" branch deterministically (Phase 3 review items 1 and 5), without
// needing a real clock-timing race against the 15-minute freshness
// boundary: filterOwnerConfirmedCandidates is the pure function create()
// uses to decide, from already-fetched evalResults, which discovered
// candidates survive authoritative reconfirmation.
func TestFilterOwnerConfirmedCandidates(t *testing.T) {
	nodeA, nodeB, nodeC := uuid.New(), uuid.New(), uuid.New()

	t.Run("discovery >= 2 but authoritative reconfirm < 2: only one candidate survives", func(t *testing.T) {
		candidates := []uuid.UUID{nodeA, nodeB}
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			nodeA: {InstanceID: nodeA, ObservationKind: "owner_confirmed"},
			nodeB: {InstanceID: nodeB, ObservationKind: "absence_confirmed"},
		}
		confirmed := filterOwnerConfirmedCandidates(candidates, evalResults)
		if len(confirmed) != 1 || confirmed[0] != nodeA {
			t.Fatalf("confirmed = %v, want exactly [nodeA]", confirmed)
		}
		if len(confirmed) >= 2 {
			t.Fatal("create() must treat this as < 2 confirmed and write nothing")
		}
	})

	t.Run("discovery >= 2 but one candidate is degraded (unverifiable) at evaluationAt", func(t *testing.T) {
		candidates := []uuid.UUID{nodeA, nodeB}
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			nodeA: {InstanceID: nodeA, ObservationKind: "owner_confirmed"},
			nodeB: {InstanceID: nodeB, ObservationKind: "degraded"},
		}
		confirmed := filterOwnerConfirmedCandidates(candidates, evalResults)
		if len(confirmed) != 1 {
			t.Fatalf("confirmed = %v, want exactly 1", confirmed)
		}
	})

	t.Run("discovery >= 2 but one candidate is missing from evalResults entirely (no provider_state row)", func(t *testing.T) {
		candidates := []uuid.UUID{nodeA, nodeB}
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			nodeA: {InstanceID: nodeA, ObservationKind: "owner_confirmed"},
		}
		confirmed := filterOwnerConfirmedCandidates(candidates, evalResults)
		if len(confirmed) != 1 {
			t.Fatalf("confirmed = %v, want exactly 1", confirmed)
		}
	})

	t.Run("all candidates reconfirmed: full set survives", func(t *testing.T) {
		candidates := []uuid.UUID{nodeA, nodeB, nodeC}
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			nodeA: {InstanceID: nodeA, ObservationKind: "owner_confirmed"},
			nodeB: {InstanceID: nodeB, ObservationKind: "owner_confirmed"},
			nodeC: {InstanceID: nodeC, ObservationKind: "owner_confirmed"},
		}
		confirmed := filterOwnerConfirmedCandidates(candidates, evalResults)
		if len(confirmed) != 3 {
			t.Fatalf("confirmed = %v, want all 3", confirmed)
		}
	})
}

// TestClassifyCrossNodeDuplicateMembership exercises reconcile()'s pure
// add/remove/degrade decision logic deterministically (Phase 3 review item
// 4): a Node newly eligible per discovery that the authoritative evaluate()
// call does NOT classify owner_confirmed must never join the affected set,
// regardless of why (absence_confirmed, degraded, or simply missing from
// evalResults).
func TestClassifyCrossNodeDuplicateMembership(t *testing.T) {
	current, newNode := uuid.New(), uuid.New()
	currentSet := map[uuid.UUID]bool{current: true}

	t.Run("new eligible Node classified absence_confirmed must not be added", func(t *testing.T) {
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			current: {InstanceID: current, ObservationKind: "owner_confirmed"},
			newNode: {InstanceID: newNode, ObservationKind: "absence_confirmed"},
		}
		decision := classifyCrossNodeDuplicateMembership([]uuid.UUID{current, newNode}, currentSet, evalResults)
		if len(decision.ToAdd) != 0 {
			t.Fatalf("ToAdd = %v, want empty", decision.ToAdd)
		}
		if len(decision.ToRemove) != 0 {
			t.Fatalf("ToRemove = %v, want empty", decision.ToRemove)
		}
		if decision.AnyDegradedRetained {
			t.Fatal("AnyDegradedRetained must be false: the only retained Node was owner_confirmed")
		}
	})

	t.Run("new eligible Node classified degraded must not be added", func(t *testing.T) {
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			current: {InstanceID: current, ObservationKind: "owner_confirmed"},
			newNode: {InstanceID: newNode, ObservationKind: "degraded"},
		}
		decision := classifyCrossNodeDuplicateMembership([]uuid.UUID{current, newNode}, currentSet, evalResults)
		if len(decision.ToAdd) != 0 {
			t.Fatalf("ToAdd = %v, want empty", decision.ToAdd)
		}
		if decision.AnyDegradedRetained {
			t.Fatal("AnyDegradedRetained must be false: newNode is not a retained/current Node")
		}
	})

	t.Run("new eligible Node missing from evalResults entirely must not be added", func(t *testing.T) {
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			current: {InstanceID: current, ObservationKind: "owner_confirmed"},
		}
		decision := classifyCrossNodeDuplicateMembership([]uuid.UUID{current, newNode}, currentSet, evalResults)
		if len(decision.ToAdd) != 0 {
			t.Fatalf("ToAdd = %v, want empty", decision.ToAdd)
		}
	})

	t.Run("new eligible Node owner_confirmed is added", func(t *testing.T) {
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			current: {InstanceID: current, ObservationKind: "owner_confirmed"},
			newNode: {InstanceID: newNode, ObservationKind: "owner_confirmed"},
		}
		decision := classifyCrossNodeDuplicateMembership([]uuid.UUID{current, newNode}, currentSet, evalResults)
		if len(decision.ToAdd) != 1 || decision.ToAdd[0] != newNode {
			t.Fatalf("ToAdd = %v, want exactly [newNode]", decision.ToAdd)
		}
	})

	t.Run("retained Node classified absence_confirmed is removed", func(t *testing.T) {
		evalResults := map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			current: {InstanceID: current, ObservationKind: "absence_confirmed"},
		}
		decision := classifyCrossNodeDuplicateMembership([]uuid.UUID{current}, currentSet, evalResults)
		if len(decision.ToRemove) != 1 || decision.ToRemove[0] != current {
			t.Fatalf("ToRemove = %v, want exactly [current]", decision.ToRemove)
		}
	})

	t.Run("retained Node degraded or missing from evalResults sets AnyDegradedRetained without removing it", func(t *testing.T) {
		for _, evalResults := range []map[uuid.UUID]crossNodeDuplicateEvidenceRow{
			{current: {InstanceID: current, ObservationKind: "degraded"}},
			{},
		} {
			decision := classifyCrossNodeDuplicateMembership([]uuid.UUID{current}, currentSet, evalResults)
			if !decision.AnyDegradedRetained {
				t.Fatalf("AnyDegradedRetained = false for evalResults=%v, want true", evalResults)
			}
			if len(decision.ToRemove) != 0 {
				t.Fatalf("ToRemove = %v, want empty (degraded/unclassifiable must never trigger removal)", decision.ToRemove)
			}
		}
	})
}
