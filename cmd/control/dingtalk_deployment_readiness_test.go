package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/dingtalk"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

const dingtalkDeploymentHelperEnvironment = "CONTROL_DINGTALK_DEPLOYMENT_HELPER"

// TestDingTalkDeploymentReadinessMainHelper is the real main-process entry
// used by TestDingTalkDeploymentReadiness. The fallback roots are installed in
// the child, before production main constructs its immutable HTTP transport.
func TestDingTalkDeploymentReadinessMainHelper(t *testing.T) {
	if os.Getenv(dingtalkDeploymentHelperEnvironment) != "1" {
		return
	}
	der, err := base64.RawStdEncoding.DecodeString(os.Getenv("CONTROL_DINGTALK_TEST_CERT_DER"))
	if err != nil {
		t.Fatal("decode controlled TLS certificate")
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal("parse controlled TLS certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	x509.SetFallbackRoots(roots)
	main()
}

type dingtalkDeploymentJob struct {
	jobID, operationID                                                        uuid.UUID
	idempotencyKey                                                            string
	payload                                                                   []byte
	payloadHash                                                               []byte
	status                                                                    string
	attempt, maxAttempts                                                      int
	replaySafe, allowUnknownEffectReplay, allowDirectSuccess, rollbackAllowed bool
	availableAt                                                               time.Time
	policy                                                                    []byte
}

// TestDingTalkDeploymentReadiness covers only the deployment composition
// boundary: a disabled real main never sends, while an enabled real main
// executes the durable notification job through controlled TLS, persists the
// unknown 503 result as retry_wait, and after SIGTERM/restart completes the
// same job with the same durable identity, payload, hash, and policy.
func TestDingTalkDeploymentReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	environmentID := "dingtalk-readiness-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	fixture := seedCrossNodeDuplicateOwnershipFixture(t, ctx, owner, environmentID, "dingtalk-readiness")
	configureDingTalkFixturePolicy(t, ctx, owner, fixture)
	const disabledEmail = "disabled@example.invalid"
	const enabledEmail = "enabled@example.invalid"
	for _, node := range fixture.nodes {
		finalizeDingTalkFixture(t, ctx, owner, runtime, fixture, node, []string{disabledEmail})
	}
	lifecycle, err := assetstore.NewCrossNodeDuplicateOwnershipLifecycleRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := lifecycle.Evaluate(ctx, environmentID, "antigravity:"+disabledEmail)
	if err != nil || disabled == nil || !disabled.Created {
		t.Fatal("disabled lifecycle transition did not commit")
	}
	assertDingTalkIntentCounts(t, ctx, owner, 0, 0, 0)

	server, mode, requests, contractOK := controlledDingTalkTLSServer(t)
	defer server.Close()
	startMain := newDingTalkMainStarter(t, runtime, environmentID, writeDingTalkControlAuthFiles(t), server)
	disabledProcess, disabledDone, disabledOutput := startMain(false)
	waitDingTalkHTTPReady(t, disabledProcess, disabledDone, "disabled")
	assertDingTalkMetrics(t, disabledProcess, disabledDone, "pending", 0)
	assertDingTalkIntentCounts(t, ctx, owner, 0, 0, 0)
	if requests.Load() != 0 || !strings.Contains(disabledOutput.String(), "control starting") {
		t.Fatal("disabled startup/request contract failed")
	}
	stopDingTalkMain(t, disabledProcess, disabledDone)

	registry, err := newProductionJobRegistry(dingtalk.Config{})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.SetNotificationDelivery(registry, false)
	unchanged, err := lifecycle.Evaluate(ctx, environmentID, "antigravity:"+disabledEmail)
	if err != nil || unchanged == nil || unchanged.Created || unchanged.Status != "ACTIVE" {
		t.Fatal("enable changed existing occurrence")
	}
	assertDingTalkIntentCounts(t, ctx, owner, 0, 0, 0)
	for _, node := range fixture.nodes {
		finalizeDingTalkFixture(t, ctx, owner, runtime, fixture, node, []string{disabledEmail, enabledEmail})
	}
	enabled, err := lifecycle.Evaluate(ctx, environmentID, "antigravity:"+enabledEmail)
	if err != nil || enabled == nil || !enabled.Created {
		t.Fatalf("enabled lifecycle transition failed: %v", err)
	}
	initial := readDingTalkDeploymentJob(t, ctx, owner, "dingtalk:duplicate:"+enabled.OccurrenceID.String()+":active")
	if initial.status != "pending" || initial.attempt != 0 {
		t.Fatal("new intent is not pending at attempt zero")
	}
	assertDingTalkPolicy(t, initial)
	assertDingTalkIntentCounts(t, ctx, owner, 1, 1, 1)

	first, firstDone, firstOutput := startMain(true)
	waitDingTalkHTTPReady(t, first, firstDone, "enabled first start")
	waitDingTalkJobStatus(t, ctx, owner, initial.jobID, "retry_wait", 15*time.Second)
	retryWait := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	// Stop immediately once durable retry_wait is visible, before diagnostics
	// could consume the production backoff and permit a second claim.
	stopDingTalkMain(t, first, firstDone)
	assertDingTalkStableIdentity(t, initial, retryWait)
	stopped := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	assertDingTalkStableIdentity(t, retryWait, stopped)
	if stopped.status != "retry_wait" || stopped.attempt != 1 || !stopped.availableAt.Equal(retryWait.availableAt) {
		t.Fatalf("SIGTERM retry state: status=%s attempt=%d", stopped.status, stopped.attempt)
	}
	if !strings.Contains(firstOutput.String(), `"job_kind":"dingtalk_alert_delivery"`) ||
		!strings.Contains(firstOutput.String(), `"error_code":"execution_result_unknown"`) {
		t.Fatal("retry log missing safe classification")
	}
	t.Logf("persisted retry_wait: job=%s operation=%s payload_hash=%x attempt=%d available_at=%s",
		initial.jobID, initial.operationID, initial.payloadHash, stopped.attempt, stopped.availableAt.UTC().Format(time.RFC3339Nano))

	mode.Store(1)
	second, secondDone, secondOutput := startMain(true)
	waitDingTalkHTTPReady(t, second, secondDone, "enabled restart")
	waitDingTalkJobStatus(t, ctx, owner, initial.jobID, "succeeded", 20*time.Second)
	final := readDingTalkDeploymentJob(t, ctx, owner, initial.jobID)
	assertDingTalkStableIdentity(t, initial, final)
	if final.attempt != 2 || requests.Load() != 2 || !contractOK.Load() {
		t.Fatalf("restart delivery: attempt=%d controlled_hits=%d contract=%t", final.attempt, requests.Load(), contractOK.Load())
	}
	assertDingTalkMetrics(t, second, secondDone, "succeeded", 1)
	if !strings.Contains(secondOutput.String(), `"job_kind":"dingtalk_alert_delivery"`) {
		t.Fatal("restart jobs log absent")
	}
	stopDingTalkMain(t, second, secondDone)
	for _, output := range []*deploymentTestBuffer{disabledOutput, firstOutput, secondOutput} {
		for _, material := range []string{server.URL, "test-access-token", "test-signing-secret"} {
			if strings.Contains(output.String(), material) {
				t.Fatal("controlled transport material leaked to child logs")
			}
		}
	}
	t.Logf("real main restart: status=%s attempt=%d controlled_target_hits=%d stable_identity=true", final.status, final.attempt, requests.Load())
}

func assertDingTalkIntentCounts(t *testing.T, ctx context.Context, owner *pgxpool.Pool, jobs, events, outbox int) {
	t.Helper()
	var gotJobs, gotEvents, gotOutbox int
	if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM async_jobs), (SELECT count(*) FROM async_job_events), (SELECT count(*) FROM operation_outbox)`).Scan(&gotJobs, &gotEvents, &gotOutbox); err != nil {
		t.Fatal(err)
	}
	if gotJobs != jobs || gotEvents != events || gotOutbox != outbox {
		t.Fatalf("intent counts=%d/%d/%d want=%d/%d/%d", gotJobs, gotEvents, gotOutbox, jobs, events, outbox)
	}
}

func configureDingTalkFixturePolicy(t *testing.T, ctx context.Context, owner *pgxpool.Pool, fixture *crossNodeDuplicateOwnershipFixture) {
	t.Helper()
	var activationID uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy($1,$2,$3,$4,$5)`,
		fixture.nodeType, fixture.contract, []string{"antigravity"}, []string{"legacy"}, "integration-test").Scan(&activationID); err != nil {
		t.Fatal(err)
	}
	if activationID == uuid.Nil {
		t.Fatal("DingTalk fixture provider-policy activation was empty")
	}
}

// finalizeDingTalkFixture is the existing production finalize contract with
// the Provider required by enqueueDuplicateNotificationTx. The shared cmd
// fixture intentionally defaults to OpenAI for its wiring tests; this local
// test-only variant keeps that helper unchanged while using the approved
// Antigravity notification scope.
func finalizeDingTalkFixture(
	t *testing.T, ctx context.Context, owner, runtime *pgxpool.Pool,
	fixture *crossNodeDuplicateOwnershipFixture, instanceID uuid.UUID, emails []string,
) {
	t.Helper()
	pollID, fence := uuid.New(), uuid.New()
	var policyID uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2`, fixture.nodeType, fixture.contract).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	scheduledAt := fixture.baseSlot.Add(time.Duration(fixture.nextPoll[instanceID]) * 5 * time.Minute)
	fixture.nextPoll[instanceID]++
	if _, err := owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`,
		pollID, instanceID, fixture.nodeType, fixture.contract, scheduledAt, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, pollID, fence); err != nil {
		t.Fatal(err)
	}
	providerJSON, err := json.Marshal([]map[string]any{{
		"provider": "antigravity", "identifiable_count": len(emails),
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make([]map[string]any, 0, len(emails))
	for _, email := range emails {
		snapshot = append(snapshot, map[string]any{
			"provider": "antigravity", "account_key": "antigravity:" + email,
			"email": email, "basic_status": "active",
			"success_count": int64(1), "failed_count": int64(0),
			"recent_request_count": int64(0), "last_refresh_unix": nil,
			"next_retry_unix": nil, "updated_at_unix": nil,
		})
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var finalized int
	if err := runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			$3,$3,0,0,0,'v1.0.0','abcdef1',$4::jsonb,$5::jsonb,'[]'::jsonb
		)`, pollID, fence, len(emails), providerJSON, snapshotJSON).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("DingTalk fixture finalized rows = %d, want 1", finalized)
	}
}

func controlledDingTalkTLSServer(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	var mode atomic.Int32
	var requests atomic.Int32
	var contractOK atomic.Bool
	contractOK.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		query := request.URL.Query()
		if request.Method != http.MethodPost || request.URL.Path != "/" ||
			query.Get("access_token") != "test-access-token" || query.Get("timestamp") == "" || query.Get("sign") == "" ||
			request.Header.Get("Content-Type") != "application/json" {
			contractOK.Store(false)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if mode.Load() == 0 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"errcode":0,"errmsg":"ok"}`)
	}))
	return server, &mode, &requests, &contractOK
}

type dingtalkControlAuthFiles struct {
	keyring, bootstrap string
}

func writeDingTalkControlAuthFiles(t *testing.T) dingtalkControlAuthFiles {
	t.Helper()
	directory := t.TempDir()
	keyringPath := filepath.Join(directory, "keyring.json")
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	keyring := `{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"` + key + `"}]}`
	if err := os.WriteFile(keyringPath, []byte(keyring), 0600); err != nil {
		t.Fatal(err)
	}
	bootstrapPath := filepath.Join(directory, "bootstrap.secret")
	if err := os.WriteFile(bootstrapPath, []byte(strings.Repeat("b", 48)), 0600); err != nil {
		t.Fatal(err)
	}
	return dingtalkControlAuthFiles{keyring: keyringPath, bootstrap: bootstrapPath}
}

func newDingTalkMainStarter(
	t *testing.T, runtime *pgxpool.Pool, environmentID string, authFiles dingtalkControlAuthFiles,
	server *httptest.Server,
) func(bool) (*exec.Cmd, chan error, *deploymentTestBuffer) {
	t.Helper()
	certDER := base64.RawStdEncoding.EncodeToString(server.Certificate().Raw)
	return func(enabled bool) (*exec.Cmd, chan error, *deploymentTestBuffer) {
		port, err := reserveLocalPort()
		if err != nil {
			t.Fatal(err)
		}
		env := []string{
			"PATH=" + os.Getenv("PATH"),
			dingtalkDeploymentHelperEnvironment + "=1",
			"GODEBUG=x509usefallbackroots=1",
			"CONTROL_DINGTALK_TEST_CERT_DER=" + certDER,
			"DATABASE_URL=" + runtime.Config().ConnString(),
			"CONTROL_ENVIRONMENT_ID=" + environmentID,
			"CONTROL_ENVIRONMENT=dev",
			"CONTROL_AUTH_KEYRING_FILE=" + authFiles.keyring,
			"CONTROL_BOOTSTRAP_SECRET_FILE=" + authFiles.bootstrap,
			"CONTROL_MFA_REQUIRED=false",
			"CONTROL_HTTP_ADDR=127.0.0.1:" + port,
			"CONTROL_GATEWAY_DIRECTORY_ENABLED=false",
			"CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=false",
			"CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED=false",
			"CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED=false",
			"CONTROL_CLIPROXYAPI_DRIVER_ENABLED=false",
			"CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED=false",
			"CONTROL_JOB_WORKER_CONCURRENCY=1",
			"CONTROL_JOB_RECONCILER_CONCURRENCY=1",
			"CONTROL_JOB_POLL_INTERVAL=1s",
			"CONTROL_JOB_RECONCILE_INTERVAL=1s",
			"CONTROL_JOB_DATABASE_BACKOFF=1s",
			"CONTROL_JOB_SHUTDOWN_GRACE=3s",
		}
		if enabled {
			env = append(env,
				"DINGTALK_WEBHOOK_URL="+server.URL+"/?access_token=test-access-token",
				"DINGTALK_SIGNING_SECRET=test-signing-secret",
			)
		}
		command := exec.Command(os.Args[0], "-test.run=^TestDingTalkDeploymentReadinessMainHelper$")
		command.Env = env
		output := &deploymentTestBuffer{}
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait(); close(done) }()
		t.Cleanup(func() {
			_ = command.Process.Kill()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("child not reaped")
			}
		})
		return command, done, output
	}
}

func waitDingTalkHTTPReady(t *testing.T, command *exec.Cmd, done <-chan error, label string) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	pollDingTalk(t, 15*time.Second, func() (bool, error) {
		response, err := client.Get("http://" + commandHTTPAddress(command) + "/api/healthz")
		if err != nil {
			return false, nil
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, nil
	}, label+" health")
}

// commandHTTPAddress is populated by the child-independent starter through
// the process environment; keeping the address in the command makes the
// readiness helper usable without introducing a production hook.
func commandHTTPAddress(command *exec.Cmd) string {
	for _, value := range command.Env {
		if strings.HasPrefix(value, "CONTROL_HTTP_ADDR=") {
			return strings.TrimPrefix(value, "CONTROL_HTTP_ADDR=")
		}
	}
	return ""
}

func assertDingTalkMetrics(t *testing.T, command *exec.Cmd, done <-chan error, status string, count int) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	pollDingTalk(t, 5*time.Second, func() (bool, error) {
		response, err := client.Get("http://" + commandHTTPAddress(command) + "/metrics")
		if err != nil {
			return false, nil
		}
		defer response.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if readErr != nil || response.StatusCode != http.StatusOK {
			return false, readErr
		}
		return strings.Contains(string(body), fmt.Sprintf(`relay_control_async_jobs{status="%s"} %d`, status, count)), nil
	}, "metrics "+status)
}

func stopDingTalkMain(t *testing.T, command *exec.Cmd, done <-chan error) {
	t.Helper()
	if err := command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DingTalk main shutdown failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DingTalk main shutdown exceeded bound")
	}
}

func waitDingTalkJobStatus(t *testing.T, ctx context.Context, owner *pgxpool.Pool, jobID uuid.UUID, status string, timeout time.Duration) {
	t.Helper()
	pollDingTalk(t, timeout, func() (bool, error) {
		job := readDingTalkDeploymentJob(t, ctx, owner, jobID)
		return job.status == status, nil
	}, "job "+status)
}

func pollDingTalk(t *testing.T, timeout time.Duration, check func() (bool, error), label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ok, err := check()
		if err != nil {
			lastErr = err
		} else if ok {
			return
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				t.Fatalf("bounded poll %s failed: %v", label, lastErr)
			}
			t.Fatalf("bounded poll %s timed out", label)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		<-timer.C
	}
}

func readDingTalkDeploymentJob(t *testing.T, ctx context.Context, owner *pgxpool.Pool, jobIDOrKey any) dingtalkDeploymentJob {
	t.Helper()
	var row dingtalkDeploymentJob
	var err error
	query := `SELECT job_id,operation_id,idempotency_key,payload,payload_hash,status,
		attempt_count,max_attempts,replay_safe,allow_unknown_effect_replay,
		allow_direct_success,rollback_allowed,available_at,
 jsonb_build_object('kind',job_kind,'schema',payload_schema_version,'priority',priority,
 'timeout',timeout_seconds,'lease',lease_seconds,'heartbeat',heartbeat_interval_seconds,
 'attempts',max_attempts,'verify_attempts',max_verification_attempts,
 'replay_safe',replay_safe,'unknown',allow_unknown_effect_replay,
 'direct',allow_direct_success,'rollback',rollback_allowed)
 FROM async_jobs WHERE job_id=$1`
	if key, ok := jobIDOrKey.(string); ok {
		query = strings.Replace(query, "WHERE job_id=$1", "WHERE idempotency_key=$1", 1)
		err = owner.QueryRow(ctx, query, key).Scan(
			&row.jobID, &row.operationID, &row.idempotencyKey, &row.payload, &row.payloadHash, &row.status,
			&row.attempt, &row.maxAttempts, &row.replaySafe, &row.allowUnknownEffectReplay,
			&row.allowDirectSuccess, &row.rollbackAllowed, &row.availableAt, &row.policy,
		)
	} else {
		err = owner.QueryRow(ctx, query, jobIDOrKey).Scan(
			&row.jobID, &row.operationID, &row.idempotencyKey, &row.payload, &row.payloadHash, &row.status,
			&row.attempt, &row.maxAttempts, &row.replaySafe, &row.allowUnknownEffectReplay,
			&row.allowDirectSuccess, &row.rollbackAllowed, &row.availableAt, &row.policy,
		)
	}
	if err != nil {
		t.Fatalf("read DingTalk job state: %v", err)
	}
	return row
}

func assertDingTalkPolicy(t *testing.T, job dingtalkDeploymentJob) {
	t.Helper()
	if job.maxAttempts != 5 || !job.replaySafe || !job.allowUnknownEffectReplay || !job.allowDirectSuccess || job.rollbackAllowed {
		t.Fatal("DingTalk persisted policy mismatch")
	}
	var got, want map[string]any
	if err := json.Unmarshal(job.policy, &got); err != nil {
		t.Fatal("invalid persisted policy snapshot")
	}
	if err := json.Unmarshal([]byte(`{"kind":"dingtalk_alert_delivery","schema":1,"priority":50,"timeout":10,"lease":30,"heartbeat":5,"attempts":5,"verify_attempts":1,"replay_safe":true,"unknown":true,"direct":true,"rollback":false}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("complete persisted DingTalk execution policy mismatch")
	}
}

func assertDingTalkStableIdentity(t *testing.T, before, after dingtalkDeploymentJob) {
	t.Helper()
	if before.jobID != after.jobID || before.operationID != after.operationID || before.idempotencyKey != after.idempotencyKey ||
		!bytes.Equal(before.payload, after.payload) || !bytes.Equal(before.payloadHash, after.payloadHash) ||
		before.maxAttempts != after.maxAttempts || before.replaySafe != after.replaySafe ||
		before.allowUnknownEffectReplay != after.allowUnknownEffectReplay || before.allowDirectSuccess != after.allowDirectSuccess ||
		before.rollbackAllowed != after.rollbackAllowed || !bytes.Equal(before.policy, after.policy) {
		t.Fatal("DingTalk job identity/payload/hash/policy changed")
	}
}
