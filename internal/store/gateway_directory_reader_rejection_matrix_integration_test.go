package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayDirectoryReaderInitialRejectionMatrix(t *testing.T) {
	setup := func(t *testing.T, reference string) (*isolatedJobDatabase, uuid.UUID, uuid.UUID) {
		t.Helper()
		db := newIsolatedJobDatabase(t)
		ctx := context.Background()
		adminID, gatewayID := uuid.New(), uuid.New()
		if _, err := db.owner.Exec(ctx, `INSERT INTO control_admin_users
			(admin_id,login_name,display_name,role,status,activated_at)
			VALUES ($1,$2,'Matrix Admin','super_admin','enabled',clock_timestamp())`, adminID, "matrix_"+adminID.String()[:8]); err != nil {
			t.Fatal(err)
		}
		storedReference := reference
		if storedReference == "" {
			storedReference = "file://matrix/bootstrap"
		}
		insertGatewayInstance(t, ctx, db.owner, gatewayID, "http://matrix.example", storedReference)
		if reference == "" {
			if _, err := db.owner.Exec(ctx, `UPDATE gateway_instances SET reader_secret_ref=NULL WHERE instance_id=$1`, gatewayID); err != nil {
				t.Fatal(err)
			}
		}
		return db, adminID, gatewayID
	}
	call := func(t *testing.T, db *isolatedJobDatabase, adminID, gatewayID uuid.UUID, reference string) error {
		t.Helper()
		tx, err := db.owner.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(context.Background(), `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(context.Background(), `SELECT public.control_set_gateway_directory_reader_initial_v1($1,$2,$3)`, gatewayID, reference, adminID)
		if err == nil {
			if commitErr := tx.Commit(context.Background()); commitErr != nil {
				t.Fatal(commitErr)
			}
		}
		return err
	}

	t.Run("invalid actor and reference", func(t *testing.T) {
		db, adminID, gatewayID := setup(t, "")
		for _, reference := range []string{"", "not-a-reference", "https://credential.example/token"} {
			err := call(t, db, adminID, gatewayID, reference)
			requireSQLState(t, err, "22023")
		}
		if err := call(t, db, uuid.New(), gatewayID, "file://matrix/reader"); err == nil {
			t.Fatal("unknown actor unexpectedly succeeded")
		} else {
			requireSQLState(t, err, "42501")
		}
		disabledID := uuid.New()
		if _, err := db.owner.Exec(context.Background(), `INSERT INTO control_admin_users
			(admin_id,login_name,display_name,role,status,activated_at,disabled_at)
			VALUES ($1,$2,'Disabled Admin','super_admin','disabled',clock_timestamp(),clock_timestamp())`, disabledID, "disabled_"+disabledID.String()[:8]); err != nil {
			t.Fatal(err)
		}
		if err := call(t, db, disabledID, gatewayID, "file://matrix/reader"); err == nil {
			t.Fatal("disabled actor unexpectedly succeeded")
		} else {
			requireSQLState(t, err, "42501")
		}
	})

	t.Run("different reference conflict", func(t *testing.T) {
		db, adminID, gatewayID := setup(t, "")
		if err := call(t, db, adminID, gatewayID, "file://matrix/reader"); err != nil {
			t.Fatal(err)
		}
		if err := call(t, db, adminID, gatewayID, "file://matrix/other"); err == nil {
			t.Fatal("different reference unexpectedly succeeded")
		} else {
			requireSQLState(t, err, "23505")
		}
	})

	t.Run("directory history conflict", func(t *testing.T) {
		db, adminID, gatewayID := setup(t, "file://matrix/reader")
		waitDirectoryRuntimeClaimWindow(t, db)
		repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := repo.ScheduleCurrent(context.Background(), gatewayID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ClaimRunnable(context.Background(), gatewayID, uuid.New()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.owner.Exec(context.Background(), `UPDATE gateway_instances SET reader_secret_ref=NULL WHERE instance_id=$1`, gatewayID); err != nil {
			t.Fatal(err)
		}
		if err := call(t, db, adminID, gatewayID, "file://matrix/new"); err == nil {
			t.Fatal("history gateway unexpectedly configured")
		} else {
			requireSQLState(t, err, "23505")
		}
	})

	t.Run("snapshot-only history conflict", func(t *testing.T) {
		db, adminID, gatewayID := setup(t, "")
		if _, err := db.owner.Exec(context.Background(), `INSERT INTO gateway_directory_snapshots(
			snapshot_id,gateway_instance_id,fingerprint,schema_version,account_count)
			VALUES($1,$2,$3,1,0)`, uuid.New(), gatewayID, make([]byte, 32)); err != nil {
			t.Fatal(err)
		}
		if err := call(t, db, adminID, gatewayID, "file://matrix/snapshot"); err == nil {
			t.Fatal("snapshot-history gateway unexpectedly configured")
		} else {
			requireSQLState(t, err, "23505")
		}
		var ref string
		if err := db.owner.QueryRow(context.Background(), `SELECT coalesce(reader_secret_ref,'') FROM gateway_instances WHERE instance_id=$1`, gatewayID).Scan(&ref); err != nil {
			t.Fatal(err)
		}
		if ref != "" {
			t.Fatalf("reference after snapshot rejection = %q", ref)
		}
	})

	t.Run("failed terminal run history conflict", func(t *testing.T) {
		db, adminID, gatewayID := setup(t, "")
		if _, err := db.owner.Exec(context.Background(), `INSERT INTO gateway_directory_ingestion_runs(
			ingestion_run_id,gateway_instance_id,scheduled_at,status,attempt_count,created_at,terminal_at,last_failure_class)
			VALUES($1,$2,to_timestamp(floor(extract(epoch FROM clock_timestamp())/180)*180)-interval '180 seconds',
			'failed',0,clock_timestamp(),clock_timestamp(),'start_deadline_expired')`, uuid.New(), gatewayID); err != nil {
			t.Fatal(err)
		}
		if err := call(t, db, adminID, gatewayID, "file://matrix/failed"); err == nil {
			t.Fatal("failed-history gateway unexpectedly configured")
		} else {
			requireSQLState(t, err, "23505")
		}
	})

	t.Run("succeeded terminal run history conflict", func(t *testing.T) {
		db, adminID, gatewayID := setup(t, "")
		snapshotID := uuid.New()
		if _, err := db.owner.Exec(context.Background(), `INSERT INTO gateway_directory_snapshots(snapshot_id,gateway_instance_id,fingerprint,schema_version,account_count) VALUES($1,$2,$3,1,0)`, snapshotID, gatewayID, make([]byte, 32)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.owner.Exec(context.Background(), `WITH now AS MATERIALIZED (SELECT clock_timestamp() AS ts)
			INSERT INTO gateway_directory_ingestion_runs(ingestion_run_id,gateway_instance_id,scheduled_at,status,attempt_count,created_at,first_started_at,last_started_at,terminal_at,outcome,source_generated_at,received_at,content_fingerprint,snapshot_id,account_count)
			SELECT $1,$2,to_timestamp(floor(extract(epoch FROM now.ts)/180)*180)-interval '180 seconds','succeeded',1,now.ts,now.ts,now.ts,now.ts,'changed',now.ts,now.ts,$3,$4,0 FROM now`, uuid.New(), gatewayID, make([]byte, 32), snapshotID); err != nil {
			t.Fatal(err)
		}
		if err := call(t, db, adminID, gatewayID, "file://matrix/succeeded"); err == nil {
			t.Fatal("succeeded-history gateway unexpectedly configured")
		} else {
			requireSQLState(t, err, "23505")
		}
	})

	t.Run("audit failure rolls back reference", func(t *testing.T) {
		db, adminID, gatewayID := setup(t, "")
		if _, err := db.owner.Exec(context.Background(), `CREATE FUNCTION public.test_reject_reader_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit failure' USING ERRCODE='P0001'; END; $$; CREATE TRIGGER test_reject_reader_audit BEFORE INSERT ON audit_logs FOR EACH ROW WHEN (NEW.action = 'asset.gateway_directory_reader_configured') EXECUTE FUNCTION public.test_reject_reader_audit()`); err != nil {
			t.Fatal(err)
		}
		err := call(t, db, adminID, gatewayID, "file://matrix/reader")
		requireSQLState(t, err, "P0001")
		var ref string
		if err := db.owner.QueryRow(context.Background(), `SELECT coalesce(reader_secret_ref,'') FROM gateway_instances WHERE instance_id=$1`, gatewayID).Scan(&ref); err != nil {
			t.Fatal(err)
		}
		if ref != "" {
			t.Fatalf("reference after audit failure = %q", ref)
		}
		var audits int
		if err := db.owner.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if audits != 0 {
			t.Fatalf("audit rows after rollback = %d", audits)
		}
	})

	t.Run("binding history conflict", func(t *testing.T) {
		db := newIsolatedJobDatabase(t)
		ctx := context.Background()
		fixture := newRelayBindingSchemaFixture(t, ctx, db)
		if _, err := db.owner.Exec(ctx, `UPDATE control_admin_users SET status='enabled', activated_at=clock_timestamp() WHERE admin_id=$1`, fixture.adminID); err != nil {
			t.Fatal(err)
		}
		nodeID := fixture.insertNode(t, ctx, db)
		fixture.insertBinding(t, ctx, db, nodeID, fixture.accountIDs[0], "administrator_bind")
		insertGatewayDirectoryCurrentState(t, ctx, db, fixture.gatewayID, fixture.snapshotID, time.Now().UTC())
		if _, err := db.owner.Exec(ctx, `UPDATE gateway_instances SET reader_secret_ref=NULL WHERE instance_id=$1`, fixture.gatewayID); err != nil {
			t.Fatal(err)
		}
		if err := call(t, db, fixture.adminID, fixture.gatewayID, "file://binding/new"); err == nil {
			t.Fatal("binding-history gateway unexpectedly configured")
		} else {
			requireSQLState(t, err, "23505")
		}
		var bindings, snapshots, current, succeeded, audits int
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_gateway_account_bindings WHERE gateway_instance_id=$1`, fixture.gatewayID).Scan(&bindings); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_snapshots WHERE gateway_instance_id=$1`, fixture.gatewayID).Scan(&snapshots); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, fixture.gatewayID).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1 AND status='succeeded'`, fixture.gatewayID).Scan(&succeeded); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if bindings != 1 || snapshots != 1 || current != 1 || succeeded != 1 || audits != 0 {
			t.Fatalf("binding=%d snapshots=%d current=%d succeeded=%d audits=%d", bindings, snapshots, current, succeeded, audits)
		}
	})
}
