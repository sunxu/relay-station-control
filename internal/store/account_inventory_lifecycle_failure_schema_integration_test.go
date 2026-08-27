package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func installLifecyclePauseTrigger(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
) {
	t.Helper()
	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.control_test_pause_lifecycle_write()
		RETURNS trigger
		LANGUAGE plpgsql
		SET search_path=pg_catalog
		AS $$
		BEGIN
			IF coalesce(current_setting('relay_control.test_pause_lifecycle',true),'') = 'on' THEN
				PERFORM pg_sleep(30);
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER zz_control_test_pause_lifecycle_write
		BEFORE INSERT OR UPDATE ON account_inventory
		FOR EACH ROW EXECUTE FUNCTION public.control_test_pause_lifecycle_write()`); err != nil {
		t.Fatal("install lifecycle fault trigger failed")
	}
}

func waitForLifecyclePause(
	ctx context.Context, database *isolatedJobDatabase, backendPID int32,
) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var paused bool
		err := database.owner.QueryRow(ctx, `SELECT coalesce(bool_or(wait_event='PgSleep'),false)
			FROM pg_stat_activity WHERE datname=current_database() AND pid=$1`, backendPID).
			Scan(&paused)
		if err != nil {
			return err
		}
		if paused {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func terminatePausedLifecycleFinalize(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
	connection *pgxpool.Conn,
	poll preparedLifecyclePoll,
	payload lifecycleFinalizePayload,
) {
	t.Helper()
	if err := setLifecycleApplicationName(ctx, connection, "lifecycle_fault_uncommitted"); err != nil {
		t.Fatal("set lifecycle fault session classification failed")
	}
	if _, err := connection.Exec(ctx,
		`SELECT set_config('relay_control.test_pause_lifecycle','on',false)`); err != nil {
		t.Fatal("arm lifecycle fault trigger failed")
	}
	var backendPID int32
	if err := connection.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		t.Fatal("read lifecycle fault backend failed")
	}
	result := make(chan error, 1)
	go func() {
		_, finalizeErr := finalizeLifecyclePoll(ctx, connection, poll, payload)
		result <- finalizeErr
	}()
	if err := waitForLifecyclePause(ctx, database, backendPID); err != nil {
		t.Fatal("lifecycle transaction did not reach the injected write pause")
	}
	var terminated bool
	if err := database.owner.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, backendPID).
		Scan(&terminated); err != nil || !terminated {
		t.Fatal("lifecycle fault backend termination failed")
	}
	select {
	case finalizeErr := <-result:
		if finalizeErr == nil {
			t.Fatal("terminated lifecycle finalize unexpectedly returned success")
		}
	case <-ctx.Done():
		t.Fatal("terminated lifecycle finalize did not return")
	}
}

func TestAccountInventoryLifecycleUncommittedTerminationRollsBackMissingAndUpsert(t *testing.T) {
	for _, phase := range []string{"missing", "upsert"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			database := newIsolatedJobDatabase(t)
			fixture := newLifecycleSchemaFixture(t, ctx, database)
			account := lifecycleAccount{email: "fault@example.invalid", successCount: 1}
			var baselinePoll uuid.UUID
			if phase == "missing" {
				baselinePoll = fixture.finalize(t, ctx, database, []lifecycleAccount{account})
			}
			installLifecyclePauseTrigger(t, ctx, database)
			accounts := []lifecycleAccount{account}
			if phase == "missing" {
				accounts = nil
			}
			payload, err := makeLifecycleFinalizePayload(accounts)
			if err != nil {
				t.Fatal("encode lifecycle fault evidence failed")
			}
			poll := prepareLifecyclePoll(t, ctx, database, fixture,
				fixture.baseSlot.Add(time.Duration(fixture.nextPoll)*5*time.Minute))
			connection, err := database.runtime.Acquire(ctx)
			if err != nil {
				t.Fatal("acquire lifecycle fault connection failed")
			}
			terminatePausedLifecycleFinalize(t, ctx, database, connection, poll, payload)
			connection.Release()

			var status string
			var providerEvidence, snapshotEvidence, lifecycleRows, providerStateRows int
			if err := database.owner.QueryRow(ctx, `SELECT status,
				(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
				(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
				(SELECT count(*) FROM account_inventory WHERE instance_id=$2),
				(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
				FROM account_inventory_poll_runs WHERE poll_run_id=$1`, poll.pollID, fixture.instanceID).
				Scan(&status, &providerEvidence, &snapshotEvidence, &lifecycleRows, &providerStateRows); err != nil {
				t.Fatal("read lifecycle rollback classification failed")
			}
			if status != "running" || providerEvidence != 0 || snapshotEvidence != 0 {
				t.Fatal("terminated lifecycle write left partial poll evidence")
			}
			if phase == "missing" {
				var lifecycle string
				var missingCount int
				var pointer uuid.UUID
				if lifecycleRows != 1 || providerStateRows != 1 {
					t.Fatal("terminated missing transition changed baseline row counts")
				}
				if err := database.owner.QueryRow(ctx, `SELECT account.lifecycle,
					account.consecutive_missing_count,state.current_poll_run_id
					FROM account_inventory AS account
					JOIN account_inventory_provider_states AS state USING (instance_id,provider)
					WHERE account.instance_id=$1`, fixture.instanceID).
					Scan(&lifecycle, &missingCount, &pointer); err != nil {
					t.Fatal("read missing rollback state failed")
				}
				if lifecycle != "present" || missingCount != 0 || pointer != baselinePoll {
					t.Fatal("terminated missing transition changed current state")
				}
			} else if lifecycleRows != 0 || providerStateRows != 0 {
				t.Fatal("terminated upsert transition left partial current state")
			}

			finalized, err := finalizeLifecyclePoll(ctx, database.runtime, poll, payload)
			if err != nil || finalized != 1 {
				t.Fatal("retry after terminated lifecycle transaction failed")
			}
			if phase == "missing" {
				var lifecycle string
				var missingCount int
				if err := database.owner.QueryRow(ctx, `SELECT lifecycle,consecutive_missing_count
					FROM account_inventory WHERE instance_id=$1`, fixture.instanceID).
					Scan(&lifecycle, &missingCount); err != nil {
					t.Fatal("read retried missing state failed")
				}
				if lifecycle != "suspected_missing" || missingCount != 1 {
					t.Fatal("retried missing transition did not apply exactly once")
				}
			} else {
				if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory
					WHERE instance_id=$1 AND lifecycle='present'`, fixture.instanceID).
					Scan(&lifecycleRows); err != nil || lifecycleRows != 1 {
					t.Fatal("retried lifecycle upsert did not publish exactly one row")
				}
			}
			t.Logf("lifecycle_postgres_fault=success phase=%s_uncommitted_termination rollback=complete retry=single", phase)
		})
	}
}

func TestAccountInventoryLifecycleCommitUnknownReplayIsSingleState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	account := lifecycleAccount{email: "commit-unknown@example.invalid", successCount: 1}
	baselinePoll := fixture.finalize(t, ctx, database, []lifecycleAccount{account})
	payload, err := makeLifecycleFinalizePayload(nil)
	if err != nil {
		t.Fatal("encode commit-unknown evidence failed")
	}
	poll := prepareLifecyclePoll(t, ctx, database, fixture,
		fixture.baseSlot.Add(time.Duration(fixture.nextPoll)*5*time.Minute))
	connection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire commit-unknown connection failed")
	}
	if _, err := connection.Exec(ctx, `BEGIN`); err != nil {
		connection.Release()
		t.Fatal("begin commit-unknown transaction failed")
	}
	finalized, err := finalizeLifecyclePoll(ctx, connection, poll, payload)
	if err != nil || finalized != 1 {
		connection.Release()
		t.Fatal("commit-unknown transaction did not stage one finalize")
	}
	_, commitUnknownErr := connection.Exec(ctx,
		`COMMIT; SELECT pg_terminate_backend(pg_backend_pid())`)
	connection.Release()
	if commitUnknownErr == nil {
		t.Fatal("commit-unknown injection unexpectedly returned a reliable acknowledgement")
	}

	var status, lifecycle string
	var missingCount, pollEvidence, lifecycleRows int
	var providerPointer, accountSource uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT run.status,
		account.lifecycle,account.consecutive_missing_count,
		state.current_poll_run_id,account.current_poll_run_id,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$2),
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1)
		FROM account_inventory_poll_runs AS run
		JOIN account_inventory_provider_states AS state ON state.instance_id=$1 AND state.provider='openai'
		JOIN account_inventory AS account
		  ON account.instance_id=state.instance_id AND account.provider=state.provider
		WHERE run.poll_run_id=$2`, fixture.instanceID, poll.pollID).Scan(
		&status, &lifecycle, &missingCount, &providerPointer, &accountSource,
		&pollEvidence, &lifecycleRows,
	); err != nil {
		t.Fatal("read commit-unknown durable state failed")
	}
	if status != "finalized" || lifecycle != "suspected_missing" || missingCount != 1 ||
		providerPointer != poll.pollID || accountSource != baselinePoll ||
		pollEvidence != 1 || lifecycleRows != 1 {
		t.Fatal("commit-unknown transaction did not leave exactly one durable state")
	}

	replayed, err := finalizeLifecyclePoll(ctx, database.runtime, poll, payload)
	if err != nil || replayed != 0 {
		t.Fatal("commit-unknown replay was not an idempotent no-op")
	}
	var afterLifecycle string
	var afterMissing, afterEvidence, afterRows int
	if err := database.owner.QueryRow(ctx, `SELECT lifecycle,consecutive_missing_count,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$2),
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1)
		FROM account_inventory WHERE instance_id=$1`, fixture.instanceID, poll.pollID).Scan(
		&afterLifecycle, &afterMissing, &afterEvidence, &afterRows,
	); err != nil {
		t.Fatal("read commit-unknown replay state failed")
	}
	if afterLifecycle != lifecycle || afterMissing != missingCount ||
		afterEvidence != 1 || afterRows != 1 {
		t.Fatal("commit-unknown replay duplicated or advanced lifecycle state")
	}
	t.Log("lifecycle_postgres_fault=success phase=commit_unknown durable=single replay=idempotent")
}
