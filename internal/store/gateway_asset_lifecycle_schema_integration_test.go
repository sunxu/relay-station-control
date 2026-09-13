package store_test

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func newGatewayLifecycleMigrationDatabase(t *testing.T, ctx context.Context) (string, *pgx.Conn, func()) {
	t.Helper()
	ownerConfig, err := pgx.ParseConfig(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	maintenanceConfig := ownerConfig.Copy()
	maintenanceConfig.Database = "postgres"
	maintenance, err := pgx.ConnectConfig(ctx, maintenanceConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	databaseName := strings.ReplaceAll(assetFixtureSuffix(t), "-", "_")
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := maintenance.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		maintenance.Close(ctx)
		t.Fatalf("create isolated migration database: %v", err)
	}

	databaseLocation, err := url.Parse(testDatabaseURL(t))
	if err != nil {
		maintenance.Close(ctx)
		t.Fatal(err)
	}
	databaseLocation.Path = "/" + databaseName
	databaseURL := databaseLocation.String()
	databaseConfig := ownerConfig.Copy()
	databaseConfig.Database = databaseName
	database, err := pgx.ConnectConfig(ctx, databaseConfig)
	if err != nil {
		maintenance.Close(ctx)
		t.Fatalf("connect isolated database: %v", err)
	}
	cleanup := func() {
		_, _ = maintenance.Exec(context.Background(),
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		_, _ = maintenance.Exec(context.Background(), "DROP DATABASE IF EXISTS "+identifier)
		maintenance.Close(context.Background())
	}
	return databaseURL, database, cleanup
}

func applyGatewayLifecycleMigration(t *testing.T, ctx context.Context, databaseURL string, target string) error {
	t.Helper()
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	if err := runAssetGoose(t, ctx, repositoryRoot, databaseURL, "up-to", target); err != nil {
		return fmt.Errorf("apply migrations through %s: %w", target, err)
	}
	return nil
}

func seedGateway(t *testing.T, ctx context.Context, database *pgx.Conn, endpoint string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := database.Exec(ctx, `INSERT INTO gateway_instances(
		singleton_id, instance_id, display_name, management_endpoint
	) VALUES (1, $1, 'Schema Proof Gateway', $2)`, id, endpoint); err != nil {
		t.Fatalf("seed gateway: %v", err)
	}
	return id
}

func TestGatewayAssetLifecycleMigrationPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, database, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()

	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "32"); err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	if _, err := database.Exec(ctx, `INSERT INTO control_admin_users(
		admin_id, login_name, display_name, status, activated_at
	) VALUES ($1, 'schema-proof-admin', 'Schema Proof Admin', 'enabled', CURRENT_TIMESTAMP)`, adminID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	oldGatewayID := seedGateway(t, ctx, database, "http://gateway.example/")
	database.Close(ctx)

	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "33"); err != nil {
		t.Fatal(err)
	}
	databaseConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	database, err = pgx.ConnectConfig(ctx, databaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close(ctx)

	var primaryDefinition string
	if err := database.QueryRow(ctx, `SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conname='gateway_instances_pkey'`).Scan(&primaryDefinition); err != nil {
		t.Fatalf("read gateway primary key: %v", err)
	}
	if primaryDefinition != "PRIMARY KEY (instance_id)" {
		t.Fatalf("gateway primary key = %q", primaryDefinition)
	}

	var directForeignKeys int
	if err := database.QueryRow(ctx, `SELECT count(*)
		FROM pg_constraint AS fk
		JOIN pg_index AS referenced_index ON referenced_index.indexrelid = fk.conindid
		WHERE fk.contype='f'
		  AND fk.confrelid='public.gateway_instances'::regclass
		  AND referenced_index.indisprimary`).Scan(&directForeignKeys); err != nil {
		t.Fatalf("read gateway foreign keys: %v", err)
	}
	if directForeignKeys != 6 {
		t.Fatalf("gateway foreign keys using new primary key = %d, want 6 (four existing references plus two lineage references)", directForeignKeys)
	}

	var oldUniqueExists bool
	if err := database.QueryRow(ctx, `SELECT to_regclass('public.gateway_instances_instance_id_key') IS NOT NULL`).Scan(&oldUniqueExists); err != nil {
		t.Fatal(err)
	}
	if oldUniqueExists {
		t.Fatal("old gateway instance_id unique backing relation remains")
	}
	var finalIndexes []string
	rows, err := database.Query(ctx, `SELECT indexname FROM pg_indexes
		WHERE schemaname='public' AND tablename='gateway_instances'
		ORDER BY indexname`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		finalIndexes = append(finalIndexes, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if got := strings.Join(finalIndexes, ","); got != "gateway_instances_current_slot_uidx,gateway_instances_pkey" {
		t.Fatalf("final gateway indexes = %q", got)
	}
	var singletonNullable bool
	if err := database.QueryRow(ctx, `SELECT NOT attnotnull
		FROM pg_attribute
		WHERE attrelid='public.gateway_instances'::regclass
		  AND attname='singleton_id'`).Scan(&singletonNullable); err != nil {
		t.Fatal(err)
	}
	if !singletonNullable {
		t.Fatal("singleton_id is still NOT NULL")
	}

	var floor int
	if err := database.QueryRow(ctx, `SELECT phase6_evidence_floor
		FROM control_runtime_compatibility WHERE singleton_id=1`).Scan(&floor); err != nil {
		t.Fatal(err)
	}
	if floor != 1 {
		t.Fatalf("compatibility floor = %d, want 1", floor)
	}
	for _, value := range []string{
		"vault://valid/reference", "file://secret", "http://secret", "https://secret",
		"vault://has space", "vault://has\tcontrol", "vault://has?query",
		"vault://has#fragment", "vault://has@identity", " vault://trimmed", "x://a",
	} {
		var databaseAccepted bool
		if err := database.QueryRow(ctx, `SELECT public.control_valid_secret_reference($1)`, value).Scan(&databaseAccepted); err != nil {
			t.Fatal(err)
		}
		if applicationAccepted := assetstore.ValidAssetSecretReference(value); applicationAccepted != databaseAccepted {
			t.Fatalf("secret reference validator drift for %q: application=%v database=%v", value, applicationAccepted, databaseAccepted)
		}
	}
	for _, constraint := range []string{"gateway_directory_ingestion_runs_failure_fixed", "gateway_directory_ingestion_runs_state_shape"} {
		var definition string
		if err := database.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='public.gateway_directory_ingestion_runs'::regclass AND conname=$1`, constraint).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "gateway_retired") || !strings.Contains(definition, "gateway_replaced") {
			t.Fatalf("%s omitted lifecycle failure taxonomy: %s", constraint, definition)
		}
	}

	commandID := uuid.New()
	if _, err := database.Exec(ctx, `INSERT INTO asset_admin_command_receipts(
		command_id, command_kind, canonical_intent_hash, sanitized_result,
		response_status, actor_admin_id
	) VALUES ($1, 'gateway.register', decode(repeat('00', 32), 'hex'),
		'{"result":"registered"}'::jsonb, 201, $2)`, commandID, adminID); err != nil {
		t.Fatalf("insert command receipt: %v", err)
	}
	if _, err := database.Exec(ctx, `UPDATE asset_admin_command_receipts
		SET command_kind='gateway.edit' WHERE command_id=$1`, commandID); err == nil {
		t.Fatal("receipt update unexpectedly succeeded")
	}
	if _, err := database.Exec(ctx, `DELETE FROM asset_admin_command_receipts WHERE command_id=$1`, commandID); err == nil {
		t.Fatal("receipt delete unexpectedly succeeded")
	}
	if _, err := database.Exec(ctx, `TRUNCATE asset_admin_command_receipts`); err == nil {
		t.Fatal("receipt truncate unexpectedly succeeded")
	}

	var replacementBoundary time.Time
	if err := database.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&replacementBoundary); err != nil {
		t.Fatal(err)
	}
	newGatewayID := uuid.New()
	if _, err := database.Exec(ctx, `UPDATE gateway_instances SET singleton_id=NULL,lifecycle_status='retired',retired_at=$2,retired_by=$3,retire_reason='replacement',revision=2,updated_at=$2 WHERE instance_id=$1`, oldGatewayID, replacementBoundary, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_instances(singleton_id,instance_id,display_name,management_endpoint,lifecycle_status,revision) VALUES(1,$1,'Replacement','http://replacement.example','active',1)`, newGatewayID); err != nil {
		t.Fatal(err)
	}
	lineageCommandID := uuid.New()
	if _, err := database.Exec(ctx, `INSERT INTO gateway_asset_replacements(old_instance_id,new_instance_id,replaced_at,replaced_by,command_id) VALUES($1,$2,$3,$4,$5)`, oldGatewayID, newGatewayID, replacementBoundary, adminID, lineageCommandID); err != nil {
		t.Fatalf("insert valid lineage: %v", err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_asset_replacements(old_instance_id,new_instance_id,replaced_at,replaced_by,command_id) VALUES($1,$2,clock_timestamp(),$3,$4)`, newGatewayID, oldGatewayID, adminID, uuid.New()); err == nil {
		t.Fatal("lineage cycle unexpectedly succeeded")
	}
	if _, err := database.Exec(ctx, `UPDATE gateway_asset_replacements SET replaced_at=clock_timestamp() WHERE command_id=$1`, lineageCommandID); err == nil {
		t.Fatal("lineage update unexpectedly succeeded")
	}
	if _, err := database.Exec(ctx, `DELETE FROM gateway_asset_replacements WHERE command_id=$1`, lineageCommandID); err == nil {
		t.Fatal("lineage delete unexpectedly succeeded")
	}
	if _, err := database.Exec(ctx, `TRUNCATE gateway_asset_replacements`); err == nil {
		t.Fatal("lineage truncate unexpectedly succeeded")
	}
}

func TestGatewayAssetLifecycleMigrationRejectsInvalidLegacyTargets(t *testing.T) {
	for _, endpoint := range []string{
		"https://legacy-gateway.example",
		"http://legacy-gateway.example/path",
	} {
		t.Run(endpoint, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			databaseURL, database, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
			defer cleanup()
			defer database.Close(ctx)

			if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "32"); err != nil {
				t.Fatal(err)
			}
			seedGateway(t, ctx, database, endpoint)
			database.Close(ctx)

			if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "33"); err == nil {
				t.Fatalf("migration accepted invalid legacy Gateway endpoint %q", endpoint)
			}
			checkConfig, err := pgx.ParseConfig(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			check, err := pgx.ConnectConfig(ctx, checkConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer check.Close(ctx)
			var version int
			if err := check.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != 32 {
				t.Fatalf("failed migration advanced version to %d", version)
			}
			var markerExists bool
			if err := check.QueryRow(ctx, `SELECT to_regclass('public.control_runtime_compatibility') IS NOT NULL`).Scan(&markerExists); err != nil {
				t.Fatal(err)
			}
			if markerExists {
				t.Fatal("failed migration left compatibility marker behind")
			}
		})
	}
}

func TestGatewayAssetLifecycleMigrationWaitsForConcurrentGatewayReaderPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, database, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()

	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "32"); err != nil {
		t.Fatal(err)
	}
	seedGateway(t, ctx, database, "http://gateway.example/")

	tx, err := database.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE gateway_instances IN ACCESS SHARE MODE`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}

	completed := make(chan error, 1)
	go func() {
		completed <- applyGatewayLifecycleMigration(t, ctx, databaseURL, "33")
	}()
	select {
	case err := <-completed:
		_ = tx.Rollback(ctx)
		t.Fatalf("migration did not wait for the concurrent Gateway reader: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-completed; err != nil {
		t.Fatalf("migration failed after the reader released its lock: %v", err)
	}

	var primaryDefinition string
	if err := database.QueryRow(ctx, `SELECT pg_get_constraintdef(oid)
		FROM pg_constraint WHERE conname='gateway_instances_pkey'`).Scan(&primaryDefinition); err != nil {
		t.Fatal(err)
	}
	if primaryDefinition != "PRIMARY KEY (instance_id)" {
		t.Fatalf("gateway primary key after lock release = %q", primaryDefinition)
	}
}
