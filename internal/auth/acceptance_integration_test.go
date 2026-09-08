package auth

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const acceptancePassword = "correct horse battery staple acceptance"

func newIsolatedAcceptanceService(t *testing.T) (*Service, *pgxpool.Pool, string) {
	t.Helper()
	baseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if baseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" {
		t.Fatalf("parse CONTROL_DATABASE_TEST_URL: %v", err)
	}
	databaseName := "control_auth_accept_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	admin, err := pgx.Connect(context.Background(), baseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		_ = admin.Close(context.Background())
		t.Fatalf("create isolated database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	parsed.Path = "/" + databaseName
	databaseURL := parsed.String()
	repositoryRoot := acceptanceRepositoryRoot(t)
	command := exec.Command("go", "tool", "goose", "-dir", "../migrations", "postgres", databaseURL, "up")
	command.Dir = filepath.Join(repositoryRoot, "tools")
	if output, migrationErr := command.CombinedOutput(); migrationErr != nil {
		t.Fatalf("migrate isolated database: %v\n%s", migrationErr, output)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	bootstrapSecret := strings.Repeat("b", 48)
	secretPath := filepath.Join(t.TempDir(), "bootstrap.secret")
	if err = os.WriteFile(secretPath, []byte(bootstrapSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, &ValidatedConfig{
		Config:  Config{Environment: EnvironmentDev, BootstrapSecretFile: secretPath, MFARequired: true},
		Keyring: serviceTestKeyring(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, pool, bootstrapSecret
}

func acceptanceRepositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for candidate := workingDirectory; ; candidate = filepath.Dir(candidate) {
		if _, statErr := os.Stat(filepath.Join(candidate, "go.mod")); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			t.Fatal("repository root not found")
		}
	}
}

func acceptanceMeta(t *testing.T, label string) RequestMeta {
	t.Helper()
	return RequestMeta{RequestID: label + "-" + uuid.NewString(), SourceFingerprint: randomTestDigest(t, 1)}
}

func startAcceptanceBootstrap(t *testing.T, service *Service, secret string, meta RequestMeta) string {
	t.Helper()
	enrollment, err := service.StartBootstrap(context.Background(), secret, "acceptance_admin", "Acceptance Administrator", acceptancePassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(enrollment.URI)
	if err != nil {
		t.Fatal(err)
	}
	totpSecret := parsed.Query().Get("secret")
	if totpSecret == "" {
		t.Fatal("bootstrap enrollment omitted TOTP secret")
	}
	return totpSecret
}

func currentAcceptanceTOTP(t *testing.T, service *Service, secret string) string {
	t.Helper()
	databaseTime, err := service.queries.GetDatabaseTime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateTOTP(secret, databaseTime.Time.UTC())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func completeAcceptanceBootstrap(t *testing.T, service *Service, secret, totpSecret string, meta RequestMeta) (Session, []string) {
	t.Helper()
	session, recoveryCodes, err := service.CompleteBootstrap(context.Background(), secret, currentAcceptanceTOTP(t, service, totpSecret), meta)
	if err != nil {
		t.Fatal(err)
	}
	return session, recoveryCodes
}

func TestBootstrapConcurrentStartCreatesOnlyOnePendingFlow(t *testing.T) {
	service, pool, bootstrapSecret := newIsolatedAcceptanceService(t)
	ctx := context.Background()
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var workers sync.WaitGroup
	metas := []RequestMeta{acceptanceMeta(t, "bootstrap-concurrent-0"), acceptanceMeta(t, "bootstrap-concurrent-1")}
	for index := range 2 {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, err := service.StartBootstrap(ctx, bootstrapSecret, "acceptance_admin", "Acceptance Administrator", acceptancePassword, metas[index])
			errorsCh <- err
		}(index)
	}
	close(start)
	workers.Wait()
	close(errorsCh)
	successes := 0
	for err := range errorsCh {
		if err == nil {
			successes++
			continue
		}
		var serviceErr *ServiceError
		if !errors.As(err, &serviceErr) || serviceErr.Code != ErrorCodeInternal {
			t.Fatalf("unexpected concurrent bootstrap error: %v", err)
		}
	}
	if successes == 0 {
		t.Fatal("both concurrent bootstrap starts failed")
	}
	var state string
	var pendingCount, startAuditCount int
	if err := pool.QueryRow(ctx, `SELECT state FROM control_bootstrap_state WHERE singleton_id=1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_users WHERE status='pending'`).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='bootstrap.start' AND result='success'`).Scan(&startAuditCount); err != nil {
		t.Fatal(err)
	}
	if state != "in_progress" || pendingCount != 1 || startAuditCount != 1 {
		t.Fatalf("bootstrap state=%s pending=%d success_audits=%d", state, pendingCount, startAuditCount)
	}

	// A process restart must resume the one persisted enrollment rather than
	// create a second administrator or TOTP factor.
	restarted, err := NewService(pool, service.config)
	if err != nil {
		t.Fatal(err)
	}
	firstResume, err := restarted.StartBootstrap(ctx, bootstrapSecret, "acceptance_admin", "Acceptance Administrator", acceptancePassword, acceptanceMeta(t, "bootstrap-resume-0"))
	if err != nil {
		t.Fatalf("resume bootstrap after restart: %v", err)
	}
	secondResume, err := restarted.StartBootstrap(ctx, bootstrapSecret, "acceptance_admin", "Acceptance Administrator", acceptancePassword, acceptanceMeta(t, "bootstrap-resume-1"))
	if err != nil {
		t.Fatalf("resume bootstrap repeatedly: %v", err)
	}
	if firstResume.URI == "" || firstResume.URI != secondResume.URI {
		t.Fatalf("resumed enrollment changed: first=%q second=%q", firstResume.URI, secondResume.URI)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_users WHERE status='pending'`).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='bootstrap.start' AND result='success'`).Scan(&startAuditCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 1 || startAuditCount != 1 {
		t.Fatalf("resume duplicated bootstrap state: pending=%d success_audits=%d", pendingCount, startAuditCount)
	}
	if err = restarted.ResetBootstrap(ctx, bootstrapSecret, acceptanceMeta(t, "bootstrap-reset")); err != nil {
		t.Fatalf("reset in-progress bootstrap: %v", err)
	}
	if status, statusErr := restarted.BootstrapStatus(ctx); statusErr != nil || status != "required" {
		t.Fatalf("bootstrap status after reset=%q err=%v", status, statusErr)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_users`).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 0 {
		t.Fatalf("reset retained %d pending administrators", pendingCount)
	}
}

func TestBootstrapAndActivationRollbackAndCompletedIrreversibility(t *testing.T) {
	service, pool, bootstrapSecret := newIsolatedAcceptanceService(t)
	ctx := context.Background()
	meta := acceptanceMeta(t, "bootstrap-rollback")
	if status, err := service.BootstrapStatus(ctx); err != nil || status != "required" {
		t.Fatalf("initial bootstrap status=%q err=%v", status, err)
	}
	if _, err := service.StartBootstrap(ctx, strings.Repeat("x", len(bootstrapSecret)), "acceptance_admin", "Acceptance Administrator", acceptancePassword, meta); err == nil {
		t.Fatal("wrong bootstrap secret was accepted")
	} else {
		var serviceErr *ServiceError
		if !errors.As(err, &serviceErr) || serviceErr.Code != ErrorCodeForbidden {
			t.Fatalf("wrong bootstrap secret error=%v", err)
		}
	}
	missingSecretConfig := *service.config
	missingSecretConfig.BootstrapSecretFile = filepath.Join(t.TempDir(), "missing-bootstrap.secret")
	missingSecretService, err := NewService(pool, &missingSecretConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = missingSecretService.StartBootstrap(ctx, bootstrapSecret, "acceptance_admin", "Acceptance Administrator", acceptancePassword, meta); err == nil {
		t.Fatal("missing bootstrap secret file was accepted")
	} else {
		var serviceErr *ServiceError
		if !errors.As(err, &serviceErr) || serviceErr.Code != ErrorCodeForbidden {
			t.Fatalf("missing bootstrap secret file error=%v", err)
		}
	}
	var preBootstrapUsers, leakedSecrets, rejectedBootstrapAudits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_users`).Scan(&preBootstrapUsers); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs AS audit WHERE row_to_json(audit)::text LIKE '%' || $1 || '%'`, bootstrapSecret).Scan(&leakedSecrets); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id=$1 AND action='bootstrap.start' AND result='denied'`, meta.RequestID).Scan(&rejectedBootstrapAudits); err != nil {
		t.Fatal(err)
	}
	if preBootstrapUsers != 0 || leakedSecrets != 0 || rejectedBootstrapAudits != 2 {
		t.Fatalf("rejected bootstrap users=%d leaked_secret_audits=%d denied_audits=%d", preBootstrapUsers, leakedSecrets, rejectedBootstrapAudits)
	}
	totpSecret := startAcceptanceBootstrap(t, service, bootstrapSecret, meta)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION acceptance_fail_bootstrap_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.action='bootstrap.complete' AND NEW.result='success' THEN RAISE EXCEPTION 'acceptance bootstrap failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER acceptance_fail_bootstrap_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION acceptance_fail_bootstrap_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteBootstrap(ctx, bootstrapSecret, currentAcceptanceTOTP(t, service, totpSecret), meta); err == nil {
		t.Fatal("bootstrap completion unexpectedly survived injected audit failure")
	}
	var state, adminStatus string
	var confirmed, recoveryCount, sessionCount, successAuditCount int
	if err := pool.QueryRow(ctx, `SELECT state FROM control_bootstrap_state WHERE singleton_id=1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT u.status, count(t.confirmed_at)::int FROM control_admin_users AS u JOIN control_admin_totp AS t USING(admin_id) GROUP BY u.status`).Scan(&adminStatus, &confirmed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_recovery_codes`).Scan(&recoveryCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_sessions`).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='bootstrap.complete' AND result='success'`).Scan(&successAuditCount); err != nil {
		t.Fatal(err)
	}
	if state != "in_progress" || adminStatus != "pending" || confirmed != 0 || recoveryCount != 0 || sessionCount != 0 || successAuditCount != 0 {
		t.Fatalf("partial bootstrap committed: state=%s admin=%s confirmed=%d recovery=%d sessions=%d success_audit=%d", state, adminStatus, confirmed, recoveryCount, sessionCount, successAuditCount)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER acceptance_fail_bootstrap_audit ON audit_logs; DROP FUNCTION acceptance_fail_bootstrap_audit()`); err != nil {
		t.Fatal(err)
	}
	actor, originalRecoveryCodes := completeAcceptanceBootstrap(t, service, bootstrapSecret, totpSecret, meta)
	if len(originalRecoveryCodes) != 10 {
		t.Fatalf("bootstrap recovery count=%d", len(originalRecoveryCodes))
	}
	if status, statusErr := service.BootstrapStatus(ctx); statusErr != nil || status != "completed" {
		t.Fatalf("completed bootstrap status=%q err=%v", status, statusErr)
	}
	if err := service.ResetBootstrap(ctx, bootstrapSecret, meta); !errors.Is(err, ErrBootstrap) {
		t.Fatalf("completed bootstrap reset error=%v", err)
	}
	if _, err := service.StartBootstrap(ctx, bootstrapSecret, "acceptance_admin", "Acceptance Administrator", acceptancePassword, meta); !errors.Is(err, ErrBootstrap) {
		t.Fatalf("completed bootstrap start error=%v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id=$1 AND action='bootstrap.start' AND result='denied'`, meta.RequestID).Scan(&rejectedBootstrapAudits); err != nil {
		t.Fatal(err)
	}
	if rejectedBootstrapAudits != 3 {
		t.Fatalf("completed bootstrap did not add duplicate-attempt audit; denied=%d", rejectedBootstrapAudits)
	}
	if _, err := pool.Exec(ctx, `UPDATE control_bootstrap_state SET state='required', started_at=NULL, completed_at=NULL WHERE singleton_id=1`); err == nil {
		t.Fatal("ordinary SQL reopened completed bootstrap")
	}

	if _, err := pool.Exec(ctx, `CREATE FUNCTION acceptance_fail_recovery_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'acceptance recovery failure'; END $$;
		CREATE TRIGGER acceptance_fail_recovery_insert BEFORE INSERT ON control_admin_recovery_codes FOR EACH ROW EXECUTE FUNCTION acceptance_fail_recovery_insert()`); err != nil {
		t.Fatal(err)
	}
	rotated, err := service.RegenerateRecoveryCodes(ctx, actor, "acceptance recovery rollback evidence", meta)
	if err == nil || len(rotated) != 0 {
		t.Fatalf("recovery rotation result codes=%d err=%v", len(rotated), err)
	}
	var availableCount, availableBatches, regenerationAudits int
	if err = pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT batch_id) FROM control_admin_recovery_codes WHERE revoked_at IS NULL AND consumed_at IS NULL`).Scan(&availableCount, &availableBatches); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='auth.recovery_codes_regenerate' AND result='success'`).Scan(&regenerationAudits); err != nil {
		t.Fatal(err)
	}
	if availableCount != 10 || availableBatches != 1 || regenerationAudits != 0 {
		t.Fatalf("recovery rollback available=%d batches=%d success_audits=%d", availableCount, availableBatches, regenerationAudits)
	}
	recoveryProofRollback := errors.New("rollback recovery usability proof")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if consumeErr := service.consumeRecovery(ctx, service.queries.WithTx(tx), pgUUID(actor.AdminID), originalRecoveryCodes[0]); consumeErr != nil {
			return consumeErr
		}
		return recoveryProofRollback
	})
	if !errors.Is(err, recoveryProofRollback) {
		t.Fatalf("old recovery code unusable after failed rotation: %v", err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER acceptance_fail_recovery_insert ON control_admin_recovery_codes; DROP FUNCTION acceptance_fail_recovery_insert()`); err != nil {
		t.Fatal(err)
	}
	if regenerated, regenerateErr := service.RegenerateRecoveryCodes(ctx, actor, "acceptance recovery successful rotation", meta); regenerateErr != nil || len(regenerated) != 10 {
		t.Fatalf("recovery rotation after rollback codes=%d err=%v", len(regenerated), regenerateErr)
	}

	created, err := service.CreateAdmin(ctx, actor, "acceptance_target", "Acceptance Target", "acceptance activation rollback target", meta)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := service.StartActivation(ctx, created.Token, meta)
	if err != nil {
		t.Fatal(err)
	}
	activationURI, err := url.Parse(enrollment.URI)
	if err != nil {
		t.Fatal(err)
	}
	activationSecret := activationURI.Query().Get("secret")
	if _, err = pool.Exec(ctx, `CREATE FUNCTION acceptance_fail_activation_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.action='administrator.activate' AND NEW.result='success' THEN RAISE EXCEPTION 'acceptance activation failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER acceptance_fail_activation_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION acceptance_fail_activation_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, codes, completeErr := service.CompleteActivation(ctx, created.Token, "acceptance target password strong", currentAcceptanceTOTP(t, service, activationSecret), meta); completeErr == nil || len(codes) != 0 {
		t.Fatalf("activation completion survived injected failure codes=%d err=%v", len(codes), completeErr)
	}
	var targetStatus string
	var targetPasswords, targetRecovery, targetSessions, targetActivationAudits int
	if err = pool.QueryRow(ctx, `SELECT status FROM control_admin_users WHERE admin_id=$1`, pgUUID(created.Admin.ID)).Scan(&targetStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_passwords WHERE admin_id=$1`, pgUUID(created.Admin.ID)).Scan(&targetPasswords); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_recovery_codes WHERE admin_id=$1`, pgUUID(created.Admin.ID)).Scan(&targetRecovery); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_sessions WHERE admin_id=$1`, pgUUID(created.Admin.ID)).Scan(&targetSessions); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE target_admin_id=$1 AND action='administrator.activate' AND result='success'`, pgUUID(created.Admin.ID)).Scan(&targetActivationAudits); err != nil {
		t.Fatal(err)
	}
	if targetStatus != "pending" || targetPasswords != 0 || targetRecovery != 0 || targetSessions != 0 || targetActivationAudits != 0 {
		t.Fatalf("partial activation committed: status=%s passwords=%d recovery=%d sessions=%d success_audits=%d", targetStatus, targetPasswords, targetRecovery, targetSessions, targetActivationAudits)
	}
	if _, loginErr := service.Login(ctx, "acceptance_target", "acceptance target password strong", meta); !errors.Is(loginErr, ErrAuthentication) {
		t.Fatalf("pending administrator login after failed activation error=%v", loginErr)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER acceptance_fail_activation_audit ON audit_logs; DROP FUNCTION acceptance_fail_activation_audit()`); err != nil {
		t.Fatal(err)
	}
	targetSession, codes, completeErr := service.CompleteActivation(ctx, created.Token, "acceptance target password strong", currentAcceptanceTOTP(t, service, activationSecret), meta)
	if completeErr != nil || len(codes) != 10 {
		t.Fatalf("activation after rollback codes=%d err=%v", len(codes), completeErr)
	}

	if _, err = pool.Exec(ctx, `CREATE FUNCTION acceptance_fail_mfa_reset_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.action='auth.mfa_reset' AND NEW.result='success' THEN RAISE EXCEPTION 'acceptance MFA reset failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER acceptance_fail_mfa_reset_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION acceptance_fail_mfa_reset_audit()`); err != nil {
		t.Fatal(err)
	}
	if token, resetErr := service.ResetAdminMFA(ctx, actor, created.Admin.ID, "acceptance MFA reset rollback evidence", meta); resetErr == nil || token.Token != "" {
		t.Fatalf("MFA reset survived injected failure token=%q err=%v", token.Token, resetErr)
	}
	var targetTOTP, targetAvailableRecovery, targetActiveSessions, targetActiveTokens int
	if err = pool.QueryRow(ctx, `SELECT status FROM control_admin_users WHERE admin_id=$1`, pgUUID(created.Admin.ID)).Scan(&targetStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_totp WHERE admin_id=$1 AND confirmed_at IS NOT NULL`, pgUUID(created.Admin.ID)).Scan(&targetTOTP); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_recovery_codes WHERE admin_id=$1 AND revoked_at IS NULL AND consumed_at IS NULL`, pgUUID(created.Admin.ID)).Scan(&targetAvailableRecovery); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_sessions WHERE admin_id=$1 AND revoked_at IS NULL`, pgUUID(created.Admin.ID)).Scan(&targetActiveSessions); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_activation_tokens WHERE admin_id=$1 AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>CURRENT_TIMESTAMP`, pgUUID(created.Admin.ID)).Scan(&targetActiveTokens); err != nil {
		t.Fatal(err)
	}
	if targetStatus != "enabled" || targetTOTP != 1 || targetAvailableRecovery != 10 || targetActiveSessions != 1 || targetActiveTokens != 0 {
		t.Fatalf("partial MFA reset committed: status=%s totp=%d recovery=%d sessions=%d tokens=%d", targetStatus, targetTOTP, targetAvailableRecovery, targetActiveSessions, targetActiveTokens)
	}
	if authenticated, authenticateErr := service.Authenticate(ctx, targetSession.Token); authenticateErr != nil || authenticated.AdminID != created.Admin.ID {
		t.Fatalf("failed MFA reset revoked target session: session=%+v err=%v", authenticated, authenticateErr)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER acceptance_fail_mfa_reset_audit ON audit_logs; DROP FUNCTION acceptance_fail_mfa_reset_audit()`); err != nil {
		t.Fatal(err)
	}
	resetToken, resetErr := service.ResetAdminMFA(ctx, actor, created.Admin.ID, "acceptance MFA reset successful retry", meta)
	if resetErr != nil || resetToken.Token == "" {
		t.Fatalf("MFA reset after rollback token=%q err=%v", resetToken.Token, resetErr)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM control_admin_users WHERE admin_id=$1`, pgUUID(created.Admin.ID)).Scan(&targetStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_totp WHERE admin_id=$1`, pgUUID(created.Admin.ID)).Scan(&targetTOTP); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_recovery_codes WHERE admin_id=$1 AND revoked_at IS NULL AND consumed_at IS NULL`, pgUUID(created.Admin.ID)).Scan(&targetAvailableRecovery); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_sessions WHERE admin_id=$1 AND revoked_at IS NULL`, pgUUID(created.Admin.ID)).Scan(&targetActiveSessions); err != nil {
		t.Fatal(err)
	}
	if targetStatus != "pending" || targetTOTP != 0 || targetAvailableRecovery != 0 || targetActiveSessions != 0 {
		t.Fatalf("successful MFA reset state: status=%s totp=%d recovery=%d sessions=%d", targetStatus, targetTOTP, targetAvailableRecovery, targetActiveSessions)
	}
}

func TestRecoveryLoginRollbackKeepsChallengeAndCodeUsable(t *testing.T) {
	service, pool, bootstrapSecret := newIsolatedAcceptanceService(t)
	ctx := context.Background()
	meta := acceptanceMeta(t, "recovery-login-rollback")
	totpSecret := startAcceptanceBootstrap(t, service, bootstrapSecret, meta)
	_, recoveryCodes := completeAcceptanceBootstrap(t, service, bootstrapSecret, totpSecret, meta)
	login, err := service.Login(ctx, "acceptance_admin", acceptancePassword, meta)
	if err != nil || login.Challenge == nil {
		t.Fatalf("recovery login challenge=%+v err=%v", login, err)
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION acceptance_fail_recovery_login_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.action='auth.recovery_code_use' AND NEW.result='success' THEN RAISE EXCEPTION 'acceptance recovery login failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER acceptance_fail_recovery_login_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION acceptance_fail_recovery_login_audit()`); err != nil {
		t.Fatal(err)
	}
	if session, completeErr := service.CompleteLoginMFA(ctx, login.Challenge.Token, MFAMethodRecoveryCode, recoveryCodes[0], meta); completeErr == nil || session.Token != "" {
		t.Fatalf("recovery login survived injected failure session=%q err=%v", session.Token, completeErr)
	}
	var availableCodes, activeChallenges, activeSessions, successAudits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_recovery_codes WHERE revoked_at IS NULL AND consumed_at IS NULL`).Scan(&availableCodes); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_auth_challenges WHERE consumed_at IS NULL AND expires_at>CURRENT_TIMESTAMP`).Scan(&activeChallenges); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_sessions WHERE revoked_at IS NULL`).Scan(&activeSessions); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='auth.recovery_code_use' AND result='success'`).Scan(&successAudits); err != nil {
		t.Fatal(err)
	}
	if availableCodes != 10 || activeChallenges != 1 || activeSessions != 1 || successAudits != 0 {
		t.Fatalf("partial recovery login committed: codes=%d challenges=%d sessions=%d audits=%d", availableCodes, activeChallenges, activeSessions, successAudits)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER acceptance_fail_recovery_login_audit ON audit_logs; DROP FUNCTION acceptance_fail_recovery_login_audit()`); err != nil {
		t.Fatal(err)
	}
	if session, completeErr := service.CompleteLoginMFA(ctx, login.Challenge.Token, MFAMethodRecoveryCode, recoveryCodes[0], meta); completeErr != nil || session.Token == "" || session.RecoveryCodesRemaining != 9 {
		t.Fatalf("recovery login retry session=%q remaining=%d err=%v", session.Token, session.RecoveryCodesRemaining, completeErr)
	}
}

func TestCompleteLoginMFASameTimeStepOnlyOneSucceeds(t *testing.T) {
	service, pool, bootstrapSecret := newIsolatedAcceptanceService(t)
	ctx := context.Background()
	meta := acceptanceMeta(t, "mfa-concurrent")
	totpSecret := startAcceptanceBootstrap(t, service, bootstrapSecret, meta)
	_, _ = completeAcceptanceBootstrap(t, service, bootstrapSecret, totpSecret, meta)
	if _, err := pool.Exec(ctx, `UPDATE control_admin_totp SET last_used_step=NULL`); err != nil {
		t.Fatal(err)
	}
	first, err := service.Login(ctx, "acceptance_admin", acceptancePassword, meta)
	if err != nil || first.Challenge == nil {
		t.Fatalf("first challenge=%+v err=%v", first, err)
	}
	second, err := service.Login(ctx, "acceptance_admin", acceptancePassword, meta)
	if err != nil || second.Challenge == nil {
		t.Fatalf("second challenge=%+v err=%v", second, err)
	}
	code := currentAcceptanceTOTP(t, service, totpSecret)
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var workers sync.WaitGroup
	for _, challenge := range []string{first.Challenge.Token, second.Challenge.Token} {
		workers.Add(1)
		go func(challenge string) {
			defer workers.Done()
			<-start
			_, completeErr := service.CompleteLoginMFA(ctx, challenge, MFAMethodTOTP, code, meta)
			errorsCh <- completeErr
		}(challenge)
	}
	close(start)
	workers.Wait()
	close(errorsCh)
	successes, authenticationFailures := 0, 0
	for completeErr := range errorsCh {
		switch {
		case completeErr == nil:
			successes++
		case errors.Is(completeErr, ErrAuthentication):
			authenticationFailures++
		default:
			t.Fatalf("concurrent MFA returned internal error: %v", completeErr)
		}
	}
	if successes != 1 || authenticationFailures != 1 {
		t.Fatalf("concurrent MFA successes=%d authentication_failures=%d", successes, authenticationFailures)
	}
	var mfaSessions int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_sessions WHERE mfa_method='totp' AND revoked_at IS NULL`).Scan(&mfaSessions); err != nil {
		t.Fatal(err)
	}
	if mfaSessions != 2 { // bootstrap session plus exactly one login session
		t.Fatalf("active TOTP sessions=%d, want 2", mfaSessions)
	}
}

func TestCSRFAndReauthenticationProofsAreSessionBoundAndExpire(t *testing.T) {
	service, pool, bootstrapSecret := newIsolatedAcceptanceService(t)
	ctx := context.Background()
	meta := acceptanceMeta(t, "session-bound-proofs")
	totpSecret := startAcceptanceBootstrap(t, service, bootstrapSecret, meta)
	firstSession, recoveryCodes := completeAcceptanceBootstrap(t, service, bootstrapSecret, totpSecret, meta)
	login, err := service.Login(ctx, "acceptance_admin", acceptancePassword, meta)
	if err != nil || login.Challenge == nil {
		t.Fatalf("second session challenge=%+v err=%v", login, err)
	}
	secondSession, err := service.CompleteLoginMFA(ctx, login.Challenge.Token, MFAMethodRecoveryCode, recoveryCodes[0], meta)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.VerifyCSRF(ctx, firstSession, firstSession.CSRFToken); err != nil {
		t.Fatalf("first session own CSRF rejected: %v", err)
	}
	if err = service.VerifyCSRF(ctx, secondSession, secondSession.CSRFToken); err != nil {
		t.Fatalf("second session own CSRF rejected: %v", err)
	}
	if err = service.VerifyCSRF(ctx, firstSession, secondSession.CSRFToken); !errors.Is(err, ErrCSRF) {
		t.Fatalf("second CSRF accepted by first session: %v", err)
	}
	if err = service.VerifyCSRF(ctx, secondSession, firstSession.CSRFToken); !errors.Is(err, ErrCSRF) {
		t.Fatalf("first CSRF accepted by second session: %v", err)
	}
	rotated, err := service.RotateCSRF(ctx, firstSession)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.VerifyCSRF(ctx, rotated, rotated.CSRFToken); err != nil {
		t.Fatalf("rotated CSRF rejected: %v", err)
	}
	if err = service.VerifyCSRF(ctx, rotated, firstSession.CSRFToken); !errors.Is(err, ErrCSRF) {
		t.Fatalf("old CSRF remained valid after rotation: %v", err)
	}

	forgedFreshness := secondSession
	forgedFreshness.ReauthenticatedAt = firstSession.ReauthenticatedAt
	if _, err = service.RegenerateRecoveryCodes(ctx, forgedFreshness, "cross session reauthentication proof", meta); !errors.Is(err, ErrReauthenticate) {
		t.Fatalf("cross-session reauthentication proof error=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE control_admin_sessions
		SET created_at=CURRENT_TIMESTAMP-interval '1 hour',
		    absolute_expires_at=CURRENT_TIMESTAMP+interval '11 hours',
		    reauthenticated_at=CURRENT_TIMESTAMP-interval '6 minutes'
		WHERE session_id=$1`, pgUUID(firstSession.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RegenerateRecoveryCodes(ctx, firstSession, "expired reauthentication proof evidence", meta); !errors.Is(err, ErrReauthenticate) {
		t.Fatalf("expired reauthentication proof error=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE control_admin_sessions
		SET created_at=CURRENT_TIMESTAMP-interval '13 hours',
		    last_activity_at=CURRENT_TIMESTAMP-interval '13 hours',
		    absolute_expires_at=CURRENT_TIMESTAMP-interval '1 hour'
		WHERE session_id=$1`, pgUUID(secondSession.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Reauthenticate(ctx, secondSession, acceptancePassword, MFAMethodRecoveryCode, recoveryCodes[1], meta); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expired session reauthentication error=%v", err)
	}
	var stillAvailable int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_recovery_codes
		WHERE admin_id=$1 AND revoked_at IS NULL AND consumed_at IS NULL`, pgUUID(firstSession.AdminID)).Scan(&stillAvailable); err != nil {
		t.Fatal(err)
	}
	if stillAvailable != 9 {
		t.Fatalf("expired reauthentication consumed a recovery code; available=%d", stillAvailable)
	}
}

func TestDevWithoutRequiredMFASignsSessionWhileProductionRequiresChallenge(t *testing.T) {
	bootstrapService, pool, bootstrapSecret := newIsolatedAcceptanceService(t)
	ctx := context.Background()
	meta := acceptanceMeta(t, "environment-mfa-policy")
	totpSecret := startAcceptanceBootstrap(t, bootstrapService, bootstrapSecret, meta)
	bootstrapSession, _ := completeAcceptanceBootstrap(t, bootstrapService, bootstrapSecret, totpSecret, meta)

	devConfig := *bootstrapService.config
	devConfig.Environment = EnvironmentDev
	devConfig.MFARequired = false
	devService, err := NewService(pool, &devConfig)
	if err != nil {
		t.Fatal(err)
	}
	devResult, err := devService.Login(ctx, "acceptance_admin", acceptancePassword, meta)
	if err != nil {
		t.Fatalf("dev login without required MFA: %v", err)
	}
	if devResult.Session == nil || devResult.Challenge != nil || devResult.Session.MFAMethod != MFAMethodNone {
		t.Fatalf("dev MFA-disabled login result=%+v", devResult)
	}
	if authenticated, authenticateErr := devService.Authenticate(ctx, devResult.Session.Token); authenticateErr != nil || authenticated.AdminID != bootstrapSession.AdminID {
		t.Fatalf("dev direct session=%+v err=%v", authenticated, authenticateErr)
	}
	var activeChallenges int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_auth_challenges WHERE consumed_at IS NULL AND expires_at>CURRENT_TIMESTAMP`).Scan(&activeChallenges); err != nil {
		t.Fatal(err)
	}
	if activeChallenges != 0 {
		t.Fatalf("dev direct login created %d MFA challenges", activeChallenges)
	}

	productionService, err := NewService(pool, &ValidatedConfig{
		Config:  Config{Environment: EnvironmentProduction, MFARequired: true},
		Keyring: testKeyring(t, EnvironmentProduction, 1, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	productionResult, err := productionService.Login(ctx, "acceptance_admin", acceptancePassword, meta)
	if err != nil {
		t.Fatalf("production password login: %v", err)
	}
	if productionResult.Challenge == nil || productionResult.Session != nil || productionResult.Challenge.Token == "" {
		t.Fatalf("production MFA-required login result=%+v", productionResult)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_auth_challenges WHERE consumed_at IS NULL AND expires_at>CURRENT_TIMESTAMP`).Scan(&activeChallenges); err != nil {
		t.Fatal(err)
	}
	if activeChallenges != 1 {
		t.Fatalf("production login active challenges=%d, want 1", activeChallenges)
	}
}

func TestSuccessfulMFALoginClearsAccountFailuresButPreservesSourceFailures(t *testing.T) {
	service, pool, bootstrapSecret := newIsolatedAcceptanceService(t)
	ctx := context.Background()
	meta := acceptanceMeta(t, "successful-login-failure-windows")
	totpSecret := startAcceptanceBootstrap(t, service, bootstrapSecret, meta)
	_, recoveryCodes := completeAcceptanceBootstrap(t, service, bootstrapSecret, totpSecret, meta)
	accountDigest, err := LoginFingerprint(service.config.Keyring, "acceptance_admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.recordFailure(ctx, accountDigest, meta, AuditLoginPassword); err != nil {
		t.Fatal(err)
	}
	var accountWindows, sourceWindows int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_auth_failure_windows
		WHERE dimension='account' AND key_version=$1 AND subject_fingerprint=$2`, int32(accountDigest.KeyVersion), accountDigest.Sum[:]).Scan(&accountWindows); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_auth_failure_windows
		WHERE dimension='source' AND key_version=$1 AND subject_fingerprint=$2`, int32(meta.SourceFingerprint.KeyVersion), meta.SourceFingerprint.Sum[:]).Scan(&sourceWindows); err != nil {
		t.Fatal(err)
	}
	if accountWindows != 1 || sourceWindows != 1 {
		t.Fatalf("failure windows before login: account=%d source=%d", accountWindows, sourceWindows)
	}

	login, err := service.Login(ctx, "acceptance_admin", acceptancePassword, meta)
	if err != nil || login.Challenge == nil {
		t.Fatalf("password login challenge=%+v err=%v", login, err)
	}
	completed, err := service.CompleteLoginMFA(ctx, login.Challenge.Token, MFAMethodRecoveryCode, recoveryCodes[0], meta)
	if err != nil || completed.Token == "" {
		t.Fatalf("successful MFA login session=%q err=%v", completed.Token, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_auth_failure_windows
		WHERE dimension='account' AND key_version=$1 AND subject_fingerprint=$2`, int32(accountDigest.KeyVersion), accountDigest.Sum[:]).Scan(&accountWindows); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_auth_failure_windows
		WHERE dimension='source' AND key_version=$1 AND subject_fingerprint=$2`, int32(meta.SourceFingerprint.KeyVersion), meta.SourceFingerprint.Sum[:]).Scan(&sourceWindows); err != nil {
		t.Fatal(err)
	}
	if accountWindows != 0 || sourceWindows != 1 {
		t.Fatalf("failure windows after successful MFA login: account=%d source=%d", accountWindows, sourceWindows)
	}
}
