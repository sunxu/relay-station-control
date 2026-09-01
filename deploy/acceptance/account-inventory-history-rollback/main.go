package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	controlauth "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	ownerURLEnvironment       = "CONTROL_HISTORY_ROLLBACK_OWNER_URL"
	runtimeURLEnvironment     = "CONTROL_HISTORY_ROLLBACK_RUNTIME_URL"
	nodeEndpointEnvironment   = "CONTROL_HISTORY_ROLLBACK_NODE_ENDPOINT"
	nodeSecretRefEnvironment  = "CONTROL_HISTORY_ROLLBACK_NODE_SECRET_REFERENCE"
	keyringFileEnvironment    = "CONTROL_HISTORY_ROLLBACK_KEYRING_FILE"
	sessionFileEnvironment    = "CONTROL_HISTORY_ROLLBACK_SESSION_FILE"
	processStartedEnvironment = "CONTROL_HISTORY_ROLLBACK_PROCESS_STARTED_FILE"
	controlURLEnvironment     = "CONTROL_HISTORY_ROLLBACK_CONTROL_URL"
	fixtureProvider           = "openai"
	fixtureNodeType           = "cliproxyapi"
	fixtureContract           = "cliproxyapi.auth-files.v1"
	fixtureEmail              = "history-rollback@example.invalid"
	httpQueryTimeout          = 5 * time.Second
	processPollInterval       = 100 * time.Millisecond
)

var (
	fixtureInstanceID = uuid.MustParse("00000000-0000-4000-8000-000000000931")
	fixturePolicyID   = uuid.MustParse("00000000-0000-4000-8000-000000000932")
	fixtureAdminID    = uuid.MustParse("00000000-0000-4000-8000-000000000933")
	initialPollID     = uuid.MustParse("00000000-0000-4000-8000-000000000934")
	initialFence      = uuid.MustParse("00000000-0000-4000-8000-000000000935")
)

type httpSessionFixture struct {
	Session string `json:"session"`
	CSRF    string `json:"csrf"`
}

type harness struct {
	owner   *pgxpool.Pool
	runtime *pgxpool.Pool
}

type gateError struct{ reason string }

func (err gateError) Error() string { return err.reason }

func databaseGate(reason string, err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		return gateError{reason: reason + "_sqlstate_" + databaseError.Code}
	}
	return gateError{reason: reason}
}

func main() {
	if len(os.Args) != 2 {
		fail("invalid_mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	h, err := openHarness(ctx)
	if err != nil {
		fail("database_unavailable")
	}
	defer h.close()

	switch os.Args[1] {
	case "prepare":
		err = h.prepare(ctx)
	case "wait":
		err = h.waitForProcessPoll(ctx)
	case "http-query":
		err = h.httpQuery(ctx)
	case "verify-baseline":
		err = h.verifyBaseline(ctx)
	case "verify":
		err = h.verifyAfterStop(ctx)
	default:
		fail("invalid_mode")
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fail(os.Args[1] + "_timeout")
		}
		var classified gateError
		if errors.As(err, &classified) {
			fail(os.Args[1] + "_" + classified.reason)
		}
		fail(os.Args[1] + "_failed")
	}
	fmt.Printf("account_inventory_history_rollback_harness=success phase=%s\n", os.Args[1])
}

func fail(reason string) {
	fmt.Fprintf(os.Stderr, "account_inventory_history_rollback_harness=failed reason=%s\n", reason)
	os.Exit(1)
}

func openHarness(ctx context.Context) (*harness, error) {
	ownerURL, runtimeURL := os.Getenv(ownerURLEnvironment), os.Getenv(runtimeURLEnvironment)
	if ownerURL == "" || runtimeURL == "" {
		return nil, errors.New("database URL unavailable")
	}
	owner, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		return nil, err
	}
	runtime, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		owner.Close()
		return nil, err
	}
	return &harness{owner: owner, runtime: runtime}, nil
}

func (h *harness) close() {
	h.runtime.Close()
	h.owner.Close()
}

func (h *harness) prepare(ctx context.Context) error {
	endpoint, secretReference := os.Getenv(nodeEndpointEnvironment), os.Getenv(nodeSecretRefEnvironment)
	if endpoint == "" || secretReference == "" {
		return gateError{reason: "fixture_environment"}
	}
	var targetDay time.Time
	if err := h.owner.QueryRow(ctx, `SELECT date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC')
		- interval '31 days'`).Scan(&targetDay); err != nil {
		return err
	}
	targetSlot := targetDay.Add(12 * time.Hour)
	tx, err := h.owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO environments(singleton_id,environment_id,name,environment_type)
			VALUES(1,'history-forward','History Forward Schema','dev')`, nil},
		{`INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
			VALUES($1,'history_forward','History Forward Operator','enabled',clock_timestamp())`, []any{fixtureAdminID}},
		{`INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
			VALUES($1,$2,'History Forward Driver')`, []any{fixtureNodeType, fixtureContract}},
		{`INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
			VALUES($1,$2,'management_account_inventory_read')`, []any{fixtureNodeType, fixtureContract}},
		{`INSERT INTO relay_node_assets(instance_id,display_name,node_type,
			driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES($1,'History Forward Node',$2,$3,$4,$5)`,
			[]any{fixtureInstanceID, fixtureNodeType, fixtureContract, endpoint, secretReference}},
		{`INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
			VALUES($1,$2,$3,'management_account_inventory_read')`, []any{fixtureInstanceID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,
			driver_contract_version,active_providers,out_of_scope_providers,created_by,created_at)
			VALUES($1,$2,$3,ARRAY['openai'],ARRAY['legacy'],'history-forward',$4)`,
			[]any{fixturePolicyID, fixtureNodeType, fixtureContract, targetDay}},
		{`INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,
			policy_version_id,bound_by,bound_at) VALUES($1,$2,$3,'history-forward',$4)`,
			[]any{fixtureNodeType, fixtureContract, fixturePolicyID, targetDay}},
		{`INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,
			policy_version_id,effective_from,activated_by,created_at)
			VALUES($1,$2,$3,$4,'history-forward',$4)`,
			[]any{fixtureNodeType, fixtureContract, fixturePolicyID, targetDay}},
		{`INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,
			reason,actor,created_at) VALUES($1,$2,'reconciliation','history-forward',$2)`,
			[]any{fixtureInstanceID, targetDay}},
		{`INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,
			driver_contract_version,scheduled_at,provider_policy_version,max_attempts,
			poll_start_grace_seconds,created_at) VALUES($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`,
			[]any{initialPollID, fixtureInstanceID, fixtureNodeType, fixtureContract, targetSlot, fixturePolicyID}},
		{`UPDATE account_inventory_poll_runs SET status='running',attempt_count=1,
			first_started_at=clock_timestamp(),last_started_at=clock_timestamp(),
			lease_expires_at=clock_timestamp()+interval '60 seconds',lease_fencing_token=$2
			WHERE poll_run_id=$1`, []any{initialPollID, initialFence}},
	}
	for index, statement := range statements {
		if _, err := tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return gateError{reason: fmt.Sprintf("fixture_%02d", index+1)}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return gateError{reason: "fixture_commit"}
	}
	if err := h.finalize(ctx, initialPollID, initialFence, 7); err != nil {
		return gateError{reason: "initial_finalize"}
	}

	if _, err := h.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_runs DISABLE TRIGGER USER`); err != nil {
		return gateError{reason: "poll_backdate_disable"}
	}
	if _, err := h.owner.Exec(ctx, `UPDATE account_inventory_poll_runs SET
		created_at=$2::timestamptz,first_started_at=$2::timestamptz+interval '1 second',
		last_started_at=$2::timestamptz+interval '2 seconds',
		observed_at=$2::timestamptz+interval '3 seconds',
		finalized_at=$2::timestamptz+interval '4 seconds' WHERE poll_run_id=$1`, initialPollID, targetSlot); err != nil {
		return databaseGate("poll_backdate_update", err)
	}
	if _, err := h.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_runs ENABLE TRIGGER USER`); err != nil {
		return gateError{reason: "poll_backdate_enable"}
	}
	if _, err := h.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		DISABLE TRIGGER account_inventory_snapshot_items_immutable`); err != nil {
		return gateError{reason: "snapshot_backdate_disable"}
	}
	if _, err := h.owner.Exec(ctx, `UPDATE account_inventory_snapshot_items
		SET observed_at=$2 WHERE poll_run_id=$1`, initialPollID, targetSlot.Add(3*time.Second)); err != nil {
		return gateError{reason: "snapshot_backdate_update"}
	}
	if _, err := h.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		ENABLE TRIGGER account_inventory_snapshot_items_immutable`); err != nil {
		return gateError{reason: "snapshot_backdate_enable"}
	}

	var runID, fence uuid.UUID
	if err := h.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version) VALUES($1::date,$2,$3)
		RETURNING compaction_run_id`, targetDay, fixtureInstanceID, fixturePolicyID).Scan(&runID); err != nil {
		return gateError{reason: "compaction_insert"}
	}
	if err := h.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, uuid.New()).
		Scan(&runID, &fence); err != nil {
		return gateError{reason: "compaction_claim"}
	}
	var checksum string
	if err := h.runtime.QueryRow(ctx, `SELECT
		(public.control_summarize_account_inventory_compaction_v1($1,$2)->>'source_checksum_hex')`,
		runID, fence).Scan(&checksum); err != nil {
		return gateError{reason: "compaction_summarize"}
	}
	var deleted, remaining int
	if err := h.runtime.QueryRow(ctx, `WITH result AS (
		SELECT public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1) AS value)
		SELECT (value->>'deleted_count')::integer,(value->>'remaining_count')::integer FROM result`,
		runID, fence).Scan(&deleted, &remaining); err != nil || deleted != 1 || remaining != 0 {
		return gateError{reason: "snapshot_cleanup"}
	}
	var completed string
	if err := h.runtime.QueryRow(ctx, `SELECT
		public.control_complete_account_inventory_compaction_v1($1,$2,decode($3,'hex'))->>'status'`,
		runID, fence, checksum).Scan(&completed); err != nil || completed != "completed" {
		return gateError{reason: "compaction_complete"}
	}
	var plannedRollups int
	if err := h.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(100)->>'rollup_runs_created')::integer`).
		Scan(&plannedRollups); err != nil || plannedRollups < 1 {
		return gateError{reason: "rollup_plan"}
	}
	var rollupID, rollupFence uuid.UUID
	if err := h.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&rollupID, &rollupFence); err != nil {
		return gateError{reason: "rollup_claim"}
	}
	if err := h.runtime.QueryRow(ctx, `SELECT
		public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'status'`,
		rollupID, rollupFence).Scan(&completed); err != nil || completed != "completed" {
		return gateError{reason: "rollup_complete"}
	}
	var processed int
	if err := h.runtime.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_poll_retention_v1(10)->>'processed_count')::integer`).
		Scan(&processed); err != nil {
		return databaseGate("poll_cleanup", err)
	}
	if processed != 1 {
		return gateError{reason: fmt.Sprintf("poll_cleanup_count_%d", processed)}
	}
	if err := h.verifyRetainedForwardState(ctx); err != nil {
		return gateError{reason: "retained_state"}
	}
	if err := h.seedHTTPSession(ctx); err != nil {
		return gateError{reason: "http_session"}
	}
	fingerprint, err := h.historyFingerprint(ctx)
	if err != nil {
		return gateError{reason: "history_fingerprint"}
	}
	if _, err := h.owner.Exec(ctx, `CREATE TABLE history_rollback_acceptance_baseline(
		singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),fingerprint text NOT NULL,
		poll_count bigint NOT NULL)`); err != nil {
		return gateError{reason: "history_baseline"}
	}
	if _, err := h.owner.Exec(ctx, `INSERT INTO history_rollback_acceptance_baseline(fingerprint,poll_count)
		SELECT $1,count(*) FROM account_inventory_poll_runs WHERE instance_id=$2`,
		fingerprint, fixtureInstanceID); err != nil {
		return gateError{reason: "history_baseline"}
	}
	return nil
}

func (h *harness) waitForProcessPoll(ctx context.Context) error {
	startedAt, err := processStartedAt()
	if err != nil {
		return gateError{reason: "process_started"}
	}
	ticker := time.NewTicker(processPollInterval)
	defer ticker.Stop()
	for {
		complete, err := h.processPollComplete(ctx, startedAt)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *harness) processPollComplete(ctx context.Context, startedAt time.Time) (bool, error) {
	var baselineCount, count int
	if err := h.owner.QueryRow(ctx, `SELECT poll_count FROM history_rollback_acceptance_baseline
		WHERE singleton`).Scan(&baselineCount); err != nil {
		return false, err
	}
	if err := h.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_poll_runs
		WHERE instance_id=$1 AND created_at >= $2`, fixtureInstanceID, startedAt).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	if count != 1 {
		return false, gateError{reason: "process_poll_count"}
	}
	var pollID uuid.UUID
	var status string
	var attempt int
	err := h.owner.QueryRow(ctx, `SELECT poll_run_id,status,attempt_count
		FROM account_inventory_poll_runs
		WHERE instance_id=$1 AND created_at >= $2
		  AND scheduled_at=date_bin(interval '5 minutes',created_at,timestamptz '1970-01-01')`,
		fixtureInstanceID, startedAt).Scan(&pollID, &status, &attempt)
	if errors.Is(err, pgx.ErrNoRows) && count == 1 {
		return false, gateError{reason: "process_poll_slot"}
	}
	if err != nil {
		return false, err
	}
	if status != "finalized" {
		if status == "pending" || status == "running" || status == "retry_wait" {
			return false, nil
		}
		return false, gateError{reason: "process_poll_terminal"}
	}
	var mode, result, reason string
	var source, identifiable int
	if err := h.owner.QueryRow(ctx, `SELECT inventory_mode,result,reason,
		source_record_count,identifiable_record_count FROM account_inventory_poll_runs
		WHERE poll_run_id=$1`, pollID).Scan(&mode, &result, &reason, &source, &identifiable); err != nil {
		return false, err
	}
	if attempt != 1 || mode != "runtime" || result != "success" || reason != "none" || source != 1 || identifiable != 1 {
		return false, gateError{reason: "process_poll_evidence"}
	}
	var totalPolls, providerEvidence, snapshotEvidence, currentPointers int
	if err := h.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id=$1),
		(SELECT count(*) FROM account_inventory_poll_provider_results
		 WHERE poll_run_id=$2 AND provider=$3 AND identifiable_count=1 AND promotion_applied),
		(SELECT count(*) FROM account_inventory_snapshot_items
		 WHERE poll_run_id=$2 AND instance_id=$1 AND provider=$3 AND normalized_email=$4
		   AND success_count=9),
		(SELECT count(*) FROM account_inventory_provider_states
		 WHERE instance_id=$1 AND provider=$3 AND current_poll_run_id=$2)
		+(SELECT count(*) FROM account_inventory
		  WHERE instance_id=$1 AND provider=$3 AND normalized_email=$4 AND current_poll_run_id=$2)`,
		fixtureInstanceID, pollID, fixtureProvider, fixtureEmail).
		Scan(&totalPolls, &providerEvidence, &snapshotEvidence, &currentPointers); err != nil {
		return false, err
	}
	if totalPolls != baselineCount+1 || providerEvidence != 1 || snapshotEvidence != 1 || currentPointers != 2 {
		return false, gateError{reason: "process_poll_promotion"}
	}
	return true, nil
}

func (h *harness) httpQuery(ctx context.Context) error {
	controlURL := strings.TrimRight(os.Getenv(controlURLEnvironment), "/")
	sessionPath := os.Getenv(sessionFileEnvironment)
	encodedSession, err := os.ReadFile(sessionPath)
	if controlURL == "" || sessionPath == "" || err != nil {
		return gateError{reason: "http_configuration"}
	}
	var session httpSessionFixture
	if err := json.Unmarshal(encodedSession, &session); err != nil || session.Session == "" || session.CSRF == "" {
		return gateError{reason: "http_session"}
	}
	body, err := json.Marshal(map[string]any{"instance_id": fixtureInstanceID, "limit": 10})
	if err != nil {
		return err
	}
	requestContext, cancel := context.WithTimeout(ctx, httpQueryTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost,
		controlURL+"/api/account-inventory/query", strings.NewReader(string(body)))
	if err != nil {
		return gateError{reason: "http_request"}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", session.CSRF)
	request.AddCookie(&http.Cookie{Name: controlauth.SessionCookieName, Value: session.Session})
	client := &http.Client{
		Timeout: httpQueryTimeout, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return gateError{reason: "http_transport"}
	}
	defer response.Body.Close()
	encoded, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if readErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		return gateError{reason: "http_response"}
	}
	var result struct {
		Items []struct {
			InstanceID        uuid.UUID `json:"instance_id"`
			Provider          string    `json:"provider"`
			Email             string    `json:"email"`
			BasicStatus       string    `json:"basic_status"`
			Lifecycle         string    `json:"lifecycle"`
			SnapshotFreshness string    `json:"snapshot_freshness"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil || len(result.Items) != 1 || result.NextCursor != nil ||
		result.Items[0].InstanceID != fixtureInstanceID || result.Items[0].Provider != fixtureProvider ||
		result.Items[0].Email != fixtureEmail || result.Items[0].BasicStatus != "reported_active" ||
		result.Items[0].Lifecycle != "present" || result.Items[0].SnapshotFreshness != "fresh" {
		return gateError{reason: "http_query_result"}
	}
	requestID := response.Header.Get("X-Request-ID")
	var audits, matchingAudits int
	if requestID == "" {
		return gateError{reason: "http_request_id"}
	}
	if err := h.owner.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE
		category='account_inventory' AND action='account_inventory.view' AND result='success'
		AND actor_admin_id=$2 AND details->>'result_count'='1')
		FROM audit_logs WHERE request_id=$1`, requestID, fixtureAdminID).
		Scan(&audits, &matchingAudits); err != nil || audits != 1 || matchingAudits != 1 {
		return gateError{reason: "http_query_audit"}
	}
	return nil
}

func (h *harness) verifyAfterStop(ctx context.Context) error {
	if err := h.verifyBaseline(ctx); err != nil {
		return err
	}
	_, err := h.owner.Exec(ctx, `DROP TABLE history_rollback_acceptance_baseline`)
	return err
}

func (h *harness) verifyBaseline(ctx context.Context) error {
	current, err := h.historyFingerprint(ctx)
	if err != nil {
		return err
	}
	var baseline string
	if err := h.owner.QueryRow(ctx, `SELECT fingerprint
		FROM history_rollback_acceptance_baseline WHERE singleton`).Scan(&baseline); err != nil {
		return err
	}
	if baseline != current {
		return gateError{reason: "history_changed"}
	}
	var genericJobs int
	if err := h.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM async_job_kinds)
		+(SELECT count(*) FROM async_jobs)
		+(SELECT count(*) FROM async_job_events)`).Scan(&genericJobs); err != nil {
		return err
	}
	if genericJobs != 0 {
		return gateError{reason: "generic_jobs_created"}
	}
	return nil
}

func (h *harness) seedHTTPSession(ctx context.Context) error {
	keyringPath, sessionPath := os.Getenv(keyringFileEnvironment), os.Getenv(sessionFileEnvironment)
	if keyringPath == "" || sessionPath == "" {
		return errors.New("session configuration unavailable")
	}
	keyring, err := controlauth.LoadKeyringFile(keyringPath, controlauth.EnvironmentDev)
	if err != nil {
		return err
	}
	sessionToken, err := controlauth.GenerateBearerToken()
	if err != nil {
		return err
	}
	csrfToken, err := controlauth.GenerateBearerToken()
	if err != nil {
		return err
	}
	sessionDigest, err := controlauth.ComputeDigest(keyring, controlauth.DomainSessionDigest, sessionToken)
	if err != nil {
		return err
	}
	csrfDigest, err := controlauth.ComputeDigest(keyring, controlauth.DomainCSRFDigest, csrfToken)
	if err != nil {
		return err
	}
	if _, err := h.owner.Exec(ctx, `INSERT INTO control_admin_sessions(
		session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,
		created_at,last_activity_at,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',clock_timestamp(),clock_timestamp(),
		clock_timestamp()+interval '11 hours')`, uuid.New(), fixtureAdminID, sessionDigest.Sum[:],
		csrfDigest.Sum[:], int32(sessionDigest.KeyVersion)); err != nil {
		return err
	}
	encoded, err := json.Marshal(httpSessionFixture{Session: sessionToken, CSRF: csrfToken})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(sessionPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func processStartedAt() (time.Time, error) {
	path := os.Getenv(processStartedEnvironment)
	encoded, err := os.ReadFile(path)
	if path == "" || err != nil {
		return time.Time{}, errors.New("process start unavailable")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(encoded)))
	if err != nil {
		return time.Time{}, err
	}
	_, offset := startedAt.Zone()
	if offset != 0 {
		return time.Time{}, errors.New("process start is not UTC")
	}
	return startedAt.UTC(), nil
}

func (h *harness) finalize(ctx context.Context, pollID, fence uuid.UUID, success uint64) error {
	repository, err := controlstore.NewInventoryPollRepository(h.runtime)
	if err != nil {
		return err
	}
	return repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: pollID, FencingToken: fence,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "v1.0.0", Commit: "abcdef1",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixtureProvider, RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: fixtureProvider, AccountKey: fixtureProvider + ":" + fixtureEmail,
			Email: fixtureEmail, BasicStatus: drivers.AccountStateActive,
			SuccessCount: success,
		}},
	})
}

func (h *harness) verifyRetainedForwardState(ctx context.Context) error {
	var snapshots, polls, nullPointers, summaries, runs int
	if err := h.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states
		 WHERE instance_id=$2 AND current_poll_run_id IS NULL)
		+(SELECT count(*) FROM account_inventory
		  WHERE instance_id=$2 AND current_poll_run_id IS NULL),
		(SELECT count(*) FROM account_inventory_daily_summaries WHERE instance_id=$2)
		+(SELECT count(*) FROM account_inventory_daily_provider_summaries WHERE instance_id=$2),
		(SELECT count(*) FROM account_inventory_compaction_runs
		 WHERE instance_id=$2 AND status='completed')`, initialPollID, fixtureInstanceID).
		Scan(&snapshots, &polls, &nullPointers, &summaries, &runs); err != nil {
		return err
	}
	if snapshots != 0 || polls != 0 || nullPointers != 2 || summaries != 2 || runs != 1 {
		return errors.New("retained forward state invalid")
	}
	return nil
}

func (h *harness) historyFingerprint(ctx context.Context) (string, error) {
	var fingerprint string
	err := h.owner.QueryRow(ctx, `SELECT md5(
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY compaction_run_id)::text
		 FROM account_inventory_compaction_runs AS row_value),'[]') ||
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY rollup_run_id)::text
		 FROM account_inventory_daily_rollup_runs AS row_value),'[]') ||
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY compaction_run_id,account_key)::text
		 FROM account_inventory_daily_summaries AS row_value),'[]') ||
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY compaction_run_id,provider)::text
		 FROM account_inventory_daily_provider_summaries AS row_value),'[]') ||
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY rollup_run_id,account_key)::text
		 FROM account_inventory_daily_account_rollups AS row_value),'[]') ||
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY rollup_run_id,provider)::text
		 FROM account_inventory_daily_provider_rollups AS row_value),'[]') ||
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY summary_date,instance_id)::text
		 FROM account_inventory_history_retired_days AS row_value),'[]') ||
		coalesce((SELECT jsonb_agg(to_jsonb(row_value) ORDER BY occurred_at,audit_id)::text
		 FROM audit_logs AS row_value WHERE category='account_inventory_history'),'[]'))`).Scan(&fingerprint)
	return fingerprint, err
}
