package store_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

type transitionFencedRepository interface {
	TransitionFenced(context.Context, jobcore.Transition) (jobcore.TransitionOutcome, error)
}

func transitionFencedError(repository transitionFencedRepository, ctx context.Context, transition jobcore.Transition) error {
	_, err := repository.TransitionFenced(ctx, transition)
	return err
}

func transitionFencedOutcome(t *testing.T, repository transitionFencedRepository, ctx context.Context, transition jobcore.Transition) jobcore.TransitionOutcome {
	t.Helper()
	outcome, err := repository.TransitionFenced(ctx, transition)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

// executionPolicyDefinition is deliberately synthetic: these tests exercise
// the durable policy boundary and never make a DingTalk HTTP request.
func executionPolicyDefinition(kind string, unknownReplay, directSuccess, replaySafe bool) jobcore.Definition {
	definition := syntheticJobDefinition(kind)
	definition.AllowUnknownEffectReplay = unknownReplay
	definition.AllowDirectSuccess = directSuccess
	definition.ReplaySafe = replaySafe
	return definition
}

func installExecutionPolicyKind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, definition jobcore.Definition) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO async_job_kinds (
		job_kind, payload_schema_version, default_timeout_seconds, lease_seconds,
		heartbeat_interval_seconds, default_max_attempts,
		default_max_verification_attempts, replay_safe, rollback_allowed,
		allow_unknown_effect_replay, allow_direct_success
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		definition.Kind, definition.SchemaVersion, int(definition.Timeout/time.Second),
		int(definition.LeaseDuration/time.Second), int(definition.HeartbeatInterval/time.Second),
		definition.MaxAttempts, definition.MaxVerifyAttempts, definition.ReplaySafe,
		definition.AllowRollback, definition.AllowUnknownEffectReplay, definition.AllowDirectSuccess)
	if err != nil {
		t.Fatalf("install execution-policy kind: %v", err)
	}
}

func executionPolicyPayload() []byte {
	return []byte(`{"enabled":true,"mode":"safe","revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55"}`)
}

func TestDurableJobExecutionPolicyForwardMigrationDefaultsAndOldJobs(t *testing.T) {
	database := newIsolatedJobDatabase(t, "up-to", "27")
	ctx := context.Background()
	definition := syntheticJobDefinition("test.policy.old_" + assetFixtureSuffix(t))
	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insertSyntheticJobKind(t, ctx, tx, definition)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// The old schema/function must be able to create evidence before 00028.
	payload := executionPolicyPayload()
	jobID, operationID := uuid.New(), uuid.New()
	if _, err := database.owner.Exec(ctx, `SELECT * FROM public.control_enqueue_async_job(
		$1::uuid, $2::text, $3::text, 1::integer, $4::uuid, $5::jsonb,
		sha256(convert_to(public.control_job_payload_canonical($5::jsonb),'UTF8')),
		50::smallint, $6::uuid, $7::text, false::boolean
	)`, jobID, "test:old:"+uuid.NewString(), definition.Kind, operationID,
		payload, uuid.New(), "job:"+jobID.String()+":wake:v1"); err != nil {
		t.Fatalf("enqueue pre-00028 job: %v", err)
	}

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	var catalogUnknown, catalogDirect, jobUnknown, jobDirect bool
	if err := database.owner.QueryRow(ctx, `SELECT k.allow_unknown_effect_replay,k.allow_direct_success,
		j.allow_unknown_effect_replay,j.allow_direct_success
		FROM async_job_kinds k JOIN async_jobs j ON j.job_kind=k.job_kind WHERE j.job_id=$1`, jobID).
		Scan(&catalogUnknown, &catalogDirect, &jobUnknown, &jobDirect); err != nil {
		t.Fatal(err)
	}
	if catalogUnknown || catalogDirect || jobUnknown || jobDirect {
		t.Fatalf("00028 defaults changed old policy: catalog=%t/%t job=%t/%t", catalogUnknown, catalogDirect, jobUnknown, jobDirect)
	}
}

func TestDurableJobExecutionPolicyDownIsForwardOnly(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	err := runAssetGoose(t, context.Background(), "../..", database.ownerURL, "down-to", "27")
	if err == nil || !strings.Contains(err.Error(), "execution policy migration is forward-only") {
		t.Fatalf("policy down error = %v", err)
	}
}

func TestDurableJobExecutionPolicyCatalogAndJobInvariants(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()

	badCatalog := executionPolicyDefinition("test.policy.bad_"+assetFixtureSuffix(t), true, false, false)
	_, err := database.owner.Exec(ctx, `INSERT INTO async_job_kinds (
		job_kind,payload_schema_version,default_timeout_seconds,lease_seconds,heartbeat_interval_seconds,
		default_max_attempts,default_max_verification_attempts,replay_safe,rollback_allowed,
		allow_unknown_effect_replay,allow_direct_success
	) VALUES ($1,1,30,5,1,3,3,$2,$3,$4,$5)`, badCatalog.Kind, badCatalog.ReplaySafe,
		badCatalog.AllowRollback, badCatalog.AllowUnknownEffectReplay, badCatalog.AllowDirectSuccess)
	if err == nil {
		t.Fatal("catalog accepted allow_unknown_effect_replay without replay_safe")
	}
	requirePostgresCode(t, err, "23514")

	valid := executionPolicyDefinition("test.policy.independent_"+assetFixtureSuffix(t), false, true, false)
	installExecutionPolicyKind(t, ctx, database.owner, valid)
	var unknown, direct, replaySafe bool
	if err := database.owner.QueryRow(ctx, `SELECT allow_unknown_effect_replay,allow_direct_success,replay_safe
		FROM async_job_kinds WHERE job_kind=$1`, valid.Kind).Scan(&unknown, &direct, &replaySafe); err != nil {
		t.Fatal(err)
	}
	if unknown || !direct || replaySafe {
		t.Fatalf("independent policy snapshot = unknown=%t direct=%t replay_safe=%t", unknown, direct, replaySafe)
	}

	// The same invariant must also protect a direct persistence bypass.
	badJob := executionPolicyDefinition("test.policy.job_bad_"+assetFixtureSuffix(t), true, false, false)
	installExecutionPolicyKind(t, ctx, database.owner, executionPolicyDefinition(badJob.Kind, false, false, true))
	payload := executionPolicyPayload()
	hash := sha256.Sum256(payload)
	_, err = database.owner.Exec(ctx, `INSERT INTO async_jobs (
		job_id,idempotency_key,job_kind,payload_schema_version,operation_id,payload,payload_hash,priority,
		max_attempts,max_verification_attempts,timeout_seconds,lease_seconds,heartbeat_interval_seconds,
		replay_safe,rollback_allowed,allow_unknown_effect_replay,allow_direct_success,deadline_at
	) VALUES ($1,$2,$3,1,$4,$5::jsonb,$6,50,3,3,30,5,1,false,false,true,false,clock_timestamp()+interval '1 day')`,
		uuid.New(), "test:bad-job:"+uuid.NewString(), badJob.Kind, uuid.New(), payload, hash[:])
	if err == nil {
		t.Fatal("job/catalog persistence accepted invalid unknown replay policy")
	}
	requirePostgresCode(t, err, "23514")
}

func TestDurableJobExecutionPolicyEnqueueSnapshotAndSameKeyConflict(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	definition := executionPolicyDefinition("test.policy.enqueue_"+assetFixtureSuffix(t), true, true, true)
	installExecutionPolicyKind(t, ctx, database.owner, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	request := jobcore.EnqueueRequest{Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:policy:" + uuid.NewString(), Payload: executionPolicyPayload(), Priority: 50,
		PublisherEnabled: true, Actor: jobcore.ActorService}
	created := enqueueCommitted(t, ctx, database.owner, registry, request)
	if !created.Created || !created.Job.AllowDirectSuccess || !created.Job.AllowUnknownEffectReplay {
		t.Fatalf("enqueue did not round-trip policy snapshot: %+v", created.Job)
	}
	publicRepository, err := jobstore.NewJobRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	kinds, err := publicRepository.JobKinds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var foundKind bool
	for _, kind := range kinds {
		if kind.JobKind == definition.Kind {
			foundKind = true
			if !kind.AllowUnknownEffectReplay || !kind.AllowDirectSuccess {
				t.Fatalf("JobKinds policy snapshot = %+v", kind)
			}
		}
	}
	if !foundKind {
		t.Fatalf("JobKinds omitted %s", definition.Kind)
	}
	replayed := enqueueCommitted(t, ctx, database.owner, registry, request)
	if replayed.Created || replayed.Job.ID != created.Job.ID {
		t.Fatalf("same-key identical replay = %+v", replayed)
	}
	changed := definition
	changed.AllowDirectSuccess = false
	changedRegistry, err := jobcore.NewRegistry(changed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txStore, _ := jobstore.NewJobTxStore(tx)
	_, err = jobcore.EnqueueTx(ctx, txStore, changedRegistry, request)
	if !errors.Is(err, jobcore.ErrConflict) {
		t.Fatalf("same-key policy mismatch = %v, want conflict", err)
	}
	if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
		t.Fatal(rollbackErr)
	}
	changedUnknown := definition
	changedUnknown.AllowUnknownEffectReplay = false
	unknownRegistry, err := jobcore.NewRegistry(changedUnknown)
	if err != nil {
		t.Fatal(err)
	}
	tx2, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txStore2, _ := jobstore.NewJobTxStore(tx2)
	_, err = jobcore.EnqueueTx(ctx, txStore2, unknownRegistry, request)
	if !errors.Is(err, jobcore.ErrConflict) {
		t.Fatalf("same-key unknown-replay policy mismatch = %v, want conflict", err)
	}
	_ = tx2.Rollback(ctx)
}

type directSuccessExecutor struct{ executes atomic.Int32 }

func (e *directSuccessExecutor) Execute(context.Context, jobcore.Execution) jobcore.ExecuteResult {
	e.executes.Add(1)
	return jobcore.ExecuteResult{Disposition: jobcore.ExecuteSucceeded}
}
func (*directSuccessExecutor) Verify(context.Context, jobcore.Execution) jobcore.VerifyResult {
	return jobcore.VerifyResult{Disposition: jobcore.VerifyEffectUnknown}
}
func (*directSuccessExecutor) Rollback(context.Context, jobcore.Execution) jobcore.RollbackResult {
	return jobcore.RollbackResult{Disposition: jobcore.RollbackFailed}
}

func TestDurableJobDirectSuccessNeedsPersistedAuthorizationAndFence(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	for _, allowed := range []bool{false, true} {
		definition := executionPolicyDefinition("test.policy.direct_"+assetFixtureSuffix(t), false, allowed, false)
		executor := &directSuccessExecutor{}
		definition.Executor = executor
		installExecutionPolicyKind(t, ctx, database.owner, definition)
		registry, err := jobcore.NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
			Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:direct:" + uuid.NewString(),
			Payload: executionPolicyPayload(), Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
		})
		repository, err := jobstore.NewJobRepository(database.runtime)
		if err != nil {
			t.Fatal(err)
		}
		lease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "direct-worker", Token: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		// Directly exercise the DB contract first; unauthorized direct success
		// must fail even when all other fields and fencing are valid.
		err = transitionFencedError(repository, ctx, jobcore.Transition{JobID: created.Job.ID, Token: lease.Token,
			From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusSucceeded, Event: jobcore.EventSucceeded,
			Actor: jobcore.ActorWorker, ReleaseLease: true})
		if allowed && err != nil {
			t.Fatalf("authorized direct DB success: %v", err)
		}
		if !allowed {
			requirePostgresCode(t, err, "23514")
		}
		var status, event string
		if err := database.owner.QueryRow(ctx, `SELECT j.status, coalesce(e.event_type,'') FROM async_jobs j
			LEFT JOIN async_job_events e ON e.job_id=j.job_id AND e.event_type='succeeded'
			WHERE j.job_id=$1 ORDER BY e.sequence DESC LIMIT 1`, created.Job.ID).Scan(&status, &event); err != nil {
			t.Fatal(err)
		}
		if allowed && (status != "succeeded" || event != "succeeded") {
			t.Fatalf("authorized direct success state=%s event=%s", status, event)
		}
		if !allowed && status == "succeeded" {
			t.Fatal("unauthorized direct success reached succeeded")
		}
	}
}

func TestDurableJobWorkerDirectSuccessPersistsWorkerEvent(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	for _, allowed := range []bool{false, true} {
		definition := executionPolicyDefinition("test.policy.worker_direct_"+assetFixtureSuffix(t), false, allowed, false)
		executor := &directSuccessExecutor{}
		definition.Executor = executor
		installExecutionPolicyKind(t, ctx, database.owner, definition)
		registry, err := jobcore.NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
			Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:worker-direct:" + uuid.NewString(),
			Payload: executionPolicyPayload(), Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
		})
		repository, err := jobstore.NewJobRepository(database.runtime)
		if err != nil {
			t.Fatal(err)
		}
		worker, err := jobcore.NewWorker(repository, registry, jobcore.WorkerConfig{Owner: "worker-direct", Concurrency: 1,
			PollInterval: 5 * time.Millisecond, DatabaseBackoff: 5 * time.Millisecond, ShutdownGrace: time.Second,
			Retry: jobcore.BackoffPolicy{Initial: time.Millisecond, Maximum: time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- worker.Run(runCtx) }()
		worker.Wake()
		deadline := time.Now().Add(3 * time.Second)
		var status, eventActor string
		for time.Now().Before(deadline) {
			if err := database.owner.QueryRow(ctx, `SELECT j.status,coalesce(e.actor_type,'') FROM async_jobs j
			LEFT JOIN async_job_events e ON e.job_id=j.job_id AND e.event_type='succeeded'
			WHERE j.job_id=$1 ORDER BY e.sequence DESC LIMIT 1`, created.Job.ID).Scan(&status, &eventActor); err != nil {
				t.Fatal(err)
			}
			if status == "succeeded" || status == "failed" {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if allowed && (status != "succeeded" || eventActor != "worker") {
			t.Fatalf("authorized worker direct success status=%s actor=%s", status, eventActor)
		}
		if !allowed && status != "failed" {
			t.Fatalf("unauthorized worker direct success status=%s", status)
		}
		if executor.executes.Load() != 1 {
			t.Fatalf("worker Execute count=%d, want 1", executor.executes.Load())
		}
	}
}

func TestDurableJobDirectSuccessRejectsExpiredToken(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	definition := executionPolicyDefinition("test.policy.direct_expired_"+assetFixtureSuffix(t), false, true, false)
	installExecutionPolicyKind(t, ctx, database.owner, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:direct-expired:" + uuid.NewString(),
		Payload: executionPolicyPayload(), Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
	})
	repository, _ := jobstore.NewJobRepository(database.runtime)
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "expired-worker", Token: token}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE async_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	var outcome jobcore.TransitionOutcome
	outcome, err = repository.TransitionFenced(ctx, jobcore.Transition{JobID: created.Job.ID, Token: token,
		From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusSucceeded, Event: jobcore.EventSucceeded,
		Actor: jobcore.ActorWorker, ReleaseLease: true})
	if !errors.Is(err, jobcore.ErrLostLease) {
		t.Fatalf("expired direct-success token = %v, want lost lease", err)
	}
	if outcome.Status != "" || outcome.ErrorCode != "" {
		t.Fatalf("expired direct-success outcome = %+v, want zero outcome", outcome)
	}
}

func TestDurableJobExpiredUnknownPolicyRecoveryAndUnsafeBounds(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	for _, unknownReplay := range []bool{false, true} {
		definition := executionPolicyDefinition("test.policy.recovery_"+assetFixtureSuffix(t), unknownReplay, false, unknownReplay)
		installExecutionPolicyKind(t, ctx, database.owner, definition)
		registry, err := jobcore.NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
			Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:recovery:" + uuid.NewString(),
			Payload: executionPolicyPayload(), Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
		})
		repository, _ := jobstore.NewJobRepository(database.runtime)
		oldToken := uuid.New()
		if _, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "old-worker", Token: oldToken}); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `UPDATE async_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, created.Job.ID); err != nil {
			t.Fatal(err)
		}
		newToken := uuid.New()
		recovered, recoverErr := repository.ClaimRecoverable(ctx, jobcore.ClaimRequest{Owner: "reconciler", Token: newToken})
		if unknownReplay {
			if recoverErr != nil || recovered.Status != jobcore.StatusRunning || recovered.Token != newToken || recovered.Attempt != 1 {
				t.Fatalf("opted-in expired recovery = lease=%+v err=%v", recovered, recoverErr)
			}
			if err := transitionFencedError(repository, ctx, jobcore.Transition{JobID: created.Job.ID, Token: oldToken,
				From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusFailed, Event: jobcore.EventFailed,
				Actor: jobcore.ActorWorker, ReleaseLease: true}); !errors.Is(err, jobcore.ErrLostLease) {
				t.Fatalf("expired old worker fence = %v, want lost lease", err)
			}
			if err := transitionFencedError(repository, ctx, jobcore.Transition{JobID: created.Job.ID, Token: newToken,
				From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusRetryWait, Event: jobcore.EventRetryScheduled,
				Actor: jobcore.ActorReconciler, ReasonCode: "effect_unknown_unverified", RetryAfter: 0, ReleaseLease: true}); err != nil {
				t.Fatalf("unknown recovery retry transition: %v", err)
			}
			next, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "new-worker", Token: uuid.New()})
			if err != nil || next.Attempt != 2 || next.Token == newToken {
				t.Fatalf("replayed claim = lease=%+v err=%v", next, err)
			}
		} else if recoverErr != nil || recovered.Status != jobcore.StatusVerifying || recovered.Token != newToken || recovered.Attempt != 1 {
			t.Fatalf("default expired recovery = lease=%+v err=%v", recovered, recoverErr)
		}
	}
}

func TestDurableJobUnknownReplayBudgetAndCancellationAreDBBounded(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	for _, cancelled := range []bool{false, true} {
		definition := executionPolicyDefinition("test.policy.bound_"+assetFixtureSuffix(t), true, false, true)
		definition.MaxAttempts = 1
		installExecutionPolicyKind(t, ctx, database.owner, definition)
		registry, err := jobcore.NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
			Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:bound:" + uuid.NewString(),
			Payload: executionPolicyPayload(), Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
		})
		repository, _ := jobstore.NewJobRepository(database.runtime)
		lease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "bound-worker", Token: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		if cancelled {
			if _, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_request_async_job_cancel($1::uuid,'cancel_requested'::text)`, created.Job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := transitionFencedError(repository, ctx, jobcore.Transition{JobID: created.Job.ID, Token: lease.Token,
			From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusRetryWait, Event: jobcore.EventRetryScheduled,
			Actor: jobcore.ActorReconciler, ReasonCode: "effect_unknown_unverified", RetryAfter: 0, ReleaseLease: true}); err != nil {
			t.Fatalf("unsafe unknown transition cancelled=%t: %v", cancelled, err)
		}
		var status, code string
		if err := database.owner.QueryRow(ctx, `SELECT status,error_code FROM async_jobs WHERE job_id=$1`, created.Job.ID).Scan(&status, &code); err != nil {
			t.Fatal(err)
		}
		wantCode := "max_attempts_exhausted"
		if cancelled {
			wantCode = "cancel_after_unknown_effect"
		}
		if status != "failed" || code != wantCode {
			t.Fatalf("unsafe unknown result cancelled=%t state=%s code=%s", cancelled, status, code)
		}
		if _, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "after-failed", Token: uuid.New()}); !errors.Is(err, jobcore.ErrNotFound) {
			t.Fatalf("failed unknown job remained claimable: %v", err)
		}
	}
}

func TestDurableJobCancelAfterUnknownRetryIsNotSafeCancellation(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	for _, unknownReplay := range []bool{false, true} {
		definition := executionPolicyDefinition("test.policy.cancel_retry_"+assetFixtureSuffix(t), unknownReplay, false, true)
		installExecutionPolicyKind(t, ctx, database.owner, definition)
		registry, err := jobcore.NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
			Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:cancel-retry:" + uuid.NewString(),
			Payload: executionPolicyPayload(), Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
		})
		repository, _ := jobstore.NewJobRepository(database.runtime)
		lease, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "cancel-retry-worker", Token: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		if err := transitionFencedError(repository, ctx, jobcore.Transition{JobID: created.Job.ID, Token: lease.Token,
			From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusRetryWait, Event: jobcore.EventRetryScheduled,
			ReasonCode: func() string {
				if unknownReplay {
					return "effect_unknown_unverified"
				}
				return "execute_retryable_no_effect"
			}(),
			Actor: jobcore.ActorWorker, ErrorCode: func() string {
				if unknownReplay {
					return "execution_result_unknown"
				}
				return "temporary_failure"
			}(), RetryAfter: 0, ReleaseLease: true}); err != nil {
			t.Fatalf("enter retry_wait unknown=%t: %v", unknownReplay, err)
		}
		if _, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_request_async_job_cancel($1::uuid,'cancel_requested'::text)`, created.Job.ID); err != nil {
			t.Fatal(err)
		}
		var status, code string
		if err := database.owner.QueryRow(ctx, `SELECT status,coalesce(error_code,'') FROM async_jobs WHERE job_id=$1`, created.Job.ID).Scan(&status, &code); err != nil {
			t.Fatal(err)
		}
		if unknownReplay {
			if status != "failed" || code != "cancel_after_unknown_effect" {
				t.Fatalf("unknown retry cancellation became safe state=%s code=%s", status, code)
			}
		} else if status != "cancelled" || code != "" {
			t.Fatalf("ordinary retry_wait cancellation status=%s code=%s, want cancelled with nil error", status, code)
		}
	}
}

func TestDurableJobUnknownEvidenceSurvivesLaterKnownNoEffectRetry(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	definition := executionPolicyDefinition("test.policy.cancel_history_"+assetFixtureSuffix(t), true, false, true)
	installExecutionPolicyKind(t, ctx, database.owner, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:cancel-history:" + uuid.NewString(),
		Payload: executionPolicyPayload(), Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
	})
	repository, _ := jobstore.NewJobRepository(database.runtime)
	first, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "history-worker", Token: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	for _, transition := range []jobcore.Transition{
		{JobID: created.Job.ID, Token: first.Token, From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusRetryWait,
			Event: jobcore.EventRetryScheduled, Actor: jobcore.ActorWorker, ReasonCode: "effect_unknown_unverified", ErrorCode: "execution_result_unknown", ReleaseLease: true},
	} {
		if err := transitionFencedError(repository, ctx, transition); err != nil {
			t.Fatal(err)
		}
	}
	second, err := repository.ClaimRunnable(ctx, jobcore.ClaimRequest{Owner: "history-worker", Token: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := transitionFencedError(repository, ctx, jobcore.Transition{JobID: created.Job.ID, Token: second.Token,
		From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusRetryWait, Event: jobcore.EventRetryScheduled,
		Actor: jobcore.ActorWorker, ReasonCode: "execute_retryable_no_effect", ErrorCode: "known_no_effect", RetryAfter: 0, ReleaseLease: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT * FROM public.control_request_async_job_cancel($1::uuid,'cancel_requested'::text)`, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	var status, code string
	if err := database.owner.QueryRow(ctx, `SELECT status,coalesce(error_code,'') FROM async_jobs WHERE job_id=$1`, created.Job.ID).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "cancel_after_unknown_effect" {
		t.Fatalf("known no-effect retry erased unknown evidence: status=%s code=%s", status, code)
	}
}
