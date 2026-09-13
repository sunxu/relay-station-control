package store_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func nodeMonitoringACLSQLState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestNodeMonitoringOperationalRegistrarACLAndScriptPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "35"); err != nil {
		t.Fatal(err)
	}

	nodeID := uuid.New()
	adminID := uuid.New()
	if _, err := owner.Exec(ctx, `
		INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,'operational-acl-admin','Operational ACL Admin','enabled',clock_timestamp())`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `
		INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','Operational ACL Driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `
		INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `
		SELECT public.control_create_relay_node_asset(
			$1,'Operational ACL Node','cliproxyapi','v1','http://node.example',NULL,
			ARRAY['management_health_read']::text[])`, nodeID); err != nil {
		t.Fatal(err)
	}
	fenceID := uuid.New()
	if _, err := owner.Exec(ctx, `
		INSERT INTO asset_admin_command_receipts(
			command_id,command_kind,canonical_intent_hash,sanitized_result,
			response_status,actor_admin_id,committed_at)
		VALUES($1,'node.monitoring_disable',decode(repeat('00',32),'hex'),
			jsonb_build_object('instance_id',$2::text),200,$3,clock_timestamp())`,
		fenceID, nodeID, adminID); err != nil {
		t.Fatal(err)
	}

	if _, err := owner.Exec(ctx, `SET ROLE relay_control_asset_registrar`); err != nil {
		t.Fatal("SET ROLE registrar: ", err)
	}
	var latest string
	if err := owner.QueryRow(ctx, `SELECT coalesce(public.control_latest_node_disable_fence_v1($1)::text,'')`, nodeID).Scan(&latest); err != nil {
		t.Fatal("registrar latest fence lookup: ", err)
	}
	if latest != fenceID.String() {
		t.Fatalf("registrar latest fence=%s want=%s", latest, fenceID)
	}
	if _, err := owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,NULL,'deployment_enable','registrar',$2)`, nodeID, fenceID); err != nil {
		t.Fatal("registrar deployment enable: ", err)
	}
	if _, err := owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,NULL,'administrator_enable',$2::text,$3)`, nodeID, adminID.String(), fenceID); nodeMonitoringACLSQLState(err) != "42501" {
		t.Fatalf("registrar administrator reason SQLSTATE=%q err=%v", nodeMonitoringACLSQLState(err), err)
	}
	if _, err := owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,false,NULL,'administrator_disable',$2::text,$3)`, nodeID, adminID.String(), fenceID); nodeMonitoringACLSQLState(err) != "42501" {
		t.Fatalf("registrar administrator disable SQLSTATE=%q err=%v", nodeMonitoringACLSQLState(err), err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at)
		VALUES($1,clock_timestamp()+interval '1 hour','scheduled_enable','registrar',clock_timestamp())`, nodeID); err == nil {
		t.Fatal("registrar direct monitoring INSERT unexpectedly succeeded")
	}

	if _, err := owner.Exec(ctx, `RESET ROLE; SET ROLE relay_control_runtime`); err != nil {
		t.Fatal("SET ROLE runtime: ", err)
	}
	if err := owner.QueryRow(ctx, `SELECT coalesce(public.control_latest_node_disable_fence_v1($1)::text,'')`, nodeID).Scan(&latest); err != nil {
		t.Fatal("runtime latest fence lookup: ", err)
	}
	if latest != fenceID.String() {
		t.Fatalf("runtime latest fence=%s want=%s", latest, fenceID)
	}
	if _, err := owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,false,NULL,'deployment_disable','runtime',$2)`, nodeID, fenceID); nodeMonitoringACLSQLState(err) != "42501" {
		t.Fatalf("runtime deployment reason SQLSTATE=%q err=%v", nodeMonitoringACLSQLState(err), err)
	}
	if _, err := owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,NULL,'deployment_enable','runtime',$2)`, nodeID, fenceID); nodeMonitoringACLSQLState(err) != "42501" {
		t.Fatalf("runtime deployment enable SQLSTATE=%q err=%v", nodeMonitoringACLSQLState(err), err)
	}
	if _, err := owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,false,NULL,'administrator_disable',$2::text,$3)`, nodeID, adminID.String(), fenceID); err != nil {
		t.Fatal("runtime administrator disable: ", err)
	}
	if _, err := owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,NULL,'administrator_enable',$2::text,$3)`, nodeID, adminID.String(), fenceID); err != nil {
		t.Fatal("runtime administrator enable: ", err)
	}
	if _, err := owner.Exec(ctx, `RESET ROLE; SET ROLE relay_control_asset_registrar`); err != nil {
		t.Fatal("restore registrar role: ", err)
	}

	registrarURL := operationalRegistrarURL(t, databaseURL)
	for _, enabled := range []string{"true", "false"} {
		reason := "deployment_enable"
		if enabled == "false" {
			reason = "deployment_disable"
		}
		output, err := runFormalMonitoringScript(ctx, t, registrarURL, databaseURL, nodeID, enabled, reason)
		if err != nil {
			t.Fatalf("formal registrar script enabled=%s: %v\n%s", enabled, err, strings.TrimSpace(string(output)))
		}
	}
}

func runFormalMonitoringScript(ctx context.Context, t *testing.T, registrarURL, databaseURL string, nodeID uuid.UUID, enabled, reason string) ([]byte, error) {
	t.Helper()
	scriptPath := filepath.Join("..", "..", "deploy", "asset-registry", "set-node-monitoring.sql")
	script, err := os.Open(scriptPath)
	if err != nil {
		return nil, err
	}
	defer script.Close()

	args := []string{
		"-X", "--no-psqlrc", "--set=ON_ERROR_STOP=on", "--dbname", registrarURL,
		"--set=node_instance_id=" + nodeID.String(),
		"--set=enabled=" + enabled,
		"--set=effective_at=", "--set=reason=" + reason, "--set=actor=registrar-script",
	}
	var command *exec.Cmd
	if psqlPath, lookupErr := exec.LookPath("psql"); lookupErr == nil {
		command = exec.CommandContext(ctx, psqlPath, args...)
		command.Env = append(os.Environ(), "PGPASSWORD=relay_control_asset_registrar_dev_only", "PGTZ=UTC")
	} else {
		parsed, parseErr := url.Parse(databaseURL)
		if parseErr != nil {
			return nil, parseErr
		}
		databaseName := strings.TrimPrefix(parsed.Path, "/")
		container := os.Getenv("CONTROL_POSTGRES_CONTAINER")
		if container == "" {
			container = "relay-station-dev-control-postgres-1"
		}
		containerArgs := []string{
			"exec", "-i", container, "env",
			"PGPASSWORD=relay_control_asset_registrar_dev_only", "PGTZ=UTC",
			"psql", "-X", "--no-psqlrc", "--set=ON_ERROR_STOP=on",
			"--username", "relay_control_asset_registrar_dev", "--dbname", databaseName,
			"--set=node_instance_id=" + nodeID.String(), "--set=enabled=" + enabled,
			"--set=effective_at=", "--set=reason=" + reason, "--set=actor=registrar-script",
		}
		command = exec.CommandContext(ctx, "docker", containerArgs...)
	}
	command.Stdin = script
	return command.CombinedOutput()
}

func operationalRegistrarURL(t *testing.T, databaseURL string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.User("relay_control_asset_registrar_dev")
	return parsed.String()
}
