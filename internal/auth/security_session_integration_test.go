package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestSessionLookupRetainsOldKeyAndRejectsExpiredAndRevokedRows(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	keyring := testKeyring(t, EnvironmentDev, 2, 1, 2)
	service, err := NewService(pool, &ValidatedConfig{Config: Config{Environment: EnvironmentDev}, Keyring: keyring})
	if err != nil {
		t.Fatal(err)
	}
	queries := store.New(pool)
	adminID := uuid.New()
	login := "old_key_" + adminID.String()[:8]
	admin, err := queries.CreateAdminUser(ctx, store.CreateAdminUserParams{AdminID: pgUUID(adminID), LoginName: login, DisplayName: "Old Key Operator"})
	if err != nil {
		t.Fatal(err)
	}
	if admin, err = queries.ActivateAdminUser(ctx, admin.AdminID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_admin_id=$1 OR target_admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_sessions WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	createOldKeySession := func(t *testing.T, token string) uuid.UUID {
		t.Helper()
		tokenDigest, digestErr := service.digestForVersion(1, DomainSessionDigest, token)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		csrfDigest, digestErr := service.digestForVersion(1, DomainCSRFDigest, "csrf-"+token)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		sessionID := uuid.New()
		_, createErr := queries.CreateAdminSession(ctx, store.CreateAdminSessionParams{
			SessionID: pgUUID(sessionID), AdminID: admin.AdminID, TokenDigest: tokenDigest.Sum[:],
			CsrfDigest: csrfDigest.Sum[:], KeyVersion: 1, MfaMethod: string(MFAMethodNone),
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return sessionID
	}

	activeToken := "retained-old-key-session-" + adminID.String()
	activeID := createOldKeySession(t, activeToken)
	active, err := service.Authenticate(ctx, activeToken)
	if err != nil {
		t.Fatalf("retained old-key session rejected: %v", err)
	}
	if active.ID != activeID || active.KeyVersion != 1 || active.AdminID != adminID {
		t.Fatalf("old-key session mismatch: %+v", active)
	}

	revokedToken := "revoked-old-key-session-" + adminID.String()
	revokedID := createOldKeySession(t, revokedToken)
	if _, err = queries.RevokeAdminSession(ctx, store.RevokeAdminSessionParams{SessionID: pgUUID(revokedID), RevokeReason: pgtype.Text{String: string(SessionRevokeLogout), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(ctx, revokedToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked session error=%v", err)
	}

	expiredToken := "expired-old-key-session-" + adminID.String()
	expiredID := createOldKeySession(t, expiredToken)
	if _, err = pool.Exec(ctx, `UPDATE control_admin_sessions SET created_at=CURRENT_TIMESTAMP-interval '13 hours', last_activity_at=CURRENT_TIMESTAMP-interval '13 hours', absolute_expires_at=CURRENT_TIMESTAMP-interval '1 hour' WHERE session_id=$1`, pgUUID(expiredID)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(ctx, expiredToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session error=%v", err)
	}

	idleToken := "idle-old-key-session-" + adminID.String()
	idleID := createOldKeySession(t, idleToken)
	if _, err = pool.Exec(ctx, `UPDATE control_admin_sessions SET created_at=CURRENT_TIMESTAMP-interval '1 hour', last_activity_at=CURRENT_TIMESTAMP-interval '31 minutes', absolute_expires_at=CURRENT_TIMESTAMP+interval '11 hours' WHERE session_id=$1`, pgUUID(idleID)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(ctx, idleToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("idle-expired session error=%v", err)
	}
}

func TestHighRiskOperationsRequireDatabaseFreshReauthentication(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL")
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
	queries := store.New(pool)
	adminID := uuid.New()
	admin, err := queries.CreateAdminUser(ctx, store.CreateAdminUserParams{AdminID: pgUUID(adminID), LoginName: "stale_" + adminID.String()[:8], DisplayName: "Stale Operator"})
	if err != nil {
		t.Fatal(err)
	}
	if admin, err = queries.ActivateAdminUser(ctx, admin.AdminID); err != nil {
		t.Fatal(err)
	}
	stale, err := service.createSession(ctx, queries, admin, MFAMethodTOTP, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE control_admin_sessions SET created_at=CURRENT_TIMESTAMP-interval '1 hour', absolute_expires_at=CURRENT_TIMESTAMP+interval '11 hours', reauthenticated_at=CURRENT_TIMESTAMP-interval '6 minutes' WHERE session_id=$1`, pgUUID(stale.ID)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_admin_id=$1 OR target_admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_sessions WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	meta := RequestMeta{RequestID: "stale-high-risk-" + uuid.NewString(), SourceFingerprint: randomTestDigest(t, 1)}
	_, err = service.CreateAdmin(ctx, stale, "should_not_exist_"+adminID.String()[:8], "Rejected Operator", "security regression test", meta)
	if !errors.Is(err, ErrReauthenticate) {
		t.Fatalf("stale reauthentication error=%v", err)
	}
	var count int
	if queryErr := pool.QueryRow(ctx, `SELECT count(*) FROM control_admin_users WHERE login_name=$1`, "should_not_exist_"+adminID.String()[:8]).Scan(&count); queryErr != nil {
		t.Fatal(queryErr)
	}
	if count != 0 {
		t.Fatal("high-risk mutation committed with stale database reauthentication")
	}

	_, err = service.DisableAdmin(ctx, stale, adminID, "security regression test", meta)
	var serviceErr *ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != "self_disable_forbidden" {
		t.Fatalf("self-disable error=%v", err)
	}

	// The in-memory timestamp must not override database truth in the other direction.
	future := time.Now().Add(time.Hour)
	stale.ReauthenticatedAt = &future
	_, err = service.CreateAdmin(ctx, stale, "still_rejected_"+adminID.String()[:8], "Rejected Operator", "security regression test", meta)
	if !errors.Is(err, ErrReauthenticate) {
		t.Fatalf("forged in-memory freshness error=%v", err)
	}
}

func TestMFAChallengeBindsSourceRejectsExpiryReplayAndRetainsOldKey(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	keyring := testKeyring(t, EnvironmentDev, 2, 1, 2)
	service, err := NewService(pool, &ValidatedConfig{Config: Config{Environment: EnvironmentDev, MFARequired: true}, Keyring: keyring})
	if err != nil {
		t.Fatal(err)
	}
	queries := store.New(pool)
	adminID := uuid.New()
	admin, err := queries.CreateAdminUser(ctx, store.CreateAdminUserParams{AdminID: pgUUID(adminID), LoginName: "challenge_" + adminID.String()[:8], DisplayName: "Challenge Operator"})
	if err != nil {
		t.Fatal(err)
	}
	if admin, err = queries.ActivateAdminUser(ctx, admin.AdminID); err != nil {
		t.Fatal(err)
	}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := EncryptTOTPSecret(keyring, adminID.String(), []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = queries.UpsertAdminTOTP(ctx, store.UpsertAdminTOTPParams{AdminID: admin.AdminID, EncryptedSecret: encrypted.Ciphertext, Nonce: encrypted.Nonce, KeyVersion: int32(encrypted.KeyVersion)}); err != nil {
		t.Fatal(err)
	}
	if _, err = queries.ConfirmAdminTOTP(ctx, store.ConfirmAdminTOTPParams{AdminID: admin.AdminID}); err != nil {
		t.Fatal(err)
	}
	sourceA, sourceB := randomTestDigest(t, 2), randomTestDigest(t, 2)
	requestPrefix := "challenge-security-" + adminID.String()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_admin_id=$1 OR target_admin_id=$1 OR request_id LIKE $2`, pgUUID(adminID), requestPrefix+"%")
		_, _ = pool.Exec(ctx, `DELETE FROM control_auth_failure_windows WHERE subject_fingerprint IN ($1,$2)`, sourceA.Sum[:], sourceB.Sum[:])
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_sessions WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `DELETE FROM control_auth_challenges WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	createChallenge := func(t *testing.T, label string, source Digest) (string, uuid.UUID) {
		t.Helper()
		token := label + "-" + uuid.NewString()
		digest, digestErr := service.digestForVersion(1, DomainChallengeDigest, token)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		challengeID := uuid.New()
		_, createErr := queries.CreateAuthChallenge(ctx, store.CreateAuthChallengeParams{ChallengeID: pgUUID(challengeID), AdminID: admin.AdminID, TokenDigest: digest.Sum[:], KeyVersion: 1, SourceFingerprint: source.Sum[:]})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return token, challengeID
	}

	token, _ := createChallenge(t, "old-key", sourceA)
	databaseTime, err := queries.GetDatabaseTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateTOTP(secret, databaseTime.Time.UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteLoginMFA(ctx, token, MFAMethodTOTP, code, RequestMeta{RequestID: requestPrefix + "-wrong-source", SourceFingerprint: sourceB}); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("cross-source challenge error=%v", err)
	}
	session, err := service.CompleteLoginMFA(ctx, token, MFAMethodTOTP, code, RequestMeta{RequestID: requestPrefix + "-success", SourceFingerprint: sourceA})
	if err != nil {
		t.Fatalf("retained old-key challenge failed: %v", err)
	}
	if session.AdminID != adminID || session.MFAMethod != MFAMethodTOTP {
		t.Fatalf("challenge session=%+v", session)
	}
	if _, err = service.CompleteLoginMFA(ctx, token, MFAMethodTOTP, code, RequestMeta{RequestID: requestPrefix + "-replay", SourceFingerprint: sourceA}); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("replayed challenge error=%v", err)
	}

	expiredToken, expiredID := createChallenge(t, "expired", sourceA)
	if _, err = pool.Exec(ctx, `UPDATE control_auth_challenges SET created_at=CURRENT_TIMESTAMP-interval '10 minutes', expires_at=CURRENT_TIMESTAMP-interval '5 minutes' WHERE challenge_id=$1`, pgUUID(expiredID)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteLoginMFA(ctx, expiredToken, MFAMethodTOTP, code, RequestMeta{RequestID: requestPrefix + "-expired", SourceFingerprint: sourceA}); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expired challenge error=%v", err)
	}
}

func TestUnknownAndExistingAccountsShareAuthenticationFailureOutcome(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	keyring := serviceTestKeyring(t)
	service, err := NewService(pool, &ValidatedConfig{Config: Config{Environment: EnvironmentDev, MFARequired: true}, Keyring: keyring})
	if err != nil {
		t.Fatal(err)
	}
	queries := store.New(pool)
	adminID := uuid.New()
	knownLogin := "known_" + adminID.String()[:8]
	unknownLogin := "unknown_" + adminID.String()[:8]
	admin, err := queries.CreateAdminUser(ctx, store.CreateAdminUserParams{AdminID: pgUUID(adminID), LoginName: knownLogin, DisplayName: "Known Operator"})
	if err != nil {
		t.Fatal(err)
	}
	if admin, err = queries.ActivateAdminUser(ctx, admin.AdminID); err != nil {
		t.Fatal(err)
	}
	passwordPHC, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = queries.UpsertAdminPassword(ctx, store.UpsertAdminPasswordParams{AdminID: admin.AdminID, PasswordPhc: passwordPHC, ParameterVersion: 1}); err != nil {
		t.Fatal(err)
	}
	source := randomTestDigest(t, 1)
	knownDigest, _ := LoginFingerprint(keyring, knownLogin)
	unknownDigest, _ := LoginFingerprint(keyring, unknownLogin)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_admin_id=$1 OR request_id IN ($2,$3)`, pgUUID(adminID), "enumeration-known-"+adminID.String(), "enumeration-unknown-"+adminID.String())
		_, _ = pool.Exec(ctx, `DELETE FROM control_auth_failure_windows WHERE subject_fingerprint IN ($1,$2,$3)`, knownDigest.Sum[:], unknownDigest.Sum[:], source.Sum[:])
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = pool.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, pgUUID(adminID))
		_, _ = pool.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	knownResult, knownErr := service.Login(ctx, knownLogin, "wrong password material", RequestMeta{RequestID: "enumeration-known-" + adminID.String(), SourceFingerprint: source})
	unknownResult, unknownErr := service.Login(ctx, unknownLogin, "wrong password material", RequestMeta{RequestID: "enumeration-unknown-" + adminID.String(), SourceFingerprint: source})
	if !errors.Is(knownErr, ErrAuthentication) || !errors.Is(unknownErr, ErrAuthentication) {
		t.Fatalf("outcomes diverged: known=%v unknown=%v", knownErr, unknownErr)
	}
	if knownResult.Challenge != nil || knownResult.Session != nil || unknownResult.Challenge != nil || unknownResult.Session != nil {
		t.Fatalf("failure leaked result state: known=%+v unknown=%+v", knownResult, unknownResult)
	}
	for _, requestID := range []string{"enumeration-known-" + adminID.String(), "enumeration-unknown-" + adminID.String()} {
		var action, result string
		if err = pool.QueryRow(ctx, `SELECT action,result FROM audit_logs WHERE request_id=$1`, requestID).Scan(&action, &result); err != nil {
			t.Fatal(err)
		}
		if action != string(AuditLoginPassword) || result != string(AuditResultFailure) {
			t.Fatalf("audit outcome for %s = %s/%s", requestID, action, result)
		}
	}
}
