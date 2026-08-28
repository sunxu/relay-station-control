package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"regexp"
	"strings"
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

func syntheticJobDefinition(kind string) jobcore.Definition {
	return jobcore.Definition{
		Kind: kind, SchemaVersion: 1,
		Schema: jobcore.Schema{Fields: map[string]jobcore.Field{
			"enabled":   {Type: jobcore.FieldBoolean, Required: true},
			"revision":  {Type: jobcore.FieldInteger, Required: true},
			"target_id": {Type: jobcore.FieldUUID, Required: true},
			"mode":      {Type: jobcore.FieldString, Required: true, MinLength: 1, MaxLength: 16, Pattern: regexp.MustCompile(`^[a-z<>]+$`)},
		}},
		Timeout: 30 * time.Second, LeaseDuration: 5 * time.Second,
		HeartbeatInterval: time.Second, MaxAttempts: 3, MaxVerifyAttempts: 3,
		ReplaySafe: true, AllowRollback: true, Executor: syntheticNoopExecutor{},
	}
}

type syntheticNoopExecutor struct{}

func (syntheticNoopExecutor) Execute(context.Context, jobcore.Execution) jobcore.ExecuteResult {
	return jobcore.ExecuteResult{Disposition: jobcore.ExecuteNeedsVerification}
}
func (syntheticNoopExecutor) Verify(context.Context, jobcore.Execution) jobcore.VerifyResult {
	return jobcore.VerifyResult{Disposition: jobcore.VerifyEffectUnknown}
}
func (syntheticNoopExecutor) Rollback(context.Context, jobcore.Execution) jobcore.RollbackResult {
	return jobcore.RollbackResult{Disposition: jobcore.RollbackUnknown}
}

func insertSyntheticJobKind(t *testing.T, ctx context.Context, tx pgx.Tx, definition jobcore.Definition) {
	t.Helper()
	_, err := tx.Exec(ctx, `INSERT INTO async_job_kinds (
		job_kind, payload_schema_version, default_timeout_seconds, lease_seconds,
		heartbeat_interval_seconds, default_max_attempts,
		default_max_verification_attempts, replay_safe, rollback_allowed
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		definition.Kind, definition.SchemaVersion, int(definition.Timeout/time.Second),
		int(definition.LeaseDuration/time.Second), int(definition.HeartbeatInterval/time.Second),
		definition.MaxAttempts, definition.MaxVerifyAttempts,
		definition.ReplaySafe, definition.AllowRollback)
	if err != nil {
		t.Fatalf("insert synthetic job kind: %v", err)
	}
}

func TestDurableJobAtomicEnqueueCanonicalHashAndLifecycle(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	kind := "test." + assetFixtureSuffix(t)
	definition := syntheticJobDefinition(kind)
	insertSyntheticJobKind(t, ctx, tx, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	txStore, err := jobstore.NewJobTxStore(tx)
	if err != nil {
		t.Fatal(err)
	}
	request := jobcore.EnqueueRequest{
		Kind: kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:" + uuid.NewString(),
		Payload:        []byte(`{"target_id":"2CF45C9D-EA70-4D1A-AE2B-550701C22A55","revision":1,"mode":"safe","enabled":true}`),
		Priority:       50, Actor: jobcore.ActorService,
	}
	created, err := jobcore.EnqueueTx(ctx, txStore, registry, request)
	if err != nil {
		t.Fatalf("enqueue canonical Go payload: %v", err)
	}
	if !created.Created || created.Job.ID == uuid.Nil || created.Job.Status != jobcore.StatusPending {
		t.Fatalf("unexpected created job: %+v", created)
	}
	canonical, expectedHash, _, err := registry.ValidateAndHash(kind, 1, request.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(created.Job.Payload, canonical) || created.Job.PayloadHash != expectedHash {
		t.Fatal("database round trip changed canonical payload or hash")
	}

	replayed, err := jobcore.EnqueueTx(ctx, txStore, registry, request)
	if err != nil || replayed.Created || replayed.Job.ID != created.Job.ID {
		t.Fatalf("idempotent replay = %+v, %v", replayed, err)
	}
	var jobCount, eventCount, outboxCount int
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM async_jobs WHERE idempotency_key=$1),
		(SELECT count(*) FROM async_job_events WHERE job_id=$2),
		(SELECT count(*) FROM operation_outbox WHERE job_id=$2)`,
		request.IdempotencyKey, created.Job.ID).Scan(&jobCount, &eventCount, &outboxCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 || eventCount != 1 || outboxCount != 1 {
		t.Fatalf("atomic bundle counts = %d/%d/%d", jobCount, eventCount, outboxCount)
	}

	queries := generated.New(tx)
	token := uuid.New()
	claimed, err := queries.ClaimRunnableAsyncJob(ctx, generated.ClaimRunnableAsyncJobParams{
		LeaseOwner: "test-worker", LeaseFencingToken: pgtype.UUID{Bytes: token, Valid: true},
	})
	if err != nil {
		t.Fatalf("claim runnable: %v", err)
	}
	if claimed.JobID.Bytes != created.Job.ID || claimed.Status != "running" || claimed.AttemptCount != 1 || claimed.LeaseFencingToken.Bytes != token {
		t.Fatalf("invalid claim: %+v", claimed)
	}
	err = assetSavepoint(t, ctx, tx, "worker bypasses verification", func() error {
		_, err := queries.TransitionAsyncJobFenced(ctx, generated.TransitionAsyncJobFencedParams{
			JobID: claimed.JobID, ExpectedStatus: "running", LeaseFencingToken: claimed.LeaseFencingToken,
			TargetStatus: "succeeded", EventType: "succeeded", RetryDelaySeconds: 0,
			ActorType: "worker", ReleaseLease: true,
		})
		return err
	})
	requireSQLState(t, err, "23514")
	verifying, err := queries.TransitionAsyncJobFenced(ctx, generated.TransitionAsyncJobFencedParams{
		JobID: claimed.JobID, ExpectedStatus: "running", LeaseFencingToken: claimed.LeaseFencingToken,
		TargetStatus: "verifying", EventType: "verification_started", RetryDelaySeconds: 0,
		ActorType: "worker", ReleaseLease: true,
	})
	if err != nil || verifying.Status != "verifying" || verifying.LeaseFencingToken.Valid {
		t.Fatalf("worker verification handoff: status=%s err=%v", verifying.Status, err)
	}
	recoveryToken := uuid.New()
	recovered, err := queries.ClaimExpiredAsyncJob(ctx, generated.ClaimExpiredAsyncJobParams{
		LeaseOwner: "test-reconciler", LeaseFencingToken: pgtype.UUID{Bytes: recoveryToken, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	transitioned, err := queries.TransitionAsyncJobFenced(ctx, generated.TransitionAsyncJobFencedParams{
		JobID: recovered.JobID, ExpectedStatus: "verifying", LeaseFencingToken: recovered.LeaseFencingToken,
		TargetStatus: "succeeded", EventType: "succeeded", RetryDelaySeconds: 0,
		ActorType: "reconciler", ReleaseLease: true,
	})
	if err != nil || transitioned.Status != "succeeded" || !transitioned.CompletedAt.Valid {
		t.Fatalf("verified fenced success: status=%s err=%v", transitioned.Status, err)
	}
	_, err = queries.TransitionAsyncJobFenced(ctx, generated.TransitionAsyncJobFencedParams{
		JobID: claimed.JobID, ExpectedStatus: "running", LeaseFencingToken: claimed.LeaseFencingToken,
		TargetStatus: "failed", EventType: "failed", RetryDelaySeconds: 0,
		ActorType: "worker", ReleaseLease: true,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale fence error = %v", err)
	}

	err = assetSavepoint(t, ctx, tx, "mutate immutable event", func() error {
		_, err := tx.Exec(ctx, `UPDATE async_job_events SET reason_code='changed' WHERE job_id=$1`, created.Job.ID)
		return err
	})
	requireSQLState(t, err, "23514")
	err = assetSavepoint(t, ctx, tx, "leave terminal state", func() error {
		_, err := tx.Exec(ctx, `UPDATE async_jobs SET status='retry_wait', completed_at=NULL WHERE job_id=$1`, created.Job.ID)
		return err
	})
	requireSQLState(t, err, "23514")
}

func TestDurableJobDatabaseRejectsSensitiveAndMismatchedPayload(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	kind := "test." + assetFixtureSuffix(t)
	definition := syntheticJobDefinition(kind)
	insertSyntheticJobKind(t, ctx, tx, definition)

	for name, payload := range map[string]string{
		"nested raw response": `{"meta":{"rawResponse":"credential-canary"}}`,
		"nested command":      `{"items":[{"command":"credential-canary"}]}`,
	} {
		err := assetSavepoint(t, ctx, tx, name, func() error {
			_, err := tx.Exec(ctx, `SELECT * FROM public.control_enqueue_async_job(
				gen_random_uuid(), $1, $2, 1, gen_random_uuid(), $3::jsonb,
				$4::bytea, 50::smallint, gen_random_uuid(), $5, false
			)`, "test:"+uuid.NewString(), kind, payload, bytes.Repeat([]byte{0}, 32), "job:"+uuid.NewString()+":wake:v1")
			return err
		})
		requireSQLState(t, err, "23514")
		if bytes.Contains([]byte(err.Error()), []byte("credential-canary")) {
			t.Fatalf("%s reflected payload canary: %v", name, err)
		}
	}

	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, _, err := registry.ValidateAndHash(kind, 1,
		[]byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`))
	if err != nil {
		t.Fatal(err)
	}
	badHash := sha256.Sum256([]byte("different"))
	err = assetSavepoint(t, ctx, tx, "mismatched hash", func() error {
		_, err := tx.Exec(ctx, `SELECT * FROM public.control_enqueue_async_job(
			gen_random_uuid(), $1, $2, 1, gen_random_uuid(), $3::jsonb,
			$4::bytea, 50::smallint, gen_random_uuid(), $5, false
		)`, "test:"+uuid.NewString(), kind, canonical, badHash[:], "job:"+uuid.NewString()+":wake:v1")
		return err
	})
	requireSQLState(t, err, "23514")
	expandingPayload := []byte(`{"value":"` + strings.Repeat("<>&", 4000) + `"}`)
	err = assetSavepoint(t, ctx, tx, "canonical payload expansion", func() error {
		_, err := tx.Exec(ctx, `SELECT * FROM public.control_enqueue_async_job(
			gen_random_uuid(), $1, $2, 1, gen_random_uuid(), $3::jsonb,
			sha256(convert_to(public.control_job_payload_canonical($3::jsonb),'UTF8')),
			50::smallint, gen_random_uuid(), $4, false
		)`, "test:"+uuid.NewString(), kind, expandingPayload, "job:"+uuid.NewString()+":wake:v1")
		return err
	})
	requireSQLState(t, err, "23514")
}

func TestDurableJobRecoveryFencingBudgetsAndOutboxCrash(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	kind := "test." + assetFixtureSuffix(t)
	definition := syntheticJobDefinition(kind)
	insertSyntheticJobKind(t, ctx, tx, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	txStore, _ := jobstore.NewJobTxStore(tx)
	request := jobcore.EnqueueRequest{
		Kind: kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:" + uuid.NewString(),
		Payload:        []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`),
		Priority:       50, PublisherEnabled: true, Actor: jobcore.ActorService,
	}
	created, err := jobcore.EnqueueTx(ctx, txStore, registry, request)
	if err != nil {
		t.Fatal(err)
	}
	queries := generated.New(tx)
	workerToken := uuid.New()
	claimed, err := queries.ClaimRunnableAsyncJob(ctx, generated.ClaimRunnableAsyncJobParams{
		LeaseOwner: "crashed-worker", LeaseFencingToken: pgtype.UUID{Bytes: workerToken, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE async_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	recoveryToken := uuid.New()
	recovered, err := queries.ClaimExpiredAsyncJob(ctx, generated.ClaimExpiredAsyncJobParams{
		LeaseOwner: "reconciler", LeaseFencingToken: pgtype.UUID{Bytes: recoveryToken, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "verifying" || recovered.VerificationAttempt != 1 || recovered.LeaseFencingToken.Bytes != recoveryToken {
		t.Fatalf("invalid recovered lease: %+v", recovered)
	}
	_, err = queries.TransitionAsyncJobFenced(ctx, generated.TransitionAsyncJobFencedParams{
		JobID: claimed.JobID, ExpectedStatus: "running", LeaseFencingToken: pgtype.UUID{Bytes: workerToken, Valid: true},
		TargetStatus: "succeeded", EventType: "succeeded", RetryDelaySeconds: 0,
		ActorType: "worker", ReleaseLease: true,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale worker transition = %v", err)
	}
	_, err = queries.TransitionAsyncJobFenced(ctx, generated.TransitionAsyncJobFencedParams{
		JobID: recovered.JobID, ExpectedStatus: "verifying", LeaseFencingToken: recovered.LeaseFencingToken,
		TargetStatus: "verifying", EventType: "verification_started", RetryDelaySeconds: 0,
		ReasonCode: pgtype.Text{String: "effect_unknown", Valid: true},
		ErrorCode:  pgtype.Text{String: "effect_unknown", Valid: true},
		ActorType:  "reconciler", ReleaseLease: true,
	})
	if err != nil {
		t.Fatalf("schedule verification retry: %v", err)
	}
	recoveredAgain, err := queries.ClaimExpiredAsyncJob(ctx, generated.ClaimExpiredAsyncJobParams{
		LeaseOwner: "reconciler", LeaseFencingToken: pgtype.UUID{Bytes: uuid.New(), Valid: true},
	})
	if err != nil || recoveredAgain.VerificationAttempt != 2 {
		t.Fatalf("persistent verification budget = %d, %v", recoveredAgain.VerificationAttempt, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE async_jobs SET verification_attempt=3,
		lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	_, err = queries.ClaimExpiredAsyncJob(ctx, generated.ClaimExpiredAsyncJobParams{
		LeaseOwner: "reconciler", LeaseFencingToken: pgtype.UUID{Bytes: uuid.New(), Valid: true},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("exhausted recovery claim = %v", err)
	}
	var exhaustedStatus, exhaustedCode string
	if err := tx.QueryRow(ctx, `SELECT status,error_code FROM async_jobs WHERE job_id=$1`, created.Job.ID).Scan(&exhaustedStatus, &exhaustedCode); err != nil {
		t.Fatal(err)
	}
	if exhaustedStatus != "failed" || exhaustedCode != "verification_exhausted" {
		t.Fatalf("exhausted recovery = %s/%s", exhaustedStatus, exhaustedCode)
	}

	firstDispatchToken := uuid.New()
	outbox, err := queries.ClaimOutboxEvent(ctx, generated.ClaimOutboxEventParams{
		LeaseOwner: "dispatcher", LeaseFencingToken: pgtype.UUID{Bytes: firstDispatchToken, Valid: true}, LeaseSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE operation_outbox SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE event_id=$1`, outbox.EventID); err != nil {
		t.Fatal(err)
	}
	secondDispatchToken := uuid.New()
	republished, err := queries.ClaimOutboxEvent(ctx, generated.ClaimOutboxEventParams{
		LeaseOwner: "dispatcher-restarted", LeaseFencingToken: pgtype.UUID{Bytes: secondDispatchToken, Valid: true}, LeaseSeconds: 5,
	})
	if err != nil || republished.AttemptCount != 2 || republished.LeaseFencingToken.Bytes != secondDispatchToken {
		t.Fatalf("outbox crash recovery = %+v, %v", republished, err)
	}
	_, err = queries.TransitionOutboxEventFenced(ctx, generated.TransitionOutboxEventFencedParams{
		EventID: outbox.EventID, LeaseFencingToken: pgtype.UUID{Bytes: firstDispatchToken, Valid: true},
		TargetStatus: "sent", RetryDelaySeconds: 0,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale dispatcher transition = %v", err)
	}
}

func TestDurableJobDatabaseDeadlineClosesUnstartedWork(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	kind := "test." + assetFixtureSuffix(t)
	insertSyntheticJobKind(t, ctx, tx, syntheticJobDefinition(kind))
	jobID := uuid.New()
	payload := []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`)
	if _, err := tx.Exec(ctx, `INSERT INTO async_jobs (
		job_id,idempotency_key,job_kind,payload_schema_version,operation_id,payload,payload_hash,
		max_attempts,max_verification_attempts,timeout_seconds,lease_seconds,
		heartbeat_interval_seconds,replay_safe,rollback_allowed,
		available_at,deadline_at,created_at,updated_at
	) VALUES ($1,$2,$3,1,gen_random_uuid(),$4::jsonb,
		sha256(convert_to(public.control_job_payload_canonical($4::jsonb),'UTF8')),
		3,3,30,5,1,true,true,clock_timestamp()-interval '2 days',
		clock_timestamp()-interval '1 day',clock_timestamp()-interval '3 days',clock_timestamp()-interval '3 days')`,
		jobID, "test:"+uuid.NewString(), kind, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO async_job_events (
		job_id,sequence,event_type,to_status,attempt,reason_code,actor_type,occurred_at
	) VALUES ($1,1,'enqueued','pending',0,'job_enqueued','service',clock_timestamp()-interval '3 days')`, jobID); err != nil {
		t.Fatal(err)
	}
	queries := generated.New(tx)
	_, err = queries.ClaimRunnableAsyncJob(ctx, generated.ClaimRunnableAsyncJobParams{
		LeaseOwner: "worker", LeaseFencingToken: pgtype.UUID{Bytes: uuid.New(), Valid: true},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired unstarted claim = %v", err)
	}
	var status, errorCode string
	var eventCount int
	if err := tx.QueryRow(ctx, `SELECT status,error_code,
		(SELECT count(*) FROM async_job_events WHERE job_id=$1) FROM async_jobs WHERE job_id=$1`, jobID).Scan(&status, &errorCode, &eventCount); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || errorCode != "deadline_exceeded" || eventCount != 2 {
		t.Fatalf("deadline close = %s/%s events=%d", status, errorCode, eventCount)
	}
}

func TestDurableJobRuntimeRoleHasMinimumPrivileges(t *testing.T) {
	owner := connectTestDatabase(t)
	runtime := connectRuntimeDatabase(t)
	ctx := context.Background()
	var canSelectKinds, canWriteKinds, canWriteJobs, canMutateEvents, canSelectPayload bool
	var canEnqueue, canExecutePayloadHelper, canExecuteMutationGuard bool
	if err := owner.QueryRow(ctx, `SELECT
		has_table_privilege('relay_control_runtime','async_job_kinds','SELECT'),
		has_table_privilege('relay_control_runtime','async_job_kinds','INSERT'),
		has_table_privilege('relay_control_runtime','async_jobs','UPDATE'),
		has_table_privilege('relay_control_runtime','async_job_events','UPDATE'),
		has_column_privilege('relay_control_runtime','async_jobs','payload','SELECT'),
		has_function_privilege('relay_control_runtime',
			'public.control_enqueue_async_job(uuid,text,text,integer,uuid,jsonb,bytea,smallint,uuid,text,boolean)','EXECUTE'),
		has_function_privilege('relay_control_runtime','public.control_job_payload_is_safe(jsonb)','EXECUTE'),
		has_function_privilege('relay_control_runtime','public.control_guard_async_job_mutation()','EXECUTE')`).Scan(
		&canSelectKinds, &canWriteKinds, &canWriteJobs, &canMutateEvents, &canSelectPayload,
		&canEnqueue, &canExecutePayloadHelper, &canExecuteMutationGuard); err != nil {
		t.Fatal(err)
	}
	if !canSelectKinds || canWriteKinds || canWriteJobs || canMutateEvents || canSelectPayload ||
		!canEnqueue || canExecutePayloadHelper || canExecuteMutationGuard {
		t.Fatalf("unexpected runtime ACL selectKinds/writeKinds/writeJobs/mutateEvents/selectPayload=%v/%v/%v/%v/%v",
			canSelectKinds, canWriteKinds, canWriteJobs, canMutateEvents, canSelectPayload)
	}
	_, err := runtime.Exec(ctx, `INSERT INTO async_job_kinds (
		job_kind,payload_schema_version,default_timeout_seconds,lease_seconds,default_max_attempts
	) VALUES ('runtime.forbidden',1,30,5,3)`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("runtime catalog insert error = %v", err)
	}
	var jobsCount, expiredLeases, outboxCount int64
	if err := runtime.QueryRow(ctx, `SELECT count(*), count(*) FILTER (
		WHERE status IN ('running','verifying','rolling_back') AND lease_expires_at <= clock_timestamp()
	) FROM async_jobs`).Scan(&jobsCount, &expiredLeases); err != nil {
		t.Fatalf("runtime job metrics projection: %v", err)
	}
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM operation_outbox
		WHERE status IN ('pending','retry_wait') AND available_at <= clock_timestamp()`).Scan(&outboxCount); err != nil {
		t.Fatalf("runtime outbox metrics projection: %v", err)
	}
}

func TestDurableJobProtectedDownRequiresEmptyEvidenceTables(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ownerConfig, err := pgx.ParseConfig(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	databaseName := strings.ReplaceAll("job_"+assetFixtureSuffix(t), "-", "_")
	maintenanceConfig := ownerConfig.Copy()
	maintenanceConfig.Database = "postgres"
	maintenance, err := pgx.ConnectConfig(ctx, maintenanceConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close(context.Background())
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = maintenance.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, databaseName)
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+identifier)
	})
	isolatedConfig := ownerConfig.Copy()
	isolatedConfig.Database = databaseName
	isolatedLocation, err := url.Parse(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	isolatedLocation.Path = "/" + databaseName
	isolatedURL := isolatedLocation.String()
	repositoryRoot := "../.."
	if err := runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "up"); err != nil {
		t.Fatal(err)
	}
	checkVersion := func(stage string) {
		connection, err := pgx.ConnectConfig(ctx, isolatedConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close(context.Background())
		var version int
		var tableName *string
		if err := connection.QueryRow(ctx, `SELECT max(version_id) FILTER (WHERE is_applied), to_regclass('public.async_job_kinds')::text FROM goose_db_version`).Scan(&version, &tableName); err != nil {
			t.Fatal(err)
		}
		if stage == "after-up" && (version != 9 || tableName == nil) {
			t.Fatalf("%s version/table = %d/%v", stage, version, tableName)
		}
		if stage == "after-down" && (version != 3 || tableName != nil) {
			t.Fatalf("%s version/table = %d/%v", stage, version, tableName)
		}
		if stage == "after-reup" && (version != 4 || tableName == nil) {
			t.Fatalf("%s version/table = %d/%v", stage, version, tableName)
		}
	}
	checkVersion("after-up")
	if err := runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "down-to", "3"); err != nil {
		t.Fatalf("empty durable-job down: %v", err)
	}
	checkVersion("after-down")
	if err := runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	checkVersion("after-reup")
	isolated, err := pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, `INSERT INTO async_job_kinds (
		job_kind,payload_schema_version,default_timeout_seconds,lease_seconds,
		heartbeat_interval_seconds,default_max_attempts,default_max_verification_attempts
	) VALUES ('test.protected_down',1,30,5,1,3,3)`); err != nil {
		isolated.Close(ctx)
		t.Fatal(err)
	}
	isolated.Close(ctx)
	definition := syntheticJobDefinition("test.protected_down")
	definition.ReplaySafe = false
	definition.AllowRollback = false
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	request := jobcore.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:" + uuid.NewString(), Priority: 50, Actor: jobcore.ActorService,
		Payload: []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`),
	}
	type enqueueOutcome struct {
		result jobcore.EnqueueResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan enqueueOutcome, 2)
	for index := 0; index < 2; index++ {
		go func() {
			connection, connectErr := pgx.ConnectConfig(ctx, isolatedConfig)
			if connectErr != nil {
				outcomes <- enqueueOutcome{err: connectErr}
				return
			}
			defer connection.Close(context.Background())
			tx, beginErr := connection.Begin(ctx)
			if beginErr != nil {
				outcomes <- enqueueOutcome{err: beginErr}
				return
			}
			defer tx.Rollback(context.Background())
			store, storeErr := jobstore.NewJobTxStore(tx)
			if storeErr != nil {
				outcomes <- enqueueOutcome{err: storeErr}
				return
			}
			<-start
			result, enqueueErr := jobcore.EnqueueTx(ctx, store, registry, request)
			if enqueueErr == nil {
				enqueueErr = tx.Commit(ctx)
			}
			outcomes <- enqueueOutcome{result: result, err: enqueueErr}
		}()
	}
	close(start)
	createdCount := 0
	var commonJobID uuid.UUID
	for index := 0; index < 2; index++ {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("concurrent enqueue: %v", outcome.err)
		}
		if outcome.result.Created {
			createdCount++
		}
		if commonJobID == uuid.Nil {
			commonJobID = outcome.result.Job.ID
		}
		if outcome.result.Job.ID != commonJobID {
			t.Fatalf("concurrent enqueue returned different jobs")
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent enqueue created count = %d", createdCount)
	}
	isolated, err = pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, `CREATE TABLE synthetic_job_confirmations (
		job_id uuid PRIMARY KEY, result text NOT NULL
	)`); err != nil {
		isolated.Close(ctx)
		t.Fatal(err)
	}
	isolated.Close(ctx)
	pool, err := pgxpool.New(ctx, isolatedURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := jobstore.NewJobRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: lease.ID, Token: lease.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusVerifying, Event: jobcore.EventVerification,
		Actor: jobcore.ActorWorker, ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	recovery, err := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: recovery.ID, Token: recovery.Token, From: []jobcore.Status{jobcore.StatusVerifying},
		To: jobcore.StatusSucceeded, Event: jobcore.EventSucceeded,
		Actor: jobcore.ActorReconciler, ReleaseLease: true,
		Mutation: func(ctx context.Context, database jobcore.DBTX) error {
			_, err := database.Exec(ctx, `INSERT INTO synthetic_job_confirmations(job_id,result) VALUES ($1,'confirmed')`, recovery.ID)
			return err
		},
	}); err != nil {
		t.Fatalf("transaction-bound success mutation: %v", err)
	}
	var persistedStatus, confirmation string
	if err := pool.QueryRow(ctx, `SELECT job.status, confirmation.result
		FROM async_jobs AS job JOIN synthetic_job_confirmations AS confirmation USING(job_id)
		WHERE job.job_id=$1`, recovery.ID).Scan(&persistedStatus, &confirmation); err != nil {
		t.Fatal(err)
	}
	if persistedStatus != "succeeded" || confirmation != "confirmed" {
		t.Fatalf("atomic mutation persisted %s/%s", persistedStatus, confirmation)
	}

	failedRequest := request
	failedRequest.IdempotencyKey = "test:" + uuid.NewString()
	failedRequest.OperationID = uuid.New()
	connection, err := pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	createTx, err := connection.Begin(ctx)
	if err != nil {
		connection.Close(ctx)
		t.Fatal(err)
	}
	createStore, _ := jobstore.NewJobTxStore(createTx)
	failedJob, err := jobcore.EnqueueTx(ctx, createStore, registry, failedRequest)
	if err == nil {
		err = createTx.Commit(ctx)
	}
	connection.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failedLease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "worker", Token: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if failedLease.ID != failedJob.Job.ID {
		t.Fatal("claimed unexpected rollback fixture")
	}
	if err := repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: failedLease.ID, Token: failedLease.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: jobcore.StatusVerifying, Event: jobcore.EventVerification,
		Actor: jobcore.ActorWorker, ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
	failedRecovery, err := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	mutationFailure := errors.New("synthetic mutation failure")
	err = repository.TransitionFenced(ctx, jobcore.Transition{
		JobID: failedRecovery.ID, Token: failedRecovery.Token, From: []jobcore.Status{jobcore.StatusVerifying},
		To: jobcore.StatusSucceeded, Event: jobcore.EventSucceeded,
		Actor: jobcore.ActorReconciler, ReleaseLease: true,
		Mutation: func(ctx context.Context, database jobcore.DBTX) error {
			if _, err := database.Exec(ctx, `INSERT INTO synthetic_job_confirmations(job_id,result) VALUES ($1,'must_rollback')`, failedRecovery.ID); err != nil {
				return err
			}
			return mutationFailure
		},
	})
	if !errors.Is(err, mutationFailure) {
		t.Fatalf("mutation rollback error = %v", err)
	}
	var confirmationCount int
	if err := pool.QueryRow(ctx, `SELECT job.status,
		(SELECT count(*) FROM synthetic_job_confirmations WHERE job_id=job.job_id)
		FROM async_jobs AS job WHERE job.job_id=$1`, failedRecovery.ID).Scan(&persistedStatus, &confirmationCount); err != nil {
		t.Fatal(err)
	}
	if persistedStatus != "verifying" || confirmationCount != 0 {
		t.Fatalf("failed mutation leaked state %s/%d", persistedStatus, confirmationCount)
	}
	pool.Close()
	err = runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "down-to", "3")
	if err == nil || !strings.Contains(err.Error(), "durable job evidence exists") {
		t.Fatalf("protected down error = %v", err)
	}
	isolated, err = pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close(context.Background())
	var version int
	if err := isolated.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("migration version after refused down = %d", version)
	}
}
