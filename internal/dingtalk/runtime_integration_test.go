package dingtalk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dingtalk "github.com/sunxu/relay-station-control/internal/dingtalk"
	"github.com/sunxu/relay-station-control/internal/jobs"
	store "github.com/sunxu/relay-station-control/internal/store"
)

// runtimeJobDatabase is intentionally local to this package because the store
// package's isolated fixture is private. Runtime operations use the real
// store repository below.
type runtimeJobDatabase struct {
	ownerURL   string
	runtimeURL string
	owner      *pgxpool.Pool
	runtime    *pgxpool.Pool
}

func newRuntimeJobDatabase(t *testing.T) *runtimeJobDatabase {
	t.Helper()
	ownerURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	runtimeURL := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if ownerURL == "" || runtimeURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests")
	}

	ownerConfig, err := pgx.ParseConfig(ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "dingtalk_rt_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	maintenanceConfig := ownerConfig.Copy()
	maintenanceConfig.Database = "postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	maintenance, err := pgx.ConnectConfig(ctx, maintenanceConfig)
	if err != nil {
		t.Fatalf("connect PostgreSQL maintenance database: %v", err)
	}
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := maintenance.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		maintenance.Close(context.Background())
		t.Fatalf("create isolated runtime database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_, _ = maintenance.Exec(cleanupCtx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		_, _ = maintenance.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier)
		_ = maintenance.Close(cleanupCtx)
	})

	ownerLocation, err := url.Parse(ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	ownerLocation.Path = "/" + databaseName
	runtimeLocation, err := url.Parse(runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	runtimeLocation.Path = "/" + databaseName
	database := &runtimeJobDatabase{ownerURL: ownerLocation.String(), runtimeURL: runtimeLocation.String()}

	migrationCtx, migrationCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	err = runRuntimeGoose(migrationCtx, database.ownerURL)
	migrationCancel()
	if err != nil {
		t.Fatal(err)
	}
	poolCtx, poolCancel := context.WithTimeout(context.Background(), 45*time.Second)
	database.owner, err = pgxpool.New(poolCtx, database.ownerURL)
	if err != nil {
		poolCancel()
		t.Fatal(err)
	}
	database.runtime, err = pgxpool.New(poolCtx, database.runtimeURL)
	poolCancel()
	if err != nil {
		database.owner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.runtime.Close()
		database.owner.Close()
	})
	return database
}

func runRuntimeGoose(ctx context.Context, databaseURL string) error {
	command := exec.CommandContext(ctx, "go", "tool", "goose", "up")
	command.Dir = filepath.Join("..", "..", "tools")
	command.Env = append(os.Environ(),
		"GOOSE_DRIVER=postgres",
		"GOOSE_DBSTRING="+databaseURL,
		"GOOSE_MIGRATION_DIR=../migrations",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("goose up: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

type trackedExecutor struct {
	delegate  jobs.Executor
	executes  atomic.Int32
	verifies  atomic.Int32
	rollbacks atomic.Int32
}

func (e *trackedExecutor) Execute(ctx context.Context, execution jobs.Execution) jobs.ExecuteResult {
	e.executes.Add(1)
	return e.delegate.Execute(ctx, execution)
}
func (e *trackedExecutor) Verify(ctx context.Context, execution jobs.Execution) jobs.VerifyResult {
	e.verifies.Add(1)
	return e.delegate.Verify(ctx, execution)
}
func (e *trackedExecutor) Rollback(ctx context.Context, execution jobs.Execution) jobs.RollbackResult {
	e.rollbacks.Add(1)
	return e.delegate.Rollback(ctx, execution)
}

type observingRepository struct {
	jobs.Repository
	mu         sync.Mutex
	claims     []uuid.UUID
	recoveries []uuid.UUID
}

func (r *observingRepository) ClaimRunnable(ctx context.Context, request jobs.ClaimRequest) (*jobs.Lease, error) {
	lease, err := r.Repository.ClaimRunnable(ctx, request)
	if err == nil && lease != nil {
		r.mu.Lock()
		r.claims = append(r.claims, lease.Token)
		r.mu.Unlock()
	}
	return lease, err
}

func (r *observingRepository) ClaimRecoverable(ctx context.Context, request jobs.ClaimRequest) (*jobs.Lease, error) {
	lease, err := r.Repository.ClaimRecoverable(ctx, request)
	if err == nil && lease != nil {
		r.mu.Lock()
		r.recoveries = append(r.recoveries, lease.Token)
		r.mu.Unlock()
	}
	return lease, err
}

type dropTransitionRepository struct {
	jobs.Repository
	dropped chan jobs.Transition
	once    atomic.Bool
}

type holdTransitionRepository struct {
	jobs.Repository
	held    chan jobs.Transition
	release chan struct{}
}

func (r *holdTransitionRepository) TransitionFenced(ctx context.Context, transition jobs.Transition) (jobs.TransitionOutcome, error) {
	select {
	case r.held <- transition:
		select {
		case <-r.release:
		case <-ctx.Done():
			return jobs.TransitionOutcome{}, ctx.Err()
		}
	default:
	}
	return r.Repository.TransitionFenced(ctx, transition)
}

func mustJobRepository(t *testing.T, pool *pgxpool.Pool) *store.JobRepository {
	t.Helper()
	repository, err := store.NewJobRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func requestRuntimeCancel(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID) error {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	txStore, err := store.NewJobTxStore(tx)
	if err != nil {
		return err
	}
	if _, err := jobs.RequestCancelTx(context.Background(), txStore, jobID, jobs.ActorService, "operator_cancelled"); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}

func waitUntilRuntimeAvailable(t *testing.T, availableAt time.Time) {
	t.Helper()
	if delay := time.Until(availableAt); delay > 0 {
		time.Sleep(delay + 50*time.Millisecond)
	}
}

// setFixtureDeadlinePast is only test-database setup. It temporarily disables
// the immutable trigger in one owner transaction, changes a valid seeded job's
// deadline, restores the trigger before commit, and never disables it while a
// Worker/Reconciler operation is under test.
func setFixtureDeadlinePast(t *testing.T, database *runtimeJobDatabase, jobID uuid.UUID) {
	t.Helper()
	tx, err := database.owner.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `ALTER TABLE async_jobs DISABLE TRIGGER async_jobs_mutation_guard`); err != nil {
		t.Fatal(err)
	}
	updated, err := tx.Exec(context.Background(), `UPDATE async_jobs SET created_at=clock_timestamp()-interval '2 seconds', deadline_at=clock_timestamp()-interval '1 second' WHERE job_id=$1 AND status IN ('retry_wait','running')`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RowsAffected() != 1 {
		t.Fatal("deadline fixture did not update exactly one nonterminal job")
	}
	if _, err := tx.Exec(context.Background(), `ALTER TABLE async_jobs ENABLE TRIGGER async_jobs_mutation_guard`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (r *dropTransitionRepository) TransitionFenced(ctx context.Context, transition jobs.Transition) (jobs.TransitionOutcome, error) {
	if r.once.CompareAndSwap(false, true) {
		r.dropped <- transition
		return jobs.TransitionOutcome{}, errors.New("test crash before durable transition commit")
	}
	return r.Repository.TransitionFenced(ctx, transition)
}

func dingtalkRuntimeWorker(t *testing.T, repository jobs.Repository, registry *jobs.Registry, owner string) *jobs.Worker {
	t.Helper()
	worker, err := jobs.NewWorker(repository, registry, jobs.WorkerConfig{
		Owner: owner, Concurrency: 1, PollInterval: 5 * time.Millisecond, DatabaseBackoff: 5 * time.Millisecond,
		ShutdownGrace: 2 * time.Second, Retry: runtimeBackoff(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func dingtalkRuntimeReconciler(t *testing.T, repository jobs.Repository, registry *jobs.Registry) *jobs.Reconciler {
	t.Helper()
	reconciler, err := jobs.NewReconciler(repository, registry, jobs.ReconcilerConfig{
		Owner: "runtime-reconciler", Concurrency: 1, PollInterval: 5 * time.Millisecond, DatabaseBackoff: 5 * time.Millisecond,
		ShutdownGrace: 2 * time.Second, Retry: runtimeBackoff(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

func runtimeBackoff() jobs.BackoffPolicy {
	return jobs.BackoffPolicy{Initial: time.Second, Maximum: time.Second, Jitter: func(time.Duration) time.Duration { return 0 }}
}

func startRuntimeLoop(t *testing.T, run func(context.Context) error) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("runtime loop did not drain: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("runtime loop did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func runWorkerUntil(t *testing.T, worker *jobs.Worker, database *runtimeJobDatabase, jobID uuid.UUID, want jobs.Status, timeout time.Duration, expectedAttempt ...int) {
	t.Helper()
	stop := startRuntimeLoop(t, worker.Run)
	defer stop()
	deadline := time.Now().Add(timeout)
	for {
		var status string
		var attempt int
		if err := database.owner.QueryRow(context.Background(), `SELECT status,attempt_count FROM async_jobs WHERE job_id=$1`, jobID).Scan(&status, &attempt); err != nil {
			t.Fatal(err)
		}
		attemptReady := len(expectedAttempt) == 0 || attempt >= expectedAttempt[0]
		if jobs.Status(status) == want && attemptReady {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s status=%s, want %s", jobID, status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runReconcilerUntil(t *testing.T, reconciler *jobs.Reconciler, database *runtimeJobDatabase, jobID uuid.UUID, want jobs.Status, timeout time.Duration) {
	t.Helper()
	stop := startRuntimeLoop(t, reconciler.Run)
	defer stop()
	waitRuntimeStatus(t, database, jobID, want, timeout)
}

func waitRuntimeStatus(t *testing.T, database *runtimeJobDatabase, jobID uuid.UUID, want jobs.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var status string
		if err := database.owner.QueryRow(context.Background(), `SELECT status FROM async_jobs WHERE job_id=$1`, jobID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if jobs.Status(status) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime status=%s, want %s", status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func enqueueRuntimeJob(t *testing.T, database *runtimeJobDatabase, registry *jobs.Registry, definition jobs.Definition, payload []byte, key string) jobs.Job {
	t.Helper()
	// Construct each synthetic transition once, before the durable job exists.
	// Recovery never rebuilds this snapshot or its operation identity.
	if definition.Kind == dingtalk.JobKind {
		var snapshot dingtalk.Payload
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		snapshot.OccurrenceID = uuid.NewString()
		key = "dingtalk:availability:" + snapshot.OccurrenceID + ":active"
		var err error
		payload, err = json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
	}
	tx, err := database.runtime.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	txStore, err := store.NewJobTxStore(tx)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.NewSHA1(uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86"), []byte(key))
	result, err := jobs.EnqueueTx(context.Background(), txStore, registry, jobs.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: definition.SchemaVersion, OperationID: operationID,
		IdempotencyKey: key, Payload: payload, Priority: 50, PublisherEnabled: false, Actor: jobs.ActorService,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Job.OperationID != operationID {
		t.Fatal("runtime enqueue did not create the durable job")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return result.Job
}

func expireRuntimeLease(t *testing.T, database *runtimeJobDatabase, jobID uuid.UUID) {
	t.Helper()
	if _, err := database.owner.Exec(context.Background(), `UPDATE async_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
}

func readRuntimeSnapshot(t *testing.T, database *runtimeJobDatabase, jobID uuid.UUID) (key string, operation uuid.UUID, payload []byte, hash []byte, available time.Time, status jobs.Status, attempt int, leaseToken *uuid.UUID, errorCode *string) {
	t.Helper()
	var operationID pgtype.UUID
	var token pgtype.UUID
	var errorText pgtype.Text
	var availableAt time.Time
	var statusText string
	if err := database.owner.QueryRow(context.Background(), `SELECT idempotency_key,operation_id,payload,payload_hash,available_at,status,attempt_count,lease_fencing_token,error_code FROM async_jobs WHERE job_id=$1`, jobID).Scan(&key, &operationID, &payload, &hash, &availableAt, &statusText, &attempt, &token, &errorText); err != nil {
		t.Fatal(err)
	}
	if token.Valid {
		value := uuid.UUID(token.Bytes)
		leaseToken = &value
	}
	if errorText.Valid {
		value := errorText.String
		errorCode = &value
	}
	return key, uuid.UUID(operationID.Bytes), payload, hash, availableAt.UTC(), jobs.Status(statusText), attempt, leaseToken, errorCode
}

func readRuntimeEvents(t *testing.T, database *runtimeJobDatabase, jobID uuid.UUID) (events, reasons []string) {
	t.Helper()
	rows, err := database.owner.Query(context.Background(), `SELECT event_type,COALESCE(reason_code,'') FROM async_job_events WHERE job_id=$1 ORDER BY sequence`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var eventType, reason string
		if err := rows.Scan(&eventType, &reason); err != nil {
			t.Fatal(err)
		}
		events = append(events, eventType)
		reasons = append(reasons, reason)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return events, reasons
}

func runtimePayloadAndDefinition(t *testing.T, executor jobs.Executor) jobs.Definition {
	t.Helper()
	return dingtalk.Definition(executor)
}

func runtimeTestPayload() []byte {
	return []byte(`{"occurrence_id":"00000000-0000-4000-8000-000000000001","occurrence_type":"TOKEN_INVALID","transition":"ACTIVE","reason":"token_invalid","severity":"Critical","environment_id":"test","environment_name":"test","account_key":"antigravity:example@example.com","email":"example@example.com","provider":"antigravity","instance_ids":["00000000-0000-4000-8000-000000000003"],"node_names":["relay-a"],"started_at":"2026-09-11T00:00:00Z","transitioned_at":"2026-09-11T00:00:00Z"}`)
}

func registerOrdinaryDefinition(t *testing.T, database *runtimeJobDatabase, definition jobs.Definition) {
	t.Helper()
	_, err := database.owner.Exec(context.Background(), `INSERT INTO async_job_kinds (
		job_kind,payload_schema_version,default_timeout_seconds,lease_seconds,heartbeat_interval_seconds,
		default_max_attempts,default_max_verification_attempts,replay_safe,allow_unknown_effect_replay,allow_direct_success,rollback_allowed
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, definition.Kind, definition.SchemaVersion,
		int(definition.Timeout/time.Second), int(definition.LeaseDuration/time.Second), int(definition.HeartbeatInterval/time.Second),
		definition.MaxAttempts, definition.MaxVerifyAttempts, definition.ReplaySafe, definition.AllowUnknownEffectReplay,
		definition.AllowDirectSuccess, definition.AllowRollback)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDingTalkUnknownReplayBudgetAndStableSnapshot(t *testing.T) {
	database := newRuntimeJobDatabase(t)
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"errcode":-1,"errmsg":"busy"}`)
	}))
	defer server.Close()
	tracked := &trackedExecutor{delegate: dingtalk.ExecutorAtForRuntimeTest(t, server, "")}
	definition := runtimePayloadAndDefinition(t, tracked)
	registry, err := jobs.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	repository := &observingRepository{Repository: mustJobRepository(t, database.runtime)}
	created := enqueueRuntimeJob(t, database, registry, definition, runtimeTestPayload(), "rt:dingtalk:unknown:"+uuid.NewString())
	initialKey, initialOperation, initialPayload, initialHash, _, _, _, _, _ := readRuntimeSnapshot(t, database, created.ID)
	var claimTokens []uuid.UUID
	var persistedBackoffs []time.Time
	worker := dingtalkRuntimeWorker(t, repository, registry, "worker-1")
	for attempt := 1; attempt <= definition.MaxAttempts; attempt++ {
		runWorkerUntil(t, worker, database, created.ID, func() jobs.Status {
			if attempt == definition.MaxAttempts {
				return jobs.StatusFailed
			}
			return jobs.StatusRetryWait
		}(), 8*time.Second, attempt)
		key, operation, payload, hash, available, status, gotAttempt, token, errorCode := readRuntimeSnapshot(t, database, created.ID)
		if key != initialKey || operation != initialOperation || string(payload) != string(initialPayload) || string(hash) != string(initialHash) {
			t.Fatalf("attempt %d changed stable identity/payload", attempt)
		}
		if gotAttempt != attempt || token != nil {
			// A released transition must clear the lease; the repository records
			// each claim token independently for the fencing assertion below.
			t.Fatalf("attempt %d status=%s attempt=%d lease_present=%t", attempt, status, gotAttempt, token != nil)
		}
		if attempt == 1 && !available.After(created.CreatedAt) {
			t.Fatal("persisted retry backoff was not written")
		}
		if attempt == definition.MaxAttempts && valueOrEmpty(errorCode) != "max_attempts_exhausted" {
			t.Fatalf("fifth attempt error_code=%s", valueOrEmpty(errorCode))
		}
		if attempt < definition.MaxAttempts {
			persistedBackoffs = append(persistedBackoffs, available)
			before := available
			repository.Repository = mustJobRepository(t, database.runtime)
			worker = dingtalkRuntimeWorker(t, repository, registry, fmt.Sprintf("worker-%d", attempt+1))
			if _, err := repository.ClaimRunnable(context.Background(), jobs.ClaimRequest{Owner: "early-claim", Token: uuid.New()}); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("attempt %d claimed before available_at: %v", attempt, err)
			}
			_, _, _, _, unchanged, _, _, _, _ := readRuntimeSnapshot(t, database, created.ID)
			if !unchanged.Equal(before) {
				t.Fatalf("attempt %d available_at changed across worker reconstruction: %v -> %v", attempt, before, unchanged)
			}
			waitUntilRuntimeAvailable(t, before)
		}
	}
	if hits.Load() != int32(definition.MaxAttempts) {
		t.Fatalf("HTTP target hits=%d, want exactly %d and no sixth attempt", hits.Load(), definition.MaxAttempts)
	}
	if len(persistedBackoffs) != definition.MaxAttempts-1 {
		t.Fatalf("persisted backoff samples=%d", len(persistedBackoffs))
	}
	repository.mu.Lock()
	claimTokens = append(claimTokens, repository.claims...)
	repository.mu.Unlock()
	if len(claimTokens) != definition.MaxAttempts {
		t.Fatalf("normal claims=%d, want %d", len(claimTokens), definition.MaxAttempts)
	}
	for i := range claimTokens {
		for j := 0; j < i; j++ {
			if claimTokens[i] == claimTokens[j] {
				t.Fatal("worker reconstruction reused a fencing token")
			}
		}
	}
	types, reasons := readRuntimeEvents(t, database, created.ID)
	unknownReasons, verificationEvents := 0, 0
	for i, eventType := range types {
		if reasons[i] == jobs.ReasonEffectUnknownUnverified {
			unknownReasons++
		}
		if eventType == string(jobs.EventVerification) {
			verificationEvents++
		}
	}
	if unknownReasons != definition.MaxAttempts || verificationEvents != 0 || tracked.verifies.Load() != 0 || tracked.rollbacks.Load() != 0 {
		t.Fatalf("unknown_reason_events=%d verification_events=%d verify_calls=%d rollback_calls=%d", unknownReasons, verificationEvents, tracked.verifies.Load(), tracked.rollbacks.Load())
	}
	if _, err := repository.ClaimRunnable(context.Background(), jobs.ClaimRequest{Owner: "after-terminal", Token: uuid.New()}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("terminal job remained claimable: %v", err)
	}
	var count int
	if err := database.owner.QueryRow(context.Background(), `SELECT count(*) FROM async_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("logical job count=%d err=%v", count, err)
	}
}

func TestRuntimeDingTalkAcceptedCrashReconcilesAndFreshWorkerSucceeds(t *testing.T) {
	database := newRuntimeJobDatabase(t)
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer server.Close()
	tracked := &trackedExecutor{delegate: dingtalk.ExecutorAtForRuntimeTest(t, server, "")}
	definition := runtimePayloadAndDefinition(t, tracked)
	registry, err := jobs.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	repository := &observingRepository{Repository: mustJobRepository(t, database.runtime)}
	created := enqueueRuntimeJob(t, database, registry, definition, runtimeTestPayload(), "rt:dingtalk:crash:"+uuid.NewString())
	initialKey, initialOperation, initialPayload, initialHash, _, _, _, _, _ := readRuntimeSnapshot(t, database, created.ID)
	dropped := make(chan jobs.Transition, 1)
	crashRepository := &dropTransitionRepository{Repository: repository, dropped: dropped}
	worker := dingtalkRuntimeWorker(t, crashRepository, registry, "crash-worker")
	stop := startRuntimeLoop(t, worker.Run)
	defer stop()
	select {
	case transition := <-dropped:
		if transition.To != jobs.StatusSucceeded || transition.Event != jobs.EventSucceeded {
			t.Fatalf("intercepted transition target=%s event=%s", transition.To, transition.Event)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("worker did not POST and reach uncommitted success transition")
	}
	if hits.Load() != 1 {
		t.Fatalf("accepted POST hits=%d, want 1", hits.Load())
	}
	stop()
	_, _, _, _, _, status, attempt, oldToken, _ := readRuntimeSnapshot(t, database, created.ID)
	if status != jobs.StatusRunning || attempt != 1 || oldToken == nil {
		t.Fatalf("crash left status=%s attempt=%d lease_present=%t", status, attempt, oldToken != nil)
	}
	expireRuntimeLease(t, database, created.ID)
	runReconcilerUntil(t, dingtalkRuntimeReconciler(t, repository, registry), database, created.ID, jobs.StatusRetryWait, 8*time.Second)
	key, operation, payload, hash, retryAt, status, attempt, token, _ := readRuntimeSnapshot(t, database, created.ID)
	if status != jobs.StatusRetryWait || attempt != 1 || token != nil || key != initialKey || operation != initialOperation || string(payload) != string(initialPayload) || string(hash) != string(initialHash) {
		t.Fatalf("reconciler recovery changed durable snapshot: status=%s attempt=%d lease_present=%t", status, attempt, token != nil)
	}
	if _, err := repository.ClaimRunnable(context.Background(), jobs.ClaimRequest{Owner: "too-early", Token: uuid.New()}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("retry_wait was claimable before persisted available_at %v: %v", retryAt, err)
	}
	_, _, _, _, unchanged, _, _, _, _ := readRuntimeSnapshot(t, database, created.ID)
	if !unchanged.Equal(retryAt) {
		t.Fatalf("available_at changed across reconciler reconstruction: %v -> %v", retryAt, unchanged)
	}
	waitUntilRuntimeAvailable(t, retryAt)
	// Reconstruct the real adapter as well as the worker. Hold only its final
	// transition so the old token is tested while the new running lease is valid.
	repository.Repository = mustJobRepository(t, database.runtime)
	held := &holdTransitionRepository{Repository: repository, held: make(chan jobs.Transition, 1), release: make(chan struct{})}
	freshWorker := dingtalkRuntimeWorker(t, held, registry, "fresh-worker")
	stopFresh := startRuntimeLoop(t, freshWorker.Run)
	defer stopFresh()
	var freshTransition jobs.Transition
	select {
	case freshTransition = <-held.held:
	case <-time.After(8 * time.Second):
		t.Fatal("fresh worker did not reach direct success")
	}
	if freshTransition.To != jobs.StatusSucceeded || freshTransition.Token == *oldToken {
		t.Fatal("fresh worker did not obtain a new success-authorized fence")
	}
	var validLease bool
	if err := database.owner.QueryRow(context.Background(), `SELECT status='running' AND lease_fencing_token=$2 AND lease_expires_at>clock_timestamp() FROM async_jobs WHERE job_id=$1`, created.ID, freshTransition.Token).Scan(&validLease); err != nil || !validLease {
		t.Fatalf("fresh running lease is not valid: %v", err)
	}
	beforeEvents, _ := readRuntimeEvents(t, database, created.ID)
	stale := freshTransition
	stale.Token = *oldToken
	outcome, err := repository.TransitionFenced(context.Background(), stale)
	if !errors.Is(err, jobs.ErrLostLease) || outcome != (jobs.TransitionOutcome{}) {
		t.Fatalf("old running fence was not rejected: %v", err)
	}
	afterEvents, _ := readRuntimeEvents(t, database, created.ID)
	if len(afterEvents) != len(beforeEvents) {
		t.Fatal("rejected stale success wrote an event")
	}
	close(held.release)
	waitRuntimeStatus(t, database, created.ID, jobs.StatusSucceeded, 8*time.Second)
	stopFresh()
	key, operation, payload, hash, _, status, attempt, newToken, _ := readRuntimeSnapshot(t, database, created.ID)
	if status != jobs.StatusSucceeded || attempt != 2 || key != initialKey || operation != initialOperation || string(payload) != string(initialPayload) || string(hash) != string(initialHash) || newToken != nil {
		t.Fatalf("fresh worker completion status=%s attempt=%d lease_present=%t", status, attempt, newToken != nil)
	}
	if hits.Load() < 2 {
		t.Fatalf("target hits=%d, want at least 2 after replay", hits.Load())
	}
	repository.mu.Lock()
	claims := append([]uuid.UUID(nil), repository.claims...)
	recoveries := append([]uuid.UUID(nil), repository.recoveries...)
	repository.mu.Unlock()
	if len(claims) != 2 || len(recoveries) != 1 || claims[0] == claims[1] || claims[0] == recoveries[0] || claims[1] == recoveries[0] || oldToken == nil || claims[0] != *oldToken || claims[1] == *oldToken {
		t.Fatalf("lease/fence token count or uniqueness mismatch: claims=%d recoveries=%d", len(claims), len(recoveries))
	}
	types, reasons := readRuntimeEvents(t, database, created.ID)
	verificationEvents, rollbackEvents, unknownReasons := 0, 0, 0
	for i, eventType := range types {
		if eventType == string(jobs.EventVerification) {
			verificationEvents++
		}
		if eventType == string(jobs.EventRollbackStarted) {
			rollbackEvents++
		}
		if reasons[i] == jobs.ReasonEffectUnknownUnverified {
			unknownReasons++
		}
	}
	if verificationEvents != 0 || rollbackEvents != 0 || unknownReasons != 1 || tracked.verifies.Load() != 0 || tracked.rollbacks.Load() != 0 {
		t.Fatalf("verify_events=%d rollback_events=%d unknown_reason_events=%d verify_calls=%d rollback_calls=%d", verificationEvents, rollbackEvents, unknownReasons, tracked.verifies.Load(), tracked.rollbacks.Load())
	}
	var jobCount int
	if err := database.owner.QueryRow(context.Background(), `SELECT count(*) FROM async_jobs`).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 {
		t.Fatalf("durable job count=%d", jobCount)
	}
}

func TestRuntimeDingTalkTimeoutResetBothPolicies(t *testing.T) {
	for _, mode := range []string{"timeout", "reset"} {
		t.Run(mode, func(t *testing.T) {
			database := newRuntimeJobDatabase(t)
			var hits atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "timeout" {
					<-r.Context().Done()
					return
				}
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
			}))
			defer server.Close()
			tracked := &trackedExecutor{delegate: dingtalk.ExecutorAtForRuntimeTest(t, server, "")}
			trueDefinition := dingtalk.Definition(tracked)
			ordinaryDefinition := trueDefinition
			ordinaryDefinition.Kind = "ordinary.dingtalk_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			ordinaryDefinition.AllowUnknownEffectReplay = false
			ordinaryDefinition.AllowDirectSuccess = false
			registerOrdinaryDefinition(t, database, ordinaryDefinition)
			registry, err := jobs.NewRegistry(trueDefinition, ordinaryDefinition)
			if err != nil {
				t.Fatal(err)
			}
			repository := &observingRepository{Repository: mustJobRepository(t, database.runtime)}

			ordinary := enqueueRuntimeJob(t, database, registry, ordinaryDefinition, runtimeTestPayload(), "rt:ordinary:"+mode+":"+uuid.NewString())
			runWorkerUntil(t, dingtalkRuntimeWorker(t, repository, registry, "ordinary-timeout-worker"), database, ordinary.ID, jobs.StatusVerifying, 12*time.Second)
			_, _, _, _, _, status, attempt, _, _ := readRuntimeSnapshot(t, database, ordinary.ID)
			if status != jobs.StatusVerifying || attempt != 1 {
				t.Fatalf("ordinary status=%s attempt=%d", status, attempt)
			}
			if hits.Load() != 1 {
				t.Fatalf("ordinary HTTP hits=%d", hits.Load())
			}
			runReconcilerUntil(t, dingtalkRuntimeReconciler(t, repository, registry), database, ordinary.ID, jobs.StatusFailed, 8*time.Second)
			if hits.Load() != 1 || tracked.verifies.Load() != 1 || tracked.rollbacks.Load() != 0 {
				t.Fatalf("ordinary hits=%d verify_calls=%d rollback_calls=%d", hits.Load(), tracked.verifies.Load(), tracked.rollbacks.Load())
			}

			trueJob := enqueueRuntimeJob(t, database, registry, trueDefinition, runtimeTestPayload(), "rt:true:"+mode+":"+uuid.NewString())
			originalKey, originalOperation, originalPayload, originalHash, _, _, _, _, _ := readRuntimeSnapshot(t, database, trueJob.ID)
			runWorkerUntil(t, dingtalkRuntimeWorker(t, repository, registry, "true-timeout-worker-1"), database, trueJob.ID, jobs.StatusRetryWait, 12*time.Second)
			_, _, _, _, retryAt, status, attempt, _, _ := readRuntimeSnapshot(t, database, trueJob.ID)
			if status != jobs.StatusRetryWait || attempt != 1 {
				t.Fatalf("DingTalk status=%s attempt=%d", status, attempt)
			}
			waitUntilRuntimeAvailable(t, retryAt)
			runWorkerUntil(t, dingtalkRuntimeWorker(t, repository, registry, "true-timeout-worker-2"), database, trueJob.ID, jobs.StatusRetryWait, 12*time.Second, 2)
			key, operation, payload, hash, _, _, attempt, _, _ := readRuntimeSnapshot(t, database, trueJob.ID)
			if key != originalKey || operation != originalOperation || string(payload) != string(originalPayload) || string(hash) != string(originalHash) || attempt != 2 {
				t.Fatal("timeout/reset replay changed durable identity, payload or attempt budget")
			}
			if hits.Load() != 3 {
				t.Fatalf("same endpoint HTTP hits=%d, want ordinary plus two DingTalk attempts", hits.Load())
			}
			events, reasons := readRuntimeEvents(t, database, trueJob.ID)
			verificationEvents, unknownReasons := 0, 0
			for i, event := range events {
				if event == string(jobs.EventVerification) {
					verificationEvents++
				}
				if reasons[i] == jobs.ReasonEffectUnknownUnverified {
					unknownReasons++
				}
			}
			if verificationEvents != 0 || unknownReasons != 2 || tracked.verifies.Load() != 1 || tracked.rollbacks.Load() != 0 {
				t.Fatalf("DingTalk verification_events=%d unknown_reason_events=%d", verificationEvents, unknownReasons)
			}
		})
	}
}

func TestRuntimeDingTalkExpiredRunningBothPolicies(t *testing.T) {
	database := newRuntimeJobDatabase(t)
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"errcode":-1,"errmsg":"busy"}`)
	}))
	defer server.Close()
	trueExecutor := &trackedExecutor{delegate: dingtalk.ExecutorAtForRuntimeTest(t, server, "")}
	trueDefinition := dingtalk.Definition(trueExecutor)
	ordinaryDefinition := trueDefinition
	ordinaryDefinition.Kind = "ordinary.expired_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ordinaryDefinition.AllowUnknownEffectReplay = false
	ordinaryDefinition.AllowDirectSuccess = false
	registerOrdinaryDefinition(t, database, ordinaryDefinition)
	registry, err := jobs.NewRegistry(trueDefinition, ordinaryDefinition)
	if err != nil {
		t.Fatal(err)
	}
	repository := &observingRepository{Repository: mustJobRepository(t, database.runtime)}
	for _, testCase := range []struct {
		name       string
		definition jobs.Definition
		want       jobs.Status
	}{{"dingtalk", trueDefinition, jobs.StatusRetryWait}, {"ordinary", ordinaryDefinition, jobs.StatusFailed}} {
		t.Run(testCase.name, func(t *testing.T) {
			job := enqueueRuntimeJob(t, database, registry, testCase.definition, runtimeTestPayload(), "rt:expired:"+testCase.name+":"+uuid.NewString())
			dropped := make(chan jobs.Transition, 1)
			crashRepository := &dropTransitionRepository{Repository: repository, dropped: dropped}
			worker := dingtalkRuntimeWorker(t, crashRepository, registry, "expired-worker")
			stop := startRuntimeLoop(t, worker.Run)
			defer stop()
			select {
			case <-dropped:
			case <-time.After(8 * time.Second):
				t.Fatal("worker did not leave running lease uncommitted")
			}
			stop()
			_, _, _, _, _, status, _, _, _ := readRuntimeSnapshot(t, database, job.ID)
			if status != jobs.StatusRunning {
				t.Fatalf("pre-expiry status=%s", status)
			}
			expireRuntimeLease(t, database, job.ID)
			runReconcilerUntil(t, dingtalkRuntimeReconciler(t, repository, registry), database, job.ID, testCase.want, 8*time.Second)
			if testCase.want == jobs.StatusRetryWait {
				// Close this scenario before starting another worker in the same
				// isolated DB; otherwise its due retry could contaminate the contrast.
				if trueExecutor.verifies.Load() != 0 {
					t.Fatal("DingTalk expired running recovery called Verify")
				}
				if err := requestRuntimeCancel(t, database.runtime, job.ID); err != nil {
					t.Fatal(err)
				}
			} else if trueExecutor.verifies.Load() != 1 {
				t.Fatal("ordinary expired running recovery did not call Verify")
			}
		})
	}
	if hits.Load() != 2 {
		t.Fatalf("expired-running POST count=%d, want 2", hits.Load())
	}
}

func TestRuntimeDingTalkRetryWaitCancellationAndDeadlineGuards(t *testing.T) {
	database := newRuntimeJobDatabase(t)
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"errcode":-1,"errmsg":"busy"}`)
	}))
	defer server.Close()
	tracked := &trackedExecutor{delegate: dingtalk.ExecutorAtForRuntimeTest(t, server, "")}
	definition := dingtalk.Definition(tracked)
	registry, err := jobs.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	repository := &observingRepository{Repository: mustJobRepository(t, database.runtime)}

	cancelJob := enqueueRuntimeJob(t, database, registry, definition, runtimeTestPayload(), "rt:cancel:"+uuid.NewString())
	runWorkerUntil(t, dingtalkRuntimeWorker(t, repository, registry, "cancel-worker"), database, cancelJob.ID, jobs.StatusRetryWait, 8*time.Second)
	if err := requestRuntimeCancel(t, database.runtime, cancelJob.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, status, _, _, errorCode := readRuntimeSnapshot(t, database, cancelJob.ID)
	if status != jobs.StatusFailed || errorCode == nil || *errorCode != "cancel_after_unknown_effect" {
		t.Fatalf("cancel status=%s error_code=%s", status, valueOrEmpty(errorCode))
	}

	deadlineJob := enqueueRuntimeJob(t, database, registry, definition, runtimeTestPayload(), "rt:deadline:"+uuid.NewString())
	runWorkerUntil(t, dingtalkRuntimeWorker(t, repository, registry, "deadline-first"), database, deadlineJob.ID, jobs.StatusRetryWait, 8*time.Second)
	setFixtureDeadlinePast(t, database, deadlineJob.ID)
	if _, err := repository.ClaimRunnable(context.Background(), jobs.ClaimRequest{Owner: "deadline-claim", Token: uuid.New()}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("deadline claim error=%v", err)
	}
	_, _, _, _, _, status, _, _, errorCode = readRuntimeSnapshot(t, database, deadlineJob.ID)
	if status != jobs.StatusFailed || errorCode == nil || *errorCode != "deadline_exceeded" {
		t.Fatalf("deadline status=%s error_code=%s", status, valueOrEmpty(errorCode))
	}
	if hits.Load() != 2 {
		t.Fatalf("guards caused extra POST hits=%d", hits.Load())
	}
	if tracked.verifies.Load() != 0 || tracked.rollbacks.Load() != 0 {
		t.Fatalf("DingTalk verify_calls=%d rollback_calls=%d", tracked.verifies.Load(), tracked.rollbacks.Load())
	}
}

func TestRuntimeDingTalkExpiredRunningReplayGuards(t *testing.T) {
	for _, guard := range []struct {
		name, code string
		attempts   int
	}{
		{"cancel", "cancel_after_unknown_effect", 1},
		{"deadline", "job_deadline_exceeded", 1},
		{"exhausted", "max_attempts_exhausted", 5},
	} {
		t.Run(guard.name, func(t *testing.T) {
			database := newRuntimeJobDatabase(t)
			var hits atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = io.WriteString(w, `{"errcode":-1,"errmsg":"busy"}`)
			}))
			defer server.Close()
			tracked := &trackedExecutor{delegate: dingtalk.ExecutorAtForRuntimeTest(t, server, "")}
			definition := dingtalk.Definition(tracked)
			registry, err := jobs.NewRegistry(definition)
			if err != nil {
				t.Fatal(err)
			}
			repository := mustJobRepository(t, database.runtime)
			job := enqueueRuntimeJob(t, database, registry, definition, runtimeTestPayload(), "guard")
			key, operation, payload, hash, _, _, _, _, _ := readRuntimeSnapshot(t, database, job.ID)
			for attempt := 1; attempt < guard.attempts; attempt++ {
				runWorkerUntil(t, dingtalkRuntimeWorker(t, repository, registry, "before-crash"), database, job.ID, jobs.StatusRetryWait, 8*time.Second, attempt)
				_, _, _, _, available, _, _, _, _ := readRuntimeSnapshot(t, database, job.ID)
				waitUntilRuntimeAvailable(t, available)
			}
			dropped := &dropTransitionRepository{Repository: repository, dropped: make(chan jobs.Transition, 1)}
			worker := dingtalkRuntimeWorker(t, dropped, registry, "guard-crash-worker")
			stop := startRuntimeLoop(t, worker.Run)
			defer stop()
			select {
			case <-dropped.dropped:
			case <-time.After(8 * time.Second):
				t.Fatal("unknown Execute did not reach the crash boundary")
			}
			stop()
			_, _, _, _, _, status, attempt, _, _ := readRuntimeSnapshot(t, database, job.ID)
			if status != jobs.StatusRunning || attempt != guard.attempts {
				t.Fatalf("pre-recovery status=%s attempt=%d", status, attempt)
			}
			switch guard.name {
			case "cancel":
				if err := requestRuntimeCancel(t, database.runtime, job.ID); err != nil {
					t.Fatal(err)
				}
			case "deadline":
				setFixtureDeadlinePast(t, database, job.ID)
			}
			expireRuntimeLease(t, database, job.ID)
			reconstructed := mustJobRepository(t, database.runtime)
			runReconcilerUntil(t, dingtalkRuntimeReconciler(t, reconstructed, registry), database, job.ID, jobs.StatusFailed, 8*time.Second)
			gotKey, gotOperation, gotPayload, gotHash, _, _, gotAttempt, _, code := readRuntimeSnapshot(t, database, job.ID)
			if valueOrEmpty(code) != guard.code || gotAttempt != guard.attempts ||
				gotKey != key || gotOperation != operation || string(gotPayload) != string(payload) || string(gotHash) != string(hash) {
				t.Fatalf("recovery guard code=%s attempt=%d or durable identity changed", valueOrEmpty(code), gotAttempt)
			}
			if _, err := reconstructed.ClaimRunnable(context.Background(), jobs.ClaimRequest{Owner: "blocked-replay", Token: uuid.New()}); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("guard allowed a further claim: %v", err)
			}
			if hits.Load() != int32(guard.attempts) || tracked.verifies.Load() != 0 || tracked.rollbacks.Load() != 0 {
				t.Fatal("recovery guard performed extra Execute/Verify/Rollback")
			}
		})
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
