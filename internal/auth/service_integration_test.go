package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestConcurrentAuthenticationFailuresReachAccountThreshold(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	keyring := serviceTestKeyring(t)
	service := &Service{pool: pool, queries: store.New(pool), config: &ValidatedConfig{Config: Config{Environment: EnvironmentDev}, Keyring: keyring}}
	loginSuffix := randomTestDigest(t, 1)
	login := "missing_" + hex.EncodeToString(loginSuffix.Sum[:6])
	account, err := LoginFingerprint(keyring, login)
	if err != nil {
		t.Fatal(err)
	}
	source := randomTestDigest(t, 1)
	requestID := "rate-test-" + hex.EncodeToString(account.Sum[:8])
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM control_auth_failure_windows WHERE key_version=$1 AND subject_fingerprint IN ($2,$3)`, int32(1), account.Sum[:], source.Sum[:])
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE request_id=$1`, requestID)
	})

	start := make(chan struct{})
	errorsCh := make(chan error, 5)
	var workers sync.WaitGroup
	for range 5 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errorsCh <- service.recordFailure(ctx, account, RequestMeta{RequestID: requestID, SourceFingerprint: source}, AuditLoginMFA)
		}()
	}
	close(start)
	workers.Wait()
	close(errorsCh)
	for failureErr := range errorsCh {
		if failureErr != nil {
			t.Fatalf("record concurrent failure: %v", failureErr)
		}
	}
	blocked, err := service.isBlocked(ctx, []rateSubject{{RateLimitAccount, account}})
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("account remained unblocked after five rolling-window failures")
	}
	if _, err = service.Login(ctx, login, "irrelevant password material", RequestMeta{RequestID: requestID, SourceFingerprint: source}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("blocked nonexistent account login error=%v", err)
	}
	var rateLimitAudits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE request_id=$1 AND action=$2 AND result=$3
		  AND details->>'rate_limit_dimension'=$4`, requestID, string(AuditRateLimit), string(AuditResultLimited), string(RateLimitAccount)).Scan(&rateLimitAudits); err != nil {
		t.Fatal(err)
	}
	if rateLimitAudits != 1 {
		t.Fatalf("account rate-limit audits=%d, want 1", rateLimitAudits)
	}
}

func TestAdministratorActivationCreatesAuditedMFAServiceSession(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	keyring := serviceTestKeyring(t)
	service := &Service{pool: pool, queries: store.New(pool), config: &ValidatedConfig{Config: Config{Environment: EnvironmentDev, MFARequired: true}, Keyring: keyring}}
	creatorID := uuid.New()
	creatorSuffix := randomTestDigest(t, 1)
	creatorLogin := "creator_" + hex.EncodeToString(creatorSuffix.Sum[:4])
	creator, err := service.queries.CreateAdminUser(ctx, store.CreateAdminUserParams{AdminID: pgUUID(creatorID), LoginName: creatorLogin, DisplayName: "Creator Operator"})
	if err != nil {
		t.Fatal(err)
	}
	creator, err = service.queries.ActivateAdminUser(ctx, creator.AdminID)
	if err != nil {
		t.Fatal(err)
	}
	var actor Session
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var createErr error
		actor, createErr = service.createSession(ctx, service.queries.WithTx(tx), creator, MFAMethodTOTP, true)
		return createErr
	})
	if err != nil {
		t.Fatal(err)
	}
	var targetID uuid.UUID
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_admin_id=$1 OR target_admin_id=$1 OR actor_admin_id=$2 OR target_admin_id=$2`, pgUUID(creatorID), nullableUUID(targetID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		if targetID != uuid.Nil {
			_, _ = pool.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, pgUUID(targetID))
		}
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, pgUUID(creatorID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	meta := RequestMeta{RequestID: "activation-integration-test", SourceFingerprint: randomTestDigest(t, 1)}
	pendingSuffix := randomTestDigest(t, 1)
	pendingLogin := "pending_" + hex.EncodeToString(pendingSuffix.Sum[:4])
	accountFingerprint, err := LoginFingerprint(keyring, pendingLogin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM control_auth_failure_windows WHERE (key_version=$1 AND subject_fingerprint=$2) OR (key_version=$3 AND subject_fingerprint=$4)`, int32(accountFingerprint.KeyVersion), accountFingerprint.Sum[:], int32(meta.SourceFingerprint.KeyVersion), meta.SourceFingerprint.Sum[:])
	})
	created, err := service.CreateAdmin(ctx, actor, pendingLogin, "Pending Operator", "integration test administrator creation", meta)
	if err != nil {
		t.Fatal(err)
	}
	targetID = created.Admin.ID
	if created.Token == "" || !created.ExpiresAt.After(time.Now()) {
		t.Fatal("activation token was not returned exactly once")
	}
	enrollment, err := service.StartActivation(ctx, created.Token, meta)
	if err != nil {
		t.Fatal(err)
	}
	totpURL, err := url.Parse(enrollment.URI)
	if err != nil {
		t.Fatal(err)
	}
	secret := totpURL.Query().Get("secret")
	if secret == "" {
		t.Fatal("TOTP enrollment URI omitted secret")
	}
	code, err := GenerateTOTP(secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	session, recoveryCodes, err := service.CompleteActivation(ctx, created.Token, "correct horse battery staple", code, meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(recoveryCodes) != 10 || session.Token == "" || session.CSRFToken == "" || session.MFAMethod != MFAMethodTOTP {
		t.Fatalf("incomplete activation result: codes=%d session=%+v", len(recoveryCodes), session)
	}
	loaded, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatalf("authenticate activated session: %v", err)
	}
	if loaded.AdminID != targetID || service.VerifyCSRF(ctx, loaded, session.CSRFToken) != nil {
		t.Fatal("activated session identity or CSRF binding mismatch")
	}
	if _, err = service.StartActivation(ctx, created.Token, meta); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("consumed activation token error = %v", err)
	}
	reauthenticated, err := service.Reauthenticate(ctx, loaded, "correct horse battery staple", MFAMethodRecoveryCode, recoveryCodes[0], meta)
	if err != nil {
		t.Fatalf("reauthenticate with recovery code: %v", err)
	}
	if _, err = service.Authenticate(ctx, session.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("pre-reauth session error = %v", err)
	}
	regenerated, err := service.RegenerateRecoveryCodes(ctx, reauthenticated, "integration test recovery rotation", meta)
	if err != nil || len(regenerated) != 10 {
		t.Fatalf("regenerate recovery codes: count=%d err=%v", len(regenerated), err)
	}
	changed, err := service.ChangePassword(ctx, reauthenticated, "correct horse battery staple", "correct horse battery staple version two", MFAMethodNone, "", meta)
	if err != nil {
		t.Fatalf("change password under fresh reauthentication: %v", err)
	}
	if _, err = service.Authenticate(ctx, reauthenticated.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("pre-password-change session error = %v", err)
	}
	if _, err = service.Login(ctx, pendingLogin, "correct horse battery staple", meta); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("old password login error = %v", err)
	}
	login, err := service.Login(ctx, pendingLogin, "correct horse battery staple version two", meta)
	if err != nil || login.Challenge == nil || login.Session != nil {
		t.Fatalf("new password login result=%+v err=%v", login, err)
	}
	loggedIn, err := service.CompleteLoginMFA(ctx, login.Challenge.Token, MFAMethodRecoveryCode, regenerated[0], meta)
	if err != nil {
		t.Fatalf("complete recovery-code login: %v", err)
	}
	if _, err = service.Authenticate(ctx, changed.Token); err != nil {
		t.Fatalf("password-change session unexpectedly revoked by another login: %v", err)
	}
	if _, err = service.DisableAdmin(ctx, actor, targetID, "integration test administrator disable", meta); err != nil {
		t.Fatalf("disable administrator: %v", err)
	}
	if _, err = service.Authenticate(ctx, loggedIn.Token); !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled administrator session error = %v", err)
	}
	reset, err := service.ResetAdminMFA(ctx, actor, targetID, "integration test administrator MFA reset", meta)
	if err != nil || reset.Token == "" {
		t.Fatalf("reset administrator MFA: token=%q err=%v", reset.Token, err)
	}
	replacement, err := service.RegenerateActivationToken(ctx, actor, targetID, "integration test activation token rotation", meta)
	if err != nil || replacement.Token == "" || replacement.Token == reset.Token {
		t.Fatalf("regenerate activation token: token=%q err=%v", replacement.Token, err)
	}
	if _, err = service.StartActivation(ctx, reset.Token, meta); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("revoked reset token error = %v", err)
	}

	var auditCount int
	err = pool.QueryRow(ctx, `SELECT count(DISTINCT action) FROM audit_logs WHERE (actor_admin_id=$1 OR target_admin_id=$1) AND result='success' AND action IN ('administrator.create','administrator.activate','session.create','auth.reauthenticate','auth.recovery_codes_regenerate','auth.password_change','administrator.disable','auth.mfa_reset','administrator.activation_token_generate')`, pgUUID(targetID)).Scan(&auditCount)
	if err != nil || auditCount < 9 {
		t.Fatalf("successful state audit count=%d error=%v", auditCount, err)
	}
}

func TestSecurityRejectionsUseIndependentSanitizedAuditActions(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service := &Service{pool: pool, queries: store.New(pool)}
	meta := RequestMeta{RequestID: "security-rejection-" + uuid.NewString(), SourceFingerprint: randomTestDigest(t, 1)}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE request_id=$1`, meta.RequestID) })

	service.AuditSecurityRejection(ctx, AuditCSRF, nil, ErrorCodeCSRFRejected, meta)
	service.AuditSecurityRejection(ctx, AuditAuthorization, nil, ErrorCodeForbidden, meta)

	rows, err := pool.Query(ctx, `SELECT action, result, details->>'error_code' FROM audit_logs WHERE request_id=$1 ORDER BY action`, meta.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string][2]string)
	for rows.Next() {
		var action, result, code string
		if err = rows.Scan(&action, &result, &code); err != nil {
			t.Fatal(err)
		}
		got[action] = [2]string{result, code}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got[string(AuditCSRF)] != [2]string{string(AuditResultDenied), string(ErrorCodeCSRFRejected)} {
		t.Fatalf("CSRF audit = %#v", got[string(AuditCSRF)])
	}
	if got[string(AuditAuthorization)] != [2]string{string(AuditResultDenied), string(ErrorCodeForbidden)} {
		t.Fatalf("authorization audit = %#v", got[string(AuditAuthorization)])
	}
}

func TestNewServiceInitializesActiveSessionGaugeFromDatabase(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service, err := NewService(pool, &ValidatedConfig{Config: Config{Environment: EnvironmentDev}, Keyring: serviceTestKeyring(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range service.Metrics().Snapshot() {
		if sample.Name == "relay_control_auth_active_sessions" && sample.Labels["environment"] == string(EnvironmentDev) {
			if sample.Value < 0 {
				t.Fatalf("active session gauge = %v", sample.Value)
			}
			return
		}
	}
	t.Fatal("active session gauge was absent after service startup")
}

func TestNewServiceFailsClosedWhenSessionGaugeCannotLoad(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if _, err = NewService(pool, &ValidatedConfig{Config: Config{Environment: EnvironmentDev}, Keyring: serviceTestKeyring(t)}); err == nil {
		t.Fatal("service startup succeeded without an observable session gauge")
	}
}

func randomTestDigest(t *testing.T, version KeyVersion) Digest {
	t.Helper()
	var sum [32]byte
	if _, err := rand.Read(sum[:]); err != nil {
		t.Fatal(err)
	}
	return Digest{KeyVersion: version, Sum: sum}
}
