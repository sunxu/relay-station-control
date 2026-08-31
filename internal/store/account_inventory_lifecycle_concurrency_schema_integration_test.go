package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	productstore "github.com/sunxu/relay-station-control/internal/store"
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
			pollSlot := fixture.baseSlot.Add(time.Duration(fixture.nextPoll) * 5 * time.Minute)
			poll := prepareLifecyclePoll(t, ctx, database, fixture, pollSlot)
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
			var providerOutAt, accountOutAt, auditAt, healthScheduledAt time.Time
			var matchingAudits int
			if err := database.owner.QueryRow(ctx, `SELECT
				state.monitoring_status,state.current_poll_run_id,state.out_of_scope_since,
				state.health_scheduled_at,
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
				&providerStatus, &providerPointer, &providerOutAt, &healthScheduledAt,
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
			expectedHealthSlot := pollSlot
			if scopeFirst {
				expectedHealthSlot = fixture.baseSlot
			}
			if !healthScheduledAt.Equal(expectedHealthSlot) {
				t.Fatalf("scope/finalize health slot=%s, want %s", healthScheduledAt, expectedHealthSlot)
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

func TestAccountInventoryHistoryConcurrentRetentionQueryPromotionAndScope(t *testing.T) {
	type retentionResult struct {
		processed int
		deleted   int
		err       error
	}
	type scopeResult struct {
		activationID uuid.UUID
		err          error
	}
	for _, scopeFirst := range []bool{false, true} {
		name := "retention_first"
		if scopeFirst {
			name = "scope_first"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			fixture := newReadonlyQueryFixture(t, ctx, nil)
			database, lifecycle := fixture.database, fixture.lifecycle
			if err := database.owner.QueryRow(ctx, `SELECT
				(((clock_timestamp() AT TIME ZONE 'UTC')::date-34)::timestamp
				 AT TIME ZONE 'UTC')+interval '12 hours'`).Scan(&lifecycle.baseSlot); err != nil {
				t.Fatal(err)
			}
			zPoll := lifecycle.finalize(t, ctx, database, []lifecycleAccount{{
				email: "four-way-z@example.invalid", successCount: 1,
			}})
			aPoll := lifecycle.finalize(t, ctx, database, []lifecycleAccount{{
				email: "four-way-a@example.invalid", successCount: 1,
			}})
			if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
				DISABLE TRIGGER account_inventory_snapshot_items_immutable`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.owner.Exec(ctx, `DELETE FROM account_inventory_snapshot_items
				WHERE poll_run_id=ANY($1::uuid[])`, []uuid.UUID{zPoll, aPoll}); err != nil {
				t.Fatal(err)
			}
			if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
				ENABLE TRIGGER account_inventory_snapshot_items_immutable`); err != nil {
				t.Fatal(err)
			}
			summaryDate := lifecycle.baseSlot.UTC().Truncate(24 * time.Hour)
			var compactionID uuid.UUID
			if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
				summary_date,instance_id,provider_policy_version,status,checksum_version,
				source_snapshot_count,source_poll_count,source_provider_result_count,
				source_duplicate_count,source_checksum,deleted_snapshot_count,created_at,
				summarized_at,deleting_at,completed_at,updated_at
			) VALUES($1,$2,$3,'completed',1,2,2,2,0,decode(repeat('81',32),'hex'),2,
				$1::date+interval '1 day',$1::date+interval '1 day 1 hour',
				$1::date+interval '1 day 2 hours',$1::date+interval '1 day 3 hours',
				$1::date+interval '1 day 3 hours') RETURNING compaction_run_id`, summaryDate,
				lifecycle.instanceID, lifecycle.policyID).Scan(&compactionID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_rollup_runs(
				summary_date,instance_id,status,completed_fencing_token,expected_segment_count,
				completed_segment_count,checksum_version,segment_checksum,created_at,completed_at,updated_at
			) VALUES($1,$2,'completed',$3,1,1,1,decode(repeat('82',32),'hex'),
				$1::date+interval '1 hour',$1::date+interval '2 hours',$1::date+interval '2 hours')`,
				summaryDate, lifecycle.instanceID, uuid.New()); err != nil {
				t.Fatal(err)
			}

			var currentSlot time.Time
			if err := database.owner.QueryRow(ctx, `SELECT date_bin(
				interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01')`).Scan(&currentSlot); err != nil {
				t.Fatal(err)
			}
			newPoll := prepareLifecyclePoll(t, ctx, database, lifecycle, currentSlot)
			payload, err := makeLifecycleFinalizePayload([]lifecycleAccount{
				{email: "four-way-a@example.invalid", successCount: 2},
				{email: "four-way-z@example.invalid", successCount: 2},
			})
			if err != nil {
				t.Fatal(err)
			}

			var definition string
			if err := database.owner.QueryRow(ctx, `SELECT pg_get_functiondef(
				'public.control_delete_account_inventory_poll_retention_v1(integer)'::regprocedure)`).
				Scan(&definition); err != nil {
				t.Fatal(err)
			}
			normalized := strings.Join(strings.Fields(definition), " ")
			fragments := []string{
				"INTO target_poll_ids FROM (",
				"FROM public.account_inventory_provider_states AS state WHERE state.current_poll_run_id=ANY(target_poll_ids) ORDER BY state.instance_id,state.provider FOR UPDATE;",
				"FROM public.account_inventory AS account WHERE account.current_poll_run_id=ANY(target_poll_ids) ORDER BY account.instance_id,account.provider,account.account_key FOR UPDATE;",
				"FOR target_poll IN SELECT poll.poll_run_id",
				"DELETE FROM public.account_inventory_poll_runs",
			}
			last := -1
			for _, fragment := range fragments {
				index := strings.Index(normalized, fragment)
				if index <= last {
					t.Fatalf("poll retention current lock order drift: %q", fragment)
				}
				last = index
			}

			var providerPointer, aPointer, zPointer string
			var aLifecycle, zLifecycle string
			if err := database.owner.QueryRow(ctx, `SELECT
				state.current_poll_run_id::text,
				(SELECT current_poll_run_id::text FROM account_inventory
				 WHERE instance_id=$1 AND normalized_email='four-way-a@example.invalid'),
				(SELECT lifecycle FROM account_inventory
				 WHERE instance_id=$1 AND normalized_email='four-way-a@example.invalid'),
				(SELECT current_poll_run_id::text FROM account_inventory
				 WHERE instance_id=$1 AND normalized_email='four-way-z@example.invalid'),
				(SELECT lifecycle FROM account_inventory
				 WHERE instance_id=$1 AND normalized_email='four-way-z@example.invalid')
			FROM account_inventory_provider_states AS state
			WHERE state.instance_id=$1 AND state.provider='openai'`, lifecycle.instanceID).Scan(
				&providerPointer, &aPointer, &aLifecycle, &zPointer, &zLifecycle,
			); err != nil {
				t.Fatal(err)
			}
			if providerPointer != aPoll.String() || aPointer != aPoll.String() ||
				aLifecycle != "present" || zPointer != zPoll.String() || zLifecycle != "suspected_missing" {
				t.Fatal("four-way fixture did not create the intended inverse current-pointer order")
			}

			retentionConnection, err := database.runtime.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retentionConnection.Release()
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
			retentionName := "history_four_way_retention_" + name
			finalizeName := "history_four_way_finalize_" + name
			scopeName := "history_four_way_scope_" + name
			for connection, applicationName := range map[*pgxpool.Conn]string{
				retentionConnection: retentionName,
				finalizeConnection:  finalizeName,
				scopeConnection:     scopeName,
			} {
				if err := setLifecycleApplicationName(ctx, connection, applicationName); err != nil {
					t.Fatal(err)
				}
				if _, err := connection.Exec(ctx, `SET statement_timeout='10s'`); err != nil {
					t.Fatal(err)
				}
			}

			retentionDone := make(chan retentionResult, 1)
			finalizeDone := make(chan lifecycleFinalizeResult, 1)
			scopeDone := make(chan scopeResult, 1)
			startRetention := func() {
				go func() {
					var result retentionResult
					result.err = retentionConnection.QueryRow(ctx, `WITH retained AS (
						SELECT public.control_delete_account_inventory_poll_retention_v1(2) AS value
					) SELECT (value->>'processed_count')::integer,
						(value->>'deleted_row_count')::integer FROM retained`).Scan(
						&result.processed, &result.deleted)
					retentionDone <- result
				}()
			}
			startFinalize := func() {
				go func() {
					finalized, finalizeErr := finalizeLifecyclePoll(ctx, finalizeConnection, newPoll, payload)
					finalizeDone <- lifecycleFinalizeResult{finalized: finalized, err: finalizeErr}
				}()
			}
			startScope := func() {
				go func() {
					var result scopeResult
					result.err = scopeConnection.QueryRow(ctx, `SELECT
						public.control_activate_provider_policy_with_lifecycle(
							$1,$2,ARRAY['legacy'],ARRAY['openai'],
							'four-way-test','four-way scope transition',NULL
						)`, lifecycle.nodeType, lifecycle.contract).Scan(&result.activationID)
					scopeDone <- result
				}()
			}

			var lead pgx.Tx
			var retained retentionResult
			var scoped scopeResult
			if scopeFirst {
				lead, err = scopeConnection.Begin(ctx)
				if err == nil {
					err = lead.QueryRow(ctx, `SELECT
						public.control_activate_provider_policy_with_lifecycle(
							$1,$2,ARRAY['legacy'],ARRAY['openai'],
							'four-way-test','four-way scope transition',NULL
						)`, lifecycle.nodeType, lifecycle.contract).Scan(&scoped.activationID)
				}
				if err != nil {
					t.Fatal(err)
				}
				startRetention()
				if err := waitForLifecycleLock(ctx, database, retentionName); err != nil {
					_ = lead.Rollback(ctx)
					t.Fatal(err)
				}
				startFinalize()
				if err := waitForLifecycleLock(ctx, database, finalizeName); err != nil {
					_ = lead.Rollback(ctx)
					t.Fatal(err)
				}
			} else {
				lead, err = retentionConnection.Begin(ctx)
				if err == nil {
					err = lead.QueryRow(ctx, `WITH retained AS (
						SELECT public.control_delete_account_inventory_poll_retention_v1(2) AS value
					) SELECT (value->>'processed_count')::integer,
						(value->>'deleted_row_count')::integer FROM retained`).Scan(
						&retained.processed, &retained.deleted)
				}
				if err != nil {
					t.Fatal(err)
				}
				startFinalize()
				if err := waitForLifecycleLock(ctx, database, finalizeName); err != nil {
					_ = lead.Rollback(ctx)
					t.Fatal(err)
				}
				startScope()
				if err := waitForLifecycleLock(ctx, database, scopeName); err != nil {
					_ = lead.Rollback(ctx)
					t.Fatal(err)
				}
			}
			queryCtx, queryCancel := context.WithTimeout(ctx, 2*time.Second)
			page, queryErr := fixture.repository.QueryPageAndAudit(queryCtx,
				productstore.AccountInventoryQuery{InstanceID: lifecycle.instanceID, Limit: 10}, fixture.audit)
			queryCancel()
			if queryErr != nil {
				_ = lead.Rollback(ctx)
				t.Fatalf("current query blocked behind uncommitted four-way writes: %v", queryErr)
			}
			baseline := map[string]productstore.AccountInventoryLifecycle{
				"four-way-a@example.invalid": productstore.AccountInventoryPresent,
				"four-way-z@example.invalid": productstore.AccountInventorySuspectedMissing,
			}
			if len(page.Items) != len(baseline) {
				_ = lead.Rollback(ctx)
				t.Fatalf("uncommitted current query rows=%d", len(page.Items))
			}
			for _, item := range page.Items {
				if baseline[item.Email] != item.Lifecycle {
					_ = lead.Rollback(ctx)
					t.Fatalf("uncommitted current query exposed %s/%s", item.Email, item.Lifecycle)
				}
			}
			if err := lead.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			if scopeFirst {
				select {
				case retained = <-retentionDone:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			} else {
				select {
				case scoped = <-scopeDone:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			var finalized lifecycleFinalizeResult
			select {
			case finalized = <-finalizeDone:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if retained.err != nil || retained.processed != 2 || retained.deleted != 4 ||
				scoped.err != nil || scoped.activationID == uuid.Nil ||
				finalized.err != nil || finalized.finalized != 1 {
				t.Fatalf("four-way operations retention=%d/%d/%v scope=%s/%v finalize=%d/%v",
					retained.processed, retained.deleted, retained.err, scoped.activationID,
					scoped.err, finalized.finalized, finalized.err)
			}

			page, err = fixture.repository.QueryPageAndAudit(ctx,
				productstore.AccountInventoryQuery{InstanceID: lifecycle.instanceID, Limit: 10}, fixture.audit)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 2 {
				t.Fatalf("final current query rows=%d", len(page.Items))
			}
			for _, item := range page.Items {
				if item.Lifecycle != productstore.AccountInventoryOutOfScope || item.OutOfScopeSince == nil {
					t.Fatalf("final current query exposed partial item: %+v", item)
				}
			}

			var status, providerCurrent, accountCurrents, failureReason string
			var healthSlot time.Time
			var promotionApplied bool
			var oldPolls, newPollRows, newResultRows, deletedPolls, deletedProviders int
			var scopeAudits, accountRows, validOutOfScope int
			if err := database.owner.QueryRow(ctx, `SELECT
				state.monitoring_status,coalesce(state.current_poll_run_id::text,''),
				state.health_scheduled_at,
				coalesce(string_agg(coalesce(account.current_poll_run_id::text,''),','
				 ORDER BY account.account_key),''),
				result.promotion_applied,coalesce(result.promotion_skipped_reason,''),
				(SELECT count(*) FROM account_inventory_poll_runs
				 WHERE poll_run_id=ANY($3::uuid[])),
				(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=$4),
				(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$4),
				compaction.deleted_poll_count,compaction.deleted_provider_result_count,
				(SELECT count(*) FROM account_inventory_scope_transition_audits
				 WHERE activation_id=$5),count(account.*),
				count(account.*) FILTER (WHERE account.lifecycle='out_of_scope'
				 AND account.out_of_scope_since IS NOT NULL
				 AND account.consecutive_missing_count=0)
			FROM account_inventory_provider_states AS state
			JOIN account_inventory AS account USING(instance_id,provider)
			JOIN account_inventory_poll_provider_results AS result
			  ON result.poll_run_id=$4 AND result.provider=state.provider
			JOIN account_inventory_compaction_runs AS compaction
			  ON compaction.compaction_run_id=$2
			WHERE state.instance_id=$1 AND state.provider='openai'
			GROUP BY state.monitoring_status,state.current_poll_run_id,state.health_scheduled_at,
				result.promotion_applied,result.promotion_skipped_reason,
				compaction.deleted_poll_count,compaction.deleted_provider_result_count`,
				lifecycle.instanceID, compactionID, []uuid.UUID{zPoll, aPoll}, newPoll.pollID,
				scoped.activationID).Scan(&status, &providerCurrent, &healthSlot, &accountCurrents,
				&promotionApplied, &failureReason, &oldPolls, &newPollRows, &newResultRows,
				&deletedPolls, &deletedProviders, &scopeAudits, &accountRows, &validOutOfScope); err != nil {
				t.Fatal(err)
			}
			expectedPointer, expectedAccounts, expectedApplied, expectedReason :=
				newPoll.pollID.String(), newPoll.pollID.String()+","+newPoll.pollID.String(), true, ""
			expectedHealth := currentSlot
			if scopeFirst {
				expectedPointer, expectedAccounts, expectedApplied, expectedReason = "", ",", false, "policy_changed"
				expectedHealth = lifecycle.baseSlot.Add(5 * time.Minute)
			}
			if status != "out_of_scope" || providerCurrent != expectedPointer ||
				accountCurrents != expectedAccounts || !healthSlot.Equal(expectedHealth) ||
				promotionApplied != expectedApplied || failureReason != expectedReason ||
				oldPolls != 0 || newPollRows != 1 || newResultRows != 1 ||
				deletedPolls != 2 || deletedProviders != 2 || scopeAudits != 1 ||
				accountRows != 2 || validOutOfScope != 2 {
				t.Fatalf("four-way final state=%s pointers=%q/%q health=%s promotion=%t/%q old/new/result=%d/%d/%d deleted=%d/%d audits=%d accounts=%d/%d",
					status, providerCurrent, accountCurrents, healthSlot, promotionApplied,
					failureReason, oldPolls, newPollRows, newResultRows, deletedPolls,
					deletedProviders, scopeAudits, accountRows, validOutOfScope)
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
