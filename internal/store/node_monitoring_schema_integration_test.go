package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestNodeMonitoringOperationsMigrationPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	owner.Close(ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "35"); err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)

	adminID, nodeID := uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'monitoring-admin','Monitoring Admin','enabled',clock_timestamp())`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `SELECT public.control_create_relay_node_asset($1,'Monitoring Node','cliproxyapi','v1','http://node.example',NULL,ARRAY['management_health_read']::text[])`, nodeID); err != nil {
		t.Fatal(err)
	}
	repository, err := assetstore.NewNodeMonitoringRepository(pool)
	if err != nil {
		t.Fatal(err)
	}

	enable := assetstore.NodeMonitoringCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "enable", InstanceID: nodeID}
	enabled, err := repository.Enable(ctx, enable)
	if err != nil || enabled.HTTPStatus != 200 {
		t.Fatalf("enable status=%d err=%v", enabled.HTTPStatus, err)
	}
	var enabledBody struct {
		Result           string `json:"result"`
		MonitoringActive bool   `json:"monitoring_active"`
	}
	if err = json.Unmarshal(enabled.Body, &enabledBody); err != nil || enabledBody.Result != "enabled" || !enabledBody.MonitoringActive {
		t.Fatalf("enable body=%s err=%v", enabled.Body, err)
	}
	enableReplay, err := repository.Enable(ctx, enable)
	if err != nil || !enableReplay.Replayed || !jsonEqual(enableReplay.Body, enabled.Body) {
		t.Fatalf("enable replay=%s err=%v", enableReplay.Body, err)
	}

	disable := assetstore.NodeMonitoringCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "disable", InstanceID: nodeID}
	disabled, err := repository.Disable(ctx, disable)
	if err != nil || disabled.HTTPStatus != 200 {
		t.Fatalf("disable status=%d err=%v", disabled.HTTPStatus, err)
	}
	var disabledBody struct {
		Result           string `json:"result"`
		MonitoringActive bool   `json:"monitoring_active"`
	}
	if err = json.Unmarshal(disabled.Body, &disabledBody); err != nil || disabledBody.Result != "disabled" || disabledBody.MonitoringActive {
		t.Fatalf("disable body=%s err=%v", disabled.Body, err)
	}
	disableReplay, err := repository.Disable(ctx, disable)
	if err != nil || !disableReplay.Replayed || !jsonEqual(disableReplay.Body, disabled.Body) {
		t.Fatalf("disable replay=%s err=%v", disableReplay.Body, err)
	}
	var generationBefore int64
	if err = owner.QueryRow(ctx, `SELECT node_generation FROM asset_registry_generations WHERE singleton_id=1`).Scan(&generationBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) VALUES($1,clock_timestamp()+interval '1 hour','scheduled_enable','scheduler',clock_timestamp())`, nodeID); err != nil {
		t.Fatal(err)
	}
	futureDisable := assetstore.NodeMonitoringCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "disable-future", InstanceID: nodeID}
	futureResult, err := repository.Disable(ctx, futureDisable)
	if err != nil {
		t.Fatal(err)
	}
	var futureBody struct {
		Result    string `json:"result"`
		Cancelled int64  `json:"cancelled_future_monitoring_count"`
	}
	if err = json.Unmarshal(futureResult.Body, &futureBody); err != nil || futureBody.Result != "disabled" || futureBody.Cancelled != 1 {
		t.Fatalf("future-only disable body=%s err=%v", futureResult.Body, err)
	}
	var generationAfter int64
	if err = owner.QueryRow(ctx, `SELECT node_generation FROM asset_registry_generations WHERE singleton_id=1`).Scan(&generationAfter); err != nil {
		t.Fatal(err)
	}
	if generationAfter != generationBefore {
		t.Fatalf("future-only disable generation=%d before=%d", generationAfter, generationBefore)
	}
	alreadyDisabled, err := repository.Disable(ctx, assetstore.NodeMonitoringCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "disable-already", InstanceID: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	var alreadyBody struct {
		Result string `json:"result"`
	}
	if err = json.Unmarshal(alreadyDisabled.Body, &alreadyBody); err != nil || alreadyBody.Result != "already_disabled" {
		t.Fatalf("already-disabled body=%s err=%v", alreadyDisabled.Body, err)
	}

	var receiptCount, auditCount int
	if err = owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM asset_admin_command_receipts WHERE command_kind='node.monitoring_disable'), (SELECT count(*) FROM audit_logs WHERE action='node.monitoring_disable')`).Scan(&receiptCount, &auditCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 3 || auditCount != 2 {
		t.Fatalf("disable receipt/audit=%d/%d", receiptCount, auditCount)
	}
	var indexExists, oldFunctionExists, newFunctionExecutable, readerSecretReadable bool
	var activationInsert, activationUpdate, activationDelete, activationTruncate bool
	if err = owner.QueryRow(ctx, `SELECT to_regclass('public.asset_admin_command_receipts_node_disable_fence_idx') IS NOT NULL, to_regprocedure('public.control_set_node_inventory_monitoring(uuid,boolean,timestamptz,text,text)') IS NOT NULL, has_function_privilege('relay_control_runtime',to_regprocedure('public.control_set_node_inventory_monitoring(uuid,boolean,timestamptz,text,text,uuid)'),'EXECUTE')`).Scan(&indexExists, &oldFunctionExists, &newFunctionExecutable); err != nil {
		t.Fatal(err)
	}
	if !indexExists || oldFunctionExists || !newFunctionExecutable {
		t.Fatalf("index/old/new=%t/%t/%t", indexExists, oldFunctionExists, newFunctionExecutable)
	}
	if err = owner.QueryRow(ctx, `SELECT has_column_privilege('relay_control_runtime','relay_node_assets','reader_secret_ref','SELECT')`).Scan(&readerSecretReadable); err != nil {
		t.Fatal(err)
	}
	if readerSecretReadable {
		t.Fatal("runtime unexpectedly can read relay_node_assets.reader_secret_ref")
	}
	if _, err = pool.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring($1,false,NULL,'deployment_disable','runtime')`, nodeID); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("old five-parameter writer error=%v", err)
	}
	if err = owner.QueryRow(ctx, `SELECT has_table_privilege('relay_control_runtime','relay_node_inventory_monitoring_activations','INSERT'), has_any_column_privilege('relay_control_runtime','relay_node_inventory_monitoring_activations','UPDATE'), has_table_privilege('relay_control_runtime','relay_node_inventory_monitoring_activations','DELETE'), has_table_privilege('relay_control_runtime','relay_node_inventory_monitoring_activations','TRUNCATE')`).Scan(&activationInsert, &activationUpdate, &activationDelete, &activationTruncate); err != nil {
		t.Fatal(err)
	}
	if activationInsert || !activationUpdate || activationDelete || activationTruncate {
		t.Fatalf("runtime activation privileges=%t/%t/%t/%t", activationInsert, activationUpdate, activationDelete, activationTruncate)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,canonical_intent_hash,sanitized_result,response_status,actor_admin_id,committed_at) VALUES($1,'node.monitoring_disable',decode(repeat('00',32),'hex'),jsonb_build_object('instance_id',$2::text),200,$3,(SELECT committed_at FROM asset_admin_command_receipts WHERE command_kind='node.monitoring_disable' LIMIT 1))`, uuid.New(), nodeID, adminID); err == nil {
		t.Fatal("receipt timestamp tie unexpectedly succeeded")
	}
	if _, err = owner.Exec(ctx, `UPDATE asset_admin_command_receipts SET response_status=201 WHERE command_id=$1`, disable.CommandID); err == nil {
		t.Fatal("disable receipt update unexpectedly succeeded")
	}
	if _, err = owner.Exec(ctx, `DELETE FROM asset_admin_command_receipts WHERE command_id=$1`, disable.CommandID); err == nil {
		t.Fatal("disable receipt delete unexpectedly succeeded")
	}
}
