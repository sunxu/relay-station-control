package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
)

// This covers the transaction-facing contract: v2 returns an actual ACTIVE
// transition, and the same transaction persists its durable job bundle.
func TestAccountAvailabilityNotificationTransitionIsTransactional(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	ctx := context.Background()
	if _, err := f.db.owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('availability-test','Availability test','dev') ON CONFLICT (singleton_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var environmentID string
	if err := f.db.owner.QueryRow(ctx, `SELECT environment_id FROM environments WHERE singleton_id=1`).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	registry, err := jobcore.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	f.repo.SetNotificationDelivery(registry, false)
	f.event(t, 0, "availability-failure-1", "availability-failure-h1", "token_invalid", f.now.Add(-2*time.Minute))
	f.event(t, 0, "availability-failure-2", "availability-failure-h2", "token_invalid", f.now.Add(-time.Minute))
	f.reconcile(t)

	var jobs int
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs WHERE job_kind='dingtalk_alert_delivery'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("notification jobs=%d, want 1", jobs)
	}
	var payload []byte
	if err := f.db.owner.QueryRow(ctx, `SELECT payload FROM async_jobs WHERE job_kind='dingtalk_alert_delivery'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["transition"] != "ACTIVE" || fields["occurrence_type"] != "TOKEN_INVALID" || fields["environment_id"] != environmentID {
		t.Fatalf("unexpected notification payload: %s", payload)
	}
}

func TestNotificationDisplaySnapshotRuntimeACLAndProjection(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	ctx := context.Background()
	if _, err := f.db.owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('availability-acl-test','Availability ACL test','dev') ON CONFLICT (singleton_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var environmentID string
	if err := f.db.owner.QueryRow(ctx, `SELECT environment_id FROM environments WHERE singleton_id=1`).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	var runtimeExec, publicExec bool
	if err := f.db.owner.QueryRow(ctx, `SELECT has_function_privilege('relay_control_runtime','public.control_notification_display_snapshot_v1(text,uuid[])','EXECUTE'),has_function_privilege('public','public.control_notification_display_snapshot_v1(text,uuid[])','EXECUTE')`).Scan(&runtimeExec, &publicExec); err != nil {
		t.Fatal(err)
	}
	if !runtimeExec || publicExec {
		t.Fatalf("display helper ACL runtime=%v public=%v", runtimeExec, publicExec)
	}
	var environmentName string
	var instanceIDs []string
	var nodeNames []string
	if err := f.db.runtime.QueryRow(ctx, `SELECT environment_name,instance_ids::text[],node_names FROM public.control_notification_display_snapshot_v1($1,$2)`, environmentID, []uuid.UUID{f.node}).Scan(&environmentName, &instanceIDs, &nodeNames); err != nil {
		t.Fatal(err)
	}
	if environmentName == "" || len(instanceIDs) != 1 || len(nodeNames) != 1 || nodeNames[0] == "" {
		t.Fatalf("display helper projection name=%q ids=%v nodes=%v", environmentName, instanceIDs, nodeNames)
	}
	var emptyIDs []string
	var emptyNames []string
	if err := f.db.runtime.QueryRow(ctx, `SELECT instance_ids::text[],node_names FROM public.control_notification_display_snapshot_v1($1,$2)`, environmentID, []uuid.UUID{}).Scan(&emptyIDs, &emptyNames); err != nil {
		t.Fatal(err)
	}
	if len(emptyIDs) != 0 || len(emptyNames) != 0 {
		t.Fatalf("empty display helper projection ids=%v nodes=%v", emptyIDs, emptyNames)
	}
}

func availabilityNotificationRegistry(t *testing.T) *jobcore.Registry {
	t.Helper()
	registry, err := jobcore.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func availabilityNotificationEnvironment(t *testing.T, f *availabilityFixture) {
	t.Helper()
	if _, err := f.db.owner.Exec(context.Background(), `INSERT INTO environments(environment_id,name,environment_type) VALUES('availability-notification-test','Availability notification test','dev') ON CONFLICT (singleton_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
}

func availabilityNotificationCounts(t *testing.T, f *availabilityFixture) (int, int, int, int) {
	t.Helper()
	ctx := context.Background()
	var occurrences, jobs, events, outbox int
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM account_availability_occurrences`).Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs WHERE job_kind='dingtalk_alert_delivery'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM async_job_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM operation_outbox`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	return occurrences, jobs, events, outbox
}

func TestAccountAvailabilityNotificationPaginationZeroAndMultipleTransitions(t *testing.T) {
	f := newAvailabilityFixture(t, 105)
	availabilityNotificationEnvironment(t, f)
	f.repo.SetNotificationDelivery(availabilityNotificationRegistry(t), false)
	before := f.now.Add(-3 * time.Minute)
	f.event(t, 0, "blocked-1", "blocked-1-h", "account_blocked", before)
	f.event(t, 0, "blocked-2", "blocked-2-h", "account_blocked", before.Add(time.Second))
	f.event(t, 0, "token-1", "token-1-h", "token_invalid", before)
	f.event(t, 0, "token-2", "token-2-h", "token_invalid", before.Add(time.Second))
	f.event(t, 104, "tail-1", "tail-1-h", "token_invalid", before)
	f.event(t, 104, "tail-2", "tail-2-h", "token_invalid", before.Add(time.Second))
	tx, err := f.db.runtime.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(context.Background(), `SELECT account_key,transitions FROM public.control_reconcile_account_availability_v2(NULL,NULL)`)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	var sameKeyTransitions int
	for rows.Next() {
		var accountKey string
		var encoded []byte
		if err := rows.Scan(&accountKey, &encoded); err != nil {
			rows.Close()
			_ = tx.Rollback(context.Background())
			t.Fatal(err)
		}
		if accountKey == f.keys[0] {
			var transitions []map[string]any
			if err := json.Unmarshal(encoded, &transitions); err != nil {
				rows.Close()
				_ = tx.Rollback(context.Background())
				t.Fatal(err)
			}
			sameKeyTransitions = len(transitions)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	rows.Close()
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sameKeyTransitions != 2 {
		t.Fatalf("same-key transitions=%d, want 2 in one pagination row", sameKeyTransitions)
	}
	if got, err := f.repo.Reconcile(context.Background()); err != nil || got != 105 {
		t.Fatalf("paginated reconcile count=%d err=%v", got, err)
	}
	_, jobs, events, outbox := availabilityNotificationCounts(t, f)
	if jobs != 3 || events != 3 || outbox != 3 {
		t.Fatalf("multiple transition bundle jobs=%d events=%d outbox=%d", jobs, events, outbox)
	}
	var pageCount, zeroRows int
	dbRows, err := f.db.runtime.Query(context.Background(), `SELECT transitions FROM public.control_reconcile_account_availability_v2(NULL,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	for dbRows.Next() {
		var encoded []byte
		if err := dbRows.Scan(&encoded); err != nil {
			dbRows.Close()
			t.Fatal(err)
		}
		pageCount++
		var transitions []map[string]any
		if err := json.Unmarshal(encoded, &transitions); err != nil {
			dbRows.Close()
			t.Fatal(err)
		}
		if len(transitions) == 0 {
			zeroRows++
		}
	}
	dbRows.Close()
	if err := dbRows.Err(); err != nil {
		t.Fatal(err)
	}
	if pageCount != 100 || zeroRows != 100 {
		t.Fatalf("v2 zero-transition first page rows=%d/%d, want 100/100", zeroRows, pageCount)
	}
}

func TestAccountAvailabilityNotificationNoBackfillAfterDisabledEnable(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	availabilityNotificationEnvironment(t, f)
	before := f.now.Add(-2 * time.Minute)
	f.event(t, 0, "historical-1", "historical-1-h", "token_invalid", before)
	f.event(t, 0, "historical-2", "historical-2-h", "token_invalid", before.Add(time.Second))
	if _, err := f.repo.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if occurrences, jobs, events, outbox := availabilityNotificationCounts(t, f); occurrences != 1 || jobs != 0 || events != 0 || outbox != 0 {
		t.Fatalf("disabled notifications residue occurrences=%d jobs=%d events=%d outbox=%d", occurrences, jobs, events, outbox)
	}
	f.clock(t, "file_disabled", nil, f.now.Add(-time.Minute), f.now.Add(-time.Minute).Truncate(5*time.Minute))
	f.repo.SetNotificationDelivery(availabilityNotificationRegistry(t), false)
	if _, err := f.repo.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, jobs, events, outbox := availabilityNotificationCounts(t, f); jobs != 0 || events != 0 || outbox != 0 {
		t.Fatalf("enable after disabled state backfilled jobs=%d events=%d outbox=%d", jobs, events, outbox)
	}
	f.event(t, 0, "post-enable-success", "post-enable-success-h", "", f.now.Add(-time.Second))
	f.clock(t, "file_active", nil, f.now.Add(-time.Second), f.now.Add(-time.Second).Truncate(5*time.Minute))
	if got, err := f.repo.Reconcile(context.Background()); err != nil || got != 1 {
		t.Fatalf("post-enable recovery reconcile count=%d err=%v", got, err)
	}
	if occurrences, jobs, events, outbox := availabilityNotificationCounts(t, f); occurrences != 1 || jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("post-enable RESOLVED bundle occurrences=%d jobs=%d events=%d outbox=%d", occurrences, jobs, events, outbox)
	}
}

func TestAccountAvailabilityNotificationActiveStableAndResolvedOnce(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	availabilityNotificationEnvironment(t, f)
	f.repo.SetNotificationDelivery(availabilityNotificationRegistry(t), false)
	before := f.now.Add(-2 * time.Minute)
	f.event(t, 0, "stable-1", "stable-1-h", "token_invalid", before)
	f.event(t, 0, "stable-2", "stable-2-h", "token_invalid", before.Add(time.Second))
	f.reconcile(t)
	f.reconcile(t)
	if _, jobs, _, _ := availabilityNotificationCounts(t, f); jobs != 1 {
		t.Fatalf("stable ACTIVE notification jobs=%d, want 1", jobs)
	}
	f.event(t, 0, "stable-success", "stable-success-h", "", f.now.Add(-time.Second))
	f.reconcile(t)
	f.reconcile(t)
	if _, jobs, _, _ := availabilityNotificationCounts(t, f); jobs != 2 {
		t.Fatalf("RESOLVED notification jobs=%d, want 2", jobs)
	}
}

func TestAccountAvailabilityNotificationRenameUsesTransitionSnapshots(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	availabilityNotificationEnvironment(t, f)
	f.repo.SetNotificationDelivery(availabilityNotificationRegistry(t), false)
	before := f.now.Add(-2 * time.Minute)
	f.event(t, 0, "rename-1", "rename-1-h", "token_invalid", before)
	f.event(t, 0, "rename-2", "rename-2-h", "token_invalid", before.Add(time.Second))
	f.reconcile(t)
	var originalName string
	if err := f.db.owner.QueryRow(context.Background(), `SELECT display_name FROM relay_node_assets WHERE instance_id=$1`, f.node).Scan(&originalName); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.owner.Exec(context.Background(), `UPDATE relay_node_assets SET display_name='Relay renamed' WHERE instance_id=$1`, f.node); err != nil {
		t.Fatal(err)
	}
	f.event(t, 0, "rename-success", "rename-success-h", "", f.now.Add(-time.Second))
	f.reconcile(t)
	rows, err := f.db.owner.Query(context.Background(), `SELECT payload FROM async_jobs WHERE job_kind='dingtalk_alert_delivery' ORDER BY created_at,job_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var value struct {
			Transition string   `json:"transition"`
			NodeNames  []string `json:"node_names"`
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			t.Fatal(err)
		}
		names = append(names, value.Transition+":"+value.NodeNames[0])
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "ACTIVE:"+originalName || names[1] != "RESOLVED:Relay renamed" {
		t.Fatalf("transition display snapshots=%v", names)
	}
}

func TestAccountAvailabilityNotificationWriteFaultsRollbackAllEvidence(t *testing.T) {
	for _, table := range []string{"async_jobs", "async_job_events", "operation_outbox"} {
		t.Run(table, func(t *testing.T) {
			f := newAvailabilityFixture(t, 1)
			availabilityNotificationEnvironment(t, f)
			f.repo.SetNotificationDelivery(availabilityNotificationRegistry(t), false)
			before := f.now.Add(-2 * time.Minute)
			f.event(t, 0, "fault-1-"+table, "fault-1-"+table, "token_invalid", before)
			f.event(t, 0, "fault-2-"+table, "fault-2-"+table, "token_invalid", before.Add(time.Second))
			ctx := context.Background()
			functionName := "availability_fault_" + table
			triggerName := functionName + "_trigger"
			if _, err := f.db.owner.Exec(ctx, `CREATE FUNCTION public.`+functionName+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'availability notification write fault'; END $$; CREATE TRIGGER `+triggerName+` BEFORE INSERT ON public.`+table+` FOR EACH ROW EXECUTE FUNCTION public.`+functionName+`()`); err != nil {
				t.Fatal(err)
			}
			_, reconcileErr := f.repo.Reconcile(ctx)
			if reconcileErr == nil {
				t.Fatal("faulted notification transaction unexpectedly succeeded")
			}
			occurrences, jobs, events, outbox := availabilityNotificationCounts(t, f)
			if occurrences != 0 || jobs != 0 || events != 0 || outbox != 0 {
				t.Fatalf("fault residue occurrences=%d jobs=%d events=%d outbox=%d", occurrences, jobs, events, outbox)
			}
			if _, err := f.db.owner.Exec(ctx, `DROP TRIGGER `+triggerName+` ON public.`+table+`; DROP FUNCTION public.`+functionName+`()`); err != nil {
				t.Fatal(err)
			}
			if got, err := f.repo.Reconcile(ctx); err != nil || got != 1 {
				t.Fatalf("post-fault reconcile count=%d err=%v", got, err)
			}
			occurrences, jobs, events, outbox = availabilityNotificationCounts(t, f)
			if occurrences != 1 || jobs != 1 || events != 1 || outbox != 1 {
				t.Fatalf("successful retry bundle occurrences=%d jobs=%d events=%d outbox=%d", occurrences, jobs, events, outbox)
			}
		})
	}
}
