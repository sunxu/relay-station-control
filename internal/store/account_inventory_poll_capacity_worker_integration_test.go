package store_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
)

// This is deliberately opt-in: it creates an isolated migrated PostgreSQL
// database and exercises the real claim/finalize functions through Worker.
func TestAccountInventoryPollCapacityWorkerSlowDriver(t *testing.T) {
	if os.Getenv("CONTROL_POLL_CAPACITY_WORKER_ACCEPTANCE") != "1" {
		t.Skip("slow worker capacity acceptance is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleWorkerFixture(t, ctx, database)
	const nodes = 50
	const concurrency = 25
	const grace = 120 * time.Second
	for index := 1; index < nodes; index++ {
		instanceID := uuid.New()
		if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,
			management_endpoint,reader_secret_ref
		) VALUES ($1,$2,$3,$4,$5,$6)`, instanceID,
			fmt.Sprintf("Slow Capacity Node %02d", index), fixture.nodeType, fixture.contract,
			fmt.Sprintf("http://slow-capacity-%02d.example.invalid", index),
			"docker-secret://synthetic/slow-capacity-reader"); err != nil {
			t.Fatal(err)
		}
	}
	var scheduledSlot time.Time
	for {
		var databaseNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp(),date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01')`).Scan(&databaseNow, &scheduledSlot); err != nil {
			t.Fatal(err)
		}
		if databaseNow.Before(scheduledSlot.Add(5 * time.Second)) {
			break
		}
		waitUntil := scheduledSlot.Add(5 * time.Minute).Add(2 * time.Second)
		t.Logf("waiting for fresh database slot; current slot age=%s", databaseNow.Sub(scheduledSlot).Round(time.Second))
		for {
			remaining := time.Until(waitUntil)
			if remaining <= 0 {
				break
			}
			t.Logf("waiting for fresh slot: remaining=%s", remaining.Round(time.Second))
			select {
			case <-time.After(min(30*time.Second, remaining)):
			case <-ctx.Done():
				t.Fatal("timed out waiting for a fresh database slot")
			}
		}
	}
	var policyID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT policy_version_id FROM provider_inventory_policy_bindings WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, nodes)
	for range nodes {
		id := uuid.New()
		ids = append(ids, id)
		if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
		) SELECT $1,instance_id,$2,$3,date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01'),$4,2,$5,clock_timestamp()
		FROM relay_node_assets ORDER BY instance_id LIMIT 1 OFFSET $6`, id, fixture.nodeType, fixture.contract, policyID, int(grace.Seconds()), len(ids)-1); err != nil {
			t.Fatal(err)
		}
	}
	var inserted int
	if err := database.owner.QueryRow(ctx, "SELECT count(*) FROM account_inventory_poll_runs").Scan(&inserted); err != nil || inserted != nodes {
		t.Fatalf("fixture poll rows=%d want=%d err=%v", inserted, nodes, err)
	}
	t.Logf("slow worker starting: fixture polls=%d slot=%s", inserted, scheduledSlot)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	driver := &slowCapacityDriver{delay: 14 * time.Second}
	slowRepository := &slowFinalizeRepository{InventoryPollRepository: repository, delay: 9 * time.Second}
	var dispatchMu sync.Mutex
	dispatchTimes := make([]time.Time, 0, nodes)
	var observers atomic.Int32
	worker, err := inventorypoll.NewWorker(slowRepository, driver, inventorypoll.Config{
		Period: 5 * time.Minute, PollStartGrace: grace, Concurrency: concurrency,
		WorstCasePollDuration: 15 * time.Second, LeaseDuration: 30 * time.Second,
		MaxAttempts: 2, DispatchMargin: 10 * time.Second, FinalizeMargin: 10 * time.Second,
		SchedulerInterval: 5 * time.Minute, WorkerScanInterval: 10 * time.Millisecond,
		ReconcileInterval: time.Second, DatabaseBackoffInitial: time.Second,
		DatabaseBackoffMaximum: 5 * time.Second, ShutdownGrace: 5 * time.Second,
		ReconcileLimit: nodes,
		Observer: pollCapacityObserver{record: func(event inventorypoll.Event) {
			if event.Result == inventorypoll.EventResultFailure {
				t.Logf("worker failure: action=%s reason=%s", event.Action, event.Reason)
			}
			if event.Action == inventorypoll.EventActionDispatch {
				dispatchMu.Lock()
				dispatchTimes = append(dispatchTimes, time.Now())
				dispatchMu.Unlock()
			}
		}},
		LifecycleObserver: func(observerCtx context.Context) {
			timer := time.NewTimer(29 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				observers.Add(1)
			case <-observerCtx.Done():
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()
	defer stop()
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		var finalized, pending, running, retry int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='finalized'),count(*) FILTER (WHERE status='pending'),count(*) FILTER (WHERE status='running'),count(*) FILTER (WHERE status='retry_wait') FROM account_inventory_poll_runs`).Scan(&finalized, &pending, &running, &retry); err != nil {
			stop()
			t.Fatal(err)
		}
		if finalized == nodes && pending == 0 && running == 0 && retry == 0 && observers.Load() == nodes {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var finalized, terminal, nonfirstAttempts int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='finalized'),count(*) FILTER (WHERE status IN ('finalized','abandoned')),count(*) FILTER (WHERE attempt_count <> 1) FROM account_inventory_poll_runs`).Scan(&finalized, &terminal, &nonfirstAttempts); err != nil {
		t.Fatal(err)
	}
	if finalized != nodes || terminal != nodes || nonfirstAttempts != 0 {
		t.Fatalf("terminal states finalized=%d terminal=%d nonfirst_attempts=%d", finalized, terminal, nonfirstAttempts)
	}
	dispatchMu.Lock()
	dispatchCount := len(dispatchTimes)
	dispatchMu.Unlock()
	if dispatchCount != nodes || driver.maximum.Load() > concurrency {
		t.Fatalf("dispatches=%d want=%d maximum_concurrency=%d want<=%d", dispatchCount, nodes, driver.maximum.Load(), concurrency)
	}
	if observers.Load() != nodes {
		t.Fatalf("completed lifecycle observers=%d want=%d", observers.Load(), nodes)
	}
	dispatchMu.Lock()
	if dispatchCount >= concurrency+1 && dispatchTimes[concurrency].Sub(dispatchTimes[0]) < 51*time.Second {
		dispatchMu.Unlock()
		t.Fatalf("second batch dispatched too early: first_to_second=%s", dispatchTimes[concurrency].Sub(dispatchTimes[0]))
	}
	dispatchMu.Unlock()
	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	for _, dispatchedAt := range dispatchTimes {
		if !dispatchedAt.Before(scheduledSlot.Add(grace)) {
			t.Fatalf("first dispatch at %s exceeded grace deadline %s", dispatchedAt, scheduledSlot.Add(grace))
		}
	}
	t.Logf("slow_worker_capacity=passed nodes=%d concurrency=%d maximum_concurrency=%d first_dispatches=%d lifecycle_observers=%d nonfirst_attempts=%d terminal_states=%d", nodes, concurrency, driver.maximum.Load(), dispatchCount, observers.Load(), nonfirstAttempts, terminal)
}

type slowFinalizeRepository struct {
	*pollstore.InventoryPollRepository
	delay time.Duration
}

func (r *slowFinalizeRepository) FinalizeFenced(ctx context.Context, request inventorypoll.FinalizeRequest) error {
	timer := time.NewTimer(r.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return r.InventoryPollRepository.FinalizeFenced(ctx, request)
	case <-ctx.Done():
		return ctx.Err()
	}
}

type pollCapacityObserver struct {
	record func(inventorypoll.Event)
}

func (o pollCapacityObserver) Observe(ctx context.Context, event inventorypoll.Event) {
	o.record(event)
	_ = ctx
}

type slowCapacityDriver struct {
	delay   time.Duration
	current atomic.Int32
	maximum atomic.Int32
	retries atomic.Int32
}

func (d *slowCapacityDriver) ListAccountInventory(ctx context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
	active := d.current.Add(1)
	defer d.current.Add(-1)
	for old := d.maximum.Load(); active > old && !d.maximum.CompareAndSwap(old, active); old = d.maximum.Load() {
	}
	select {
	case <-time.After(d.delay):
	case <-ctx.Done():
		return drivers.InventoryObservation{}, ctx.Err()
	}
	return lifecycleCompleteObservation(nil), nil
}
