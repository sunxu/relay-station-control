package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const lifecycleCapacityAccountsPerNode = 1000

type lifecycleCapacityPoll struct {
	instanceID uuid.UUID
	pollID     uuid.UUID
	fence      uuid.UUID
}

func TestAccountInventoryLifecycleCapacityOneTenFifty(t *testing.T) {
	if os.Getenv("CONTROL_LIFECYCLE_CAPACITY_ACCEPTANCE") != "1" {
		t.Skip("lifecycle capacity acceptance is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)

	runtimeConfig, err := pgxpool.ParseConfig(database.runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig.MaxConns = 10
	runtimeConfig.MinConns = 0
	runtime, err := pgxpool.NewWithConfig(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	providerJSON, snapshotJSON := lifecycleCapacityPayload(t)
	polls := make([]lifecycleCapacityPoll, 50)
	for index := range polls {
		instanceID := fixture.instanceID
		if index > 0 {
			instanceID = uuid.New()
			if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
				instance_id,display_name,node_type,driver_contract_version,
				management_endpoint,reader_secret_ref
			) VALUES ($1,'Lifecycle Capacity Node',$2,$3,$4,
				'docker-secret://synthetic/lifecycle-capacity-reader')`, instanceID,
				fixture.nodeType, fixture.contract, fmt.Sprintf("http://capacity-%02d.example.invalid", index)); err != nil {
				t.Fatal(err)
			}
		}
		polls[index] = lifecycleCapacityPoll{instanceID: instanceID, pollID: uuid.New(), fence: uuid.New()}
		if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
		) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, polls[index].pollID,
			instanceID, fixture.nodeType, fixture.contract, fixture.baseSlot, fixture.policyID); err != nil {
			t.Fatal(err)
		}
	}

	type checkpoint struct{ total, first int }
	checkpoints := []checkpoint{{total: 1, first: 0}, {total: 10, first: 1}, {total: 50, first: 10}}
	totalStarted := time.Now()
	var totalWALBytes int64
	for _, point := range checkpoints {
		batch := polls[point.first:point.total]
		for _, poll := range batch {
			if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
				SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
				last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '30 seconds',
				lease_fencing_token=$2 WHERE poll_run_id=$1`, poll.pollID, poll.fence); err != nil {
				t.Fatal(err)
			}
		}
		var walStart string
		if err := database.owner.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&walStart); err != nil {
			t.Fatal(err)
		}
		stopSampling := make(chan struct{})
		var maximumLockWaiters atomic.Int64
		samplingDone := make(chan struct{})
		go sampleLifecycleLockWaiters(ctx, database.owner, stopSampling, samplingDone, &maximumLockWaiters)

		type result struct {
			duration time.Duration
			affected int
			err      error
		}
		results := make(chan result, len(batch))
		semaphore := make(chan struct{}, 10)
		var workers sync.WaitGroup
		batchStarted := time.Now()
		for _, poll := range batch {
			poll := poll
			workers.Add(1)
			go func() {
				defer workers.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				started := time.Now()
				var affected int
				err := runtime.QueryRow(ctx, `SELECT count(*)
					FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
						$1,$2,true,true,true,'runtime',true,true,false,'success','none',
						$3,$3,0,0,0,'v1.0.0','abcdef1',$4::jsonb,$5::jsonb,'[]'::jsonb
					)`, poll.pollID, poll.fence, lifecycleCapacityAccountsPerNode,
					providerJSON, snapshotJSON).Scan(&affected)
				results <- result{duration: time.Since(started), affected: affected, err: err}
			}()
		}
		workers.Wait()
		close(results)
		close(stopSampling)
		<-samplingDone
		batchDuration := time.Since(batchStarted)
		var maximumTransaction time.Duration
		for outcome := range results {
			if outcome.err != nil || outcome.affected != 1 {
				t.Fatalf("nodes=%d lifecycle finalize affected=%d err=%v", point.total, outcome.affected, outcome.err)
			}
			if outcome.duration > maximumTransaction {
				maximumTransaction = outcome.duration
			}
		}
		var walBytes int64
		if err := database.owner.QueryRow(ctx,
			`SELECT pg_wal_lsn_diff(pg_current_wal_lsn(),$1::pg_lsn)::bigint`, walStart).Scan(&walBytes); err != nil {
			t.Fatal(err)
		}
		totalWALBytes += walBytes
		var lifecycleRows int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory`).Scan(&lifecycleRows); err != nil {
			t.Fatal(err)
		}
		if lifecycleRows != point.total*lifecycleCapacityAccountsPerNode || walBytes <= 0 ||
			maximumTransaction >= 30*time.Second || batchDuration >= 120*time.Second || maximumLockWaiters.Load() > 9 {
			t.Fatalf("nodes=%d rows=%d wal_bytes=%d max_tx=%s batch=%s max_lock_waiters=%d",
				point.total, lifecycleRows, walBytes, maximumTransaction, batchDuration, maximumLockWaiters.Load())
		}
		t.Logf("lifecycle_capacity=passed nodes=%d accounts_per_node=%d rows=%d wal_bytes=%d max_transaction_ms=%d batch_ms=%d max_lock_waiters=%d",
			point.total, lifecycleCapacityAccountsPerNode, lifecycleRows, walBytes,
			maximumTransaction.Milliseconds(), batchDuration.Milliseconds(), maximumLockWaiters.Load())
	}
	if time.Since(totalStarted) >= 120*time.Second || totalWALBytes > 512<<20 {
		t.Fatalf("lifecycle capacity budget exceeded")
	}
}

func lifecycleCapacityPayload(t *testing.T) ([]byte, []byte) {
	t.Helper()
	providerJSON, err := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "identifiable_count": lifecycleCapacityAccountsPerNode,
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	if err != nil {
		t.Fatal(err)
	}
	items := make([]map[string]any, 0, lifecycleCapacityAccountsPerNode)
	for index := 0; index < lifecycleCapacityAccountsPerNode; index++ {
		email := fmt.Sprintf("capacity-%04d@example.invalid", index)
		items = append(items, map[string]any{
			"provider": fixtureProviderName, "account_key": fixtureProviderName + ":" + email,
			"email": email, "basic_status": "active", "success_count": int64(index),
			"failed_count": int64(0), "recent_request_count": int64(0),
			"last_refresh_unix": nil, "next_retry_unix": nil, "updated_at_unix": nil,
		})
	}
	snapshotJSON, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return providerJSON, snapshotJSON
}

func sampleLifecycleLockWaiters(
	ctx context.Context, owner *pgxpool.Pool, stop <-chan struct{}, done chan<- struct{}, maximum *atomic.Int64,
) {
	defer close(done)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			var waiters int64
			if err := owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname=current_database() AND usename='relay_control_app_dev'
				  AND wait_event_type='Lock'`).Scan(&waiters); err != nil {
				continue
			}
			for observed := maximum.Load(); waiters > observed && !maximum.CompareAndSwap(observed, waiters); observed = maximum.Load() {
			}
		}
	}
}
