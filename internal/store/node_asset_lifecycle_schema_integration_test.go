package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestNodeAssetLifecycleMigrationPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, database, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	database.Close(ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "34"); err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	database, err = pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close(ctx)
	var floor, version int
	if err = database.QueryRow(ctx, `SELECT phase6_evidence_floor,(SELECT max(version_id) FROM goose_db_version WHERE is_applied) FROM control_runtime_compatibility WHERE singleton_id=1`).Scan(&floor, &version); err != nil {
		t.Fatal(err)
	}
	if floor != 2 || version != 34 {
		t.Fatalf("floor/version=%d/%d", floor, version)
	}
	var serverMajor int
	if err = database.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer / 10000`).Scan(&serverMajor); err != nil || serverMajor != 18 {
		t.Fatalf("server major=%d err=%v", serverMajor, err)
	}
	var expression string
	if err = database.QueryRow(ctx, `SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE d.adrelid='relay_node_inventory_monitoring_activations'::regclass AND a.attname='active_range'`).Scan(&expression); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expression, "cancelled_at") || !strings.Contains(expression, "'empty'") {
		t.Fatalf("generated active_range=%q", expression)
	}
	var exclusionCount int
	if err = database.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='relay_node_inventory_monitoring_activations'::regclass AND contype='x'`).Scan(&exclusionCount); err != nil || exclusionCount != 1 {
		t.Fatalf("monitoring exclusions=%d err=%v", exclusionCount, err)
	}
	var secretReadable, authorizeExecutable, abandonExecutable, generationWritable bool
	var nodeInsertable, capabilityInsertable, creatorExecutable bool
	if err = database.QueryRow(ctx, `SELECT
		has_column_privilege('relay_control_runtime','relay_node_assets','reader_secret_ref','SELECT'),
		has_function_privilege('relay_control_runtime','public.control_authorize_account_inventory_poll_dispatch(uuid,integer,uuid)','EXECUTE'),
		has_function_privilege('relay_control_runtime','public.control_abandon_node_inventory_poll_runs(uuid,timestamptz,text)','EXECUTE'),
		has_table_privilege('relay_control_runtime','asset_registry_generations','UPDATE'),
		has_table_privilege('relay_control_runtime','relay_node_assets','INSERT'),
		has_table_privilege('relay_control_runtime','node_capabilities','INSERT'),
		has_function_privilege('relay_control_runtime','public.control_create_relay_node_asset(uuid,text,text,text,text,text,text[])','EXECUTE')`).Scan(&secretReadable, &authorizeExecutable, &abandonExecutable, &generationWritable, &nodeInsertable, &capabilityInsertable, &creatorExecutable); err != nil {
		t.Fatal(err)
	}
	if secretReadable || !authorizeExecutable || !abandonExecutable || generationWritable || nodeInsertable || capabilityInsertable || !creatorExecutable {
		t.Fatalf("ACL secret/authorize/abandon/generation/node-insert/cap-insert/creator=%t/%t/%t/%t/%t/%t/%t", secretReadable, authorizeExecutable, abandonExecutable, generationWritable, nodeInsertable, capabilityInsertable, creatorExecutable)
	}
}

func TestNodeLifecycleCommandsReplayAndLineagePG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "34"); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'node-admin','Node Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_account_inventory_read'),('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := assetstore.NewNodeLifecycleRepository(pool, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}

	first, second, third := uuid.New(), uuid.New(), uuid.New()
	register := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "node-register", NewInstanceID: first, DisplayName: assetstore.StringPatch{Present: true, Value: "Node A"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "https://node-a.example"}, NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_health_read", "management_account_inventory_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "vault://node/a"}}
	created, err := repository.Register(ctx, register)
	if err != nil || created.HTTPStatus != 201 {
		t.Fatalf("register status=%d err=%v", created.HTTPStatus, err)
	}
	var createdBody struct {
		Asset assetstore.NodeAsset `json:"asset"`
	}
	if err = json.Unmarshal(created.Body, &createdBody); err != nil || createdBody.Asset.Monitoring.Current || len(createdBody.Asset.Capabilities) != 2 {
		t.Fatalf("register projection=%s err=%v", created.Body, err)
	}
	for _, statement := range []string{
		`INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,'cliproxyapi','v1','management_health_read')`,
		`UPDATE node_capabilities SET capability='management_health_read' WHERE instance_id=$1`,
		`DELETE FROM node_capabilities WHERE instance_id=$1`,
	} {
		if _, mutationErr := pool.Exec(ctx, statement, first); mutationErr == nil {
			t.Fatalf("runtime capability mutation unexpectedly succeeded: %s", statement)
		}
	}
	var revision, postDirectGeneration, auditCount, receiptCount int64
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT revision FROM relay_node_assets WHERE instance_id=$1),
		(SELECT node_generation FROM asset_registry_generations WHERE singleton_id=1),
		(SELECT count(*) FROM audit_logs WHERE category='asset_node' AND details->>'instance_id'=$1::text),
		(SELECT count(*) FROM asset_admin_command_receipts WHERE command_id=$2)`, first, register.CommandID).Scan(&revision, &postDirectGeneration, &auditCount, &receiptCount); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || postDirectGeneration != 1 || auditCount != 1 || receiptCount != 1 {
		t.Fatalf("failed direct capability mutation changed truth revision/generation/audit/receipt=%d/%d/%d/%d", revision, postDirectGeneration, auditCount, receiptCount)
	}
	replay, err := repository.Register(ctx, register)
	if err != nil || !replay.Replayed || !jsonEqual(created.Body, replay.Body) {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	withoutKey, err := assetstore.NewNodeLifecycleRepository(pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := register
	other.ActorAdminID = uuid.New()
	other.ManagementEndpoint.Value = "invalid"
	if _, err = withoutKey.Register(ctx, other); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("actor-first=%v", err)
	}
	if _, err = withoutKey.Register(ctx, register); !errors.Is(err, assetstore.ErrReceiptKeyUnavailable) {
		t.Fatalf("historical SecretSet without K1=%v", err)
	}
	wrongSecret := register
	wrongSecret.Secret.Value = "vault://node/other"
	if _, err = repository.Register(ctx, wrongSecret); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("intent conflict=%v", err)
	}
	replacementKey, err := assetstore.NewNodeLifecycleRepository(pool, []byte("abcdefghijklmnopqrstuvwxyzABCDEF"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replacementKey.Register(ctx, register); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("structurally valid replacement K1=%v", err)
	}
	if restored, replayErr := repository.Register(ctx, register); replayErr != nil || !restored.Replayed || !jsonEqual(created.Body, restored.Body) {
		t.Fatalf("restored historical K1 replay=%#v err=%v", restored, replayErr)
	}
	missingKeyCommand := register
	missingKeyCommand.CommandID = uuid.New()
	missingKeyCommand.NewInstanceID = uuid.New()
	if _, err = withoutKey.Register(ctx, missingKeyCommand); !errors.Is(err, assetstore.ErrReceiptKeyUnavailable) {
		t.Fatalf("new SecretSet without K1=%v", err)
	}
	if _, err = withoutKey.Retire(ctx, assetstore.NodeCommand{CommandID: register.CommandID, ActorAdminID: admin, InstanceID: first, ExpectedRevision: 1, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("receipt command kind conflict=%v", err)
	}

	if _, err = withoutKey.Edit(ctx, assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "node-edit", InstanceID: first, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "Node A2"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS t) INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,effective_to,reason,actor,created_at,end_reason,end_actor,end_recorded_at) SELECT $1::uuid,t,t+interval '30 minutes','deployment_enable','node-test',t,'scheduled_disable','node-test',t FROM boundary UNION ALL SELECT $1::uuid,t+interval '1 hour',NULL,'scheduled_enable','node-test',t,NULL,NULL,NULL FROM boundary`, first); err != nil {
		t.Fatal(err)
	}
	monitoredEdit := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "node-monitored-edit", InstanceID: first, ExpectedRevision: 2, DisplayName: assetstore.StringPatch{Present: true, Value: "Node A3"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	monitoredResult, err := withoutKey.Edit(ctx, monitoredEdit)
	if err != nil {
		t.Fatal(err)
	}
	var monitoredBody struct {
		Asset assetstore.NodeAsset `json:"asset"`
	}
	if err = json.Unmarshal(monitoredResult.Body, &monitoredBody); err != nil || !monitoredBody.Asset.Monitoring.Current {
		t.Fatalf("monitored edit projection=%s err=%v", monitoredResult.Body, err)
	}
	monitoredReplay, err := withoutKey.Edit(ctx, monitoredEdit)
	if err != nil || !monitoredReplay.Replayed || !jsonEqual(monitoredResult.Body, monitoredReplay.Body) {
		t.Fatalf("monitored edit replay=%s err=%v", monitoredReplay.Body, err)
	}
	monitoredDetail, err := repository.Detail(ctx, first)
	if err != nil || !monitoredDetail.Asset.Monitoring.Current || monitoredDetail.Asset.Revision != monitoredBody.Asset.Revision {
		t.Fatalf("monitored detail=%#v err=%v", monitoredDetail, err)
	}
	policyID, pollRunID, token := uuid.New(), uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by) VALUES($1,'cliproxyapi','v1',ARRAY['antigravity'],ARRAY[]::text[],'node-test')`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,first_started_at,last_started_at,lease_expires_at,lease_fencing_token,created_at) SELECT $1,$2,'cliproxyapi','v1',to_timestamp(floor(extract(epoch FROM t)/300)*300),$3,'running',1,2,299,t,t,t+interval '2 seconds',$4,t FROM (SELECT clock_timestamp() AS t) boundary`, pollRunID, first, policyID, token); err != nil {
		t.Fatal(err)
	}
	pollRepository, err := assetstore.NewInventoryPollRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	var dispatchState, activeNode, activeMonitoring, leaseValid, graceValid bool
	if err = owner.QueryRow(ctx, `SELECT run.status='running',asset.lifecycle_status='active',
		EXISTS (SELECT 1 FROM relay_node_inventory_monitoring_activations monitoring WHERE monitoring.instance_id=run.instance_id AND monitoring.cancelled_at IS NULL AND clock_timestamp() <@ monitoring.active_range),
		run.lease_expires_at>clock_timestamp(),run.scheduled_at+make_interval(secs=>run.poll_start_grace_seconds)>clock_timestamp()
		FROM account_inventory_poll_runs run JOIN relay_node_assets asset ON asset.instance_id=run.instance_id WHERE run.poll_run_id=$1`, pollRunID).Scan(&dispatchState, &activeNode, &activeMonitoring, &leaseValid, &graceValid); err != nil {
		t.Fatal(err)
	}
	if !dispatchState || !activeNode || !activeMonitoring || !leaseValid || !graceValid {
		t.Fatalf("dispatch preconditions status/node/monitoring/lease/grace=%t/%t/%t/%t/%t", dispatchState, activeNode, activeMonitoring, leaseValid, graceValid)
	}
	if _, err = pollRepository.AuthorizeDispatch(ctx, inventorypoll.DispatchAuthorizationRequest{PollRunID: pollRunID, FencingToken: token, Attempt: 1, RequestTimeout: time.Second}); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err = pollRepository.AuthorizeDispatch(ctx, inventorypoll.DispatchAuthorizationRequest{PollRunID: pollRunID, FencingToken: token, Attempt: 1, RequestTimeout: time.Second}); !errors.Is(err, assetstore.ErrPollRunLeaseLost) {
		t.Fatalf("duplicate authorization=%v", err)
	}
	replace := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "node-replace", InstanceID: first, ExpectedRevision: 3, NewInstanceID: second, DisplayName: assetstore.StringPatch{Present: true, Value: "Node B"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "https://node-b.example"}, NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_account_inventory_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretClear}}
	replaceResult, replaceErr := withoutKey.Replace(ctx, replace)
	if replaceErr != nil || replaceResult.HTTPStatus != 200 {
		t.Fatalf("replace=%s err=%v", replaceResult.Body, replaceErr)
	}
	var replaceBody struct {
		OldAsset assetstore.NodeAsset `json:"old_asset"`
		NewAsset assetstore.NodeAsset `json:"new_asset"`
	}
	if err = json.Unmarshal(replaceResult.Body, &replaceBody); err != nil || replaceBody.OldAsset.Monitoring.Current || replaceBody.NewAsset.Monitoring.Current || replaceBody.OldAsset.LifecycleStatus != "retired" || replaceBody.NewAsset.LifecycleStatus != "active" {
		t.Fatalf("replace projection=%s err=%v", replaceResult.Body, err)
	}
	replaceReplay, err := withoutKey.Replace(ctx, replace)
	if err != nil || !replaceReplay.Replayed || !jsonEqual(replaceResult.Body, replaceReplay.Body) {
		t.Fatalf("replace replay=%s err=%v", replaceReplay.Body, err)
	}
	detail, err := repository.Detail(ctx, first)
	if err != nil || detail.Asset.LifecycleStatus != "retired" || detail.Successor == nil || detail.Successor.NewInstanceID != second {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	var closedCurrent, cancelledFuture, newMonitoring int
	if err = owner.QueryRow(ctx, `SELECT count(*) FILTER (WHERE effective_from<clock_timestamp() AND effective_to IS NOT NULL AND end_reason='node_replaced'),count(*) FILTER (WHERE cancelled_at IS NOT NULL AND cancel_reason='node_replaced' AND active_range='empty'::tstzrange),(SELECT count(*) FROM relay_node_inventory_monitoring_activations WHERE instance_id=$2) FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1`, first, second).Scan(&closedCurrent, &cancelledFuture, &newMonitoring); err != nil {
		t.Fatal(err)
	}
	if closedCurrent != 1 || cancelledFuture != 1 || newMonitoring != 0 {
		t.Fatalf("monitoring close/cancel/new=%d/%d/%d", closedCurrent, cancelledFuture, newMonitoring)
	}
	if _, mutationErr := pool.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,'cliproxyapi','v1','management_health_read')`, first); mutationErr == nil {
		t.Fatal("runtime appended capability to retired Node")
	}
	var oldEligible, newEligible bool
	if err = owner.QueryRow(ctx, `SELECT control_node_currently_eligible($1),control_node_currently_eligible($2)`, first, second).Scan(&oldEligible, &newEligible); err != nil {
		t.Fatal(err)
	}
	if oldEligible || newEligible {
		t.Fatalf("replacement eligibility old/new=%t/%t", oldEligible, newEligible)
	}
	var inheritedPolls, inheritedInventory, inheritedAvailability, inheritedBindings int
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id=$1),
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1),
		(SELECT count(*) FROM account_availability_checkpoints WHERE node_id=$1),
		(SELECT count(*) FROM relay_node_gateway_account_bindings WHERE relay_node_id=$1)`, second).Scan(&inheritedPolls, &inheritedInventory, &inheritedAvailability, &inheritedBindings); err != nil {
		t.Fatal(err)
	}
	if inheritedPolls+inheritedInventory+inheritedAvailability+inheritedBindings != 0 {
		t.Fatalf("replacement inherited poll/inventory/availability/binding=%d/%d/%d/%d", inheritedPolls, inheritedInventory, inheritedAvailability, inheritedBindings)
	}
	var runningStatus, runningReason string
	if err = owner.QueryRow(ctx, `SELECT status,coalesce(execution_reason,'') FROM account_inventory_poll_runs WHERE poll_run_id=$1`, pollRunID).Scan(&runningStatus, &runningReason); err != nil {
		t.Fatal(err)
	}
	if runningStatus != "running" || runningReason != "" {
		t.Fatalf("authorized run after replace=%s/%s", runningStatus, runningReason)
	}
	time.Sleep(2100 * time.Millisecond)
	reconciled, err := pollRepository.ReconcileOne(ctx)
	if err != nil || reconciled == nil || reconciled.Status != assetstore.PollRunAbandoned || reconciled.ExecutionReason != "node_replaced" {
		t.Fatalf("reconciled=%#v err=%v", reconciled, err)
	}
	var authorizationCleared bool
	if err = owner.QueryRow(ctx, `SELECT dispatch_authorized_attempt IS NULL AND dispatch_authorized_at IS NULL AND dispatch_authorized_fencing_token IS NULL FROM account_inventory_poll_runs WHERE poll_run_id=$1`, pollRunID).Scan(&authorizationCleared); err != nil || !authorizationCleared {
		t.Fatalf("authorization cleared=%t err=%v", authorizationCleared, err)
	}
	chainReplace := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "node-replace-chain", InstanceID: second, ExpectedRevision: 1, NewInstanceID: third, DisplayName: assetstore.StringPatch{Present: true, Value: "Node C"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "https://node-c.example"}, NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_account_inventory_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if result, replaceErr := withoutKey.Replace(ctx, chainReplace); replaceErr != nil || result.HTTPStatus != 200 {
		t.Fatalf("chain replace=%s err=%v", result.Body, replaceErr)
	}
	chainDetail, err := repository.Detail(ctx, second)
	if err != nil || chainDetail.Predecessor == nil || chainDetail.Successor == nil || chainDetail.Predecessor.OldInstanceID != first || chainDetail.Successor.NewInstanceID != third {
		t.Fatalf("chain detail=%#v err=%v", chainDetail, err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO relay_node_asset_replacements(old_instance_id,new_instance_id,replaced_at,replaced_by,command_id) VALUES($1,$2,clock_timestamp(),$3,$4)`, third, first, admin, uuid.New()); err == nil {
		t.Fatal("replacement lineage cycle was accepted")
	}
	if _, err = owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) SELECT $1,t,'deployment_enable','node-test',t FROM (SELECT clock_timestamp() AS t) boundary`, third); err != nil {
		t.Fatal(err)
	}
	retireResult, retireErr := repository.Retire(ctx, assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "node-retire", InstanceID: third, ExpectedRevision: 1, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}})
	if retireErr != nil || retireResult.HTTPStatus != 200 {
		t.Fatalf("retire=%s err=%v", retireResult.Body, retireErr)
	}
	var retireBody struct {
		Asset assetstore.NodeAsset `json:"asset"`
	}
	if err = json.Unmarshal(retireResult.Body, &retireBody); err != nil || retireBody.Asset.LifecycleStatus != "retired" || retireBody.Asset.Monitoring.Current {
		t.Fatalf("retire projection=%s err=%v", retireResult.Body, err)
	}
	var nodes, receipts, audits int
	var generation int64
	if err = owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM relay_node_assets),(SELECT count(*) FROM asset_admin_command_receipts WHERE command_kind LIKE 'node.%'),(SELECT count(*) FROM audit_logs WHERE category='asset_node'),(SELECT node_generation FROM asset_registry_generations WHERE singleton_id=1)`).Scan(&nodes, &receipts, &audits, &generation); err != nil {
		t.Fatal(err)
	}
	if nodes != 3 || receipts != 6 || audits != 6 || generation != 6 {
		t.Fatalf("nodes/receipts/audits/generation=%d/%d/%d/%d", nodes, receipts, audits, generation)
	}
	var body map[string]any
	if err = json.Unmarshal(created.Body, &body); err != nil || body["result"] != "registered" {
		t.Fatalf("body=%s err=%v", created.Body, err)
	}
}

func TestNodeLifecycleConcurrentSerializationPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "34"); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'race-admin','Race Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_account_inventory_read')`); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, roleErr := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return roleErr
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := assetstore.NewNodeLifecycleRepository(pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	registerCommand := func(commandID, instanceID uuid.UUID, name string) assetstore.NodeCommand {
		return assetstore.NodeCommand{
			CommandID: commandID, ActorAdminID: admin, RequestID: "race", NewInstanceID: instanceID,
			DisplayName: assetstore.StringPatch{Present: true, Value: name}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "https://race-node.example"},
			NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_account_inventory_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
		}
	}

	sameIdentity := uuid.New()
	errorsByCommand := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, registerErr := repository.Register(ctx, registerCommand(uuid.New(), sameIdentity, "Concurrent Node"))
			errorsByCommand <- registerErr
		}(index)
	}
	wait.Wait()
	close(errorsByCommand)
	registerSuccess, registerConflict := 0, 0
	for registerErr := range errorsByCommand {
		switch {
		case registerErr == nil:
			registerSuccess++
		case errors.Is(registerErr, assetstore.ErrNodeIdentityExists):
			registerConflict++
		default:
			t.Fatalf("concurrent register error=%v", registerErr)
		}
	}
	if registerSuccess != 1 || registerConflict != 1 {
		t.Fatalf("concurrent register success/conflict=%d/%d", registerSuccess, registerConflict)
	}

	oldID, newID := uuid.New(), uuid.New()
	if _, err = repository.Register(ctx, registerCommand(uuid.New(), oldID, "Lifecycle Race Node")); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, commandErr := repository.Retire(ctx, assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "race-retire", InstanceID: oldID, ExpectedRevision: 1, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}})
		results <- commandErr
	}()
	go func() {
		defer wait.Done()
		command := registerCommand(uuid.New(), newID, "Replacement Race Node")
		command.RequestID = "race-replace"
		command.InstanceID = oldID
		command.ExpectedRevision = 1
		_, commandErr := repository.Replace(ctx, command)
		results <- commandErr
	}()
	wait.Wait()
	close(results)
	lifecycleSuccess, lifecycleConflict := 0, 0
	for commandErr := range results {
		switch {
		case commandErr == nil:
			lifecycleSuccess++
		case errors.Is(commandErr, assetstore.ErrNodeRetired), errors.Is(commandErr, assetstore.ErrStaleAssetRevision):
			lifecycleConflict++
		default:
			t.Fatalf("Retire/Replace race error=%v", commandErr)
		}
	}
	if lifecycleSuccess != 1 || lifecycleConflict != 1 {
		t.Fatalf("Retire/Replace success/conflict=%d/%d", lifecycleSuccess, lifecycleConflict)
	}
	var oldStatus string
	var oldRevision int64
	var newRows, lineageRows int
	if err = owner.QueryRow(ctx, `SELECT lifecycle_status,revision,(SELECT count(*) FROM relay_node_assets WHERE instance_id=$2),(SELECT count(*) FROM relay_node_asset_replacements WHERE old_instance_id=$1) FROM relay_node_assets WHERE instance_id=$1`, oldID, newID).Scan(&oldStatus, &oldRevision, &newRows, &lineageRows); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "retired" || oldRevision != 2 || newRows != lineageRows || newRows > 1 {
		t.Fatalf("serialized lifecycle old=%s/%d new/lineage=%d/%d", oldStatus, oldRevision, newRows, lineageRows)
	}
}

func TestNodeInventoryClaimTerminalizesRetiredPendingRunPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "34"); err != nil {
		t.Fatal(err)
	}
	admin, nodeID, policyID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'claim-admin','Claim Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,lifecycle_status,revision) VALUES($1,'Claim Node','cliproxyapi','v1','https://claim-node.example','active',1)`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by) VALUES($1,'cliproxyapi','v1',ARRAY['antigravity'],ARRAY[]::text[],'claim-test')`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,max_attempts,poll_start_grace_seconds,created_at) SELECT $1,$2,'cliproxyapi','v1',to_timestamp(floor(extract(epoch FROM t)/300)*300),$3,3,299,t FROM (SELECT clock_timestamp() AS t) boundary`, runID, nodeID, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS t) UPDATE relay_node_assets SET lifecycle_status='retired',revision=2,retired_at=boundary.t,retired_by=$2,retire_reason='administrator_retire',updated_at=boundary.t FROM boundary WHERE instance_id=$1`, nodeID, admin); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, roleErr := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return roleErr
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := assetstore.NewInventoryPollRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{Token: uuid.New(), LeaseDuration: 30 * time.Second}); !errors.Is(err, inventorypoll.ErrNoWork) {
		t.Fatalf("claim retired pending run=%v", err)
	}
	var status, reason string
	var authorizationClear bool
	if err = owner.QueryRow(ctx, `SELECT status,execution_reason,dispatch_authorized_attempt IS NULL AND dispatch_authorized_at IS NULL AND dispatch_authorized_fencing_token IS NULL FROM account_inventory_poll_runs WHERE poll_run_id=$1`, runID).Scan(&status, &reason, &authorizationClear); err != nil {
		t.Fatal(err)
	}
	if status != "abandoned" || reason != "node_retired" || !authorizationClear {
		t.Fatalf("retired pending run status/reason/auth=%s/%s/%t", status, reason, authorizationClear)
	}
}

func TestNodeLifecycleFinalizationPreservesEvidenceWithoutPromotionPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "34"); err != nil {
		t.Fatal(err)
	}
	admin, nodeID, lifecycleNodeID, policyID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	baselineRunID, baselineToken := uuid.New(), uuid.New()
	monitoringRunID, monitoringToken := uuid.New(), uuid.New()
	runID, token := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'finalize-admin','Finalize Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_account_inventory_read')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,lifecycle_status,revision) VALUES($1,'Finalize Node','cliproxyapi','v1','https://finalize-node.example','active',1)`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,lifecycle_status,revision) VALUES($1,'Lifecycle Finalize Node','cliproxyapi','v1','https://lifecycle-finalize-node.example','active',1)`, lifecycleNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) SELECT target.instance_id,t,'deployment_enable','finalize-test',t FROM unnest(ARRAY[$1::uuid,$2::uuid]) AS target(instance_id) CROSS JOIN LATERAL (SELECT clock_timestamp() AS t) boundary`, nodeID, lifecycleNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by) VALUES($1,'cliproxyapi','v1',ARRAY['antigravity'],ARRAY[]::text[],'finalize-test')`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at) VALUES('cliproxyapi','v1',$1,'finalize-test',clock_timestamp())`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at) SELECT 'cliproxyapi','v1',$1,t,'finalize-test',t FROM (SELECT clock_timestamp() AS t) boundary`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,first_started_at,last_started_at,lease_expires_at,lease_fencing_token,created_at) SELECT $1,$2,'cliproxyapi','v1',to_timestamp(floor(extract(epoch FROM t)/300)*300),$3,'running',1,2,299,t,t,t+interval '30 seconds',$4,t FROM (SELECT clock_timestamp() AS t) boundary`, runID, lifecycleNodeID, policyID, token); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,first_started_at,last_started_at,lease_expires_at,lease_fencing_token,created_at) SELECT $1,$2,'cliproxyapi','v1',to_timestamp(floor(extract(epoch FROM t)/300)*300)-interval '10 minutes',$3,'running',1,2,299,t,t,t+interval '30 seconds',$4,t FROM (SELECT clock_timestamp() AS t) boundary`, baselineRunID, nodeID, policyID, baselineToken); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,first_started_at,last_started_at,lease_expires_at,lease_fencing_token,created_at) SELECT $1,$2,'cliproxyapi','v1',to_timestamp(floor(extract(epoch FROM t)/300)*300),$3,'running',1,2,299,t,t,t+interval '30 seconds',$4,t FROM (SELECT clock_timestamp() AS t) boundary`, monitoringRunID, nodeID, policyID, monitoringToken); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, roleErr := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return roleErr
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	polls, err := assetstore.NewInventoryPollRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	lastRefresh, updatedAt := int64(1735689600), int64(1735689660)
	if err = polls.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: baselineRunID, FencingToken: baselineToken,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "v1.0.0", Commit: "abcdef1",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: "antigravity", RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: "antigravity", AccountKey: "antigravity:stage2@example.invalid",
			Email: "stage2@example.invalid", BasicStatus: drivers.AccountStateActive,
			SuccessCount: 1, RecentRequestCount: 1,
			LastRefreshUnix: &lastRefresh, UpdatedAtUnix: &updatedAt,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var healthScheduledAt time.Time
	var healthDegraded bool
	var healthReason string
	if err = owner.QueryRow(ctx, `SELECT health_scheduled_at,health_degraded,health_reason FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='antigravity'`, nodeID).Scan(&healthScheduledAt, &healthDegraded, &healthReason); err != nil {
		t.Fatal(err)
	}
	var inventoryBefore, availabilityBefore, qualityBefore int
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1),
		(SELECT count(*) FROM account_availability_checkpoints WHERE node_id=$1),
		(SELECT count(*) FROM account_request_quality_events WHERE node_id=$1)`, nodeID).Scan(&inventoryBefore, &availabilityBefore, &qualityBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = polls.AuthorizeDispatch(ctx, inventorypoll.DispatchAuthorizationRequest{PollRunID: monitoringRunID, FencingToken: monitoringToken, Attempt: 1, RequestTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring($1,false,NULL,'deployment_disable','finalize-test')`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `SELECT public.control_activate_provider_policy('cliproxyapi','v1',ARRAY['openai'],ARRAY['antigravity'],'finalize-test',NULL)`); err != nil {
		t.Fatal(err)
	}
	if err = polls.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: monitoringRunID, FencingToken: monitoringToken,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "v1.0.1", Commit: "abcdef2",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: "antigravity", RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: "antigravity", AccountKey: "antigravity:skipped@example.invalid",
			Email: "skipped@example.invalid", BasicStatus: drivers.AccountStateActive,
			SuccessCount: 1, RecentRequestCount: 1,
			LastRefreshUnix: &lastRefresh, UpdatedAtUnix: &updatedAt,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var monitoringStatus, monitoringRunReason, monitoringProviderReason string
	var monitoringPromotion bool
	var monitoringProviderEvidence, monitoringSnapshotItems int
	if err = owner.QueryRow(ctx, `SELECT run.status,run.promotion_skipped_reason,result.promotion_applied,result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs run JOIN account_inventory_poll_provider_results result USING(poll_run_id)
		WHERE run.poll_run_id=$1`, monitoringRunID).Scan(&monitoringStatus, &monitoringRunReason, &monitoringPromotion, &monitoringProviderReason, &monitoringProviderEvidence, &monitoringSnapshotItems); err != nil {
		t.Fatal(err)
	}
	if monitoringStatus != "finalized" || monitoringRunReason != "monitoring_ineligible" || monitoringPromotion || monitoringProviderReason != "monitoring_ineligible" || monitoringProviderEvidence != 1 || monitoringSnapshotItems != 0 {
		t.Fatalf("monitoring fence status/run/applied/provider/evidence/items=%s/%s/%t/%s/%d/%d", monitoringStatus, monitoringRunReason, monitoringPromotion, monitoringProviderReason, monitoringProviderEvidence, monitoringSnapshotItems)
	}
	var monitoringCurrentPoll uuid.UUID
	var monitoringHealthAt time.Time
	var monitoringHealthDegraded bool
	var monitoringHealthReason string
	if err = owner.QueryRow(ctx, `SELECT current_poll_run_id,health_scheduled_at,health_degraded,health_reason FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='antigravity'`, nodeID).Scan(&monitoringCurrentPoll, &monitoringHealthAt, &monitoringHealthDegraded, &monitoringHealthReason); err != nil {
		t.Fatal(err)
	}
	if monitoringCurrentPoll != baselineRunID || !monitoringHealthAt.Equal(healthScheduledAt) || monitoringHealthDegraded != healthDegraded || monitoringHealthReason != healthReason {
		t.Fatalf("monitoring fence changed current/health: poll=%s health=%s/%t/%s", monitoringCurrentPoll, monitoringHealthAt, monitoringHealthDegraded, monitoringHealthReason)
	}
	var inventoryAfter, availabilityAfter, qualityAfter int
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1),
		(SELECT count(*) FROM account_availability_checkpoints WHERE node_id=$1),
		(SELECT count(*) FROM account_request_quality_events WHERE node_id=$1)`, nodeID).Scan(&inventoryAfter, &availabilityAfter, &qualityAfter); err != nil {
		t.Fatal(err)
	}
	if inventoryAfter != inventoryBefore || availabilityAfter != availabilityBefore || qualityAfter != qualityBefore {
		t.Fatalf("monitoring fence changed account/availability/quality rows=%d/%d %d/%d %d/%d", inventoryBefore, inventoryAfter, availabilityBefore, availabilityAfter, qualityBefore, qualityAfter)
	}
	expiryNodeID, expiryRunID, expiryToken := uuid.New(), uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,lifecycle_status,revision) VALUES($1,'Natural Expiry Node','cliproxyapi','v1','https://natural-expiry.example','active',1)`, expiryNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS t)
		INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,effective_to,reason,actor,created_at,end_reason,end_actor,end_recorded_at)
		SELECT $1,t,t+interval '1 second','deployment_enable','finalize-test',t,'scheduled_disable','finalize-test',t FROM boundary`, expiryNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,first_started_at,last_started_at,lease_expires_at,lease_fencing_token,created_at) SELECT $1,$2,'cliproxyapi','v1',to_timestamp(floor(extract(epoch FROM t)/300)*300),$3,'running',1,2,299,t,t,t+interval '30 seconds',$4,t FROM (SELECT clock_timestamp() AS t) boundary`, expiryRunID, expiryNodeID, policyID, expiryToken); err != nil {
		t.Fatal(err)
	}
	if _, err = polls.AuthorizeDispatch(ctx, inventorypoll.DispatchAuthorizationRequest{PollRunID: expiryRunID, FencingToken: expiryToken, Attempt: 1, RequestTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err = polls.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: expiryRunID, FencingToken: expiryToken,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "v1.0.2", Commit: "abcdef3",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: "antigravity", RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: "antigravity", AccountKey: "antigravity:expired@example.invalid",
			Email: "expired@example.invalid", BasicStatus: drivers.AccountStateActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var expiryRunReason, expiryProviderReason string
	var expiryPromotion bool
	var expiryItems int
	if err = owner.QueryRow(ctx, `SELECT run.promotion_skipped_reason,result.promotion_applied,result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs run JOIN account_inventory_poll_provider_results result USING(poll_run_id)
		WHERE run.poll_run_id=$1`, expiryRunID).Scan(&expiryRunReason, &expiryPromotion, &expiryProviderReason, &expiryItems); err != nil {
		t.Fatal(err)
	}
	if expiryRunReason != "monitoring_ineligible" || expiryPromotion || expiryProviderReason != "monitoring_ineligible" || expiryItems != 0 {
		t.Fatalf("natural expiry fence run/applied/provider/items=%s/%t/%s/%d", expiryRunReason, expiryPromotion, expiryProviderReason, expiryItems)
	}
	if _, err = polls.AuthorizeDispatch(ctx, inventorypoll.DispatchAuthorizationRequest{PollRunID: runID, FencingToken: token, Attempt: 1, RequestTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := assetstore.NewNodeLifecycleRepository(pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lifecycle.Retire(ctx, assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: admin, RequestID: "finalize-retire", InstanceID: lifecycleNodeID, ExpectedRevision: 1, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}); err != nil {
		t.Fatal(err)
	}
	if err = polls.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: runID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: false, ResponseShapeValid: false, ContractValid: false,
			NodeIdentityComplete: false, SnapshotComplete: false, Degraded: true,
			Result: drivers.ResultFailed, Reason: drivers.ReasonNetworkUnavailable,
			Version: "v1", Commit: "deadbeef",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: "antigravity", IdentityComplete: true, Degraded: true,
			Reason: inventorypoll.ProviderReasonTransportFailed,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var status, runReason, providerReason string
	var promotionApplied bool
	if err = owner.QueryRow(ctx, `SELECT run.status,run.promotion_skipped_reason,result.promotion_applied,result.promotion_skipped_reason FROM account_inventory_poll_runs run JOIN account_inventory_poll_provider_results result USING(poll_run_id) WHERE run.poll_run_id=$1`, runID).Scan(&status, &runReason, &promotionApplied, &providerReason); err != nil {
		t.Fatal(err)
	}
	if status != "finalized" || runReason != "node_retired" || promotionApplied || providerReason != "node_retired" {
		t.Fatalf("finalize status/run/applied/provider=%s/%s/%t/%s", status, runReason, promotionApplied, providerReason)
	}
	var currentPollRun uuid.UUID
	var healthScheduledAfter time.Time
	var healthDegradedAfter bool
	var healthReasonAfter string
	if err = owner.QueryRow(ctx, `SELECT current_poll_run_id,health_scheduled_at,health_degraded,health_reason FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='antigravity'`, nodeID).Scan(&currentPollRun, &healthScheduledAfter, &healthDegradedAfter, &healthReasonAfter); err != nil {
		t.Fatal(err)
	}
	if currentPollRun != baselineRunID || !healthScheduledAfter.Equal(healthScheduledAt) || healthDegradedAfter != healthDegraded || healthReasonAfter != healthReason {
		t.Fatalf("current/health changed after lifecycle skip: poll=%s health=%s/%t/%s", currentPollRun, healthScheduledAfter, healthDegradedAfter, healthReasonAfter)
	}
}

func TestNodeMonitoringFinalizeSerializationPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "34"); err != nil {
		t.Fatal(err)
	}
	policyID, writerFirstNode, finalizeFirstNode := uuid.New(), uuid.New(), uuid.New()
	writerFirstRun, writerFirstToken := uuid.New(), uuid.New()
	finalizeFirstRun, finalizeFirstToken := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by) VALUES($1,'cliproxyapi','v1',ARRAY['antigravity'],ARRAY[]::text[],'finalize-race')`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at) VALUES('cliproxyapi','v1',$1,'finalize-race',clock_timestamp())`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at) SELECT 'cliproxyapi','v1',$1,t,'finalize-race',t FROM (SELECT clock_timestamp() AS t) boundary`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,lifecycle_status,revision) VALUES($1,'Writer First','cliproxyapi','v1','https://writer-first.example','active',1),($2,'Finalize First','cliproxyapi','v1','https://finalize-first.example','active',1)`, writerFirstNode, finalizeFirstNode); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS t) INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) SELECT target.instance_id,t,'deployment_enable','finalize-race',t FROM unnest(ARRAY[$1::uuid,$2::uuid]) AS target(instance_id) CROSS JOIN boundary`, writerFirstNode, finalizeFirstNode); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS t) INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,first_started_at,last_started_at,lease_expires_at,lease_fencing_token,created_at) SELECT input.poll_run_id,input.instance_id,'cliproxyapi','v1',to_timestamp(floor(extract(epoch FROM t)/300)*300),$1,'running',1,2,299,t,t,t+interval '30 seconds',input.fencing_token,t FROM (VALUES($2::uuid,$3::uuid,$4::uuid),($5::uuid,$6::uuid,$7::uuid)) input(poll_run_id,instance_id,fencing_token) CROSS JOIN boundary`, policyID, writerFirstRun, writerFirstNode, writerFirstToken, finalizeFirstRun, finalizeFirstNode, finalizeFirstToken); err != nil {
		t.Fatal(err)
	}

	runtimeConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig.MaxConns = 2
	runtimeConfig.ConnConfig.RuntimeParams["application_name"] = "stage2-finalize-race"
	runtimeConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, roleErr := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return roleErr
	}
	runtimePool, err := pgxpool.NewWithConfig(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	polls, err := assetstore.NewInventoryPollRepository(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	writerConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	writerConfig.RuntimeParams["application_name"] = "stage2-monitoring-writer"
	writer, err := pgx.ConnectConfig(ctx, writerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(ctx)
	waitForLock := func(applicationName string) {
		t.Helper()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			if queryErr := owner.QueryRow(ctx, `SELECT coalesce(bool_or(wait_event_type='Lock'),false) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1`, applicationName).Scan(&blocked); queryErr != nil {
				t.Fatal(queryErr)
			}
			if blocked {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
	}
	failedEvidence := func(runID, token uuid.UUID) inventorypoll.FinalizeRequest {
		return inventorypoll.FinalizeRequest{
			PollRunID: runID, FencingToken: token,
			Node: inventorypoll.NodeEvidence{
				TransportSuccess: false, ResponseShapeValid: false, ContractValid: false,
				NodeIdentityComplete: false, SnapshotComplete: false, Degraded: true,
				Result: drivers.ResultFailed, Reason: drivers.ReasonNetworkUnavailable,
				Version: "v1", Commit: "deadbeef",
			},
			Providers: []inventorypoll.ProviderEvidence{{
				Provider: "antigravity", IdentityComplete: true, Degraded: true,
				Reason: inventorypoll.ProviderReasonTransportFailed,
			}},
		}
	}

	writerTx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writerTx.Exec(ctx, `SELECT 1 FROM relay_node_assets WHERE instance_id=$1 FOR UPDATE`, writerFirstNode); err != nil {
		t.Fatal(err)
	}
	writerFirstDone := make(chan error, 1)
	go func() { writerFirstDone <- polls.FinalizeFenced(ctx, failedEvidence(writerFirstRun, writerFirstToken)) }()
	waitForLock("stage2-finalize-race")
	if _, err = writerTx.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring($1,false,NULL,'deployment_disable','finalize-race')`, writerFirstNode); err != nil {
		t.Fatal(err)
	}
	if err = writerTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-writerFirstDone; err != nil {
		t.Fatal(err)
	}
	var writerFirstReason string
	if err = owner.QueryRow(ctx, `SELECT promotion_skipped_reason FROM account_inventory_poll_runs WHERE poll_run_id=$1`, writerFirstRun).Scan(&writerFirstReason); err != nil || writerFirstReason != "monitoring_ineligible" {
		t.Fatalf("writer-first reason=%q err=%v", writerFirstReason, err)
	}

	blocker, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `LOCK TABLE account_inventory_poll_provider_results IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	finalizeFirstDone := make(chan error, 1)
	go func() {
		finalizeFirstDone <- polls.FinalizeFenced(ctx, failedEvidence(finalizeFirstRun, finalizeFirstToken))
	}()
	waitForLock("stage2-finalize-race")
	monitoringWriterDone := make(chan error, 1)
	go func() {
		_, writeErr := writer.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring($1,false,NULL,'deployment_disable','finalize-race')`, finalizeFirstNode)
		monitoringWriterDone <- writeErr
	}()
	waitForLock("stage2-monitoring-writer")
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-finalizeFirstDone; err != nil {
		t.Fatal(err)
	}
	if err = <-monitoringWriterDone; err != nil {
		t.Fatal(err)
	}
	var finalizeFirstRunReason *string
	var finalizeFirstProviderReason string
	if err = owner.QueryRow(ctx, `SELECT run.promotion_skipped_reason,result.promotion_skipped_reason FROM account_inventory_poll_runs run JOIN account_inventory_poll_provider_results result USING(poll_run_id) WHERE run.poll_run_id=$1`, finalizeFirstRun).Scan(&finalizeFirstRunReason, &finalizeFirstProviderReason); err != nil {
		t.Fatal(err)
	}
	if finalizeFirstRunReason != nil || finalizeFirstProviderReason != "transport_failed" {
		t.Fatalf("finalize-first run/provider reason=%v/%s", finalizeFirstRunReason, finalizeFirstProviderReason)
	}
}
