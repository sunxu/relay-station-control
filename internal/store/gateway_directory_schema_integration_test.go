package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func requireGatewayDirectorySQLState(t *testing.T, err error, expected string) {
	t.Helper()
	if err == nil {
		t.Fatalf("operation unexpectedly succeeded; want SQLSTATE %s", expected)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error %T is not PostgreSQL error: %v", err, err)
	}
	if pgErr.Code != expected {
		t.Fatalf("SQLSTATE = %s, want %s: %v", pgErr.Code, expected, err)
	}
}

func TestGatewayDirectorySchemaFoundation(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	const (
		runID      = "00000000-0000-4000-8000-00000000d101"
		secondRun  = "00000000-0000-4000-8000-00000000d102"
		snapshotID = "00000000-0000-4000-8000-00000000d201"
		leaseToken = "00000000-0000-4000-8000-00000000d301"
	)

	const fixtureGatewayID = "00000000-0000-4000-8000-00000000d001"
	gatewayID := fixtureGatewayID
	err = tx.QueryRow(ctx, `
		SELECT instance_id::text
		FROM gateway_instances
		WHERE singleton_id = 1
	`).Scan(&gatewayID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO gateway_instances(
				singleton_id, instance_id, display_name, management_endpoint
			) VALUES (
				1, $1::uuid, 'Directory Schema Gateway',
				'https://gateway-directory-schema.test'
			)
		`, gatewayID); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO gateway_directory_ingestion_runs(
			ingestion_run_id, gateway_instance_id, scheduled_at, created_at
		) VALUES (
			$1::uuid, $2::uuid,
			'2026-09-03T00:00:00Z'::timestamptz,
			'2026-09-03T00:00:00Z'::timestamptz
		)
	`, runID, gatewayID); err != nil {
		t.Fatalf("insert aligned run: %v", err)
	}

	if _, err := tx.Exec(ctx, "SAVEPOINT directory_case"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO gateway_directory_ingestion_runs(
			gateway_instance_id, scheduled_at
		) VALUES (
			$1::uuid, '2026-09-03T00:00:01Z'::timestamptz
		)
	`, gatewayID)
	requireGatewayDirectorySQLState(t, err, "23514")
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT directory_case"); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, "SAVEPOINT directory_case"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO gateway_directory_ingestion_runs(
			ingestion_run_id, gateway_instance_id, scheduled_at
		) VALUES (
			$1::uuid, $2::uuid, '2026-09-03T00:03:00Z'::timestamptz
		)
	`, secondRun, gatewayID)
	requireGatewayDirectorySQLState(t, err, "23505")
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT directory_case"); err != nil {
		t.Fatal(err)
	}

	const fingerprintHex = "1111111111111111111111111111111111111111111111111111111111111111"
	if _, err := tx.Exec(ctx, `
		INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint,
			fingerprint_encoding_version, schema_version, account_count
		) VALUES (
			$1::uuid, $2::uuid, decode($3, 'hex'), 1, 1, 1
		)
	`, snapshotID, gatewayID, fingerprintHex); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES (
			$1::uuid, 137, 'CLIProxy JP-01', 'future-platform',
			'apikey', NULL, 'future-status'
		)
	`, snapshotID); err != nil {
		t.Fatalf("insert snapshot item: %v", err)
	}

	if _, err := tx.Exec(ctx, "SAVEPOINT directory_case"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO gateway_directory_snapshots(
			gateway_instance_id, fingerprint,
			fingerprint_encoding_version, schema_version, account_count
		) VALUES (
			$1::uuid, decode($2, 'hex'), 1, 1, 1
		)
	`, gatewayID, fingerprintHex)
	requireGatewayDirectorySQLState(t, err, "23505")
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT directory_case"); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE gateway_directory_ingestion_runs
		SET status='running',
		    attempt_count=1,
		    first_started_at='2026-09-03T00:00:05Z'::timestamptz,
		    last_started_at='2026-09-03T00:00:05Z'::timestamptz,
		    lease_expires_at='2026-09-03T00:00:20Z'::timestamptz,
		    lease_fencing_token=$2::uuid
		WHERE ingestion_run_id=$1::uuid
	`, runID, leaseToken); err != nil {
		t.Fatalf("start run: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE gateway_directory_ingestion_runs
		SET status='succeeded',
		    lease_expires_at=NULL,
		    lease_fencing_token=NULL,
		    terminal_at='2026-09-03T00:00:10Z'::timestamptz,
		    outcome='changed',
		    source_generated_at='2026-09-03T00:00:09Z'::timestamptz,
		    received_at='2026-09-03T00:00:10Z'::timestamptz,
		    content_fingerprint=decode($2, 'hex'),
		    snapshot_id=$3::uuid,
		    account_count=1
		WHERE ingestion_run_id=$1::uuid
	`, runID, fingerprintHex, snapshotID); err != nil {
		t.Fatalf("succeed run: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO gateway_directory_current_state(
			gateway_instance_id,
			current_snapshot_id,
			current_content_fingerprint,
			last_success_received_at,
			last_source_generated_at,
			last_success_run_id,
			updated_at
		) VALUES (
			$1::uuid,
			$2::uuid,
			decode($3, 'hex'),
			'2026-09-03T00:00:10Z'::timestamptz,
			'2026-09-03T00:00:09Z'::timestamptz,
			$4::uuid,
			'2026-09-03T00:00:10Z'::timestamptz
		)
	`, gatewayID, snapshotID, fingerprintHex, runID); err != nil {
		t.Fatalf("insert current state: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO gateway_directory_ingestion_runs(
			ingestion_run_id, gateway_instance_id, scheduled_at
		) VALUES (
			$1::uuid, $2::uuid, '2026-09-03T00:03:00Z'::timestamptz
		)
	`, secondRun, gatewayID); err != nil {
		t.Fatalf("insert next run after terminal: %v", err)
	}

	var (
		runCount      int
		snapshotCount int
		itemCount     int
		currentCount  int
	)
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1::uuid),
			(SELECT count(*) FROM gateway_directory_snapshots WHERE gateway_instance_id=$1::uuid),
			(SELECT count(*) FROM gateway_directory_snapshot_items WHERE snapshot_id=$2::uuid),
			(SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1::uuid)
	`, gatewayID, snapshotID).Scan(&runCount, &snapshotCount, &itemCount, &currentCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 2 || snapshotCount != 1 || itemCount != 1 || currentCount != 1 {
		t.Fatalf(
			"unexpected directory counts: runs=%d snapshots=%d items=%d current=%d",
			runCount, snapshotCount, itemCount, currentCount,
		)
	}

	var snapshotUpdate, snapshotDelete, itemUpdate, itemDelete bool
	if err := tx.QueryRow(ctx, `
		SELECT
			has_table_privilege('relay_control_runtime', 'gateway_directory_snapshots', 'UPDATE'),
			has_table_privilege('relay_control_runtime', 'gateway_directory_snapshots', 'DELETE'),
			has_table_privilege('relay_control_runtime', 'gateway_directory_snapshot_items', 'UPDATE'),
			has_table_privilege('relay_control_runtime', 'gateway_directory_snapshot_items', 'DELETE')
	`).Scan(&snapshotUpdate, &snapshotDelete, &itemUpdate, &itemDelete); err != nil {
		t.Fatal(err)
	}
	if snapshotUpdate || snapshotDelete || itemUpdate || itemDelete {
		t.Fatalf(
			"runtime must not mutate committed snapshot content: snapshot_update=%v snapshot_delete=%v item_update=%v item_delete=%v",
			snapshotUpdate, snapshotDelete, itemUpdate, itemDelete,
		)
	}

	const failedRunID = "00000000-0000-4000-8000-00000000d103"
	if _, err := tx.Exec(ctx, `
		INSERT INTO gateway_directory_ingestion_runs(
			ingestion_run_id, gateway_instance_id, scheduled_at, status,
			attempt_count, created_at, first_started_at, last_started_at,
			lease_expires_at, lease_fencing_token, terminal_at, last_failure_class
		) VALUES (
			$1::uuid, $2::uuid, '2026-09-03T00:06:00Z'::timestamptz,
			'failed', 1,
			'2026-09-03T00:06:00Z'::timestamptz,
			'2026-09-03T00:06:05Z'::timestamptz,
			'2026-09-03T00:06:05Z'::timestamptz,
			NULL, NULL,
			'2026-09-03T00:06:10Z'::timestamptz,
			'transport'
		)
	`, failedRunID, gatewayID); err != nil {
		t.Fatalf("insert failed terminal run: %v", err)
	}

	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "succeeded update",
			sql: `
				UPDATE gateway_directory_ingestion_runs
				SET outcome = outcome
				WHERE ingestion_run_id = $1::uuid
			`,
			args: []any{runID},
		},
		{
			name: "failed update",
			sql: `
				UPDATE gateway_directory_ingestion_runs
				SET outcome = outcome
				WHERE ingestion_run_id = $1::uuid
			`,
			args: []any{failedRunID},
		},
		{
			name: "illegal transition",
			sql: `
				UPDATE gateway_directory_ingestion_runs
				SET status = 'retry_wait'
				WHERE ingestion_run_id = $1::uuid
			`,
			args: []any{secondRun},
		},
		{
			name: "identity update",
			sql: `
				UPDATE gateway_directory_ingestion_runs
				SET scheduled_at = '2026-09-03T00:06:00Z'::timestamptz
				WHERE ingestion_run_id = $1::uuid
			`,
			args: []any{secondRun},
		},
	} {
		if _, err := tx.Exec(ctx, "SAVEPOINT gateway_directory_run_guard"); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, tc.sql, tc.args...)
		requireGatewayDirectorySQLState(t, err, "23514")
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT gateway_directory_run_guard"); err != nil {
			t.Fatal(err)
		}
		_ = tc.name
	}
}
