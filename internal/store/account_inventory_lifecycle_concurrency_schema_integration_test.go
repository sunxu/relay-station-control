package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type preparedLifecyclePoll struct {
	pollID uuid.UUID
	fence  uuid.UUID
}

type lifecycleFinalizePayload struct {
	accountCount int
	providers    []byte
	items        []byte
}

type lifecycleFinalizeResult struct {
	finalized int
	err       error
}

func prepareLifecyclePoll(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
	fixture *lifecycleSchemaFixture,
	scheduledAt time.Time,
) preparedLifecyclePoll {
	t.Helper()
	result := preparedLifecyclePoll{pollID: uuid.New(), fence: uuid.New()}
	var policyID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).
		Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, result.pollID,
		fixture.instanceID, fixture.nodeType, fixture.contract, scheduledAt, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, result.pollID, result.fence); err != nil {
		t.Fatal(err)
	}
	return result
}

func makeLifecycleFinalizePayload(accounts []lifecycleAccount) (lifecycleFinalizePayload, error) {
	providers, err := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "identifiable_count": len(accounts),
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	if err != nil {
		return lifecycleFinalizePayload{}, err
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, map[string]any{
			"provider": fixtureProviderName, "account_key": fixtureProviderName + ":" + account.email,
			"email": account.email, "basic_status": "active",
			"success_count": account.successCount, "failed_count": int64(0),
			"recent_request_count": int64(0), "last_refresh_unix": nil,
			"next_retry_unix": nil, "updated_at_unix": nil,
		})
	}
	encodedItems, err := json.Marshal(items)
	if err != nil {
		return lifecycleFinalizePayload{}, err
	}
	return lifecycleFinalizePayload{
		accountCount: len(accounts), providers: providers, items: encodedItems,
	}, nil
}

type lifecycleQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func finalizeLifecyclePoll(
	ctx context.Context,
	querier lifecycleQueryRower,
	poll preparedLifecyclePoll,
	payload lifecycleFinalizePayload,
) (int, error) {
	var finalized int
	err := querier.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			$3,$3,0,0,0,'v1.0.0','abcdef1',$4::jsonb,$5::jsonb,'[]'::jsonb
		)`, poll.pollID, poll.fence, payload.accountCount, payload.providers, payload.items).
		Scan(&finalized)
	return finalized, err
}

func setLifecycleApplicationName(
	ctx context.Context, connection *pgxpool.Conn, name string,
) error {
	_, err := connection.Exec(ctx, `SELECT set_config('application_name',$1,false)`, name)
	return err
}

func waitForLifecycleLock(
	ctx context.Context, database *isolatedJobDatabase, applicationName string,
) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := database.owner.QueryRow(ctx, `SELECT coalesce(bool_or(wait_event_type='Lock'),false)
			FROM pg_stat_activity
			WHERE datname=current_database() AND application_name=$1`, applicationName).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func lockLifecycleBinding(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, fixture *lifecycleSchemaFixture,
) pgx.Tx {
	t.Helper()
	lock, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `SELECT 1 FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2 FOR UPDATE`,
		fixture.nodeType, fixture.contract); err != nil {
		_ = lock.Rollback(ctx)
		t.Fatal(err)
	}
	return lock
}

func TestAccountInventoryLifecycleConcurrentFinalizeAndScopeTransition(t *testing.T) {
	for _, scopeFirst := range []bool{false, true} {
		name := "finalize_first"
		if scopeFirst {
			name = "scope_first"
		}
		t.Run(name, func(t *testing.T) {
			database := newIsolatedJobDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			fixture := newLifecycleSchemaFixture(t, ctx, database)
			baselinePoll := fixture.finalize(t, ctx, database, []lifecycleAccount{{
				email: "scope-race@example.invalid", successCount: 1,
			}})
			poll := prepareLifecyclePoll(t, ctx, database, fixture,
				fixture.baseSlot.Add(time.Duration(fixture.nextPoll)*5*time.Minute))
			payload, err := makeLifecycleFinalizePayload(nil)
			if err != nil {
				t.Fatal(err)
			}

			finalizeConnection, err := database.runtime.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer finalizeConnection.Release()
			scopeConnection, err := database.owner.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer scopeConnection.Release()
			finalizeName := "lifecycle_scope_race_finalize"
			scopeName := "lifecycle_scope_race_policy"
			if err := setLifecycleApplicationName(ctx, finalizeConnection, finalizeName); err != nil {
				t.Fatal(err)
			}
			if err := setLifecycleApplicationName(ctx, scopeConnection, scopeName); err != nil {
				t.Fatal(err)
			}

			bindingLock := lockLifecycleBinding(t, ctx, database, fixture)
			finalizeResult := make(chan lifecycleFinalizeResult, 1)
			scopeResult := make(chan struct {
				activationID uuid.UUID
				err          error
			}, 1)
			startFinalize := func() {
				go func() {
					finalized, finalizeErr := finalizeLifecyclePoll(ctx, finalizeConnection, poll, payload)
					finalizeResult <- lifecycleFinalizeResult{finalized: finalized, err: finalizeErr}
				}()
			}
			startScope := func() {
				go func() {
					var activationID uuid.UUID
					scopeErr := scopeConnection.QueryRow(ctx, `SELECT
						public.control_activate_provider_policy_with_lifecycle(
							$1,$2,ARRAY['legacy'],ARRAY['openai'],
							'concurrency-test','concurrent scope transition',NULL
						)`, fixture.nodeType, fixture.contract).Scan(&activationID)
					scopeResult <- struct {
						activationID uuid.UUID
						err          error
					}{activationID: activationID, err: scopeErr}
				}()
			}
			firstName, secondName := finalizeName, scopeName
			first, second := startFinalize, startScope
			if scopeFirst {
				firstName, secondName = scopeName, finalizeName
				first, second = startScope, startFinalize
			}
			first()
			if err := waitForLifecycleLock(ctx, database, firstName); err != nil {
				_ = bindingLock.Rollback(ctx)
				t.Fatal(err)
			}
			second()
			if err := waitForLifecycleLock(ctx, database, secondName); err != nil {
				_ = bindingLock.Rollback(ctx)
				t.Fatal(err)
			}
			if err := bindingLock.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			finalized := <-finalizeResult
			scope := <-scopeResult
			if finalized.err != nil || finalized.finalized != 1 || scope.err != nil || scope.activationID == uuid.Nil {
				t.Fatalf("concurrent operations did not finish atomically: finalize=%d/%v scope=%v",
					finalized.finalized, finalized.err, scope.err)
			}
			var providerStatus, lifecycle string
			var providerPointer, accountPointer uuid.UUID
			var missingCount int
			var promotionApplied bool
			var skipReason *string
			var providerOutAt, accountOutAt, auditAt time.Time
			var matchingAudits int
			if err := database.owner.QueryRow(ctx, `SELECT
				state.monitoring_status,state.current_poll_run_id,state.out_of_scope_since,
				account.lifecycle,account.consecutive_missing_count,account.current_poll_run_id,
				account.out_of_scope_since,result.promotion_applied,result.promotion_skipped_reason,
				(SELECT transitioned_at FROM account_inventory_scope_transition_audits
				 WHERE activation_id=$3),
				(SELECT count(*) FROM account_inventory_scope_transition_audits
				 WHERE activation_id=$3 AND moved_out_providers=ARRAY['openai']
				 AND reactivated_providers=ARRAY['legacy'])
			FROM account_inventory_provider_states AS state
			JOIN account_inventory AS account USING (instance_id,provider)
			JOIN account_inventory_poll_provider_results AS result
			  ON result.poll_run_id=$2 AND result.provider=state.provider
			WHERE state.instance_id=$1 AND state.provider='openai'`,
				fixture.instanceID, poll.pollID, scope.activationID).Scan(
				&providerStatus, &providerPointer, &providerOutAt,
				&lifecycle, &missingCount, &accountPointer, &accountOutAt,
				&promotionApplied, &skipReason, &auditAt, &matchingAudits,
			); err != nil {
				t.Fatal(err)
			}
			if providerStatus != "out_of_scope" || lifecycle != "out_of_scope" || missingCount != 0 ||
				accountPointer != baselinePoll || !providerOutAt.Equal(accountOutAt) ||
				!providerOutAt.Equal(auditAt) || matchingAudits != 1 {
				t.Fatal("scope/finalize race left mixed Provider, account, or audit state")
			}
			if scopeFirst {
				if promotionApplied || skipReason == nil || *skipReason != "policy_changed" ||
					providerPointer != baselinePoll {
					t.Fatal("scope-first race did not preserve the prior snapshot atomically")
				}
			} else if !promotionApplied || skipReason != nil || providerPointer != poll.pollID {
				t.Fatal("finalize-first race did not publish before the atomic scope transition")
			}
		})
	}
}

func TestAccountInventoryLifecycleConcurrentSlotsAdvanceOnlyNewestOnce(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	baseline := fixture.finalize(t, ctx, database, []lifecycleAccount{{
		email: "slot-race@example.invalid", successCount: 1,
	}})
	oldPoll := prepareLifecyclePoll(t, ctx, database, fixture,
		fixture.baseSlot.Add(time.Duration(fixture.nextPoll)*5*time.Minute))
	newPoll := prepareLifecyclePoll(t, ctx, database, fixture,
		fixture.baseSlot.Add(time.Duration(fixture.nextPoll+1)*5*time.Minute))
	payload, err := makeLifecycleFinalizePayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	newConnection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer newConnection.Release()
	oldConnection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer oldConnection.Release()
	if err := setLifecycleApplicationName(ctx, newConnection, "lifecycle_slot_new"); err != nil {
		t.Fatal(err)
	}
	if err := setLifecycleApplicationName(ctx, oldConnection, "lifecycle_slot_old"); err != nil {
		t.Fatal(err)
	}
	bindingLock := lockLifecycleBinding(t, ctx, database, fixture)
	newResult := make(chan lifecycleFinalizeResult, 1)
	oldResult := make(chan lifecycleFinalizeResult, 1)
	go func() {
		finalized, finalizeErr := finalizeLifecyclePoll(ctx, newConnection, newPoll, payload)
		newResult <- lifecycleFinalizeResult{finalized: finalized, err: finalizeErr}
	}()
	if err := waitForLifecycleLock(ctx, database, "lifecycle_slot_new"); err != nil {
		_ = bindingLock.Rollback(ctx)
		t.Fatal(err)
	}
	go func() {
		finalized, finalizeErr := finalizeLifecyclePoll(ctx, oldConnection, oldPoll, payload)
		oldResult <- lifecycleFinalizeResult{finalized: finalized, err: finalizeErr}
	}()
	if err := waitForLifecycleLock(ctx, database, "lifecycle_slot_old"); err != nil {
		_ = bindingLock.Rollback(ctx)
		t.Fatal(err)
	}
	if err := bindingLock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	newFinalized, oldFinalized := <-newResult, <-oldResult
	if newFinalized.err != nil || oldFinalized.err != nil ||
		newFinalized.finalized != 1 || oldFinalized.finalized != 1 {
		t.Fatalf("concurrent slots did not finish: new=%d/%v old=%d/%v",
			newFinalized.finalized, newFinalized.err, oldFinalized.finalized, oldFinalized.err)
	}
	var pointer, accountSource uuid.UUID
	var lifecycle string
	var missingCount int
	var newApplied, oldApplied bool
	var newReason, oldReason *string
	if err := database.owner.QueryRow(ctx, `SELECT
		state.current_poll_run_id,account.current_poll_run_id,
		account.lifecycle,account.consecutive_missing_count,
		new_result.promotion_applied,new_result.promotion_skipped_reason,
		old_result.promotion_applied,old_result.promotion_skipped_reason
		FROM account_inventory_provider_states AS state
		JOIN account_inventory AS account USING (instance_id,provider)
		JOIN account_inventory_poll_provider_results AS new_result
		  ON new_result.poll_run_id=$2 AND new_result.provider=state.provider
		JOIN account_inventory_poll_provider_results AS old_result
		  ON old_result.poll_run_id=$3 AND old_result.provider=state.provider
		WHERE state.instance_id=$1 AND state.provider='openai'`,
		fixture.instanceID, newPoll.pollID, oldPoll.pollID).Scan(
		&pointer, &accountSource, &lifecycle, &missingCount,
		&newApplied, &newReason, &oldApplied, &oldReason,
	); err != nil {
		t.Fatal(err)
	}
	if pointer != newPoll.pollID || accountSource != baseline || lifecycle != "suspected_missing" ||
		missingCount != 1 || !newApplied || newReason != nil || oldApplied ||
		oldReason == nil || *oldReason != "stale_poll" {
		t.Fatal("concurrent old/new slots did not advance only the newest slot once")
	}
}

type lifecycleCapacityMeasurement struct {
	elapsed  time.Duration
	lockWait time.Duration
	walBytes int64
}

func measureLifecycleFinalize(
	ctx context.Context,
	database *isolatedJobDatabase,
	connection *pgxpool.Conn,
	applicationName string,
	poll preparedLifecyclePoll,
	payload lifecycleFinalizePayload,
) (lifecycleCapacityMeasurement, error) {
	if err := setLifecycleApplicationName(ctx, connection, applicationName); err != nil {
		return lifecycleCapacityMeasurement{}, err
	}
	if _, err := connection.Exec(ctx, `SELECT set_config('statement_timeout','30s',false)`); err != nil {
		return lifecycleCapacityMeasurement{}, err
	}
	var beforeLSN string
	if err := database.owner.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&beforeLSN); err != nil {
		return lifecycleCapacityMeasurement{}, err
	}

	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	var monitor sync.WaitGroup
	monitor.Add(1)
	lockWait := time.Duration(0)
	waitingSince := time.Time{}
	monitorErrors := make(chan error, 1)
	go func() {
		defer func() {
			if !waitingSince.IsZero() {
				lockWait += time.Since(waitingSince)
			}
			monitor.Done()
		}()
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			err := database.owner.QueryRow(monitorCtx, `SELECT coalesce(bool_or(wait_event_type='Lock'),false)
				FROM pg_stat_activity
				WHERE datname=current_database() AND application_name=$1`, applicationName).Scan(&waiting)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					monitorErrors <- err
				}
				return
			}
			if waiting && waitingSince.IsZero() {
				waitingSince = time.Now()
			} else if !waiting && !waitingSince.IsZero() {
				lockWait += time.Since(waitingSince)
				waitingSince = time.Time{}
			}
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	startedAt := time.Now()
	finalized, finalizeErr := finalizeLifecyclePoll(ctx, connection, poll, payload)
	elapsed := time.Since(startedAt)
	stopMonitor()
	monitor.Wait()
	select {
	case monitorErr := <-monitorErrors:
		return lifecycleCapacityMeasurement{}, monitorErr
	default:
	}
	if finalizeErr != nil {
		return lifecycleCapacityMeasurement{}, finalizeErr
	}
	if finalized != 1 {
		return lifecycleCapacityMeasurement{}, fmt.Errorf("finalized rows=%d", finalized)
	}
	var afterLSN string
	if err := database.owner.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&afterLSN); err != nil {
		return lifecycleCapacityMeasurement{}, err
	}
	var walBytes int64
	if err := database.owner.QueryRow(ctx,
		`SELECT pg_wal_lsn_diff($1::pg_lsn,$2::pg_lsn)::bigint`, afterLSN, beforeLSN).
		Scan(&walBytes); err != nil {
		return lifecycleCapacityMeasurement{}, err
	}
	return lifecycleCapacityMeasurement{
		elapsed: elapsed, lockWait: lockWait, walBytes: walBytes,
	}, nil
}

func TestAccountInventoryLifecycleCapacityThousandAccounts(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	accounts := make([]lifecycleAccount, 1000)
	for index := range accounts {
		accounts[index] = lifecycleAccount{
			email:        fmt.Sprintf("capacity-%04d@example.invalid", index),
			successCount: int64(index),
		}
	}
	fullPayload, err := makeLifecycleFinalizePayload(accounts)
	if err != nil {
		t.Fatal(err)
	}
	emptyPayload, err := makeLifecycleFinalizePayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	fullPoll := prepareLifecyclePoll(t, ctx, database, fixture, fixture.baseSlot)
	emptyPoll := prepareLifecyclePoll(t, ctx, database, fixture, fixture.baseSlot.Add(5*time.Minute))
	connection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	full, err := measureLifecycleFinalize(
		ctx, database, connection, "lifecycle_capacity_full", fullPoll, fullPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := measureLifecycleFinalize(
		ctx, database, connection, "lifecycle_capacity_empty", emptyPoll, emptyPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	const walUpperBound = int64(64 << 20)
	if full.elapsed >= 30*time.Second || empty.elapsed >= 30*time.Second {
		t.Fatalf("capacity transaction exceeded 30s: full=%s empty=%s", full.elapsed, empty.elapsed)
	}
	if full.lockWait >= 5*time.Second || empty.lockWait >= 5*time.Second {
		t.Fatalf("capacity lock wait exceeded 5s: full=%s empty=%s", full.lockWait, empty.lockWait)
	}
	if full.walBytes < 0 || full.walBytes > walUpperBound ||
		empty.walBytes < 0 || empty.walBytes > walUpperBound {
		t.Fatalf("capacity WAL exceeded %d bytes: full=%d empty=%d",
			walUpperBound, full.walBytes, empty.walBytes)
	}
	var accountRows, suspectedRows, fullSnapshotRows, emptySnapshotRows int
	var pointer uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1),
		(SELECT count(*) FROM account_inventory
		 WHERE instance_id=$1 AND lifecycle='suspected_missing'
		 AND consecutive_missing_count=1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$2),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$3),
		(SELECT current_poll_run_id FROM account_inventory_provider_states
		 WHERE instance_id=$1 AND provider='openai')`,
		fixture.instanceID, fullPoll.pollID, emptyPoll.pollID).Scan(
		&accountRows, &suspectedRows, &fullSnapshotRows, &emptySnapshotRows, &pointer,
	); err != nil {
		t.Fatal(err)
	}
	if accountRows != 1000 || suspectedRows != 1000 || fullSnapshotRows != 1000 ||
		emptySnapshotRows != 0 || pointer != emptyPoll.pollID {
		t.Fatal("capacity promotions did not publish one complete and one empty snapshot atomically")
	}
	t.Logf("lifecycle_capacity accounts=%d full_ms=%d empty_ms=%d lock_wait_ms=%d wal_full_bytes=%d wal_empty_bytes=%d",
		accountRows, full.elapsed.Milliseconds(), empty.elapsed.Milliseconds(),
		(full.lockWait + empty.lockWait).Milliseconds(), full.walBytes, empty.walBytes)
}
