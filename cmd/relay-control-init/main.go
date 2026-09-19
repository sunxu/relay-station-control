// Command relay-control-init performs the bounded, idempotent deployment seed.
// It is intentionally separate from the long-running Control process so the
// latter never receives the migrator connection or bootstrap privileges.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sunxu/relay-station-control/internal/environment"
)

const (
	nodeType      = "cliproxyapi"
	driverVersion = "cliproxyapi.auth-files.v1"
)

var driverCapabilities = []string{
	"management_account_inventory_read",
	"management_health_read",
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "control-init failed")
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	databaseURL := os.Getenv("CONTROL_INIT_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		return errors.New("database configuration is required")
	}
	migrationsDir := os.Getenv("CONTROL_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "/app/migrations"
	}
	envID := os.Getenv("CONTROL_ENVIRONMENT_ID")
	envType := os.Getenv("CONTROL_ENVIRONMENT")
	identity, err := environment.Validate(envID, envType)
	if err != nil {
		return errors.New("environment configuration is invalid")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return errors.New("database configuration is invalid")
	}
	defer db.Close()
	if runtimePassword := os.Getenv("CONTROL_RUNTIME_DB_PASSWORD"); runtimePassword != "" {
		if _, err = db.Exec(`DO $do$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_control_runtime') THEN
  CREATE ROLE relay_control_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
END IF;
IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_control_asset_registrar') THEN
  CREATE ROLE relay_control_asset_registrar NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
END IF;
IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_control_app') THEN
  CREATE ROLE relay_control_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS IN ROLE relay_control_runtime;
END IF;
GRANT relay_control_runtime TO relay_control_app;
END $do$`); err != nil {
			return errors.New("runtime role bootstrap failed")
		}
		if _, err = db.Exec(`ALTER ROLE relay_control_app PASSWORD '` + strings.ReplaceAll(runtimePassword, "'", "''") + `'`); err != nil {
			return errors.New("runtime role password setup failed")
		}
	}
	if err = goose.SetDialect("postgres"); err != nil {
		return errors.New("migration dialect is invalid")
	}
	if err = goose.Up(db, migrationsDir); err != nil {
		return errors.New("database migration failed")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("database configuration is invalid")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("database unavailable")
	}

	_, err = pool.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type)
VALUES ($1,$2,$3) ON CONFLICT (singleton_id) DO NOTHING`, identity.ID, identity.ID, identity.Type)
	if err != nil {
		return errors.New("environment seed failed")
	}
	var actualID, actualType string
	if err = pool.QueryRow(ctx, `SELECT environment_id,environment_type FROM environments WHERE singleton_id=1`).Scan(&actualID, &actualType); err != nil || actualID != identity.ID || actualType != identity.Type {
		return errors.New("environment identity conflict")
	}
	if _, err = pool.Exec(ctx, `SELECT public.control_register_node_driver($1,$2,$3,'active',$4::text[])`, nodeType, driverVersion, "CLIProxyAPI", driverCapabilities); err != nil {
		return errors.New("driver catalog seed failed")
	}
	active := csvEnv("CONTROL_INITIAL_ACTIVE_PROVIDERS", []string{"antigravity"})
	outOfScope := csvEnv("CONTROL_INITIAL_OUT_OF_SCOPE_PROVIDERS", []string{})
	var policyCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM provider_inventory_policy_activations WHERE node_type=$1 AND driver_contract_version=$2 AND active_range @> CURRENT_TIMESTAMP`, nodeType, driverVersion).Scan(&policyCount)
	if err != nil {
		return errors.New("provider policy verification failed")
	}
	if policyCount == 0 {
		if _, err = pool.Exec(ctx, `SELECT public.control_activate_provider_policy($1,$2,$3::text[],$4::text[],'deployment-init',NULL)`, nodeType, driverVersion, active, outOfScope); err != nil {
			return errors.New("provider policy seed failed")
		}
	}
	var usable int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM provider_inventory_policy_activations WHERE node_type=$1 AND driver_contract_version=$2 AND active_range @> CURRENT_TIMESTAMP`, nodeType, driverVersion).Scan(&usable)
	if err != nil || usable != 1 {
		return errors.New("provider policy is not usable")
	}
	return nil
}

func csvEnv(name string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}
