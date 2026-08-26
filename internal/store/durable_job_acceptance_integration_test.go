package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestDurableJobClosedConstraintMatrix(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	validKind := "test." + assetFixtureSuffix(t)
	kindBase := map[string]any{
		"job_kind": validKind, "payload_schema_version": 1,
		"default_timeout_seconds": 30, "lease_seconds": 5,
		"heartbeat_interval_seconds": 1, "default_max_attempts": 3,
		"default_max_verification_attempts": 3, "replay_safe": true,
		"rollback_allowed": true, "lifecycle_status": "active",
		"created_at": time.Now().UTC(),
	}
	kindCases := []struct {
		name  string
		key   string
		value any
	}{
		{"empty name", "job_kind", ""},
		{"invalid name", "job_kind", "UPPER kind"},
		{"schema zero", "payload_schema_version", 0},
		{"timeout zero", "default_timeout_seconds", 0},
		{"lease short", "lease_seconds", 4},
		{"heartbeat zero", "heartbeat_interval_seconds", 0},
		{"heartbeat reaches lease", "heartbeat_interval_seconds", 5},
		{"attempts zero", "default_max_attempts", 0},
		{"verification attempts high", "default_max_verification_attempts", 101},
		{"unknown lifecycle", "lifecycle_status", "paused"},
	}
	for _, testCase := range kindCases {
		t.Run("kind/"+testCase.name, func(t *testing.T) {
			candidate := cloneJSONMap(kindBase)
			candidate[testCase.key] = testCase.value
			assertRejectedCompositeInsert(t, ctx, tx, "async_job_kinds", candidate)
		})
	}
	insertSyntheticJobKind(t, ctx, tx, syntheticJobDefinition(validKind))

	now := time.Now().UTC().Truncate(time.Microsecond)
	payload := map[string]any{"enabled": true, "mode": "safe", "revision": 1,
		"target_id": "2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadHash := sha256.Sum256(payloadBytes)
	jobBase := map[string]any{
		"job_id": uuid.New(), "idempotency_key": "test:" + uuid.NewString(),
		"job_kind": validKind, "payload_schema_version": 1, "operation_id": uuid.New(),
		"payload": payload, "payload_hash": `\x` + hex.EncodeToString(payloadHash[:]),
		"status": "pending", "priority": 50, "attempt_count": 0,
		"verification_attempt": 0, "max_attempts": 3, "max_verification_attempts": 3,
		"timeout_seconds": 30, "lease_seconds": 5, "heartbeat_interval_seconds": 1,
		"replay_safe": true, "rollback_allowed": true,
		"available_at": now, "deadline_at": now.Add(24 * time.Hour),
		"started_at": nil, "completed_at": nil, "cancel_requested_at": nil,
		"error_code": nil, "error_summary": nil, "lease_owner": nil,
		"lease_fencing_token": nil, "lease_expires_at": nil,
		"created_at": now, "updated_at": now,
	}
	jobCases := []struct {
		name   string
		change map[string]any
	}{
		{"blank idempotency", map[string]any{"idempotency_key": ""}},
		{"padded idempotency", map[string]any{"idempotency_key": " padded"}},
		{"control idempotency", map[string]any{"idempotency_key": "test:\ninvalid"}},
		{"array payload", map[string]any{"payload": []any{1}, "payload_hash": `\x` + strings.Repeat("00", 32)}},
		{"short hash", map[string]any{"payload_hash": `\x00`}},
		{"unknown status", map[string]any{"status": "paused"}},
		{"priority low", map[string]any{"priority": -1}},
		{"priority high", map[string]any{"priority": 101}},
		{"negative attempt", map[string]any{"attempt_count": -1}},
		{"attempt exceeds max", map[string]any{"attempt_count": 4, "started_at": now}},
		{"verification exceeds max", map[string]any{"verification_attempt": 4}},
		{"timeout zero", map[string]any{"timeout_seconds": 0}},
		{"lease short", map[string]any{"lease_seconds": 4}},
		{"heartbeat reaches lease", map[string]any{"heartbeat_interval_seconds": 5}},
		{"available before create", map[string]any{"available_at": now.Add(-time.Second)}},
		{"deadline at create", map[string]any{"deadline_at": now}},
		{"deadline beyond bound", map[string]any{"deadline_at": now.Add(31 * 24 * time.Hour)}},
		{"updated before create", map[string]any{"updated_at": now.Add(-time.Second)}},
		{"attempt without start", map[string]any{"attempt_count": 1}},
		{"start without attempt", map[string]any{"started_at": now}},
		{"terminal without completion", map[string]any{"status": "failed"}},
		{"pending with completion", map[string]any{"completed_at": now}},
		{"running without lease", map[string]any{"status": "running", "attempt_count": 1, "started_at": now}},
		{"pending with lease", map[string]any{"lease_owner": "worker", "lease_fencing_token": uuid.New(), "lease_expires_at": now.Add(time.Minute)}},
		{"partial verification lease", map[string]any{"status": "verifying", "lease_owner": "worker"}},
		{"invalid lease owner", map[string]any{"status": "running", "attempt_count": 1, "started_at": now, "lease_owner": "bad owner", "lease_fencing_token": uuid.New(), "lease_expires_at": now.Add(time.Minute)}},
		{"invalid error code", map[string]any{"error_code": "UPPER"}},
		{"control error summary", map[string]any{"error_summary": "unsafe\nsummary"}},
	}
	for _, testCase := range jobCases {
		t.Run("job/"+testCase.name, func(t *testing.T) {
			candidate := cloneJSONMap(jobBase)
			candidate["job_id"] = uuid.New()
			candidate["idempotency_key"] = "test:" + uuid.NewString()
			candidate["operation_id"] = uuid.New()
			for key, value := range testCase.change {
				candidate[key] = value
			}
			assertRejectedCompositeInsert(t, ctx, tx, "async_jobs", candidate)
		})
	}

	validJob := cloneJSONMap(jobBase)
	validJobID := uuid.New()
	validOperationID := uuid.New()
	validJob["job_id"], validJob["operation_id"] = validJobID, validOperationID
	validJob["idempotency_key"] = "test:" + uuid.NewString()
	insertComposite(t, ctx, tx, "async_jobs", validJob)

	eventBase := map[string]any{
		"job_id": validJobID, "sequence": 1, "event_type": "enqueued",
		"from_status": nil, "to_status": "pending", "attempt": 0,
		"reason_code": "job_enqueued", "error_code": nil,
		"actor_type": "service", "occurred_at": now,
	}
	eventCases := []struct {
		name   string
		change map[string]any
	}{
		{"sequence zero", map[string]any{"sequence": 0}},
		{"unknown type", map[string]any{"event_type": "paused"}},
		{"unknown from", map[string]any{"from_status": "paused"}},
		{"unknown to", map[string]any{"to_status": "paused"}},
		{"attempt high", map[string]any{"attempt": 101}},
		{"invalid reason", map[string]any{"reason_code": "UPPER"}},
		{"invalid error", map[string]any{"error_code": "UPPER"}},
		{"unknown actor", map[string]any{"actor_type": "administrator"}},
		{"dispatcher actor", map[string]any{"actor_type": "dispatcher"}},
		{"enqueued by worker", map[string]any{"actor_type": "worker"}},
		{"claimed by service", map[string]any{"event_type": "claimed", "from_status": "pending", "to_status": "running", "attempt": 1}},
		{"success from running", map[string]any{"event_type": "succeeded", "from_status": "running", "to_status": "succeeded", "attempt": 1, "actor_type": "reconciler"}},
		{"cancel request from pending", map[string]any{"event_type": "cancel_requested", "from_status": "pending", "to_status": "pending"}},
		{"mismatched transition", map[string]any{"event_type": "claimed", "from_status": "pending", "to_status": "pending", "attempt": 1}},
	}
	for _, testCase := range eventCases {
		t.Run("event/"+testCase.name, func(t *testing.T) {
			candidate := cloneJSONMap(eventBase)
			for key, value := range testCase.change {
				candidate[key] = value
			}
			assertRejectedCompositeInsert(t, ctx, tx, "async_job_events", candidate)
		})
	}

	eventID := uuid.New()
	outboxBase := map[string]any{
		"event_id": eventID, "event_key": "job:" + validJobID.String() + ":wake:v1",
		"job_id": validJobID, "operation_id": validOperationID, "topic": "async_job_wake",
		"envelope": map[string]any{"schema_version": 1, "event_id": eventID,
			"job_id": validJobID, "operation_id": validOperationID, "topic": "async_job_wake"},
		"status": "pending", "attempt_count": 0, "max_attempts": 5,
		"available_at": now, "sent_at": nil, "error_code": nil,
		"lease_owner": nil, "lease_fencing_token": nil, "lease_expires_at": nil,
		"created_at": now, "updated_at": now,
	}
	outboxCases := []struct {
		name   string
		change map[string]any
	}{
		{"blank event key", map[string]any{"event_key": ""}},
		{"wrong topic", map[string]any{"topic": "job_wakeup"}},
		{"extra envelope", map[string]any{"envelope": map[string]any{"schema_version": 1, "event_id": eventID, "job_id": validJobID, "operation_id": validOperationID, "topic": "async_job_wake", "payload": "forbidden"}}},
		{"unknown status", map[string]any{"status": "paused"}},
		{"attempt exceeds max", map[string]any{"attempt_count": 6}},
		{"available before create", map[string]any{"available_at": now.Add(-time.Second)}},
		{"sent without timestamp", map[string]any{"status": "sent"}},
		{"timestamp when not sent", map[string]any{"sent_at": now}},
		{"publishing without lease", map[string]any{"status": "publishing", "attempt_count": 1}},
		{"pending with lease", map[string]any{"lease_owner": "dispatcher", "lease_fencing_token": uuid.New(), "lease_expires_at": now.Add(time.Minute)}},
		{"suppressed without reason", map[string]any{"status": "suppressed"}},
		{"invalid error", map[string]any{"error_code": "UPPER"}},
		{"invalid owner", map[string]any{"status": "publishing", "attempt_count": 1, "lease_owner": "bad owner", "lease_fencing_token": uuid.New(), "lease_expires_at": now.Add(time.Minute)}},
	}
	for _, testCase := range outboxCases {
		t.Run("outbox/"+testCase.name, func(t *testing.T) {
			candidate := cloneJSONMap(outboxBase)
			candidate["event_id"] = uuid.New()
			candidate["event_key"] = "test:" + uuid.NewString()
			for key, value := range testCase.change {
				candidate[key] = value
			}
			assertRejectedCompositeInsert(t, ctx, tx, "operation_outbox", candidate)
		})
	}
}

func cloneJSONMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func insertComposite(t *testing.T, ctx context.Context, tx pgx.Tx, table string, record map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s SELECT populated.* FROM jsonb_populate_record(NULL::%s, $1::jsonb) AS populated`, table, table), encoded); err != nil {
		t.Fatalf("insert %s composite: %v", table, err)
	}
}

func assertRejectedCompositeInsert(t *testing.T, ctx context.Context, tx pgx.Tx, table string, record map[string]any) {
	t.Helper()
	err := assetSavepoint(t, ctx, tx, table, func() error {
		encoded, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			return marshalErr
		}
		_, insertErr := tx.Exec(ctx, fmt.Sprintf(
			`INSERT INTO %s SELECT populated.* FROM jsonb_populate_record(NULL::%s, $1::jsonb) AS populated`, table, table), encoded)
		return insertErr
	})
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" {
		t.Fatalf("%s insert error = %v, want check violation", table, err)
	}
}

// isolatedJobDatabase migrates a disposable PostgreSQL 18 database to v4.
// It lets concurrency and runtime-role tests commit without leaving test kinds
// or durable evidence in the shared development database.
type isolatedJobDatabase struct {
	ownerURL   string
	runtimeURL string
	owner      *pgxpool.Pool
	runtime    *pgxpool.Pool
}

func newIsolatedJobDatabase(t *testing.T) *isolatedJobDatabase {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	ownerConfig, err := pgx.ParseConfig(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	databaseName := strings.ReplaceAll("job_accept_"+assetFixtureSuffix(t), "-", "_")
	maintenanceConfig := ownerConfig.Copy()
	maintenanceConfig.Database = "postgres"
	maintenance, err := pgx.ConnectConfig(ctx, maintenanceConfig)
	if err != nil {
		t.Fatal(err)
	}
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+identifier); err != nil {
		maintenance.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = maintenance.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, databaseName)
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+identifier)
		maintenance.Close(context.Background())
	})
	ownerLocation, err := url.Parse(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	ownerLocation.Path = "/" + databaseName
	runtimeLocation, err := url.Parse(runtimeDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	runtimeLocation.Path = "/" + databaseName
	result := &isolatedJobDatabase{ownerURL: ownerLocation.String(), runtimeURL: runtimeLocation.String()}
	if err := runAssetGoose(t, ctx, "../..", result.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	result.owner, err = pgxpool.New(ctx, result.ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	result.runtime, err = pgxpool.New(ctx, result.runtimeURL)
	if err != nil {
		result.owner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		result.runtime.Close()
		result.owner.Close()
	})
	return result
}

func TestDurableJobRuntimeCatalogAndStateBypassMatrix(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	var initialKinds int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM async_job_kinds`).Scan(&initialKinds); err != nil {
		t.Fatal(err)
	}
	if initialKinds != 0 {
		t.Fatalf("production migration registered %d job kinds", initialKinds)
	}

	payload := []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`)
	payloadHash := sha256.Sum256(payload)
	unknownCases := []struct {
		name    string
		kind    string
		version int
	}{
		{"unknown kind", "unknown.kind", 1},
		{"unknown schema", "known.kind", 2},
	}
	definition := syntheticJobDefinition("known.kind")
	installJobDefinition(t, ctx, database.owner, definition)
	for _, testCase := range unknownCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_enqueue_async_job(
				$1::uuid,$2::text,$3::text,$4::integer,$5::uuid,$6::jsonb,$7::bytea,50::smallint,$8::uuid,$9::text,false
			)`, uuid.New(), "test:"+uuid.NewString(), testCase.kind, testCase.version,
				uuid.New(), payload, payloadHash[:], uuid.New(), "job:"+uuid.NewString()+":wake:v1")
			requirePostgresCode(t, err, "22023")
		})
	}

	permissionCases := []struct {
		name string
		sql  string
	}{
		{"catalog insert", `INSERT INTO async_job_kinds(job_kind,payload_schema_version,default_timeout_seconds,lease_seconds,heartbeat_interval_seconds,default_max_attempts,default_max_verification_attempts) VALUES ('runtime.forbidden',1,30,5,1,3,3)`},
		{"catalog update", `UPDATE async_job_kinds SET lifecycle_status='retired' WHERE job_kind='known.kind'`},
		{"catalog delete", `DELETE FROM async_job_kinds WHERE job_kind='known.kind'`},
		{"catalog truncate", `TRUNCATE async_job_kinds`},
		{"job delete", `DELETE FROM async_jobs`},
		{"event update", `UPDATE async_job_events SET reason_code='changed'`},
		{"event delete", `DELETE FROM async_job_events`},
		{"event truncate", `TRUNCATE async_job_events`},
		{"disable job trigger", `ALTER TABLE async_jobs DISABLE TRIGGER async_jobs_mutation_guard`},
		{"disable event trigger", `ALTER TABLE async_job_events DISABLE TRIGGER async_job_events_immutable`},
		{"execute payload helper", `SELECT public.control_job_payload_is_safe('{}'::jsonb)`},
		{"execute mutation helper", `SELECT public.control_guard_async_job_mutation()`},
	}
	for _, testCase := range permissionCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := database.runtime.Exec(ctx, testCase.sql)
			requirePostgresCode(t, err, "42501")
		})
	}

	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:" + uuid.NewString(), Payload: payload,
		Priority: 50, Actor: jobcore.ActorService,
	})
	token := uuid.New()
	var claimedID uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT job_id FROM public.control_claim_async_job($1,$2)`, "runtime-worker", token).Scan(&claimedID); err != nil {
		t.Fatal(err)
	}
	if claimedID != created.Job.ID {
		t.Fatal("runtime claimed unexpected job")
	}
	transitionCases := []struct {
		name, expected, target, event, actor string
	}{
		{"worker skips verify", "running", "succeeded", "succeeded", "worker"},
		{"worker cancels running", "running", "cancelled", "cancelled", "worker"},
		{"worker starts rollback", "running", "rolling_back", "rollback_started", "worker"},
		{"reconciler owns running", "running", "failed", "failed", "reconciler"},
		{"event mismatch", "running", "verifying", "succeeded", "worker"},
	}
	for _, testCase := range transitionCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_transition_async_job_fenced(
				$1,$2,$3,$4,$5,0,NULL,NULL,NULL,$6,true
			)`, created.Job.ID, testCase.expected, token, testCase.target, testCase.event, testCase.actor)
			requirePostgresCode(t, err, "23514")
		})
	}
	var status string
	if err := database.owner.QueryRow(ctx, `SELECT status FROM async_jobs WHERE job_id=$1`, created.Job.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("failed bypass changed status to %s", status)
	}

	if _, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_transition_async_job_fenced(
		$1,'running',$2,'verifying','verification_started',0,NULL,NULL,NULL,'worker',true
	)`, created.Job.ID, token); err != nil {
		t.Fatal(err)
	}
	recoveryToken := uuid.New()
	if err := database.runtime.QueryRow(ctx, `SELECT job_id FROM public.control_claim_expired_async_job($1,$2)`, "runtime-reconciler", recoveryToken).Scan(&claimedID); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct{ name, target, event string }{
		{"verifying directly rolled back", "rolled_back", "rolled_back"},
		{"verifying claimed by worker", "failed", "failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actor := "reconciler"
			if strings.Contains(testCase.name, "worker") {
				actor = "worker"
			}
			_, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_transition_async_job_fenced(
				$1,'verifying',$2,$3,$4,0,NULL,NULL,NULL,$5,true
			)`, created.Job.ID, recoveryToken, testCase.target, testCase.event, actor)
			requirePostgresCode(t, err, "23514")
		})
	}
	if _, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_transition_async_job_fenced(
		$1,'verifying',$2,'rolling_back','rollback_started',0,NULL,NULL,NULL,'reconciler',true
	)`, created.Job.ID, recoveryToken); err != nil {
		t.Fatal(err)
	}
	rollbackToken := uuid.New()
	if err := database.runtime.QueryRow(ctx, `SELECT job_id FROM public.control_claim_expired_async_job($1,$2)`, "runtime-reconciler", rollbackToken).Scan(&claimedID); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct{ target, event string }{
		{"cancelled", "cancelled"}, {"retry_wait", "retry_scheduled"}, {"succeeded", "succeeded"},
	} {
		t.Run("rolling back to "+testCase.target, func(t *testing.T) {
			_, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_transition_async_job_fenced(
				$1,'rolling_back',$2,$3,$4,0,NULL,NULL,NULL,'reconciler',true
			)`, created.Job.ID, rollbackToken, testCase.target, testCase.event)
			requirePostgresCode(t, err, "23514")
		})
	}
}

func installJobDefinition(t *testing.T, ctx context.Context, pool *pgxpool.Pool, definition jobcore.Definition) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO async_job_kinds (
		job_kind,payload_schema_version,default_timeout_seconds,lease_seconds,
		heartbeat_interval_seconds,default_max_attempts,default_max_verification_attempts,
		replay_safe,rollback_allowed
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, definition.Kind, definition.SchemaVersion,
		int(definition.Timeout/time.Second), int(definition.LeaseDuration/time.Second),
		int(definition.HeartbeatInterval/time.Second), definition.MaxAttempts,
		definition.MaxVerifyAttempts, definition.ReplaySafe, definition.AllowRollback)
	if err != nil {
		t.Fatalf("install job definition: %v", err)
	}
}

func enqueueCommitted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, registry *jobcore.Registry, request jobcore.EnqueueRequest) jobcore.EnqueueResult {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	store, err := jobstore.NewJobTxStore(tx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jobcore.EnqueueTx(ctx, store, registry, request)
	if err != nil {
		t.Fatalf("enqueue committed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return result
}

func requirePostgresCode(t *testing.T, err error, code string) {
	t.Helper()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != code {
		t.Fatalf("PostgreSQL error = %v, want SQLSTATE %s", err, code)
	}
}

func TestDurableJobIdempotencyAtomicBundleAndCancellation(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	definitionA := syntheticJobDefinition("test.kind_a")
	definitionB := syntheticJobDefinition("test.kind_b")
	installJobDefinition(t, ctx, database.owner, definitionA)
	installJobDefinition(t, ctx, database.owner, definitionB)
	registry, err := jobcore.NewRegistry(definitionA, definitionB)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`)
	request := jobcore.EnqueueRequest{
		Kind: definitionA.Kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:" + uuid.NewString(), Payload: payload,
		Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
	}
	created := enqueueCommitted(t, ctx, database.owner, registry, request)
	replayed := enqueueCommitted(t, ctx, database.owner, registry, request)
	if replayed.Created || replayed.Job.ID != created.Job.ID {
		t.Fatalf("identical replay = %+v", replayed)
	}

	policyDefinition := definitionA
	policyDefinition.Timeout += time.Second
	policyRegistry, err := jobcore.NewRegistry(policyDefinition)
	if err != nil {
		t.Fatal(err)
	}
	conflicts := []struct {
		name     string
		registry *jobcore.Registry
		mutate   func(*jobcore.EnqueueRequest)
	}{
		{"kind", registry, func(candidate *jobcore.EnqueueRequest) { candidate.Kind = definitionB.Kind }},
		{"operation", registry, func(candidate *jobcore.EnqueueRequest) { candidate.OperationID = uuid.New() }},
		{"payload", registry, func(candidate *jobcore.EnqueueRequest) {
			candidate.Payload = []byte(`{"enabled":true,"mode":"safe","revision":2,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`)
		}},
		{"priority", registry, func(candidate *jobcore.EnqueueRequest) { candidate.Priority = 51 }},
		{"pinned policy", policyRegistry, func(*jobcore.EnqueueRequest) {}},
	}
	for _, testCase := range conflicts {
		t.Run("idempotency conflict/"+testCase.name, func(t *testing.T) {
			candidate := request
			testCase.mutate(&candidate)
			tx, beginErr := database.owner.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(context.Background())
			store, storeErr := jobstore.NewJobTxStore(tx)
			if storeErr != nil {
				t.Fatal(storeErr)
			}
			_, enqueueErr := jobcore.EnqueueTx(ctx, store, testCase.registry, candidate)
			if !errors.Is(enqueueErr, jobcore.ErrConflict) {
				t.Fatalf("conflict error = %v", enqueueErr)
			}
		})
	}
	var jobCount, eventCount, outboxCount int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM async_jobs WHERE idempotency_key=$1),
		(SELECT count(*) FROM async_job_events WHERE job_id=$2),
		(SELECT count(*) FROM operation_outbox WHERE job_id=$2)`,
		request.IdempotencyKey, created.Job.ID).Scan(&jobCount, &eventCount, &outboxCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 || eventCount != 1 || outboxCount != 1 {
		t.Fatalf("conflict changed bundle counts %d/%d/%d", jobCount, eventCount, outboxCount)
	}
	requestCancelled(t, ctx, database.owner, created.Job.ID)

	operationID := uuid.New()
	tableSuffix := strings.ReplaceAll(assetFixtureSuffix(t), "-", "_")
	businessTable := pgx.Identifier{"synthetic_business_" + tableSuffix}.Sanitize()
	auditTable := pgx.Identifier{"synthetic_audit_" + tableSuffix}.Sanitize()
	faultFunction := pgx.Identifier{"synthetic_outbox_fault_" + tableSuffix}.Sanitize()
	faultTrigger := pgx.Identifier{"synthetic_outbox_fault_" + tableSuffix}.Sanitize()
	if _, err := database.owner.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE %s(operation_id uuid PRIMARY KEY, expected_state text NOT NULL);
		CREATE TABLE %s(operation_id uuid PRIMARY KEY, action text NOT NULL);
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.operation_id = '%s'::uuid THEN
				RAISE EXCEPTION 'synthetic outbox fault' USING ERRCODE='P0001';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER %s BEFORE INSERT ON operation_outbox
		FOR EACH ROW EXECUTE FUNCTION %s()`, businessTable, auditTable,
		faultFunction, operationID.String(), faultTrigger, faultFunction)); err != nil {
		t.Fatal(err)
	}
	faultRequest := request
	faultRequest.IdempotencyKey = "test:" + uuid.NewString()
	faultRequest.OperationID = operationID
	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s VALUES ($1,'desired')`, businessTable), operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s VALUES ($1,'accepted')`, auditTable), operationID); err != nil {
		t.Fatal(err)
	}
	txStore, _ := jobstore.NewJobTxStore(tx)
	_, err = jobcore.EnqueueTx(ctx, txStore, registry, faultRequest)
	if err == nil {
		t.Fatal("fault trigger did not reject outbox insert")
	}
	_ = tx.Rollback(ctx)
	var businessCount, auditCount int
	if err := database.owner.QueryRow(ctx, fmt.Sprintf(`SELECT
		(SELECT count(*) FROM %s WHERE operation_id=$1),
		(SELECT count(*) FROM %s WHERE operation_id=$1),
		(SELECT count(*) FROM async_jobs WHERE operation_id=$1),
		(SELECT count(*) FROM async_job_events AS event JOIN async_jobs AS job USING(job_id) WHERE job.operation_id=$1),
		(SELECT count(*) FROM operation_outbox WHERE operation_id=$1)`, businessTable, auditTable),
		operationID).Scan(&businessCount, &auditCount, &jobCount, &eventCount, &outboxCount); err != nil {
		t.Fatal(err)
	}
	if businessCount != 0 || auditCount != 0 || jobCount != 0 || eventCount != 0 || outboxCount != 0 {
		t.Fatalf("faulted bundle leaked %d/%d/%d/%d/%d", businessCount, auditCount, jobCount, eventCount, outboxCount)
	}

	successOperation := uuid.New()
	successRequest := request
	successRequest.IdempotencyKey = "test:" + uuid.NewString()
	successRequest.OperationID = successOperation
	commitTx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := commitTx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s VALUES ($1,'desired')`, businessTable), successOperation); err != nil {
		t.Fatal(err)
	}
	if _, err := commitTx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s VALUES ($1,'accepted')`, auditTable), successOperation); err != nil {
		t.Fatal(err)
	}
	commitStore, _ := jobstore.NewJobTxStore(commitTx)
	success, err := jobcore.EnqueueTx(ctx, commitStore, registry, successRequest)
	if err != nil {
		t.Fatal(err)
	}
	var visibleBeforeCommit int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs WHERE operation_id=$1`, successOperation).Scan(&visibleBeforeCommit); err != nil {
		t.Fatal(err)
	}
	if visibleBeforeCommit != 0 {
		t.Fatal("uncommitted job became visible")
	}
	listenerConfig, err := pgx.ParseConfig(database.ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := pgx.ConnectConfig(ctx, listenerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close(context.Background())
	if _, err := listener.Exec(ctx, `LISTEN async_job_wake`); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	_, notificationErr := listener.WaitForNotification(waitCtx)
	cancel()
	if !errors.Is(notificationErr, context.DeadlineExceeded) {
		t.Fatalf("enqueue emitted pre-commit notification: %v", notificationErr)
	}
	if err := commitTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, fmt.Sprintf(`SELECT
		(SELECT count(*) FROM %s WHERE operation_id=$1),
		(SELECT count(*) FROM %s WHERE operation_id=$1),
		(SELECT count(*) FROM async_jobs WHERE operation_id=$1),
		(SELECT count(*) FROM async_job_events WHERE job_id=$2),
		(SELECT count(*) FROM operation_outbox WHERE job_id=$2)`, businessTable, auditTable),
		successOperation, success.Job.ID).Scan(&businessCount, &auditCount, &jobCount, &eventCount, &outboxCount); err != nil {
		t.Fatal(err)
	}
	if businessCount != 1 || auditCount != 1 || jobCount != 1 || eventCount != 1 || outboxCount != 1 {
		t.Fatalf("committed bundle counts %d/%d/%d/%d/%d", businessCount, auditCount, jobCount, eventCount, outboxCount)
	}
	waitCtx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
	_, notificationErr = listener.WaitForNotification(waitCtx)
	cancel()
	if !errors.Is(notificationErr, context.DeadlineExceeded) {
		t.Fatalf("transaction-bound enqueue unexpectedly published notification: %v", notificationErr)
	}
	requestCancelled(t, ctx, database.owner, success.Job.ID)

	concurrentRequest := request
	concurrentRequest.IdempotencyKey = "test:" + uuid.NewString()
	concurrentRequest.OperationID = uuid.New()
	concurrent := enqueueCommitted(t, ctx, database.owner, registry, concurrentRequest)
	start := make(chan struct{})
	errorsChannel := make(chan error, 8)
	var waitGroup sync.WaitGroup
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			tx, beginErr := database.owner.Begin(ctx)
			if beginErr != nil {
				errorsChannel <- beginErr
				return
			}
			defer tx.Rollback(context.Background())
			store, storeErr := jobstore.NewJobTxStore(tx)
			if storeErr != nil {
				errorsChannel <- storeErr
				return
			}
			<-start
			_, cancelErr := jobcore.RequestCancelTx(ctx, store, concurrent.Job.ID, jobcore.ActorService, "operator_cancelled")
			if cancelErr == nil {
				cancelErr = tx.Commit(ctx)
			}
			errorsChannel <- cancelErr
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errorsChannel)
	for cancelErr := range errorsChannel {
		if cancelErr != nil {
			t.Fatalf("concurrent cancel: %v", cancelErr)
		}
	}
	var cancelEvents int
	var cancelRequested bool
	var status string
	if err := database.owner.QueryRow(ctx, `SELECT status,cancel_requested_at IS NOT NULL,
		(SELECT count(*) FROM async_job_events WHERE job_id=$1 AND event_type='cancelled')
		FROM async_jobs WHERE job_id=$1`, concurrent.Job.ID).Scan(&status, &cancelRequested, &cancelEvents); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || !cancelRequested || cancelEvents != 1 {
		t.Fatalf("concurrent cancellation = %s/%v/events=%d", status, cancelRequested, cancelEvents)
	}

	repository, err := jobstore.NewJobRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}
	requestOnly := request
	requestOnly.IdempotencyKey = "test:" + uuid.NewString()
	requestOnly.OperationID = uuid.New()
	running := enqueueCommitted(t, ctx, database.owner, registry, requestOnly)
	lease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
	if err != nil || lease.ID != running.Job.ID {
		t.Fatalf("claim request-only job: %+v %v", lease, err)
	}
	cancelOnce := func() jobcore.Job {
		tx, beginErr := database.owner.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer tx.Rollback(context.Background())
		store, _ := jobstore.NewJobTxStore(tx)
		job, cancelErr := jobcore.RequestCancelTx(ctx, store, running.Job.ID, jobcore.ActorService, "operator_cancelled")
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
		return job
	}
	if job := cancelOnce(); job.Status != jobcore.StatusRunning || !job.CancelRequested {
		t.Fatalf("running cancel mutated status: %+v", job)
	}
	_ = cancelOnce()
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM async_job_events WHERE job_id=$1 AND event_type='cancel_requested'`, running.Job.ID).Scan(&cancelEvents); err != nil {
		t.Fatal(err)
	}
	if cancelEvents != 1 {
		t.Fatalf("request-only cancellation events = %d", cancelEvents)
	}

	retryRequest := request
	retryRequest.IdempotencyKey, retryRequest.OperationID = "test:"+uuid.NewString(), uuid.New()
	retryJob := enqueueCommitted(t, ctx, database.owner, registry, retryRequest)
	retryLease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
	if err != nil || retryLease.ID != retryJob.Job.ID {
		t.Fatalf("retry cancellation fixture: %+v/%v", retryLease, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: retryLease.ID, Token: retryLease.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusRetryWait, Event: jobcore.EventRetryScheduled, Actor: jobcore.ActorWorker,
		ReasonCode: "synthetic_retry", ErrorCode: "synthetic_failure", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	if cancelledRetry, cancelErr := cancelJobCommitted(ctx, database.owner, retryJob.Job.ID); cancelErr != nil || cancelledRetry.Status != jobcore.StatusCancelled {
		t.Fatalf("retry_wait cancellation = %+v/%v", cancelledRetry, cancelErr)
	}

	verifyingRequest := request
	verifyingRequest.IdempotencyKey, verifyingRequest.OperationID = "test:"+uuid.NewString(), uuid.New()
	verifyingJob := enqueueCommitted(t, ctx, database.owner, registry, verifyingRequest)
	verifyingWorker, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
	if err != nil || verifyingWorker.ID != verifyingJob.Job.ID {
		t.Fatalf("verifying cancellation fixture: %+v/%v", verifyingWorker, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: verifyingWorker.ID, Token: verifyingWorker.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusVerifying, Event: jobcore.EventVerification, Actor: jobcore.ActorWorker, ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	if verifyingCancelled, cancelErr := cancelJobCommitted(ctx, database.owner, verifyingJob.Job.ID); cancelErr != nil ||
		verifyingCancelled.Status != jobcore.StatusVerifying || !verifyingCancelled.CancelRequested {
		t.Fatalf("verifying request-only cancellation = %+v/%v", verifyingCancelled, cancelErr)
	}
	verifyingRecovery, err := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: uuid.New()})
	if err != nil || verifyingRecovery.ID != verifyingJob.Job.ID {
		t.Fatalf("claim cancelled verifying fixture: %+v/%v", verifyingRecovery, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: verifyingRecovery.ID, Token: verifyingRecovery.Token, From: []jobcore.Status{jobcore.StatusVerifying},
		To: jobcore.StatusCancelled, Event: jobcore.EventCancelled, Actor: jobcore.ActorReconciler, ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}

	rollingRequest := request
	rollingRequest.IdempotencyKey, rollingRequest.OperationID = "test:"+uuid.NewString(), uuid.New()
	rollingJob := enqueueCommitted(t, ctx, database.owner, registry, rollingRequest)
	rollingWorker, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
	if err != nil || rollingWorker.ID != rollingJob.Job.ID {
		t.Fatalf("rolling cancellation fixture: %+v/%v", rollingWorker, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: rollingWorker.ID, Token: rollingWorker.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusVerifying, Event: jobcore.EventVerification, Actor: jobcore.ActorWorker, ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	rollingRecovery, err := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: uuid.New()})
	if err != nil || rollingRecovery.ID != rollingJob.Job.ID {
		t.Fatalf("rolling recovery fixture: %+v/%v", rollingRecovery, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: rollingRecovery.ID, Token: rollingRecovery.Token, From: []jobcore.Status{jobcore.StatusVerifying},
		To: jobcore.StatusRollingBack, Event: jobcore.EventRollbackStarted, Actor: jobcore.ActorReconciler,
		ReasonCode: "partial_effect", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	if rollingCancelled, cancelErr := cancelJobCommitted(ctx, database.owner, rollingJob.Job.ID); cancelErr != nil ||
		rollingCancelled.Status != jobcore.StatusRollingBack || !rollingCancelled.CancelRequested {
		t.Fatalf("rolling_back request-only cancellation = %+v/%v", rollingCancelled, cancelErr)
	}

	terminalRequest := request
	terminalRequest.IdempotencyKey, terminalRequest.OperationID = "test:"+uuid.NewString(), uuid.New()
	terminalJob := enqueueCommitted(t, ctx, database.owner, registry, terminalRequest)
	terminalLease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
	if err != nil || terminalLease.ID != terminalJob.Job.ID {
		t.Fatalf("terminal cancellation fixture: %+v/%v", terminalLease, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: terminalLease.ID, Token: terminalLease.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusFailed, Event: jobcore.EventFailed, Actor: jobcore.ActorWorker,
		ReasonCode: "synthetic_failure", ErrorCode: "synthetic_failure", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, cancelErr := cancelJobCommitted(ctx, database.owner, terminalJob.Job.ID); !errors.Is(cancelErr, jobcore.ErrInvalidTransition) {
		t.Fatalf("terminal cancellation = %v", cancelErr)
	}
}

func cancelJobCommitted(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID) (jobcore.Job, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return jobcore.Job{}, err
	}
	defer tx.Rollback(context.Background())
	store, err := jobstore.NewJobTxStore(tx)
	if err != nil {
		return jobcore.Job{}, err
	}
	job, err := jobcore.RequestCancelTx(ctx, store, jobID, jobcore.ActorService, "operator_cancelled")
	if err != nil {
		return jobcore.Job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return jobcore.Job{}, err
	}
	return job, nil
}

func requestCancelled(t *testing.T, ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	store, err := jobstore.NewJobTxStore(tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobcore.RequestCancelTx(ctx, store, jobID, jobcore.ActorService, "test_cleanup"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDurableJobConcurrentClaimsFencingAndRecoveryPaths(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	definition := syntheticJobDefinition("test.claims")
	installJobDefinition(t, ctx, database.owner, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := jobstore.NewJobRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}
	newRequest := func(priority int) jobcore.EnqueueRequest {
		return jobcore.EnqueueRequest{
			Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
			IdempotencyKey: "test:" + uuid.NewString(),
			Payload:        []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`),
			Priority:       priority, Actor: jobcore.ActorService,
		}
	}

	low := enqueueCommitted(t, ctx, database.owner, registry, newRequest(1))
	high := enqueueCommitted(t, ctx, database.owner, registry, newRequest(100))
	highLease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "priority-worker", Token: uuid.New()})
	if err != nil || highLease.ID != high.Job.ID {
		t.Fatalf("priority claim = %+v, %v", highLease, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: highLease.ID, Token: highLease.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusFailed, Event: jobcore.EventFailed, Actor: jobcore.ActorWorker,
		ErrorCode: "synthetic_failure", ReasonCode: "synthetic_failure", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	lowLease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "priority-worker", Token: uuid.New()})
	if err != nil || lowLease.ID != low.Job.ID {
		t.Fatalf("second priority claim = %+v, %v", lowLease, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: lowLease.ID, Token: lowLease.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusFailed, Event: jobcore.EventFailed, Actor: jobcore.ActorWorker,
		ErrorCode: "synthetic_failure", ReasonCode: "synthetic_failure", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}

	cancelled := enqueueCommitted(t, ctx, database.owner, registry, newRequest(100))
	requestCancelled(t, ctx, database.owner, cancelled.Job.ID)
	futureID := insertFutureJobBundle(t, ctx, database.owner, definition, time.Hour)

	faulted := enqueueCommitted(t, ctx, database.owner, registry, newRequest(100))
	suffix := strings.ReplaceAll(assetFixtureSuffix(t), "-", "_")
	faultFunction := pgx.Identifier{"synthetic_claim_fault_" + suffix}.Sanitize()
	faultTrigger := pgx.Identifier{"synthetic_claim_fault_" + suffix}.Sanitize()
	if _, err := database.owner.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.job_id = '%s'::uuid AND NEW.event_type = 'claimed' THEN
				RAISE EXCEPTION 'synthetic claim event fault' USING ERRCODE='P0001';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER %s BEFORE INSERT ON async_job_events
		FOR EACH ROW EXECUTE FUNCTION %s()`, faultFunction, faulted.Job.ID, faultTrigger, faultFunction)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "fault-worker", Token: uuid.New()}); err == nil {
		t.Fatal("claim event fault did not roll back claim")
	}
	var status string
	var attempt, claimedEvents int
	if err := database.owner.QueryRow(ctx, `SELECT status,attempt_count,
		(SELECT count(*) FROM async_job_events WHERE job_id=$1 AND event_type='claimed')
		FROM async_jobs WHERE job_id=$1`, faulted.Job.ID).Scan(&status, &attempt, &claimedEvents); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempt != 0 || claimedEvents != 0 {
		t.Fatalf("faulted claim leaked %s/attempt=%d/events=%d", status, attempt, claimedEvents)
	}
	if _, err := database.owner.Exec(ctx, fmt.Sprintf(`DROP TRIGGER %s ON async_job_events; DROP FUNCTION %s()`, faultTrigger, faultFunction)); err != nil {
		t.Fatal(err)
	}
	requestCancelled(t, ctx, database.owner, faulted.Job.ID)

	const runnableCount = 24
	expected := make(map[uuid.UUID]struct{}, runnableCount)
	for index := range runnableCount {
		created := enqueueCommitted(t, ctx, database.owner, registry, newRequest(20+index%3))
		expected[created.Job.ID] = struct{}{}
	}
	claimed := make(chan *jobcore.Lease, runnableCount)
	claimErrors := make(chan error, 8)
	var waitGroup sync.WaitGroup
	for worker := range 8 {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for {
				lease, claimErr := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{
					Owner: fmt.Sprintf("worker-%d", worker), Token: uuid.New(),
				})
				if errors.Is(claimErr, jobcore.ErrNotFound) {
					return
				}
				if claimErr != nil {
					claimErrors <- claimErr
					return
				}
				claimed <- lease
			}
		}(worker)
	}
	waitGroup.Wait()
	close(claimed)
	close(claimErrors)
	for claimErr := range claimErrors {
		t.Fatalf("concurrent claim: %v", claimErr)
	}
	seen := make(map[uuid.UUID]uuid.UUID, runnableCount)
	for lease := range claimed {
		if _, ok := expected[lease.ID]; !ok {
			t.Fatalf("claimed non-runnable job %s", lease.ID)
		}
		if previous, duplicate := seen[lease.ID]; duplicate {
			t.Fatalf("duplicate claim %s tokens %s/%s", lease.ID, previous, lease.Token)
		}
		seen[lease.ID] = lease.Token
	}
	if len(seen) != runnableCount {
		t.Fatalf("claimed %d jobs, want %d", len(seen), runnableCount)
	}
	var futureStatus, cancelledStatus string
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT status FROM async_jobs WHERE job_id=$1),
		(SELECT status FROM async_jobs WHERE job_id=$2)`, futureID, cancelled.Job.ID).Scan(&futureStatus, &cancelledStatus); err != nil {
		t.Fatal(err)
	}
	if futureStatus != "pending" || cancelledStatus != "cancelled" {
		t.Fatalf("future/cancelled claim guard = %s/%s", futureStatus, cancelledStatus)
	}
	connection, err := database.owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `SET TIME ZONE 'Pacific/Honolulu'`); err != nil {
		connection.Release()
		t.Fatal(err)
	}
	var timezoneClaimCount int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM public.control_claim_async_job('timezone-worker',gen_random_uuid())`).Scan(&timezoneClaimCount); err != nil {
		connection.Release()
		t.Fatal(err)
	}
	_, _ = connection.Exec(ctx, `RESET TIME ZONE`)
	connection.Release()
	if timezoneClaimCount != 0 {
		t.Fatal("non-UTC session claimed future work")
	}

	for jobID, token := range seen {
		if err := repository.TransitionFenced(ctx, jobcore.Transition{
			JobID: jobID, Token: token, From: []jobcore.Status{jobcore.StatusRunning},
			To: jobcore.StatusFailed, Event: jobcore.EventFailed, Actor: jobcore.ActorWorker,
			ErrorCode: "synthetic_failure", ReasonCode: "synthetic_failure", ReleaseLease: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	recoveryJob := enqueueCommitted(t, ctx, database.owner, registry, newRequest(50))
	workerLease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "old-worker", Token: uuid.New()})
	if err != nil || workerLease.ID != recoveryJob.Job.ID {
		t.Fatalf("recovery fixture claim: %+v %v", workerLease, err)
	}
	if err := repository.RenewLease(ctx, workerLease.ID, workerLease.Token, definition.LeaseDuration); err != nil {
		t.Fatalf("valid renewal: %v", err)
	}
	if err := repository.RenewLease(ctx, workerLease.ID, uuid.New(), definition.LeaseDuration); !errors.Is(err, jobcore.ErrLostLease) {
		t.Fatalf("wrong-token renewal = %v", err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE async_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, workerLease.ID); err != nil {
		t.Fatal(err)
	}
	recoveryToken := uuid.New()
	type recoveryOutcome struct {
		lease *jobcore.Lease
		err   error
	}
	recoveryChannel := make(chan recoveryOutcome, 1)
	staleChannel := make(chan error, 1)
	staleRenewalChannel := make(chan error, 1)
	startRace := make(chan struct{})
	go func() {
		<-startRace
		lease, claimErr := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: recoveryToken})
		recoveryChannel <- recoveryOutcome{lease: lease, err: claimErr}
	}()
	go func() {
		<-startRace
		staleChannel <- repository.TransitionFenced(ctx, jobcore.Transition{
			JobID: workerLease.ID, Token: workerLease.Token, From: []jobcore.Status{jobcore.StatusRunning},
			To: jobcore.StatusFailed, Event: jobcore.EventFailed, Actor: jobcore.ActorWorker,
			ErrorCode: "synthetic_failure", ReasonCode: "synthetic_failure", ReleaseLease: true,
		})
	}()
	go func() {
		<-startRace
		staleRenewalChannel <- repository.RenewLease(ctx, workerLease.ID, workerLease.Token, definition.LeaseDuration)
	}()
	close(startRace)
	recovery := <-recoveryChannel
	staleErr := <-staleChannel
	staleRenewalErr := <-staleRenewalChannel
	if recovery.err != nil || recovery.lease == nil || recovery.lease.Token != recoveryToken {
		t.Fatalf("reconciler claim = %+v/%v", recovery.lease, recovery.err)
	}
	if !errors.Is(staleErr, jobcore.ErrLostLease) {
		t.Fatalf("stale worker race transition = %v", staleErr)
	}
	if !errors.Is(staleRenewalErr, jobcore.ErrLostLease) {
		t.Fatalf("stale worker race renewal = %v", staleRenewalErr)
	}
	if recovery.lease.MaxVerifyAttempts != definition.MaxVerifyAttempts ||
		recovery.lease.HeartbeatInterval != definition.HeartbeatInterval {
		t.Fatalf("recovery lost pinned policy: %+v", recovery.lease.Job)
	}
	if err := repository.RenewLease(ctx, workerLease.ID, workerLease.Token, definition.LeaseDuration); !errors.Is(err, jobcore.ErrLostLease) {
		t.Fatalf("replaced worker renewal = %v", err)
	}

	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: recovery.lease.ID, Token: recovery.lease.Token, From: []jobcore.Status{jobcore.StatusVerifying},
		To: jobcore.StatusRetryWait, Event: jobcore.EventRetryScheduled,
		Actor: jobcore.ActorReconciler, ReasonCode: "effect_not_applied", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	secondWorker, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "second-worker", Token: uuid.New()})
	if err != nil || secondWorker.ID != recoveryJob.Job.ID || secondWorker.Attempt != 2 {
		t.Fatalf("retry claim = %+v/%v", secondWorker, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: secondWorker.ID, Token: secondWorker.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusVerifying, Event: jobcore.EventVerification,
		Actor: jobcore.ActorWorker, ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	secondRecovery, err := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: uuid.New()})
	if err != nil || secondRecovery.ID != recoveryJob.Job.ID || secondRecovery.VerificationAttempt != 2 {
		t.Fatalf("second recovery = %+v/%v", secondRecovery, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: secondRecovery.ID, Token: secondRecovery.Token, From: []jobcore.Status{jobcore.StatusVerifying},
		To: jobcore.StatusRollingBack, Event: jobcore.EventRollbackStarted,
		Actor: jobcore.ActorReconciler, ReasonCode: "partial_effect", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	rollbackLease, err := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: uuid.New()})
	if err != nil || rollbackLease.ID != recoveryJob.Job.ID || rollbackLease.VerificationAttempt != 3 {
		t.Fatalf("rollback recovery = %+v/%v", rollbackLease, err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: rollbackLease.ID, Token: rollbackLease.Token, From: []jobcore.Status{jobcore.StatusRollingBack},
		To: jobcore.StatusRolledBack, Event: jobcore.EventRolledBack,
		Actor: jobcore.ActorReconciler, ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	var verificationAttempt, finalAttempt, sequenceCount int
	var completed bool
	if err := database.owner.QueryRow(ctx, `SELECT status,attempt_count,verification_attempt,
		completed_at IS NOT NULL,(SELECT count(*) FROM async_job_events WHERE job_id=$1)
		FROM async_jobs WHERE job_id=$1`, recoveryJob.Job.ID).Scan(
		&status, &finalAttempt, &verificationAttempt, &completed, &sequenceCount); err != nil {
		t.Fatal(err)
	}
	if status != "rolled_back" || finalAttempt != 2 || verificationAttempt != 3 || !completed || sequenceCount != 10 {
		t.Fatalf("atomic recovery path = %s attempts=%d/%d completed=%v events=%d",
			status, finalAttempt, verificationAttempt, completed, sequenceCount)
	}

	policyCases := []struct {
		name       string
		definition jobcore.Definition
		target     jobcore.Status
		event      jobcore.EventType
	}{
		{name: "replay forbidden", definition: syntheticJobDefinition("test.no_replay"), target: jobcore.StatusRetryWait, event: jobcore.EventRetryScheduled},
		{name: "rollback forbidden", definition: syntheticJobDefinition("test.no_rollback"), target: jobcore.StatusRollingBack, event: jobcore.EventRollbackStarted},
	}
	policyCases[0].definition.ReplaySafe = false
	policyCases[1].definition.AllowRollback = false
	for _, testCase := range policyCases {
		t.Run(testCase.name, func(t *testing.T) {
			installJobDefinition(t, ctx, database.owner, testCase.definition)
			policyRegistry, registryErr := jobcore.NewRegistry(testCase.definition)
			if registryErr != nil {
				t.Fatal(registryErr)
			}
			request := newRequest(50)
			request.Kind = testCase.definition.Kind
			created := enqueueCommitted(t, ctx, database.owner, policyRegistry, request)
			worker, claimErr := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
			if claimErr != nil || worker.ID != created.Job.ID {
				t.Fatalf("policy worker claim = %+v/%v", worker, claimErr)
			}
			if transitionErr := repository.TransitionFenced(ctx, jobcore.Transition{
				JobID: worker.ID, Token: worker.Token, From: []jobcore.Status{jobcore.StatusRunning},
				To: jobcore.StatusVerifying, Event: jobcore.EventVerification,
				Actor: jobcore.ActorWorker, ReleaseLease: true,
			}); transitionErr != nil {
				t.Fatal(transitionErr)
			}
			reconciler, claimErr := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: uuid.New()})
			if claimErr != nil || reconciler.ID != created.Job.ID {
				t.Fatalf("policy recovery claim = %+v/%v", reconciler, claimErr)
			}
			transitionErr := repository.TransitionFenced(ctx, jobcore.Transition{
				JobID: reconciler.ID, Token: reconciler.Token, From: []jobcore.Status{jobcore.StatusVerifying},
				To: testCase.target, Event: testCase.event, Actor: jobcore.ActorReconciler,
				ReasonCode: "policy_forbidden", ReleaseLease: true,
			})
			requirePostgresCode(t, transitionErr, "23514")
			if transitionErr := repository.TransitionFenced(ctx, jobcore.Transition{
				JobID: reconciler.ID, Token: reconciler.Token, From: []jobcore.Status{jobcore.StatusVerifying},
				To: jobcore.StatusFailed, Event: jobcore.EventFailed, Actor: jobcore.ActorReconciler,
				ReasonCode: "policy_forbidden", ErrorCode: "policy_forbidden", ReleaseLease: true,
			}); transitionErr != nil {
				t.Fatal(transitionErr)
			}
		})
	}
}

func insertFutureJobBundle(t *testing.T, ctx context.Context, pool *pgxpool.Pool, definition jobcore.Definition, delay time.Duration) uuid.UUID {
	t.Helper()
	jobID, operationID, eventID := uuid.New(), uuid.New(), uuid.New()
	payload := []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `INSERT INTO async_jobs (
		job_id,idempotency_key,job_kind,payload_schema_version,operation_id,payload,payload_hash,
		priority,max_attempts,max_verification_attempts,timeout_seconds,lease_seconds,
		heartbeat_interval_seconds,replay_safe,rollback_allowed,available_at,deadline_at
	) VALUES ($1,$2,$3,$4,$5,$6::jsonb,
		sha256(convert_to(public.control_job_payload_canonical($6::jsonb),'UTF8')),
		50,$7,$8,$9,$10,$11,$12,$13,clock_timestamp()+$14::interval,clock_timestamp()+interval '1 day')`,
		jobID, "test:"+uuid.NewString(), definition.Kind, definition.SchemaVersion, operationID, payload,
		definition.MaxAttempts, definition.MaxVerifyAttempts, int(definition.Timeout/time.Second),
		int(definition.LeaseDuration/time.Second), int(definition.HeartbeatInterval/time.Second),
		definition.ReplaySafe, definition.AllowRollback, fmt.Sprintf("%f seconds", delay.Seconds())); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO async_job_events(job_id,sequence,event_type,to_status,attempt,reason_code,actor_type)
		VALUES ($1,1,'enqueued','pending',0,'job_enqueued','service')`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO operation_outbox(event_id,event_key,job_id,operation_id,topic,envelope,status,error_code)
		VALUES ($1,$2,$3,$4,'async_job_wake',jsonb_build_object(
			'schema_version',1,'event_id',$1::uuid,'job_id',$3::uuid,'operation_id',$4::uuid,'topic','async_job_wake'
		),'suppressed','publisher_disabled')`, eventID, "job:"+jobID.String()+":wake:v1", jobID, operationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func TestDurableJobIndexesAndPublicReadSnapshot(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	definition := syntheticJobDefinition("test.reads")
	installJobDefinition(t, ctx, database.owner, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	requests := make([]jobcore.EnqueueResult, 0, 7)
	for index := range 7 {
		request := jobcore.EnqueueRequest{
			Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
			IdempotencyKey: "test:" + uuid.NewString(),
			Payload:        []byte(fmt.Sprintf(`{"enabled":true,"mode":"safe","revision":%d,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`, index+1)),
			Priority:       50, Actor: jobcore.ActorService,
		}
		requests = append(requests, enqueueCommitted(t, ctx, database.owner, registry, request))
		time.Sleep(time.Millisecond)
	}
	requestCancelled(t, ctx, database.owner, requests[0].Job.ID)
	repository, err := jobstore.NewJobRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}

	pageOne, err := repository.ListJobs(ctx, jobstore.JobListFilters{JobKind: definition.Kind}, nil, 2)
	if err != nil || len(pageOne.Items) != 2 || !pageOne.HasMore {
		t.Fatalf("first page = %+v/%v", pageOne, err)
	}
	if !sort.SliceIsSorted(pageOne.Items, func(left, right int) bool {
		if !pageOne.Items[left].CreatedAt.Equal(pageOne.Items[right].CreatedAt) {
			return pageOne.Items[left].CreatedAt.After(pageOne.Items[right].CreatedAt)
		}
		return strings.Compare(pageOne.Items[left].JobID.String(), pageOne.Items[right].JobID.String()) > 0
	}) {
		t.Fatal("first page is not in stable descending order")
	}
	last := pageOne.Items[len(pageOne.Items)-1]
	pageTwo, err := repository.ListJobs(ctx, jobstore.JobListFilters{JobKind: definition.Kind},
		&jobstore.JobCursor{CreatedAt: last.CreatedAt, JobID: last.JobID}, 2)
	if err != nil || len(pageTwo.Items) != 2 || !pageTwo.HasMore {
		t.Fatalf("second page = %+v/%v", pageTwo, err)
	}
	for _, first := range pageOne.Items {
		for _, second := range pageTwo.Items {
			if first.JobID == second.JobID {
				t.Fatalf("cursor repeated job %s", first.JobID)
			}
		}
	}
	cancelledPage, err := repository.ListJobs(ctx, jobstore.JobListFilters{
		JobKind: definition.Kind, Status: string(jobcore.StatusCancelled),
	}, nil, 200)
	if err != nil || len(cancelledPage.Items) != 1 || cancelledPage.Items[0].JobID != requests[0].Job.ID {
		t.Fatalf("combined kind/status filter = %+v/%v", cancelledPage, err)
	}
	from := requests[2].Job.CreatedAt
	to := time.Now().UTC().Add(time.Second)
	ranged, err := repository.ListJobs(ctx, jobstore.JobListFilters{
		JobKind: definition.Kind, Status: string(jobcore.StatusPending), CreatedFrom: &from, CreatedTo: &to,
	}, nil, 200)
	if err != nil || len(ranged.Items) != 5 {
		t.Fatalf("combined time filter count = %d/%v", len(ranged.Items), err)
	}
	empty, err := repository.ListJobs(ctx, jobstore.JobListFilters{JobKind: "does.not.exist"}, nil, 50)
	if err != nil || len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("empty filter result = %+v/%v", empty, err)
	}
	if _, err := repository.ListJobs(ctx, jobstore.JobListFilters{}, nil, 201); !errors.Is(err, jobstore.ErrInvalidJobQuery) {
		t.Fatalf("limit 201 = %v", err)
	}
	if _, err := repository.ListJobs(ctx, jobstore.JobListFilters{}, nil, 0); !errors.Is(err, jobstore.ErrInvalidJobQuery) {
		t.Fatalf("limit zero = %v", err)
	}

	snapshotTx, err := database.owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	snapshotQueries := generated.New(snapshotTx)
	params := generated.ListAsyncJobsPublicParams{JobKind: pgtype.Text{String: definition.Kind, Valid: true}, PageSize: 200}
	beforeRows, err := snapshotQueries.ListAsyncJobsPublic(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	newRequest := jobcore.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:" + uuid.NewString(),
		Payload:        []byte(`{"enabled":true,"mode":"safe","revision":99,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`),
		Priority:       50, Actor: jobcore.ActorService,
	}
	newJob := enqueueCommitted(t, ctx, database.owner, registry, newRequest)
	afterRows, err := snapshotQueries.ListAsyncJobsPublic(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeRows) != len(afterRows) {
		t.Fatalf("repeatable-read list changed %d -> %d", len(beforeRows), len(afterRows))
	}
	if err := snapshotTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := repository.ListJobs(ctx, jobstore.JobListFilters{JobKind: definition.Kind}, nil, 200)
	if err != nil || len(current.Items) != len(beforeRows)+1 {
		t.Fatalf("post-snapshot list = %d/%v", len(current.Items), err)
	}

	detailTx, err := database.owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	detailQueries := generated.New(detailTx)
	detailBefore, err := detailQueries.GetAsyncJobPublic(ctx, pgtype.UUID{Bytes: newJob.Job.ID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	requestCancelled(t, ctx, database.owner, newJob.Job.ID)
	eventsInSnapshot, err := detailQueries.ListAsyncJobEventsPublic(ctx, pgtype.UUID{Bytes: newJob.Job.ID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if detailBefore.Status != "pending" || len(eventsInSnapshot) != 1 {
		t.Fatalf("detail snapshot observed concurrent cancel: %s/events=%d", detailBefore.Status, len(eventsInSnapshot))
	}
	if err := detailTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	detailAfter, err := repository.Job(ctx, newJob.Job.ID)
	if err != nil || detailAfter.Status != "cancelled" || len(detailAfter.Events) != 2 {
		t.Fatalf("current detail after cancel = %+v/%v", detailAfter, err)
	}

	planTx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer planTx.Rollback(context.Background())
	if _, err := planTx.Exec(ctx, `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	plans := []struct {
		name, index, sql string
	}{
		{"idempotency", "async_jobs_idempotency_key_key", `SELECT * FROM async_jobs WHERE idempotency_key='missing'`},
		{"runnable", "async_jobs_runnable_idx", `SELECT job_id FROM async_jobs WHERE status IN ('pending','retry_wait') AND cancel_requested_at IS NULL AND available_at<=clock_timestamp() ORDER BY priority DESC,available_at,created_at,job_id LIMIT 1`},
		{"expired lease", "async_jobs_expired_lease_idx", `SELECT job_id FROM async_jobs WHERE status IN ('running','verifying','rolling_back') AND lease_expires_at<=clock_timestamp() ORDER BY lease_expires_at,created_at,job_id LIMIT 1`},
		{"event sequence", "async_job_events_pkey", `SELECT sequence FROM async_job_events WHERE job_id='00000000-0000-0000-0000-000000000001' ORDER BY sequence`},
		{"outbox pending", "operation_outbox_pending_idx", `SELECT event_id FROM operation_outbox WHERE status IN ('pending','retry_wait') AND available_at<=clock_timestamp() ORDER BY available_at,created_at,event_id LIMIT 1`},
		{"job list", "async_jobs_list_idx", `SELECT job_id FROM async_jobs ORDER BY created_at DESC,job_id DESC LIMIT 50`},
	}
	for _, planCase := range plans {
		t.Run("explain/"+planCase.name, func(t *testing.T) {
			rows, queryErr := planTx.Query(ctx, `EXPLAIN (COSTS OFF) `+planCase.sql)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			var lines []string
			for rows.Next() {
				var line string
				if scanErr := rows.Scan(&line); scanErr != nil {
					rows.Close()
					t.Fatal(scanErr)
				}
				lines = append(lines, line)
			}
			rows.Close()
			plan := strings.Join(lines, "\n")
			if !strings.Contains(plan, planCase.index) {
				t.Fatalf("plan does not use %s:\n%s", planCase.index, plan)
			}
		})
	}
}

func TestDurableJobProtectedDownEachEvidenceClassPreservesPriorSchema(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, context.Context, *isolatedJobDatabase, jobcore.Definition)
	}{
		{"kind", func(t *testing.T, ctx context.Context, database *isolatedJobDatabase, definition jobcore.Definition) {
			installJobDefinition(t, ctx, database.owner, definition)
		}},
		{"job", func(t *testing.T, ctx context.Context, database *isolatedJobDatabase, definition jobcore.Definition) {
			installJobDefinition(t, ctx, database.owner, definition)
			insertBareJob(t, ctx, database.owner, definition)
		}},
		{"event", func(t *testing.T, ctx context.Context, database *isolatedJobDatabase, definition jobcore.Definition) {
			installJobDefinition(t, ctx, database.owner, definition)
			jobID, _ := insertBareJob(t, ctx, database.owner, definition)
			if _, err := database.owner.Exec(ctx, `INSERT INTO async_job_events(
				job_id,sequence,event_type,to_status,attempt,reason_code,actor_type
			) VALUES ($1,1,'enqueued','pending',0,'job_enqueued','service')`, jobID); err != nil {
				t.Fatal(err)
			}
		}},
		{"outbox", func(t *testing.T, ctx context.Context, database *isolatedJobDatabase, definition jobcore.Definition) {
			installJobDefinition(t, ctx, database.owner, definition)
			jobID, operationID := insertBareJob(t, ctx, database.owner, definition)
			eventID := uuid.New()
			if _, err := database.owner.Exec(ctx, `INSERT INTO operation_outbox(
				event_id,event_key,job_id,operation_id,topic,envelope,status,error_code
			) VALUES ($1,$2,$3,$4,'async_job_wake',jsonb_build_object(
				'schema_version',1,'event_id',$1::uuid,'job_id',$3::uuid,
				'operation_id',$4::uuid,'topic','async_job_wake'
			),'suppressed','publisher_disabled')`, eventID, "job:"+jobID.String()+":wake:v1", jobID, operationID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			database := newIsolatedJobDatabase(t)
			ctx := context.Background()
			if _, err := database.owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type)
				VALUES ('test-env','Protected Down Sentinel','dev')`); err != nil {
				t.Fatal(err)
			}
			definition := syntheticJobDefinition("test.down_" + testCase.name)
			testCase.setup(t, ctx, database, definition)
			err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down-to", "3")
			if err == nil || !strings.Contains(err.Error(), "durable job evidence exists") {
				t.Fatalf("%s evidence down = %v", testCase.name, err)
			}
			var version, environmentCount int
			var environmentsTable, authTable, assetsTable, jobsTable *string
			if err := database.owner.QueryRow(ctx, `SELECT
				(SELECT max(version_id) FROM goose_db_version WHERE is_applied),
				(SELECT count(*) FROM environments WHERE environment_id='test-env'),
				to_regclass('public.environments')::text,
				to_regclass('public.control_admin_users')::text,
				to_regclass('public.gateway_instances')::text,
				to_regclass('public.async_jobs')::text`).Scan(
				&version, &environmentCount, &environmentsTable, &authTable, &assetsTable, &jobsTable); err != nil {
				t.Fatal(err)
			}
			if version != 4 || environmentCount != 1 || environmentsTable == nil || authTable == nil || assetsTable == nil || jobsTable == nil {
				t.Fatalf("refused down damaged schema version=%d env=%d tables=%v/%v/%v/%v",
					version, environmentCount, environmentsTable, authTable, assetsTable, jobsTable)
			}
		})
	}
}

func insertBareJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, definition jobcore.Definition) (uuid.UUID, uuid.UUID) {
	t.Helper()
	jobID, operationID := uuid.New(), uuid.New()
	payload := []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO async_jobs (
		job_id,idempotency_key,job_kind,payload_schema_version,operation_id,payload,payload_hash,
		priority,max_attempts,max_verification_attempts,timeout_seconds,lease_seconds,
		heartbeat_interval_seconds,replay_safe,rollback_allowed,deadline_at
	) VALUES ($1,$2,$3,$4,$5,$6::jsonb,
		sha256(convert_to(public.control_job_payload_canonical($6::jsonb),'UTF8')),
		50,$7,$8,$9,$10,$11,$12,$13,clock_timestamp()+interval '1 day')`,
		jobID, "test:"+uuid.NewString(), definition.Kind, definition.SchemaVersion, operationID,
		payload, definition.MaxAttempts, definition.MaxVerifyAttempts,
		int(definition.Timeout/time.Second), int(definition.LeaseDuration/time.Second),
		int(definition.HeartbeatInterval/time.Second), definition.ReplaySafe, definition.AllowRollback); err != nil {
		t.Fatal(err)
	}
	return jobID, operationID
}
