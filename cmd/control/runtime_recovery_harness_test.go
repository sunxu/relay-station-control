package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type runningLeaseSnapshot struct {
	job       dingtalkDeploymentJob
	token     uuid.UUID
	expiresAt time.Time
}

func readRunningLeaseSnapshot(t *testing.T, ctx context.Context, owner *pgxpool.Pool, jobID uuid.UUID) runningLeaseSnapshot {
	t.Helper()
	// This deliberately reads the persisted lease through the owner connection;
	// it never changes job state.
	var snapshot runningLeaseSnapshot
	if err := owner.QueryRow(ctx, `
		SELECT lease_fencing_token, lease_expires_at
		FROM async_jobs WHERE job_id=$1`, jobID).Scan(&snapshot.token, &snapshot.expiresAt); err != nil {
		t.Fatalf("read running lease: %v", err)
	}
	if snapshot.token == uuid.Nil || snapshot.expiresAt.IsZero() {
		t.Fatal("running job has no valid persisted lease/fence")
	}
	return snapshot
}

// TestRuntimeRecoveryPendingRestart proves the smallest real-process recovery
// path. The notification is created by the production lifecycle repository;
// this test does not enqueue or mutate an async_jobs row directly.
func TestRuntimeRecoveryPendingRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	environmentID := "recovery-pending-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	fixture := seedCrossNodeDuplicateOwnershipFixture(t, ctx, owner, environmentID, "recovery-pending")
	configureDingTalkFixturePolicy(t, ctx, owner, fixture)
	const email = "pending-recovery@example.invalid"
	for _, node := range fixture.nodes {
		finalizeDingTalkFixture(t, ctx, owner, runtime, fixture, node, []string{email})
	}

	registry, err := newProductionJobRegistry(dingtalk.Config{})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := assetstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.SetNotificationDelivery(registry, false)
	transition, err := lifecycle.Evaluate(ctx, environmentID, "antigravity:"+email)
	if err != nil || transition == nil || !transition.Created || transition.Status != "ACTIVE" {
		t.Fatalf("production lifecycle did not create ACTIVE occurrence: %v", err)
	}

	job := readDingTalkDeploymentJob(t, ctx, owner, "dingtalk:duplicate:"+transition.OccurrenceID.String()+":active")
	if job.status != "pending" || job.attempt != 0 {
		t.Fatalf("initial job state=%s attempt=%d, want pending/0", job.status, job.attempt)
	}
	initial := job

	server, mode, requests, contractOK := controlledDingTalkTLSServer(t)
	defer server.Close()
	mode.Store(1)
	startMain := newDingTalkMainStarter(t, runtime, environmentID, writeDingTalkControlAuthFiles(t), server)
	process, done, output := startMain(true)
	waitDingTalkHTTPReady(t, process, done, "pending recovery")
	waitDingTalkJobStatus(t, ctx, owner, initial.jobID, "succeeded", 20*time.Second)

	final := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	assertDingTalkStableIdentity(t, initial, final)
	if final.attempt != 1 || requests.Load() != 1 || !contractOK.Load() {
		t.Fatalf("pending recovery status=%s attempt=%d requests=%d contract=%t", final.status, final.attempt, requests.Load(), contractOK.Load())
	}
	if output.String() == "" {
		t.Fatal("production main produced no bounded output")
	}
	stopDingTalkMain(t, process, done)
}

// TestRuntimeRecoveryRetryWaitRestart proves that a retryable/unknown result
// is durable before shutdown and that a new production main resumes the same
// job after the persisted database backoff. No job row or available_at value
// is mutated by the test.
func TestRuntimeRecoveryRetryWaitRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	environmentID := "recovery-retry-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	fixture := seedCrossNodeDuplicateOwnershipFixture(t, ctx, owner, environmentID, "recovery-retry")
	configureDingTalkFixturePolicy(t, ctx, owner, fixture)
	const email = "retry-recovery@example.invalid"
	for _, node := range fixture.nodes {
		finalizeDingTalkFixture(t, ctx, owner, runtime, fixture, node, []string{email})
	}

	registry, err := newProductionJobRegistry(dingtalk.Config{})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := assetstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.SetNotificationDelivery(registry, false)
	transition, err := lifecycle.Evaluate(ctx, environmentID, "antigravity:"+email)
	if err != nil || transition == nil || !transition.Created || transition.Status != "ACTIVE" {
		t.Fatalf("production lifecycle did not create ACTIVE occurrence: %v", err)
	}

	initial := readDingTalkDeploymentJob(t, ctx, owner, "dingtalk:duplicate:"+transition.OccurrenceID.String()+":active")
	if initial.status != "pending" || initial.attempt != 0 {
		t.Fatalf("initial job state=%s attempt=%d, want pending/0", initial.status, initial.attempt)
	}

	server, mode, requests, contractOK := controlledDingTalkTLSServer(t)
	defer server.Close()
	startMain := newDingTalkMainStarter(t, runtime, environmentID, writeDingTalkControlAuthFiles(t), server)
	first, firstDone, firstOutput := startMain(true)
	waitDingTalkHTTPReady(t, first, firstDone, "retry-wait first start")
	waitDingTalkJobStatus(t, ctx, owner, initial.jobID, "retry_wait", 20*time.Second)
	beforeStop := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	if beforeStop.attempt != 1 || beforeStop.availableAt.IsZero() {
		t.Fatalf("retry_wait state=%s attempt=%d available_at=%s", beforeStop.status, beforeStop.attempt, beforeStop.availableAt)
	}
	assertDingTalkStableIdentity(t, initial, beforeStop)
	availableAt := beforeStop.availableAt
	stopDingTalkMain(t, first, firstDone)

	afterStop := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	if afterStop.status != "retry_wait" || afterStop.attempt != 1 || !afterStop.availableAt.Equal(availableAt) {
		t.Fatalf("retry_wait changed across stop: status=%s attempt=%d available_at=%s", afterStop.status, afterStop.attempt, afterStop.availableAt)
	}
	assertDingTalkStableIdentity(t, beforeStop, afterStop)
	if !strings.Contains(firstOutput.String(), `"error_code":"execution_result_unknown"`) {
		t.Fatal("first process did not record the controlled unknown result")
	}

	mode.Store(1)
	second, secondDone, secondOutput := startMain(true)
	waitDingTalkHTTPReady(t, second, secondDone, "retry-wait restart")
	waitDingTalkJobStatus(t, ctx, owner, initial.jobID, "succeeded", 45*time.Second)
	final := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	assertDingTalkStableIdentity(t, initial, final)
	if final.attempt != 2 || requests.Load() != 2 || !contractOK.Load() {
		t.Fatalf("retry restart status=%s attempt=%d requests=%d contract=%t", final.status, final.attempt, requests.Load(), contractOK.Load())
	}
	if secondOutput.String() == "" {
		t.Fatal("restart process produced no bounded output")
	}
	stopDingTalkMain(t, second, secondDone)
}

// TestRuntimeRecoveryRunningLeaseCrash proves recovery from a real process
// crash while the production worker owns a live lease. The endpoint blocks the
// first and recovered executions so both the stale and replacement fence can
// be inspected before either request is allowed to complete.
func TestRuntimeRecoveryRunningLeaseCrash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	environmentID := "recovery-running-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	fixture := seedCrossNodeDuplicateOwnershipFixture(t, ctx, owner, environmentID, "recovery-running")
	configureDingTalkFixturePolicy(t, ctx, owner, fixture)
	const email = "running-recovery@example.invalid"
	for _, node := range fixture.nodes {
		finalizeDingTalkFixture(t, ctx, owner, runtime, fixture, node, []string{email})
	}

	registry, err := newProductionJobRegistry(dingtalk.Config{})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := assetstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.SetNotificationDelivery(registry, false)
	transition, err := lifecycle.Evaluate(ctx, environmentID, "antigravity:"+email)
	if err != nil || transition == nil || !transition.Created || transition.Status != "ACTIVE" {
		t.Fatalf("production lifecycle did not create ACTIVE occurrence: %v", err)
	}
	initial := readDingTalkDeploymentJob(t, ctx, owner, "dingtalk:duplicate:"+transition.OccurrenceID.String()+":active")
	if initial.status != "pending" || initial.attempt != 0 {
		t.Fatalf("initial job state=%s attempt=%d, want pending/0", initial.status, initial.attempt)
	}

	firstHit := make(chan struct{})
	firstRelease := make(chan struct{})
	secondHit := make(chan struct{})
	secondRelease := make(chan struct{})
	var requests atomic.Int32
	var contractOK atomic.Bool
	contractOK.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/" ||
			request.URL.Query().Get("access_token") != "test-access-token" ||
			request.URL.Query().Get("timestamp") == "" || request.URL.Query().Get("sign") == "" ||
			request.Header.Get("Content-Type") != "application/json" {
			contractOK.Store(false)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.Copy(io.Discard, request.Body)
		ordinal := requests.Add(1)
		if ordinal == 1 {
			close(firstHit)
			<-firstRelease
		}
		if ordinal == 2 {
			close(secondHit)
			<-secondRelease
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer server.Close()
	authFiles := writeDingTalkControlAuthFiles(t)
	startMain := newDingTalkMainStarter(t, runtime, environmentID, authFiles, server)
	process, done, _ := startMain(true)
	waitDingTalkHTTPReady(t, process, done, "running lease first start")
	waitForDingTalkRequest(t, firstHit, 20*time.Second)
	waitDingTalkRunningLease(t, ctx, owner, initial.jobID)
	old := readRunningLeaseSnapshot(t, ctx, owner, initial.jobID)
	old.job = readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	if old.job.attempt != 1 || old.job.status != "running" || !old.expiresAt.After(time.Now()) {
		t.Fatalf("old lease state=%s attempt=%d expires=%s", old.job.status, old.job.attempt, old.expiresAt)
	}

	if err := process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := waitDingTalkKilled(done); err != nil {
		t.Fatalf("production main was not killed by SIGKILL: %v", err)
	}
	afterCrash := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	assertDingTalkStableIdentity(t, initial, afterCrash)
	if afterCrash.status != "running" || afterCrash.attempt != 1 {
		t.Fatalf("post-crash state=%s attempt=%d", afterCrash.status, afterCrash.attempt)
	}

	// Let the old HTTP request finish only after PostgreSQL's persisted lease
	// has expired. Its process is already dead, so this response cannot commit.
	waitDingTalkLeaseExpired(t, ctx, owner, initial.jobID, old.expiresAt)
	close(firstRelease)

	second, secondDone, _ := startMain(true)
	waitDingTalkHTTPReady(t, second, secondDone, "running lease recovery")
	waitForDingTalkRequest(t, secondHit, 45*time.Second)
	var recovered runningLeaseSnapshot
	waitDingTalkNewLease(t, ctx, owner, initial.jobID, old.token, &recovered)
	recovered.job = readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	if recovered.job.status != "running" || recovered.token == old.token {
		t.Fatalf("recovered lease status=%s old=%s new=%s", recovered.job.status, old.token, recovered.token)
	}

	jobRepository, err := assetstore.NewJobRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	_, err = jobRepository.TransitionFenced(ctx, jobcore.Transition{
		JobID: recovered.job.jobID, Token: old.token,
		From: []jobcore.Status{jobcore.StatusRunning}, To: jobcore.StatusSucceeded,
		Event: jobcore.EventSucceeded, Actor: jobcore.ActorWorker, ReleaseLease: true,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "lease") {
		t.Fatalf("stale fence transition error=%v, want lost lease", err)
	}

	close(secondRelease)
	waitDingTalkJobStatus(t, ctx, owner, initial.jobID, "succeeded", 45*time.Second)
	final := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	assertDingTalkStableIdentity(t, initial, final)
	if final.attempt < 2 || requests.Load() != 2 || !contractOK.Load() {
		t.Fatalf("running recovery status=%s attempt=%d requests=%d contract=%t", final.status, final.attempt, requests.Load(), contractOK.Load())
	}
	stopDingTalkMain(t, second, secondDone)
}

func waitForDingTalkRequest(t *testing.T, signal <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(timeout):
		t.Fatal("controlled endpoint request did not arrive")
	}
}

func waitDingTalkKilled(done <-chan error) error {
	err := <-done
	if err == nil {
		return fmt.Errorf("process exited cleanly")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("exit=%v", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		return fmt.Errorf("exit=%v", err)
	}
	return nil
}

func waitDingTalkRunningLease(t *testing.T, ctx context.Context, owner *pgxpool.Pool, jobID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		var attempt int
		var token uuid.UUID
		var expires time.Time
		err := owner.QueryRow(ctx, `SELECT status,attempt_count,lease_fencing_token,lease_expires_at FROM async_jobs WHERE job_id=$1`, jobID).Scan(&status, &attempt, &token, &expires)
		if err == nil && status == "running" && attempt == 1 && token != uuid.Nil && expires.After(time.Now()) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not acquire a live running lease")
}

func waitDingTalkLeaseExpired(t *testing.T, ctx context.Context, owner *pgxpool.Pool, jobID uuid.UUID, expiresAt time.Time) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var expired bool
		if err := owner.QueryRow(ctx, `SELECT lease_expires_at <= clock_timestamp() FROM async_jobs WHERE job_id=$1`, jobID).Scan(&expired); err == nil && expired && time.Now().After(expiresAt) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("running lease did not expire according to database time")
}

func waitDingTalkNewLease(t *testing.T, ctx context.Context, owner *pgxpool.Pool, jobID, oldToken uuid.UUID, recovered *runningLeaseSnapshot) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		var attempt int
		var token uuid.UUID
		var expires time.Time
		err := owner.QueryRow(ctx, `SELECT status,attempt_count,lease_fencing_token,lease_expires_at FROM async_jobs WHERE job_id=$1`, jobID).Scan(&status, &attempt, &token, &expires)
		if err == nil && status == "running" && attempt >= 2 && token != uuid.Nil && token != oldToken && expires.After(time.Now()) {
			recovered.token = token
			recovered.expiresAt = expires
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("restarted worker did not acquire a new fenced running lease")
}
