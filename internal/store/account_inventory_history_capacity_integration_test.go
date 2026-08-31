package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAccountInventoryHistoryCapacityOneTenFifty(t *testing.T) {
	for _, nodeCount := range []int{1, 10, 50} {
		nodeCount := nodeCount
		t.Run(fmt.Sprintf("nodes_%d", nodeCount), func(t *testing.T) {
			database := newIsolatedJobDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			fixture := newLifecycleSchemaFixture(t, ctx, database)
			targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
			slot := targetDate.Add(12 * time.Hour)
			accountsPerNode := 1000 / nodeCount

			if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
				SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`,
				targetDate, fixture.policyID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
				DISABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
				DISABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
				t.Fatal(err)
			}

			for index := 0; index < nodeCount; index++ {
				instanceID := fixture.instanceID
				if index > 0 {
					instanceID = uuid.New()
					if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
						instance_id,display_name,node_type,driver_contract_version,
						management_endpoint,reader_secret_ref
					) VALUES($1,'History Capacity Node',$2,$3,$4,
						'docker-secret://synthetic/history-capacity-reader')`, instanceID,
						fixture.nodeType, fixture.contract,
						fmt.Sprintf("http://history-capacity-%02d.example.invalid", index)); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
					instance_id,effective_from,reason,actor,created_at
				) VALUES($1,$2,'reconciliation','history-capacity',$2)`,
					instanceID, targetDate); err != nil {
					t.Fatal(err)
				}
				pollID := uuid.New()
				if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
					poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
					provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
					created_at,first_started_at,last_started_at,finalized_at,observed_at,
					transport_success,response_shape_valid,contract_valid,inventory_mode,
					node_identity_complete,snapshot_complete,degraded,result,reason,
					source_record_count,identifiable_record_count,unidentified_record_count,
					unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
				) VALUES($1,$2,$3,$4,$5::timestamptz,$6,'finalized',1,2,299,
					$5::timestamptz+interval '1 second',$5::timestamptz+interval '2 seconds',
					$5::timestamptz+interval '2 seconds',$5::timestamptz+interval '4 seconds',
					$5::timestamptz+interval '3 seconds',true,true,true,'runtime',
					true,true,false,'success','none',$7,$7,0,0,0,'unknown','unknown')`,
					pollID, instanceID, fixture.nodeType, fixture.contract, slot,
					fixture.policyID, accountsPerNode); err != nil {
					t.Fatal(err)
				}
				if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results(
					poll_run_id,provider,identifiable_count,missing_identity_count,
					duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason,
					promotion_applied,promotion_skipped_reason
				) VALUES($1,'openai',$2,0,0,true,true,false,'complete',true,NULL)`,
					pollID, accountsPerNode); err != nil {
					t.Fatal(err)
				}
				if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_snapshot_items(
					poll_run_id,instance_id,provider,account_key,normalized_email,basic_status,
					success_count,failed_count,recent_request_count,observed_at
				) SELECT $1,$2,'openai',
					'openai:history-capacity-' || $3 || '-' || item::text || '@example.invalid',
					'history-capacity-' || $3 || '-' || item::text || '@example.invalid',
					'active',item,0,0,$4::timestamptz+interval '3 seconds'
				FROM generate_series(1,$5) AS item`, pollID, instanceID, fmt.Sprintf("%02d", index), slot,
					accountsPerNode); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
				ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
				ENABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
				t.Fatal(err)
			}

			var plannedCompactions int
			if err := database.runtime.QueryRow(ctx, `SELECT
				(public.control_plan_account_inventory_history_v1(1000)->>'compaction_runs_created')::integer`).
				Scan(&plannedCompactions); err != nil || plannedCompactions != nodeCount {
				t.Fatalf("nodes=%d planned compactions=%d err=%v", nodeCount, plannedCompactions, err)
			}
			for index := 0; index < nodeCount; index++ {
				var runID, fence uuid.UUID
				if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
					FROM public.control_claim_account_inventory_compaction_v1($1,300)`, uuid.New()).
					Scan(&runID, &fence); err != nil {
					t.Fatal(err)
				}
				var checksum string
				var sourceSnapshots, accountSegments int
				if err := database.runtime.QueryRow(ctx, `WITH summarized AS (
					SELECT public.control_summarize_account_inventory_compaction_v1($1,$2) AS value
				) SELECT value->>'source_checksum_hex',
					(value->>'source_snapshot_count')::integer,
					(value->>'account_segment_count')::integer FROM summarized`, runID, fence).
					Scan(&checksum, &sourceSnapshots, &accountSegments); err != nil {
					t.Fatal(err)
				}
				if len(checksum) != 64 || sourceSnapshots != accountsPerNode || accountSegments != accountsPerNode {
					t.Fatalf("nodes=%d snapshots=%d segments=%d checksum_length=%d",
						nodeCount, sourceSnapshots, accountSegments, len(checksum))
				}
				var deleted, remaining int
				if err := database.runtime.QueryRow(ctx, `WITH batch AS (
					SELECT public.control_delete_account_inventory_snapshot_batch_v1($1,$2,5000) AS value
				) SELECT (value->>'deleted_count')::integer,
					(value->>'remaining_count')::integer FROM batch`, runID, fence).
					Scan(&deleted, &remaining); err != nil || deleted != accountsPerNode || remaining != 0 {
					t.Fatalf("nodes=%d deleted=%d remaining=%d err=%v", nodeCount, deleted, remaining, err)
				}
				var status string
				if err := database.runtime.QueryRow(ctx, `SELECT
					public.control_complete_account_inventory_compaction_v1(
						$1,$2,decode($3,'hex'))->>'status'`, runID, fence, checksum).
					Scan(&status); err != nil || status != "completed" {
					t.Fatalf("nodes=%d compaction status=%q err=%v", nodeCount, status, err)
				}
			}

			var plannedRollups int
			if err := database.runtime.QueryRow(ctx, `SELECT
				(public.control_plan_account_inventory_history_v1(1000)->>'rollup_runs_created')::integer`).
				Scan(&plannedRollups); err != nil || plannedRollups != nodeCount {
				t.Fatalf("nodes=%d planned rollups=%d err=%v", nodeCount, plannedRollups, err)
			}
			for index := 0; index < nodeCount; index++ {
				var runID, fence uuid.UUID
				if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
					FROM public.control_claim_account_inventory_daily_rollup_v1($1,300)`, uuid.New()).
					Scan(&runID, &fence); err != nil {
					t.Fatal(err)
				}
				var status string
				if err := database.runtime.QueryRow(ctx, `SELECT
					public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'status'`,
					runID, fence).Scan(&status); err != nil || status != "completed" {
					t.Fatalf("nodes=%d rollup status=%q err=%v", nodeCount, status, err)
				}
			}

			var compactions, rollups, snapshots, accountRollups, providerRollups int
			if err := database.owner.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM account_inventory_compaction_runs WHERE status='completed'),
				(SELECT count(*) FROM account_inventory_daily_rollup_runs WHERE status='completed'),
				(SELECT count(*) FROM account_inventory_snapshot_items),
				(SELECT count(*) FROM account_inventory_daily_account_rollups),
				(SELECT count(*) FROM account_inventory_daily_provider_rollups)`).Scan(
				&compactions, &rollups, &snapshots, &accountRollups, &providerRollups); err != nil {
				t.Fatal(err)
			}
			if compactions != nodeCount || rollups != nodeCount || snapshots != 0 ||
				accountRollups != 1000 || providerRollups != nodeCount {
				t.Fatalf("nodes=%d compactions=%d rollups=%d snapshots=%d account_rollups=%d provider_rollups=%d",
					nodeCount, compactions, rollups, snapshots, accountRollups, providerRollups)
			}
			t.Logf("history_capacity=passed nodes=%d total_accounts=1000 account_rollups=%d",
				nodeCount, accountRollups)
		})
	}
}
