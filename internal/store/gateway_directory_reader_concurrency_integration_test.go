package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	store "github.com/sunxu/relay-station-control/internal/store"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestGatewayDirectoryReaderInitialConcurrency(t *testing.T) {
	setup := func(t *testing.T) (*isolatedJobDatabase, uuid.UUID, uuid.UUID) {
		t.Helper()
		db := newIsolatedJobDatabase(t)
		ctx := context.Background()
		adminID, gatewayID := uuid.New(), uuid.New()
		if _, err := db.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at)
			VALUES($1,$2,'Concurrency Admin','super_admin','enabled',clock_timestamp())`, adminID, "concurrency_"+adminID.String()[:8]); err != nil {
			t.Fatal(err)
		}
		insertGatewayInstance(t, ctx, db.owner, gatewayID, "http://concurrency.example", "file://concurrency/bootstrap")
		if _, err := db.owner.Exec(ctx, `UPDATE gateway_instances SET reader_secret_ref=NULL WHERE instance_id=$1`, gatewayID); err != nil {
			t.Fatal(err)
		}
		return db, adminID, gatewayID
	}
	fill := func(ctx context.Context, db *isolatedJobDatabase, adminID, gatewayID uuid.UUID, ref string) error {
		tx, err := db.owner.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT public.control_set_gateway_directory_reader_initial_v1($1,$2,$3)`, gatewayID, ref, adminID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	t.Run("fill holds gateway lock before schedule", func(t *testing.T) {
		db, adminID, gatewayID := setup(t)
		ctx := context.Background()
		lockTx, err := db.owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lockTx.Rollback(ctx)
		if _, err := lockTx.Exec(ctx, `SELECT instance_id FROM gateway_instances WHERE instance_id=$1 FOR UPDATE`, gatewayID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			repo, e := store.NewGatewayDirectoryIngestionRepository(db.runtime)
			if e == nil {
				_, _, _, e = repo.ScheduleCurrent(ctx, gatewayID)
			}
			result <- e
		}()
		select {
		case <-result:
			t.Fatal("schedule completed while gateway lock was held")
		case <-time.After(200 * time.Millisecond):
		}
		if _, err := lockTx.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
			t.Fatal(err)
		}
		var fillResult string
		if err := lockTx.QueryRow(ctx, `SELECT public.control_set_gateway_directory_reader_initial_v1($1,$2,$3)`, gatewayID, "file://concurrency/reader", adminID).Scan(&fillResult); err != nil {
			t.Fatal(err)
		}
		if fillResult != "configured" {
			t.Fatalf("fill result = %q", fillResult)
		}
		if err := lockTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("schedule did not unblock")
		}
		var runs, audits int
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&runs); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if runs != 1 || audits != 1 {
			t.Fatalf("runs=%d audits=%d, want 1/1", runs, audits)
		}
	})

	t.Run("schedule holds NULL lock before fill", func(t *testing.T) {
		db, adminID, gatewayID := setup(t)
		ctx := context.Background()
		lockTx, err := db.runtime.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lockTx.Rollback(ctx)
		queries := generated.New(lockTx)
		_, err = queries.CreateOrGetGatewayDirectoryIngestionRun(ctx, pgtype.UUID{Bytes: gatewayID, Valid: true})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("NULL ScheduleCurrent query error=%v, want pgx.ErrNoRows", err)
		}
		result := make(chan error, 1)
		go func() { result <- fill(ctx, db, adminID, gatewayID, "file://concurrency/reader") }()
		select {
		case <-result:
			t.Fatal("fill completed while NULL gateway lock was held")
		case <-time.After(200 * time.Millisecond):
		}
		if err := lockTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		var runs int
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&runs); err != nil {
			t.Fatal(err)
		}
		if runs != 0 {
			t.Fatalf("runs after NULL schedule lock = %d, want 0", runs)
		}
	})

	t.Run("different references one winner", func(t *testing.T) {
		db, adminID, gatewayID := setup(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		results := make(chan error, 2)
		for _, ref := range []string{"file://concurrency/one", "file://concurrency/two"} {
			go func(ref string) { results <- fill(ctx, db, adminID, gatewayID, ref) }(ref)
		}
		var successes, conflicts int
		for range 2 {
			switch err := <-results; {
			case err == nil:
				successes++
			default:
				if strings.Contains(err.Error(), "23505") {
					conflicts++
				} else {
					t.Fatalf("competition error: %v", err)
				}
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
		}
		var reference, endpoint string
		if err := db.owner.QueryRow(ctx, `SELECT reader_secret_ref, management_endpoint FROM gateway_instances WHERE instance_id=$1`, gatewayID).Scan(&reference, &endpoint); err != nil {
			t.Fatal(err)
		}
		if reference != "file://concurrency/one" && reference != "file://concurrency/two" {
			t.Fatalf("winner reference = %q", reference)
		}
		if endpoint != "http://concurrency.example" {
			t.Fatalf("endpoint changed = %q", endpoint)
		}
		var audits int
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if audits != 1 {
			t.Fatalf("competition audit count = %d, want 1", audits)
		}
	})

	t.Run("registrar lock timeout", func(t *testing.T) {
		db, adminID, gatewayID := setup(t)
		ctx := context.Background()
		lockTx, err := db.owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lockTx.Rollback(ctx)
		if _, err := lockTx.Exec(ctx, `SELECT instance_id FROM gateway_instances WHERE instance_id=$1 FOR UPDATE`, gatewayID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			tx, err := db.owner.Begin(ctx)
			if err != nil {
				result <- err
				return
			}
			defer tx.Rollback(context.Background())
			if _, err := tx.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar; SET LOCAL lock_timeout='200ms'`); err != nil {
				result <- err
				return
			}
			_, err = tx.Exec(ctx, `SELECT public.control_set_gateway_directory_reader_initial_v1($1,$2,$3)`, gatewayID, "file://concurrency/timeout", adminID)
			result <- err
		}()
		select {
		case err := <-result:
			requireSQLState(t, err, "55P03")
		case <-time.After(2 * time.Second):
			t.Fatal("lock timeout call did not return")
		}
		var ref string
		var audits int
		if err := db.owner.QueryRow(ctx, `SELECT coalesce(reader_secret_ref,'') FROM gateway_instances WHERE instance_id=$1`, gatewayID).Scan(&ref); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if ref != "" || audits != 0 {
			t.Fatalf("after timeout ref=%q audits=%d", ref, audits)
		}
		if err := lockTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := fill(ctx, db, adminID, gatewayID, "file://concurrency/timeout"); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT reader_secret_ref FROM gateway_instances WHERE instance_id=$1`, gatewayID).Scan(&ref); err != nil {
			t.Fatal(err)
		}
		if ref != "file://concurrency/timeout" {
			t.Fatalf("reference after unlock = %q", ref)
		}
	})

	t.Run("binding and fill share gateway lock", func(t *testing.T) {
		db := newIsolatedJobDatabase(t)
		ctx := context.Background()
		fixture := newRelayBindingSchemaFixture(t, ctx, db)
		if _, err := db.owner.Exec(ctx, `UPDATE control_admin_users SET status='enabled', activated_at=clock_timestamp() WHERE admin_id=$1`, fixture.adminID); err != nil {
			t.Fatal(err)
		}
		nodeID := fixture.insertNode(t, ctx, db)
		insertGatewayDirectoryCurrentState(t, ctx, db, fixture.gatewayID, fixture.snapshotID, time.Now().UTC())
		if _, err := db.owner.Exec(ctx, `UPDATE gateway_instances SET reader_secret_ref=NULL WHERE instance_id=$1`, fixture.gatewayID); err != nil {
			t.Fatal(err)
		}
		lockTx, err := db.owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lockTx.Rollback(ctx)
		if _, err := lockTx.Exec(ctx, `SELECT instance_id FROM gateway_instances WHERE instance_id=$1 FOR UPDATE`, fixture.gatewayID); err != nil {
			t.Fatal(err)
		}
		bindingResult := make(chan error, 1)
		go func() {
			repo, e := store.NewRelayBindingRepository(db.runtime)
			if e == nil {
				_, e = repo.Bind(ctx, store.BindParams{RelayNodeID: nodeID, GatewayInstanceID: fixture.gatewayID, GatewayAccountID: fixture.accountIDs[0], AdminID: fixture.adminID, RequestID: "binding-lock-" + uuid.NewString()})
			}
			bindingResult <- e
		}()
		fillResult := make(chan error, 1)
		go func() { fillResult <- fill(ctx, db, fixture.adminID, fixture.gatewayID, "file://binding/reader") }()
		select {
		case <-bindingResult:
			t.Fatal("binding completed while Gateway lock was held")
		case <-time.After(200 * time.Millisecond):
		}
		select {
		case <-fillResult:
			t.Fatal("fill completed while Gateway lock was held")
		case <-time.After(200 * time.Millisecond):
		}
		if err := lockTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-bindingResult; err != nil {
			t.Fatal(err)
		}
		if err := <-fillResult; err == nil {
			t.Fatal("fill unexpectedly bypassed binding history")
		} else {
			requireSQLState(t, err, "23505")
		}
		var ref, endpoint string
		if err := db.owner.QueryRow(ctx, `SELECT coalesce(reader_secret_ref,''), management_endpoint FROM gateway_instances WHERE instance_id=$1`, fixture.gatewayID).Scan(&ref, &endpoint); err != nil {
			t.Fatal(err)
		}
		var bindings, audits int
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_gateway_account_bindings WHERE gateway_instance_id=$1`, fixture.gatewayID).Scan(&bindings); err != nil {
			t.Fatal(err)
		}
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if ref != "" || endpoint != "http://binding-gateway.test" || bindings != 1 || audits != 0 {
			t.Fatalf("ref=%q endpoint=%q bindings=%d audits=%d", ref, endpoint, bindings, audits)
		}
	})
}
