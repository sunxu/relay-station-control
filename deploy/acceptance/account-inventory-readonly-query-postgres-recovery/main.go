package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	controlauth "github.com/sunxu/relay-station-control/internal/auth"
	productstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	ownerURLEnvironment    = "CONTROL_READONLY_QUERY_RECOVERY_OWNER_URL"
	runtimeURLEnvironment  = "CONTROL_READONLY_QUERY_RECOVERY_RUNTIME_URL"
	readyFileEnvironment   = "CONTROL_READONLY_QUERY_RECOVERY_READY_FILE"
	goFileEnvironment      = "CONTROL_READONLY_QUERY_RECOVERY_GO_FILE"
	controlURLEnvironment  = "CONTROL_READONLY_QUERY_RECOVERY_CONTROL_URL"
	keyringFileEnvironment = "CONTROL_READONLY_QUERY_RECOVERY_KEYRING_FILE"
	sessionFileEnvironment = "CONTROL_READONLY_QUERY_RECOVERY_SESSION_FILE"

	fixtureNodeType = "readonly-query-recovery"
	fixtureContract = "v1"
	fixtureProvider = "openai"
	fixtureEmail    = "readonly-query-recovery@example.invalid"
)

var (
	errInvalidConfiguration = errors.New("invalid_configuration")
	errAcceptanceInvariant  = errors.New("acceptance_invariant")

	fixtureInstanceID = uuid.MustParse("20000000-0000-4000-8000-000000000001")
	fixturePolicyID   = uuid.MustParse("20000000-0000-4000-8000-000000000002")
	fixturePollID     = uuid.MustParse("20000000-0000-4000-8000-000000000003")
	fixtureFence      = uuid.MustParse("20000000-0000-4000-8000-000000000004")
	fixtureActorID    = uuid.MustParse("20000000-0000-4000-8000-000000000005")
)

func main() {
	os.Exit(runMain())
}

func runMain() (exitCode int) {
	exitCode = 1
	phase := "invalid"
	defer func() {
		if recover() != nil {
			fmt.Fprintf(os.Stderr, "account_inventory_readonly_query_recovery=failed phase=%s reason=acceptance_invariant\n", phase)
			exitCode = 1
		}
	}()
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "account_inventory_readonly_query_recovery=failed phase=invalid reason=invalid_configuration")
		return 2
	}
	phase = os.Args[1]
	var err error
	switch phase {
	case "prepare":
		err = runPrepare()
	case "outage-window":
		err = runOutageWindow()
	case "wait":
		err = runWait()
	case "restart-verify":
		err = runRestartVerify()
	case "statement-timeout":
		err = runStatementTimeout()
	case "connection-exhaustion":
		err = runConnectionExhaustion()
	case "audit-commit-failure":
		err = runAuditCommitFailure()
	case "control-health":
		err = runControlHealth()
	case "rotate-keyring":
		err = runRotateKeyring()
	case "http-restart-query":
		err = runHTTPRestartQuery()
	case "http-inflight-stop":
		err = runHTTPInflightStop()
	default:
		fmt.Fprintln(os.Stderr, "account_inventory_readonly_query_recovery=failed phase=invalid reason=invalid_configuration")
		return 2
	}
	if err != nil {
		reason := "acceptance_invariant"
		if errors.Is(err, errInvalidConfiguration) {
			reason = "invalid_configuration"
		}
		fmt.Fprintf(os.Stderr, "account_inventory_readonly_query_recovery=failed phase=%s reason=%s\n", phase, reason)
		return 1
	}
	return 0
}

func runControlHealth() error {
	controlURL := os.Getenv(controlURLEnvironment)
	if controlURL == "" {
		return errInvalidConfiguration
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, controlURL+"/api/healthz", nil)
	if err != nil {
		return errInvalidConfiguration
	}
	response, err := (&http.Client{Timeout: 500 * time.Millisecond}).Do(request)
	if err != nil {
		return errAcceptanceInvariant
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil || response.StatusCode != http.StatusOK {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=control-health status=ready")
	return nil
}

type httpSessionFixture struct {
	Session string `json:"session"`
	CSRF    string `json:"csrf"`
}

type recoveryKeyringDocument struct {
	FormatVersion int                          `json:"format_version"`
	Environment   string                       `json:"environment"`
	Current       int                          `json:"current"`
	Keys          []recoveryKeyringDocumentKey `json:"keys"`
}

type recoveryKeyringDocumentKey struct {
	Version int    `json:"version"`
	Key     string `json:"key"`
}

func runRotateKeyring() error {
	path := os.Getenv(keyringFileEnvironment)
	encoded, err := os.ReadFile(path)
	if path == "" || err != nil {
		return errInvalidConfiguration
	}
	var document recoveryKeyringDocument
	if err := json.Unmarshal(encoded, &document); err != nil || document.FormatVersion != 1 ||
		document.Environment != "dev" || document.Current != 1 || len(document.Keys) != 1 || document.Keys[0].Version != 1 {
		return errAcceptanceInvariant
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return errAcceptanceInvariant
	}
	document.Current = 2
	document.Keys = append(document.Keys, recoveryKeyringDocumentKey{
		Version: 2, Key: base64.RawStdEncoding.EncodeToString(key),
	})
	encoded, err = json.Marshal(document)
	if err != nil {
		return errAcceptanceInvariant
	}
	temporary := path + ".next"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return errAcceptanceInvariant
	}
	defer func() { _ = os.Remove(temporary) }()
	if err := os.Rename(temporary, path); err != nil {
		return errAcceptanceInvariant
	}
	if _, err := controlauth.LoadKeyringFile(path, controlauth.EnvironmentDev); err != nil {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=rotate-keyring previous_key=retained current_key_version=2")
	return nil
}

func runHTTPRestartQuery() error {
	controlURL := strings.TrimRight(os.Getenv(controlURLEnvironment), "/")
	sessionPath := os.Getenv(sessionFileEnvironment)
	encodedSession, err := os.ReadFile(sessionPath)
	if controlURL == "" || sessionPath == "" || err != nil {
		return errInvalidConfiguration
	}
	var session httpSessionFixture
	if err := json.Unmarshal(encodedSession, &session); err != nil || session.Session == "" || session.CSRF == "" {
		return errAcceptanceInvariant
	}
	body, err := json.Marshal(map[string]any{"instance_id": fixtureInstanceID, "limit": 10})
	if err != nil {
		return errAcceptanceInvariant
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL+"/api/account-inventory/query", strings.NewReader(string(body)))
	if err != nil {
		return errInvalidConfiguration
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", controlURL)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	request.AddCookie(&http.Cookie{Name: controlauth.DevSessionCookieName, Value: session.Session})
	client := &http.Client{
		Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return errAcceptanceInvariant
	}
	defer response.Body.Close()
	encoded, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if readErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		return errAcceptanceInvariant
	}
	var result struct {
		Items []struct {
			InstanceID  uuid.UUID `json:"instance_id"`
			Provider    string    `json:"provider"`
			Email       string    `json:"email"`
			BasicStatus string    `json:"basic_status"`
			Lifecycle   string    `json:"lifecycle"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil || len(result.Items) != 1 || result.NextCursor != nil ||
		result.Items[0].InstanceID != fixtureInstanceID || result.Items[0].Provider != fixtureProvider ||
		result.Items[0].Email != fixtureEmail || result.Items[0].BasicStatus != "reported_active" ||
		result.Items[0].Lifecycle != "present" {
		return errAcceptanceInvariant
	}
	requestID := response.Header.Get("X-Request-ID")
	owner, err := openPool(ctx, ownerURLEnvironment, 1, 2*time.Second, 0)
	if err != nil {
		return errAcceptanceInvariant
	}
	defer owner.Close()
	if requestID == "" {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, requestID); err != nil || count != 1 {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=http-restart-query status=200 cache=no-store current_items=1 audit_commits=1 rotated_session_accepted=1")
	return nil
}

func runHTTPInflightStop() error {
	controlURL := strings.TrimRight(os.Getenv(controlURLEnvironment), "/")
	sessionPath := os.Getenv(sessionFileEnvironment)
	readyFile, goFile := os.Getenv(readyFileEnvironment), os.Getenv(goFileEnvironment)
	encodedSession, err := os.ReadFile(sessionPath)
	if controlURL == "" || sessionPath == "" || readyFile == "" || goFile == "" || err != nil {
		return errInvalidConfiguration
	}
	var session httpSessionFixture
	if err := json.Unmarshal(encodedSession, &session); err != nil || session.Session == "" || session.CSRF == "" {
		return errAcceptanceInvariant
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return errAcceptanceInvariant
	}
	defer owner.Close()
	var auditsBefore int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE category='account_inventory' AND action='account_inventory.view'`).Scan(&auditsBefore); err != nil {
		return errAcceptanceInvariant
	}
	lock, err := owner.Begin(ctx)
	if err != nil {
		return errAcceptanceInvariant
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(ctx, `LOCK TABLE account_inventory IN ACCESS EXCLUSIVE MODE`); err != nil {
		return errAcceptanceInvariant
	}
	body, err := json.Marshal(map[string]any{"instance_id": fixtureInstanceID, "limit": 10})
	if err != nil {
		return errAcceptanceInvariant
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		controlURL+"/api/account-inventory/query", strings.NewReader(string(body)))
	if err != nil {
		return errInvalidConfiguration
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", controlURL)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	request.AddCookie(&http.Cookie{Name: controlauth.DevSessionCookieName, Value: session.Session})
	client := &http.Client{
		Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	requestResult := make(chan bool, 1)
	go func() {
		response, requestErr := client.Do(request)
		if response != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
		}
		requestResult <- requestErr != nil && response == nil
	}()
	waitContext, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for {
		var waiting bool
		err := owner.QueryRow(waitContext, `SELECT EXISTS(
			SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock'
			AND query LIKE '%control_query_current_account_inventory_v1%'
		)`).Scan(&waiting)
		if err != nil {
			return errAcceptanceInvariant
		}
		if waiting {
			break
		}
		select {
		case <-waitContext.Done():
			return errAcceptanceInvariant
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
		return errInvalidConfiguration
	}
	if err := waitForSignal(ctx, goFile); err != nil {
		return err
	}
	if err := lock.Rollback(ctx); err != nil {
		return errAcceptanceInvariant
	}
	if !<-requestResult {
		return errAcceptanceInvariant
	}
	var auditsAfter int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE category='account_inventory' AND action='account_inventory.view'`).Scan(&auditsAfter); err != nil || auditsAfter != auditsBefore {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=http-inflight-stop response_delivered=0 audit_commits=0")
	return nil
}

func runPrepare() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	runtime, err := openPool(ctx, runtimeURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := seedFixture(ctx, owner, runtime); err != nil {
		return errAcceptanceInvariant
	}
	page, err := queryAndAudit(ctx, runtime, "readonly-query-recovery-prepare")
	if err != nil || !isCurrentTruth(page) {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, "readonly-query-recovery-prepare"); err != nil || count != 1 {
		return errAcceptanceInvariant
	}
	if err := seedHTTPSession(ctx, owner); err != nil {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=prepare current_items=1 audit_commits=1")
	return nil
}

func runOutageWindow() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := openPool(ctx, runtimeURLEnvironment, 2, 250*time.Millisecond, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	repository, err := productstore.NewAccountInventoryRepository(pool)
	if err != nil || repository.CheckCompatibility(ctx) != nil {
		return errAcceptanceInvariant
	}
	readyFile, goFile := os.Getenv(readyFileEnvironment), os.Getenv(goFileEnvironment)
	if readyFile == "" || goFile == "" || os.WriteFile(readyFile, []byte("ready\n"), 0o600) != nil {
		return errInvalidConfiguration
	}
	if err := waitForSignal(ctx, goFile); err != nil {
		return err
	}

	queryDone := make(chan bool, 1)
	go func() {
		queryContext, queryCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer queryCancel()
		page, queryErr := repository.QueryPageAndAudit(queryContext, fixtureQuery(), fixtureAudit("readonly-query-recovery-outage"))
		queryDone <- queryErr != nil && isZeroPage(page)
	}()
	if !<-queryDone {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=outage-window management_query_fail_closed=1")
	return nil
}

func runWait() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := openPool(ctx, ownerURLEnvironment, 1, 250*time.Millisecond, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		pingContext, pingCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := pool.Ping(pingContext)
		pingCancel()
		if err == nil {
			var serverVersion int
			if err := pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil || serverVersion < 180000 || serverVersion >= 190000 {
				return errAcceptanceInvariant
			}
			fmt.Println("account_inventory_readonly_query_recovery=success phase=wait server_major=18")
			return nil
		}
		select {
		case <-ctx.Done():
			return errAcceptanceInvariant
		case <-ticker.C:
		}
	}
}

func runRestartVerify() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 1, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	runtime, err := openPool(ctx, runtimeURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer runtime.Close()
	page, err := queryAndAudit(ctx, runtime, "readonly-query-recovery-restart")
	if err != nil || !isCurrentTruth(page) {
		return errAcceptanceInvariant
	}
	var currentRows int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory WHERE instance_id=$1`, fixtureInstanceID).Scan(&currentRows); err != nil || currentRows != 1 {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, "readonly-query-recovery-restart"); err != nil || count != 1 {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=restart-verify recovered_current_items=1 audit_commits=1")
	return nil
}

func runStatementTimeout() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	faultPool, err := openPool(ctx, runtimeURLEnvironment, 1, 2*time.Second, 100*time.Millisecond)
	if err != nil {
		return err
	}
	defer faultPool.Close()
	repository, err := productstore.NewAccountInventoryRepository(faultPool)
	if err != nil || repository.CheckCompatibility(ctx) != nil {
		return errAcceptanceInvariant
	}
	lock, err := owner.Begin(ctx)
	if err != nil {
		return errAcceptanceInvariant
	}
	if _, err := lock.Exec(ctx, `LOCK TABLE account_inventory IN ACCESS EXCLUSIVE MODE`); err != nil {
		_ = lock.Rollback(ctx)
		return errAcceptanceInvariant
	}
	requestID := "readonly-query-recovery-statement-timeout"
	page, queryErr := repository.QueryPageAndAudit(ctx, fixtureQuery(), fixtureAudit(requestID))
	if queryErr == nil || !isZeroPage(page) {
		_ = lock.Rollback(ctx)
		return errAcceptanceInvariant
	}
	if err := lock.Rollback(ctx); err != nil {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, requestID); err != nil || count != 0 {
		return errAcceptanceInvariant
	}
	recovered, err := repository.QueryPageAndAudit(ctx, fixtureQuery(), fixtureAudit(requestID))
	if err != nil || !isCurrentTruth(recovered) {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, requestID); err != nil || count != 1 {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=statement-timeout fail_closed=1 recovered_current_items=1")
	return nil
}

func runConnectionExhaustion() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 1, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	faultPool, err := openPool(ctx, runtimeURLEnvironment, 1, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer faultPool.Close()
	repository, err := productstore.NewAccountInventoryRepository(faultPool)
	if err != nil || repository.CheckCompatibility(ctx) != nil {
		return errAcceptanceInvariant
	}
	held, err := faultPool.Acquire(ctx)
	if err != nil {
		return errAcceptanceInvariant
	}
	requestID := "readonly-query-recovery-exhaustion"
	exhaustedContext, exhaustedCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	page, queryErr := repository.QueryPageAndAudit(exhaustedContext, fixtureQuery(), fixtureAudit(requestID))
	exhaustedCancel()
	held.Release()
	if queryErr == nil || !isZeroPage(page) {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, requestID); err != nil || count != 0 {
		return errAcceptanceInvariant
	}
	recovered, err := repository.QueryPageAndAudit(ctx, fixtureQuery(), fixtureAudit(requestID))
	if err != nil || !isCurrentTruth(recovered) {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, requestID); err != nil || count != 1 {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=connection-exhaustion fail_closed=1 recovered_current_items=1")
	return nil
}

func runAuditCommitFailure() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 1, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	runtime, err := openPool(ctx, runtimeURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer runtime.Close()
	repository, err := productstore.NewAccountInventoryRepository(runtime)
	if err != nil || repository.CheckCompatibility(ctx) != nil {
		return errAcceptanceInvariant
	}
	if _, err := owner.Exec(ctx, `CREATE FUNCTION public.control_acceptance_reject_view_audit_commit()
		RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
		BEGIN
			IF NEW.action='account_inventory.view' THEN
				RAISE EXCEPTION 'injected deferred view audit failure' USING ERRCODE='P0503';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE CONSTRAINT TRIGGER zz_control_acceptance_reject_view_audit_commit
		AFTER INSERT ON audit_logs DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION public.control_acceptance_reject_view_audit_commit()`); err != nil {
		return errAcceptanceInvariant
	}
	defer func() {
		_, _ = owner.Exec(context.Background(), `DROP TRIGGER IF EXISTS zz_control_acceptance_reject_view_audit_commit ON audit_logs;
			DROP FUNCTION IF EXISTS public.control_acceptance_reject_view_audit_commit()`)
	}()
	requestID := "readonly-query-recovery-audit-failure"
	page, queryErr := repository.QueryPageAndAudit(ctx, fixtureQuery(), fixtureAudit(requestID))
	if queryErr == nil || !isZeroPage(page) {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, requestID); err != nil || count != 0 {
		return errAcceptanceInvariant
	}
	if _, err := owner.Exec(ctx, `DROP TRIGGER zz_control_acceptance_reject_view_audit_commit ON audit_logs;
		DROP FUNCTION public.control_acceptance_reject_view_audit_commit()`); err != nil {
		return errAcceptanceInvariant
	}
	recovered, err := repository.QueryPageAndAudit(ctx, fixtureQuery(), fixtureAudit(requestID))
	if err != nil || !isCurrentTruth(recovered) {
		return errAcceptanceInvariant
	}
	if count, err := auditCount(ctx, owner, requestID); err != nil || count != 1 {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_readonly_query_recovery=success phase=audit-commit-failure fail_closed=1 recovered_current_items=1")
	return nil
}

func seedHTTPSession(ctx context.Context, owner *pgxpool.Pool) error {
	keyringPath := os.Getenv(keyringFileEnvironment)
	sessionPath := os.Getenv(sessionFileEnvironment)
	if keyringPath == "" || sessionPath == "" {
		return errInvalidConfiguration
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
	transaction, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if _, err := transaction.Exec(ctx, `UPDATE control_admin_users
		SET status='enabled',activated_at=clock_timestamp(),updated_at=clock_timestamp()
		WHERE admin_id=$1 AND status='pending'`, fixtureActorID); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO control_admin_sessions(
		session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,
		created_at,last_activity_at,absolute_expires_at
	) VALUES($1,$2,$3,$4,$5,'none',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '11 hours')`,
		uuid.New(), fixtureActorID, sessionDigest.Sum[:], csrfDigest.Sum[:], int32(sessionDigest.KeyVersion)); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return err
	}
	encoded, err := json.Marshal(httpSessionFixture{Session: sessionToken, CSRF: csrfToken})
	if err != nil {
		return err
	}
	return os.WriteFile(sessionPath, append(encoded, '\n'), 0o600)
}

func seedFixture(ctx context.Context, owner, runtime *pgxpool.Pool) error {
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO environments(environment_id,name,environment_type) VALUES('readonly-query-recovery','Readonly Query Recovery','dev')`, nil},
		{`INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES($1,$2,'Readonly Query Recovery Driver')`, []any{fixtureNodeType, fixtureContract}},
		{`INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES($1,$2,'management_account_inventory_read')`, []any{fixtureNodeType, fixtureContract}},
		{`INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES($1,'Readonly Query Recovery Node',$2,$3,'http://readonly-query-recovery.invalid','docker-secret://synthetic/readonly-query-recovery')`, []any{fixtureInstanceID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,$2,$3,'management_account_inventory_read')`, []any{fixtureInstanceID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by)
			VALUES($1,$2,$3,ARRAY['openai'],ARRAY['legacy'],'acceptance')`, []any{fixturePolicyID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at)
			VALUES($1,$2,$3,'acceptance',clock_timestamp())`, []any{fixtureNodeType, fixtureContract, fixturePolicyID}},
		{`INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at)
			VALUES($1,$2,$3,clock_timestamp(),'acceptance',CURRENT_TIMESTAMP)`, []any{fixtureNodeType, fixtureContract, fixturePolicyID}},
		{`INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,max_attempts,poll_start_grace_seconds,created_at)
			VALUES($1,$2,$3,$4,date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01')-interval '30 minutes',$5,2,299,clock_timestamp())`, []any{fixturePollID, fixtureInstanceID, fixtureNodeType, fixtureContract, fixturePolicyID}},
		{`UPDATE account_inventory_poll_runs SET status='running',attempt_count=1,first_started_at=clock_timestamp(),last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',lease_fencing_token=$2 WHERE poll_run_id=$1`, []any{fixturePollID, fixtureFence}},
		{`INSERT INTO control_admin_users(admin_id,login_name,display_name) VALUES($1,'readonly_query_recovery','Readonly Query Recovery Operator')`, []any{fixtureActorID}},
	}
	transaction, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return err
	}
	providers, err := json.Marshal([]map[string]any{{
		"provider": fixtureProvider, "identifiable_count": 1, "missing_identity_count": 0,
		"duplicate_identity_count": 0, "identity_complete": true, "snapshot_complete": true,
		"degraded": false, "reason": "complete",
	}})
	if err != nil {
		return err
	}
	snapshots, err := json.Marshal([]map[string]any{{
		"provider": fixtureProvider, "account_key": fixtureProvider + ":" + fixtureEmail,
		"email": fixtureEmail, "basic_status": "active", "success_count": int64(1),
		"failed_count": int64(0), "recent_request_count": int64(0),
		"last_refresh_unix": nil, "next_retry_unix": nil, "updated_at_unix": nil,
	}})
	if err != nil {
		return err
	}
	var finalized int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
		$1,$2,true,true,true,'runtime',true,true,false,'success','none',1,1,0,0,0,'v1.0.0','abcdef1',$3::jsonb,$4::jsonb,'[]'::jsonb
	)`, fixturePollID, fixtureFence, providers, snapshots).Scan(&finalized); err != nil {
		return err
	}
	if finalized != 1 {
		return errAcceptanceInvariant
	}
	return nil
}

func openPool(ctx context.Context, environment string, maxConnections int32, connectTimeout, statementTimeout time.Duration) (*pgxpool.Pool, error) {
	databaseURL := os.Getenv(environment)
	if databaseURL == "" {
		return nil, errInvalidConfiguration
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errInvalidConfiguration
	}
	config.MaxConns = maxConnections
	config.ConnConfig.ConnectTimeout = connectTimeout
	config.ConnConfig.RuntimeParams["application_name"] = "readonly-query-recovery"
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"
	if statementTimeout > 0 {
		config.ConnConfig.RuntimeParams["statement_timeout"] = fmt.Sprintf("%dms", statementTimeout.Milliseconds())
	}
	return pgxpool.NewWithConfig(ctx, config)
}

func queryAndAudit(ctx context.Context, pool *pgxpool.Pool, requestID string) (productstore.AccountInventoryPage, error) {
	repository, err := productstore.NewAccountInventoryRepository(pool)
	if err != nil {
		return productstore.AccountInventoryPage{}, err
	}
	if err := repository.CheckCompatibility(ctx); err != nil {
		return productstore.AccountInventoryPage{}, err
	}
	return repository.QueryPageAndAudit(ctx, fixtureQuery(), fixtureAudit(requestID))
}

func fixtureQuery() productstore.AccountInventoryQuery {
	return productstore.AccountInventoryQuery{InstanceID: fixtureInstanceID, Limit: 10}
}

func fixtureAudit(requestID string) productstore.AccountInventoryViewAudit {
	fingerprint := sha256.Sum256([]byte("readonly-query-recovery"))
	return productstore.AccountInventoryViewAudit{
		ActorAdminID: fixtureActorID, SourceFingerprint: fingerprint[:], RequestID: requestID,
	}
}

func auditCount(ctx context.Context, owner *pgxpool.Pool, requestID string) (int, error) {
	var count int
	err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id=$1 AND action='account_inventory.view'`, requestID).Scan(&count)
	return count, err
}

func isCurrentTruth(page productstore.AccountInventoryPage) bool {
	return len(page.Items) == 1 && !page.HasMore && page.ContinuationAccountKey == "" &&
		page.Items[0].InstanceID == fixtureInstanceID && page.Items[0].Provider == fixtureProvider &&
		page.Items[0].Email == fixtureEmail && page.Items[0].BasicStatus == productstore.AccountInventoryBasicStatusReportedActive &&
		page.Items[0].Lifecycle == productstore.AccountInventoryPresent
}

func isZeroPage(page productstore.AccountInventoryPage) bool {
	return len(page.Items) == 0 && !page.HasMore && page.ContinuationAccountKey == ""
}

func waitForSignal(ctx context.Context, path string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return errInvalidConfiguration
		}
		select {
		case <-ctx.Done():
			return errAcceptanceInvariant
		case <-ticker.C:
		}
	}
}
