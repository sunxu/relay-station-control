package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

// insertRaceObservations is a plain package-level counter (this test file
// runs sequentially, one t.Run at a time, but the hook itself may fire from
// concurrent goroutines within a single sub-test, hence atomic ops) used by
// the "insert-race path is deterministically exercised" sub-test below.
var insertRaceObservations int64

// crossNodeDuplicateOwnershipConcurrencyPool builds a dedicated runtime pool
// with enough MaxConns for genuinely concurrent, independent PostgreSQL
// transactions/connections (Phase 4 review requirement F/5.3: real
// transactions/connections, not a single-connection Go goroutine helper).
func crossNodeDuplicateOwnershipConcurrencyPool(t *testing.T, database *isolatedJobDatabase) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(database.runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 8
	config.MinConns = 0
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// evaluateOutcome captures one concurrent Evaluate() call's result.
type evaluateOutcome struct {
	result *productstore.CrossNodeDuplicateOwnershipEvaluation
	err    error
}

// runConcurrentEvaluate launches `workers` goroutines, each on its own
// connection acquired from pool, and releases them simultaneously via a
// closed start channel so their Evaluate() transactions genuinely overlap
// at the PostgreSQL level (row lock / unique index contention), not just at
// the Go scheduler level.
func runConcurrentEvaluate(
	ctx context.Context, lifecycle *productstore.CrossNodeDuplicateOwnershipLifecycleRepository,
	environmentID, accountKey string, workers int,
) []evaluateOutcome {
	start := make(chan struct{})
	outcomes := make([]evaluateOutcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			result, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
			outcomes[i] = evaluateOutcome{result: result, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	return outcomes
}

// TestCrossNodeDuplicateOwnershipConcurrency is the Phase 4.3 PostgreSQL
// concurrency regression (design.md §5.3): every scenario drives real,
// independent transactions against a dedicated multi-connection pool, and
// relies only on the existing Phase 1B/3 primitives (the ACTIVE partial
// unique index, occurrence FOR UPDATE row lock, the
// (occurrence_id,evaluation_id,instance_id) evidence unique constraint, and
// the single-transaction lifecycle) -- no lease/fencing is introduced here
// (see Phase 4.2 finding recorded in tasks.md/design.md: none of these
// scenarios exhibit a correctness failure the current primitives don't
// already close).
func TestCrossNodeDuplicateOwnershipConcurrency(t *testing.T) {
	ctx := context.Background()

	t.Run("concurrent first detect: two workers race the same account_key, only one ACTIVE occurrence survives", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-detect"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencydetect", 2)
		accountKey := fixtureProviderName + ":concurrency-detect@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-detect@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-detect@example.invalid"}, 1, false)

		outcomes := runConcurrentEvaluate(ctx, lifecycle, environmentID, accountKey, 2)

		var occurrenceIDs = map[string]int{}
		for i, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("worker %d: %v", i, outcome.err)
			}
			if outcome.result == nil {
				t.Fatalf("worker %d: expected a non-nil result (>=2 eligible owners exist)", i)
			}
			occurrenceIDs[outcome.result.OccurrenceID.String()]++
		}
		if len(occurrenceIDs) != 1 {
			t.Fatalf("occurrence_ids observed = %v, want exactly one shared occurrence_id "+
				"(the losing INSERT must re-select FOR UPDATE and reconcile against the winner's row, "+
				"per the insert-race path)", occurrenceIDs)
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("exactly one occurrence row must exist after the race, never a pairwise/duplicate row")
		}
	})

	t.Run("insert-race path is deterministically exercised: the losing worker actually re-selects FOR UPDATE and re-evaluates", func(t *testing.T) {
		restore := productstore.SetCrossNodeDuplicateOwnershipInsertRaceHookForTest(func() {
			atomic.AddInt64(&insertRaceObservations, 1)
		})
		defer restore()
		atomic.StoreInt64(&insertRaceObservations, 0)

		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-detect-race-hook"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencyracehook", 2)
		accountKey := fixtureProviderName + ":concurrency-race-hook@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-race-hook@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-race-hook@example.invalid"}, 1, false)

		// Both workers start with no ACTIVE occurrence in existence
		// (confirmed by the environment/account_key being brand new to
		// this sub-test) and are released simultaneously, so both take the
		// create() path and genuinely contend on the same partial unique
		// index INSERT -- PostgreSQL serializes that contention
		// deterministically (the second INSERT blocks until the first
		// commits, then resolves ON CONFLICT DO NOTHING RETURNING to zero
		// rows), so the fallback branch is not probabilistic here.
		outcomes := runConcurrentEvaluate(ctx, lifecycle, environmentID, accountKey, 2)
		for i, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("worker %d: %v", i, outcome.err)
			}
			if outcome.result == nil {
				t.Fatalf("worker %d: expected a non-nil result (>=2 eligible owners exist)", i)
			}
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("exactly one occurrence row must exist after the race")
		}
		// The assertion the user asked for: not "same occurrence_id
		// implies the fallback ran", but direct proof the fallback branch
		// (re-select FOR UPDATE -> re-discover -> re-evaluate) was actually
		// entered by at least one of the two concurrent workers.
		if observed := atomic.LoadInt64(&insertRaceObservations); observed < 1 {
			t.Fatalf("insert-race fallback hook fired %d times, want >= 1: "+
				"the losing worker must have actually hit ON CONFLICT DO NOTHING RETURNING "+
				"returning zero rows and re-entered reconcile() via a fresh FOR UPDATE select", observed)
		}
	})

	t.Run("concurrent refresh/refresh: two workers reconfirm the same membership, no lost update", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-refresh"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencyrefresh", 2)
		accountKey := fixtureProviderName + ":concurrency-refresh@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-refresh@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-refresh@example.invalid"}, 1, false)
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		outcomes := runConcurrentEvaluate(ctx, lifecycle, environmentID, accountKey, 2)
		for i, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("worker %d: %v", i, outcome.err)
			}
			if outcome.result == nil || outcome.result.OccurrenceID != created.OccurrenceID || outcome.result.Status != "ACTIVE" {
				t.Fatalf("worker %d: result = %+v, want ACTIVE on the same occurrence_id", i, outcome.result)
			}
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("refresh must never create a second occurrence row")
		}
		assertNodeSetEqual(t,
			listOccurrenceNodeIDs(t, ctx, database, created.OccurrenceID),
			group.nodes[0], group.nodes[1])
	})

	t.Run("concurrent refresh/resolve: the later lock holder re-reads source truth, never un-resolves", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-resolve"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencyresolve", 2)
		accountKey := fixtureProviderName + ":concurrency-resolve@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-resolve@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-resolve@example.invalid"}, 1, false)
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		// B's next fresh+complete promoted snapshot proves absence -- both
		// concurrent workers now see resolve-eligible source truth.
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)

		outcomes := runConcurrentEvaluate(ctx, lifecycle, environmentID, accountKey, 2)
		resolvedCount := 0
		for i, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("worker %d: %v", i, outcome.err)
			}
			if outcome.result == nil {
				// The straggler's initial FOR UPDATE select found no ACTIVE
				// row (the winner already resolved it) and discovery then
				// found <2 eligible owners: a correct no-op, not a race bug.
				continue
			}
			if outcome.result.OccurrenceID != created.OccurrenceID {
				t.Fatalf("worker %d: result on an unexpected occurrence_id: %+v", i, outcome.result)
			}
			if outcome.result.Status == "RESOLVED" {
				resolvedCount++
			} else {
				t.Fatalf("worker %d: result = %+v, a non-nil result on this occurrence must be RESOLVED", i, outcome.result)
			}
		}
		if resolvedCount != 1 {
			t.Fatalf("resolvedCount = %d, want exactly 1 (the later lock holder must never overwrite the resolve with a stale ACTIVE decision)", resolvedCount)
		}
		if occurrenceStatus(t, ctx, database, created.OccurrenceID) != "RESOLVED" {
			t.Fatal("occurrence row must end RESOLVED, never reverted to ACTIVE")
		}
	})

	t.Run("concurrent add/remove: a newly eligible owner and a newly absent owner both land without a lost update", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-addremove"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencyaddremove", 3)
		accountKey := fixtureProviderName + ":concurrency-addremove@example.invalid"
		// A/B duplicate first; C stays silent (no snapshot yet) so it is
		// not part of the initial occurrence.
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-addremove@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-addremove@example.invalid"}, 1, false)
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		// B goes fresh absent (must be removed); C becomes fresh present
		// (must be added) -- both true before the concurrent pass.
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
		group.finalize(t, ctx, database, group.nodes[2], []string{"concurrency-addremove@example.invalid"}, 1, false)

		outcomes := runConcurrentEvaluate(ctx, lifecycle, environmentID, accountKey, 2)
		for i, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("worker %d: %v", i, outcome.err)
			}
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("add/remove must never create a second occurrence row")
		}
		assertNodeSetEqual(t,
			listOccurrenceNodeIDs(t, ctx, database, created.OccurrenceID),
			group.nodes[0], group.nodes[2])
	})

	t.Run("concurrent reopen: two workers see a fresh duplicate after RESOLVED history, only one new ACTIVE occurrence is created", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-reopen"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencyreopen", 2)
		accountKey := fixtureProviderName + ":concurrency-reopen@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-reopen@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-reopen@example.invalid"}, 1, false)
		first, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
		if _, err := lifecycle.Evaluate(ctx, environmentID, accountKey); err != nil {
			t.Fatal(err)
		}
		if occurrenceStatus(t, ctx, database, first.OccurrenceID) != "RESOLVED" {
			t.Fatal("setup: prior occurrence must be RESOLVED before reopen")
		}
		// Both Nodes fresh present again: a brand-new duplicate.
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-reopen@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-reopen@example.invalid"}, 1, false)

		outcomes := runConcurrentEvaluate(ctx, lifecycle, environmentID, accountKey, 2)
		reopened := map[string]int{}
		for i, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("worker %d: %v", i, outcome.err)
			}
			if outcome.result == nil {
				t.Fatalf("worker %d: expected a non-nil reopen result", i)
			}
			if outcome.result.OccurrenceID == first.OccurrenceID {
				t.Fatalf("worker %d: reopen must create a brand-new occurrence_id, not mutate the old RESOLVED row", i)
			}
			reopened[outcome.result.OccurrenceID.String()]++
		}
		if len(reopened) != 1 {
			t.Fatalf("reopened occurrence_ids = %v, want exactly one new ACTIVE occurrence", reopened)
		}
		var activeCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrences
			WHERE environment_id=$1 AND account_key=$2 AND status='ACTIVE'`, environmentID, accountKey).Scan(&activeCount); err != nil {
			t.Fatal(err)
		}
		if activeCount != 1 {
			t.Fatalf("ACTIVE occurrence rows = %d, want exactly 1", activeCount)
		}
	})

	t.Run("reconciler vs normal worker: concurrent Reconcile and Evaluate on the same ACTIVE duplicate converge on one occurrence", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-reconciler-worker"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := productstore.NewCrossNodeDuplicateOwnershipRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		reconciler, err := productstore.NewCrossNodeDuplicateOwnershipReconciler(pool, reader, lifecycle)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencyreconciler", 2)
		accountKey := fixtureProviderName + ":concurrency-reconciler@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-reconciler@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-reconciler@example.invalid"}, 1, false)
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var workerErr, reconcilerErr error
		var workerResult *productstore.CrossNodeDuplicateOwnershipEvaluation
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			workerResult, workerErr = lifecycle.Evaluate(ctx, environmentID, accountKey)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, reconcilerErr = reconciler.Reconcile(ctx, environmentID)
		}()
		close(start)
		wg.Wait()

		if workerErr != nil {
			t.Fatalf("normal worker: %v", workerErr)
		}
		if reconcilerErr != nil {
			t.Fatalf("reconciler: %v", reconcilerErr)
		}
		if workerResult == nil || workerResult.OccurrenceID != created.OccurrenceID || workerResult.Status != "ACTIVE" {
			t.Fatalf("normal worker result = %+v, want ACTIVE on the same occurrence_id", workerResult)
		}
		if occurrenceRowCount(t, ctx, database, environmentID, accountKey) != 1 {
			t.Fatal("reconciler and normal worker racing the same key must never create a second occurrence row")
		}
		assertNodeSetEqual(t,
			listOccurrenceNodeIDs(t, ctx, database, created.OccurrenceID),
			group.nodes[0], group.nodes[1])
	})

	t.Run("reconciler vs normal worker: concurrent resolve-eligible pass never leaves the occurrence reverted to ACTIVE", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "concurrency-reconciler-resolve"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		pool := crossNodeDuplicateOwnershipConcurrencyPool(t, database)
		lifecycle, err := productstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := productstore.NewCrossNodeDuplicateOwnershipRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
		reconciler, err := productstore.NewCrossNodeDuplicateOwnershipReconciler(pool, reader, lifecycle)
		if err != nil {
			t.Fatal(err)
		}
		group := newOwnershipNodeGroup(t, ctx, database, "concurrencyreconcilerresolve", 2)
		accountKey := fixtureProviderName + ":concurrency-reconciler-resolve@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"concurrency-reconciler-resolve@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"concurrency-reconciler-resolve@example.invalid"}, 1, false)
		created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
		if err != nil {
			t.Fatal(err)
		}
		// B's next fresh+complete promoted snapshot proves absence: both
		// the reconciler and the normal worker now see resolve-eligible
		// source truth.
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var workerErr, reconcilerErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, workerErr = lifecycle.Evaluate(ctx, environmentID, accountKey)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, reconcilerErr = reconciler.Reconcile(ctx, environmentID)
		}()
		close(start)
		wg.Wait()

		if workerErr != nil {
			t.Fatalf("normal worker: %v", workerErr)
		}
		if reconcilerErr != nil {
			t.Fatalf("reconciler: %v", reconcilerErr)
		}
		if occurrenceStatus(t, ctx, database, created.OccurrenceID) != "RESOLVED" {
			t.Fatal("occurrence must end RESOLVED: neither a reconciler pass racing a normal worker, " +
				"nor the reverse, may revert an already-resolved occurrence back to ACTIVE")
		}
	})
}

// listOccurrenceNodeIDs is a thin query helper for the concurrency
// assertions above: the current affected-node membership for occurrenceID.
func listOccurrenceNodeIDs(t *testing.T, ctx context.Context, database *isolatedJobDatabase, occurrenceID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := database.owner.Query(ctx, `SELECT instance_id FROM cross_node_duplicate_occurrence_nodes
		WHERE occurrence_id=$1`, occurrenceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var nodeIDs []uuid.UUID
	for rows.Next() {
		var nodeID uuid.UUID
		if err := rows.Scan(&nodeID); err != nil {
			t.Fatal(err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return nodeIDs
}
