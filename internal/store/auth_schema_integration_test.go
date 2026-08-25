package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("CONTROL_DATABASE_TEST_URL"); value != "" {
		return value
	}
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	t.Skip("set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests")
	return ""
}

func connectTestDatabase(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), testDatabaseURL(t))
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func runtimeDatabaseURL(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL"); value != "" {
		return value
	}
	t.Skip("set CONTROL_RUNTIME_DATABASE_TEST_URL to run product-role integration tests")
	return ""
}

func connectRuntimeDatabase(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), runtimeDatabaseURL(t))
	if err != nil {
		t.Fatalf("connect PostgreSQL as product runtime: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate random fixture: %v", err)
	}
	return value
}

func randomLogin(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "_" + hex.EncodeToString(randomBytes(t, 6))
}

func requireRejected(t *testing.T, err error, operation string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", operation)
	}
}

func TestAuthenticationSchemaConstraints(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	for name, statement := range map[string]string{
		"uppercase login":  `INSERT INTO control_admin_users (login_name, display_name) VALUES ('Invalid', 'Test Operator')`,
		"non-local source": `INSERT INTO control_admin_users (login_name, display_name, auth_source) VALUES ('valid_user', 'Test Operator', 'oidc')`,
		"non-super role":   `INSERT INTO control_admin_users (login_name, display_name, role) VALUES ('valid_user', 'Test Operator', 'viewer')`,
		"blank display":    `INSERT INTO control_admin_users (login_name, display_name) VALUES ('valid_user', ' ')`,
	} {
		_, err := tx.Exec(ctx, "SAVEPOINT constraint_case")
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, statement)
		requireRejected(t, err, name)
		if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT constraint_case"); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
	}

	login := randomLogin(t, "duplicate")
	if _, err := tx.Exec(ctx, `INSERT INTO control_admin_users (login_name, display_name) VALUES ($1, 'First Operator')`, login); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO control_admin_users (login_name, display_name) VALUES ($1, 'Second Operator')`, login)
	requireRejected(t, err, "duplicate login")
}

func TestBootstrapCompletionCannotBeReopened(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	var adminID string
	err = tx.QueryRow(ctx,
		`INSERT INTO control_admin_users (login_name, display_name) VALUES ($1, 'Bootstrap Operator') RETURNING admin_id::text`,
		randomLogin(t, "bootstrap"),
	).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE control_bootstrap_state SET state='in_progress', pending_admin_id=$1, started_at=CURRENT_TIMESTAMP WHERE singleton_id=1`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE control_bootstrap_state SET state='completed', pending_admin_id=NULL, completed_at=CURRENT_TIMESTAMP WHERE singleton_id=1`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE control_bootstrap_state SET state='required', started_at=NULL, completed_at=NULL WHERE singleton_id=1`)
	requireRejected(t, err, "reopen completed bootstrap")
}

func TestLastEnabledAdministratorIsProtected(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err = tx.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE control_admin_users SET status='disabled', disabled_at=CURRENT_TIMESTAMP WHERE status='enabled'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`); err != nil {
		t.Fatal(err)
	}

	var adminID string
	err = tx.QueryRow(ctx,
		`INSERT INTO control_admin_users (login_name, display_name, status, activated_at)
		 VALUES ($1, 'Only Operator', 'enabled', CURRENT_TIMESTAMP) RETURNING admin_id::text`,
		randomLogin(t, "only"),
	).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx,
		`UPDATE control_admin_users SET status='disabled', disabled_at=CURRENT_TIMESTAMP WHERE admin_id=$1`,
		adminID,
	)
	requireRejected(t, err, "disable last enabled administrator")
}

func TestRuntimeRoleCanDisableSecondAdministratorButNotSafetyGuardOrLastAdministrator(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	// Isolate the last-administrator invariant inside this rollback-only fixture.
	if _, err = tx.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE control_admin_users SET status='disabled', disabled_at=CURRENT_TIMESTAMP WHERE status='enabled'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`); err != nil {
		t.Fatal(err)
	}
	var securityDefiner, fixedSearchPath, safeOwner bool
	if err = tx.QueryRow(ctx, `SELECT p.prosecdef,
		COALESCE('search_path=pg_catalog'=ANY(p.proconfig), false),
		pg_get_userbyid(p.proowner) <> 'relay_control_runtime'
		FROM pg_proc AS p
		JOIN pg_namespace AS n ON n.oid=p.pronamespace
		WHERE n.nspname='public' AND p.proname='control_protect_enabled_admin'`).Scan(&securityDefiner, &fixedSearchPath, &safeOwner); err != nil {
		t.Fatal(err)
	}
	if !securityDefiner || !fixedSearchPath || !safeOwner {
		t.Fatalf("unsafe trigger function metadata: security_definer=%v fixed_search_path=%v safe_owner=%v", securityDefiner, fixedSearchPath, safeOwner)
	}

	var firstID, secondID string
	if err = tx.QueryRow(ctx, `INSERT INTO control_admin_users (login_name, display_name, status, activated_at)
		VALUES ($1, 'Runtime First Operator', 'enabled', CURRENT_TIMESTAMP) RETURNING admin_id::text`,
		randomLogin(t, "runtime_first")).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO control_admin_users (login_name, display_name, status, activated_at)
		VALUES ($1, 'Runtime Second Operator', 'enabled', CURRENT_TIMESTAMP) RETURNING admin_id::text`,
		randomLogin(t, "runtime_second")).Scan(&secondID); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `SET LOCAL ROLE relay_control_runtime`); err != nil {
		t.Fatalf("assume runtime capability role: %v", err)
	}
	var canSelectGuard, canUpdateGuard bool
	if err = tx.QueryRow(ctx, `SELECT
		has_table_privilege(current_user, 'public.control_admin_safety_guard', 'SELECT'),
		has_table_privilege(current_user, 'public.control_admin_safety_guard', 'UPDATE')`).Scan(&canSelectGuard, &canUpdateGuard); err != nil {
		t.Fatal(err)
	}
	if canSelectGuard || canUpdateGuard {
		t.Fatalf("runtime unexpectedly has direct safety-guard privileges: select=%v update=%v", canSelectGuard, canUpdateGuard)
	}

	if _, err = tx.Exec(ctx, `UPDATE control_admin_users
		SET status='disabled', disabled_at=CURRENT_TIMESTAMP WHERE admin_id=$1`, secondID); err != nil {
		t.Fatalf("runtime disable second administrator: %v", err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT runtime_last_admin`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE control_admin_users
		SET status='disabled', disabled_at=CURRENT_TIMESTAMP WHERE admin_id=$1`, firstID)
	requireRejected(t, err, "runtime disable last enabled administrator")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("runtime last-administrator SQLSTATE = %v, want check violation 23514", err)
	}
	if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT runtime_last_admin`); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
}

func TestRecoveryAndActivationTokensAreConsumedOnce(t *testing.T) {
	databaseURL := testDatabaseURL(t)
	ctx := context.Background()
	setup, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close(ctx)

	var creatorID, pendingID string
	creatorLogin := randomLogin(t, "creator")
	pendingLogin := randomLogin(t, "pending")
	if err = setup.QueryRow(ctx,
		`INSERT INTO control_admin_users (login_name, display_name, status, activated_at)
		 VALUES ($1, 'Creator Operator', 'enabled', CURRENT_TIMESTAMP) RETURNING admin_id::text`,
		creatorLogin,
	).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	if err = setup.QueryRow(ctx,
		`INSERT INTO control_admin_users (login_name, display_name)
		 VALUES ($1, 'Pending Operator') RETURNING admin_id::text`,
		pendingLogin,
	).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}

	// This suite targets disposable integration databases. Temporarily disabling the
	// last-admin trigger in cleanup is limited to the migration-owner test connection.
	t.Cleanup(func() {
		_, _ = setup.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = setup.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id IN ($1, $2)`, creatorID, pendingID)
		_, _ = setup.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	recoveryDigest := randomBytes(t, 32)
	activationDigest := randomBytes(t, 32)
	if _, err = setup.Exec(ctx,
		`INSERT INTO control_admin_recovery_codes (admin_id, batch_id, code_digest, key_version)
		 VALUES ($1, gen_random_uuid(), $2, 1)`, creatorID, recoveryDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = setup.Exec(ctx,
		`INSERT INTO control_admin_activation_tokens
		 (admin_id, created_by_admin_id, token_digest, key_version, expires_at)
		 VALUES ($1, $2, $3, 1, CURRENT_TIMESTAMP + interval '24 hours')`,
		pendingID, creatorID, activationDigest); err != nil {
		t.Fatal(err)
	}

	testConcurrentConsume := func(t *testing.T, statement string, digest []byte) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan int64, 2)
		errors := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				conn, connectErr := pgx.Connect(ctx, databaseURL)
				if connectErr != nil {
					errors <- connectErr
					return
				}
				defer conn.Close(ctx)
				<-start
				tag, execErr := conn.Exec(ctx, statement, digest)
				if execErr != nil {
					errors <- execErr
					return
				}
				results <- tag.RowsAffected()
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		close(errors)
		for workerErr := range errors {
			if workerErr != nil {
				t.Fatal(workerErr)
			}
		}
		var succeeded int64
		for affected := range results {
			succeeded += affected
		}
		if succeeded != 1 {
			t.Fatalf("concurrent consumption changed %d rows, want exactly 1", succeeded)
		}
	}

	t.Run("recovery code", func(t *testing.T) {
		testConcurrentConsume(t,
			`UPDATE control_admin_recovery_codes SET consumed_at=CURRENT_TIMESTAMP
			 WHERE key_version=1 AND code_digest=$1 AND consumed_at IS NULL AND revoked_at IS NULL`,
			recoveryDigest,
		)
	})
	t.Run("activation token", func(t *testing.T) {
		testConcurrentConsume(t,
			`UPDATE control_admin_activation_tokens SET consumed_at=CURRENT_TIMESTAMP
			 WHERE key_version=1 AND token_digest=$1 AND consumed_at IS NULL
			 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP`,
			activationDigest,
		)
	})
}

func TestAuditLogsAreImmutableForProductSQL(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	var auditID string
	err = tx.QueryRow(ctx,
		`INSERT INTO audit_logs (category, action, result, request_id)
		 VALUES ('session', 'session.create', 'success', $1) RETURNING audit_id::text`,
		"schema-test-"+hex.EncodeToString(randomBytes(t, 6)),
	).Scan(&auditID)
	if err != nil {
		t.Fatal(err)
	}

	for name, statement := range map[string]string{
		"update":   `UPDATE audit_logs SET result='failure' WHERE audit_id=$1`,
		"delete":   `DELETE FROM audit_logs WHERE audit_id=$1`,
		"truncate": `TRUNCATE audit_logs`,
	} {
		if _, err = tx.Exec(ctx, "SAVEPOINT audit_mutation"); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, statement, auditID)
		requireRejected(t, err, name+" audit log")
		if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT audit_mutation"); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
	}
}

func TestAuthenticationTimestampsUseTimeZoneAwareColumns(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()

	var timezone string
	if err := conn.QueryRow(ctx, `SHOW TIME ZONE`).Scan(&timezone); err != nil {
		t.Fatal(err)
	}
	if timezone != "UTC" {
		t.Fatalf("database test connection timezone = %q, want UTC", timezone)
	}

	var wrongTypeCount int
	err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name IN (
		    'control_bootstrap_state', 'control_admin_users', 'control_admin_passwords',
		    'control_admin_totp', 'control_admin_recovery_codes',
		    'control_admin_activation_tokens', 'control_auth_challenges',
		    'control_admin_sessions', 'control_auth_failure_windows', 'audit_logs'
		  )
		  AND column_name LIKE '%\_at' ESCAPE '\'
		  AND data_type <> 'timestamp with time zone'`).Scan(&wrongTypeCount)
	if err != nil {
		t.Fatal(err)
	}
	if wrongTypeCount != 0 {
		t.Fatalf("found %d authentication timestamp columns without time zone", wrongTypeCount)
	}
}

func TestAuthenticationSchemaIsPresent(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var migrationVersion int64
	err := conn.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&migrationVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if migrationVersion < 2 {
		t.Fatalf("database migration version = %d, want at least 2", migrationVersion)
	}
}

func TestLockActiveActivationTokenByDigestFiltersStateAndLocks(t *testing.T) {
	databaseURL := testDatabaseURL(t)
	ctx := context.Background()
	setup, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close(ctx)

	var creatorID pgtype.UUID
	if err = setup.QueryRow(ctx,
		`INSERT INTO control_admin_users (login_name, display_name, status, activated_at)
		 VALUES ($1, 'Activation Creator', 'enabled', CURRENT_TIMESTAMP) RETURNING admin_id`,
		randomLogin(t, "lock_creator"),
	).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}

	type tokenFixture struct {
		adminID pgtype.UUID
		digest  []byte
	}
	fixtures := make(map[string]tokenFixture, 4)
	for _, state := range []string{"valid", "expired", "revoked", "consumed"} {
		var adminID pgtype.UUID
		if err = setup.QueryRow(ctx,
			`INSERT INTO control_admin_users (login_name, display_name)
			 VALUES ($1, 'Activation Target') RETURNING admin_id`,
			randomLogin(t, "lock_"+state),
		).Scan(&adminID); err != nil {
			t.Fatal(err)
		}
		digest := randomBytes(t, 32)
		switch state {
		case "valid":
			_, err = setup.Exec(ctx, `
				INSERT INTO control_admin_activation_tokens
				    (admin_id, created_by_admin_id, token_digest, key_version, expires_at)
				VALUES ($1, $2, $3, 1, CURRENT_TIMESTAMP + interval '1 hour')`,
				adminID, creatorID, digest)
		case "expired":
			_, err = setup.Exec(ctx, `
				INSERT INTO control_admin_activation_tokens
				    (admin_id, created_by_admin_id, token_digest, key_version, created_at, expires_at)
				VALUES ($1, $2, $3, 1, CURRENT_TIMESTAMP - interval '2 hours', CURRENT_TIMESTAMP - interval '1 hour')`,
				adminID, creatorID, digest)
		case "revoked":
			_, err = setup.Exec(ctx, `
				INSERT INTO control_admin_activation_tokens
				    (admin_id, created_by_admin_id, token_digest, key_version, expires_at, revoked_at)
				VALUES ($1, $2, $3, 1, CURRENT_TIMESTAMP + interval '1 hour', CURRENT_TIMESTAMP)`,
				adminID, creatorID, digest)
		case "consumed":
			_, err = setup.Exec(ctx, `
				INSERT INTO control_admin_activation_tokens
				    (admin_id, created_by_admin_id, token_digest, key_version, expires_at, consumed_at)
				VALUES ($1, $2, $3, 1, CURRENT_TIMESTAMP + interval '1 hour', CURRENT_TIMESTAMP)`,
				adminID, creatorID, digest)
		}
		if err != nil {
			t.Fatal(err)
		}
		fixtures[state] = tokenFixture{adminID: adminID, digest: digest}
	}

	t.Cleanup(func() {
		_, _ = setup.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = setup.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1 OR login_name LIKE 'lock_%'`, creatorID)
		_, _ = setup.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	queries := store.New(setup)
	valid := fixtures["valid"]
	locked, err := queries.LockActiveActivationTokenByDigest(ctx, store.LockActiveActivationTokenByDigestParams{
		KeyVersion:  1,
		TokenDigest: valid.digest,
	})
	if err != nil {
		t.Fatalf("find valid activation token: %v", err)
	}
	if !locked.AdminID.Valid || locked.AdminID.Bytes != valid.adminID.Bytes {
		t.Fatal("valid activation token resolved to wrong administrator")
	}

	for _, state := range []string{"expired", "revoked", "consumed"} {
		fixture := fixtures[state]
		_, err = queries.LockActiveActivationTokenByDigest(ctx, store.LockActiveActivationTokenByDigestParams{
			KeyVersion:  1,
			TokenDigest: fixture.digest,
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s activation token error = %v, want pgx.ErrNoRows", state, err)
		}
	}

	lockTx, err := setup.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lockedQueries := store.New(lockTx)
	if _, err = lockedQueries.LockActiveActivationTokenByDigest(ctx, store.LockActiveActivationTokenByDigestParams{
		KeyVersion:  1,
		TokenDigest: valid.digest,
	}); err != nil {
		_ = lockTx.Rollback(ctx)
		t.Fatal(err)
	}

	contender, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		_ = lockTx.Rollback(ctx)
		t.Fatal(err)
	}
	defer contender.Close(ctx)
	contenderTx, err := contender.Begin(ctx)
	if err != nil {
		_ = lockTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = contenderTx.Exec(ctx, `SET LOCAL lock_timeout = '200ms'`); err != nil {
		_ = contenderTx.Rollback(ctx)
		_ = lockTx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = store.New(contenderTx).ConsumeActivationToken(ctx, store.ConsumeActivationTokenParams{
		KeyVersion:  1,
		TokenDigest: valid.digest,
	})
	if err == nil {
		_ = contenderTx.Rollback(ctx)
		_ = lockTx.Rollback(ctx)
		t.Fatal("activation token consumption was not blocked by FOR UPDATE lock")
	}
	if rollbackErr := contenderTx.Rollback(ctx); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if err = lockTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = queries.ConsumeActivationToken(ctx, store.ConsumeActivationTokenParams{
		KeyVersion:  1,
		TokenDigest: valid.digest,
	}); err != nil {
		t.Fatalf("consume activation token after releasing lock: %v", err)
	}
}

func TestRotateAdminSessionCSRFOnlyUpdatesActiveSession(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()

	var adminID pgtype.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO control_admin_users (login_name, display_name, status, activated_at)
		 VALUES ($1, 'Session Operator', 'enabled', CURRENT_TIMESTAMP) RETURNING admin_id`,
		randomLogin(t, "csrf"),
	).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = conn.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, adminID)
		_, _ = conn.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	type sessionFixture struct {
		id         pgtype.UUID
		csrfDigest []byte
	}
	fixtures := make(map[string]sessionFixture, 3)
	for _, state := range []string{"active", "revoked", "expired"} {
		tokenDigest := randomBytes(t, 32)
		csrfDigest := randomBytes(t, 32)
		var sessionID pgtype.UUID
		var err error
		switch state {
		case "active":
			err = conn.QueryRow(ctx, `
				INSERT INTO control_admin_sessions
				    (admin_id, token_digest, csrf_digest, key_version, mfa_method, absolute_expires_at)
				VALUES ($1, $2, $3, 1, 'none', CURRENT_TIMESTAMP + interval '1 hour')
				RETURNING session_id`, adminID, tokenDigest, csrfDigest).Scan(&sessionID)
		case "revoked":
			err = conn.QueryRow(ctx, `
				INSERT INTO control_admin_sessions
				    (admin_id, token_digest, csrf_digest, key_version, mfa_method,
				     absolute_expires_at, revoked_at, revoke_reason)
				VALUES ($1, $2, $3, 1, 'none', CURRENT_TIMESTAMP + interval '1 hour',
				        CURRENT_TIMESTAMP, 'logout')
				RETURNING session_id`, adminID, tokenDigest, csrfDigest).Scan(&sessionID)
		case "expired":
			err = conn.QueryRow(ctx, `
				INSERT INTO control_admin_sessions
				    (admin_id, token_digest, csrf_digest, key_version, mfa_method,
				     created_at, last_activity_at, absolute_expires_at)
				VALUES ($1, $2, $3, 1, 'none', CURRENT_TIMESTAMP - interval '2 hours',
				        CURRENT_TIMESTAMP - interval '90 minutes', CURRENT_TIMESTAMP - interval '1 hour')
				RETURNING session_id`, adminID, tokenDigest, csrfDigest).Scan(&sessionID)
		}
		if err != nil {
			t.Fatal(err)
		}
		fixtures[state] = sessionFixture{id: sessionID, csrfDigest: csrfDigest}
	}

	queries := store.New(conn)
	active := fixtures["active"]
	newDigest := randomBytes(t, 32)
	rotated, err := queries.RotateAdminSessionCSRF(ctx, store.RotateAdminSessionCSRFParams{
		SessionID:  active.id,
		CsrfDigest: newDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(rotated.CsrfDigest) != string(newDigest) {
		t.Fatal("rotated session did not return the new CSRF digest")
	}
	var oldCount, newCount int
	if err = conn.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE csrf_digest=$2), count(*) FILTER (WHERE csrf_digest=$3)
		 FROM control_admin_sessions WHERE session_id=$1`,
		active.id, active.csrfDigest, newDigest,
	).Scan(&oldCount, &newCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 || newCount != 1 {
		t.Fatalf("CSRF digest counts old=%d new=%d, want old=0 new=1", oldCount, newCount)
	}

	for _, state := range []string{"revoked", "expired"} {
		fixture := fixtures[state]
		_, err = queries.RotateAdminSessionCSRF(ctx, store.RotateAdminSessionCSRFParams{
			SessionID:  fixture.id,
			CsrfDigest: randomBytes(t, 32),
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("rotate %s session error = %v, want pgx.ErrNoRows", state, err)
		}
		var persisted []byte
		if err = conn.QueryRow(ctx,
			`SELECT csrf_digest FROM control_admin_sessions WHERE session_id=$1`, fixture.id,
		).Scan(&persisted); err != nil {
			t.Fatal(err)
		}
		if string(persisted) != string(fixture.csrfDigest) {
			t.Fatalf("%s session CSRF digest changed despite inactive state", state)
		}
	}
}

func TestAuthFailureRollingWindowIsConcurrentAndRestartSafe(t *testing.T) {
	owner := connectTestDatabase(t)
	runtimeURL := runtimeDatabaseURL(t)
	ctx := context.Background()
	fingerprint := randomBytes(t, 32)

	t.Cleanup(func() {
		_, _ = owner.Exec(ctx, `DELETE FROM control_auth_failure_windows
			WHERE dimension='account' AND key_version=1 AND subject_fingerprint=$1`, fingerprint)
	})

	type result struct {
		count   int32
		blocked bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 5)
	var workers sync.WaitGroup
	for range 5 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			conn, err := pgx.Connect(ctx, runtimeURL)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer conn.Close(ctx)
			<-start
			row, err := store.New(conn).RecordAuthFailure(ctx, store.RecordAuthFailureParams{
				Dimension:          "account",
				KeyVersion:         1,
				SubjectFingerprint: fingerprint,
				FailureThreshold:   5,
			})
			results <- result{count: row.FailureCount, blocked: row.BlockedUntil.Valid, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	seenCounts := make(map[int32]int, 5)
	blockedResults := 0
	for workerResult := range results {
		if workerResult.err != nil {
			t.Fatalf("record concurrent authentication failure: %v", workerResult.err)
		}
		seenCounts[workerResult.count]++
		if workerResult.blocked {
			blockedResults++
		}
	}
	for want := int32(1); want <= 5; want++ {
		if seenCounts[want] != 1 {
			t.Fatalf("rolling count %d observed %d times, want once (all counts: %v)", want, seenCounts[want], seenCounts)
		}
	}
	if blockedResults != 1 {
		t.Fatalf("concurrent threshold returned blocked state %d times, want once at count 5", blockedResults)
	}

	// A new product connection models a Control process restart: PostgreSQL state,
	// not process memory, must continue enforcing the block.
	restarted, err := pgx.Connect(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(ctx)
	blocked, err := store.New(restarted).IsAuthFailureSubjectBlocked(ctx, store.IsAuthFailureSubjectBlockedParams{
		Dimension:          "account",
		KeyVersion:         1,
		SubjectFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("rate-limit block was lost after reconnect")
	}

	var eventCount int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM control_auth_failure_events
		WHERE dimension='account' AND key_version=1 AND subject_fingerprint=$1`, fingerprint).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 5 {
		t.Fatalf("persisted failure events = %d, want 5", eventCount)
	}
}

func TestAuthFailureRollingWindowExcludesExpiredBoundary(t *testing.T) {
	owner := connectTestDatabase(t)
	runtime := connectRuntimeDatabase(t)
	ctx := context.Background()
	fingerprint := randomBytes(t, 32)

	t.Cleanup(func() {
		_, _ = owner.Exec(ctx, `DELETE FROM control_auth_failure_windows
			WHERE dimension='source' AND key_version=1 AND subject_fingerprint=$1`, fingerprint)
	})
	if _, err := owner.Exec(ctx, `INSERT INTO control_auth_failure_windows
		(dimension, key_version, subject_fingerprint)
		VALUES ('source', 1, $1)`, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO control_auth_failure_events
			(dimension, key_version, subject_fingerprint, occurred_at)
		VALUES
			('source', 1, $1, clock_timestamp() - interval '15 minutes 1 second'),
			('source', 1, $1, clock_timestamp() - interval '14 minutes 59 seconds')`, fingerprint); err != nil {
		t.Fatal(err)
	}

	queries := store.New(runtime)
	first, err := queries.RecordAuthFailure(ctx, store.RecordAuthFailureParams{
		Dimension:          "source",
		KeyVersion:         1,
		SubjectFingerprint: fingerprint,
		FailureThreshold:   3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.FailureCount != 2 || first.BlockedUntil.Valid {
		t.Fatalf("first rolling result count=%d blocked=%v, want count=2 unblocked", first.FailureCount, first.BlockedUntil.Valid)
	}
	second, err := queries.RecordAuthFailure(ctx, store.RecordAuthFailureParams{
		Dimension:          "source",
		KeyVersion:         1,
		SubjectFingerprint: fingerprint,
		FailureThreshold:   3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.FailureCount != 3 || !second.BlockedUntil.Valid {
		t.Fatalf("second rolling result count=%d blocked=%v, want count=3 blocked", second.FailureCount, second.BlockedUntil.Valid)
	}
	var remaining time.Duration
	if err = owner.QueryRow(ctx, `SELECT blocked_until - clock_timestamp()
		FROM control_auth_failure_windows
		WHERE dimension='source' AND key_version=1 AND subject_fingerprint=$1`, fingerprint).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining < 14*time.Minute+55*time.Second || remaining > 15*time.Minute {
		t.Fatalf("block duration remaining = %v, want approximately 15 minutes", remaining)
	}
}

func TestBootstrapResetAuditUUIDSurvivesPendingAdministratorDeletion(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	// Make the singleton deterministic inside the rollback-only owner transaction.
	if _, err = tx.Exec(ctx, `ALTER TABLE control_bootstrap_state DISABLE TRIGGER control_bootstrap_state_transition_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE control_bootstrap_state
		SET state='required', pending_admin_id=NULL, started_at=NULL, completed_at=NULL,
		    updated_at=CURRENT_TIMESTAMP WHERE singleton_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE control_bootstrap_state ENABLE TRIGGER control_bootstrap_state_transition_guard`); err != nil {
		t.Fatal(err)
	}

	var adminID, auditID pgtype.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO control_admin_users (login_name, display_name)
		VALUES ($1, 'Reset Pending Operator') RETURNING admin_id`, randomLogin(t, "reset_audit")).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE control_bootstrap_state
		SET state='in_progress', pending_admin_id=$1, started_at=CURRENT_TIMESTAMP,
		    updated_at=CURRENT_TIMESTAMP WHERE singleton_id=1`, adminID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO audit_logs
		(category, action, result, target_admin_id, request_id)
		VALUES ('bootstrap', 'bootstrap.start', 'success', $1, $2) RETURNING audit_id`,
		adminID, "bootstrap-reset-"+hex.EncodeToString(randomBytes(t, 6))).Scan(&auditID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE control_bootstrap_state
		SET state='required', pending_admin_id=NULL, started_at=NULL, completed_at=NULL,
		    updated_at=CURRENT_TIMESTAMP
		WHERE singleton_id=1 AND state='in_progress' AND pending_admin_id=$1`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1 AND status='pending'`, adminID); err != nil {
		t.Fatalf("delete pending bootstrap administrator: %v", err)
	}
	var persistedTarget pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT target_admin_id FROM audit_logs WHERE audit_id=$1`, auditID).Scan(&persistedTarget); err != nil {
		t.Fatal(err)
	}
	if !persistedTarget.Valid || persistedTarget.Bytes != adminID.Bytes {
		t.Fatal("audit target UUID did not survive pending administrator deletion")
	}
}

func TestProductRuntimeRoleHasLeastPrivilegeAndCannotMutateAudit(t *testing.T) {
	runtime := connectRuntimeDatabase(t)
	ctx := context.Background()

	var currentUser, tableOwner string
	var canSelect, canInsert, canUpdate, canDelete, canTruncate bool
	if err := runtime.QueryRow(ctx, `
		SELECT current_user,
		       (SELECT tableowner FROM pg_tables WHERE schemaname=current_schema() AND tablename='audit_logs'),
		       has_table_privilege(current_user, 'audit_logs', 'SELECT'),
		       has_table_privilege(current_user, 'audit_logs', 'INSERT'),
		       has_table_privilege(current_user, 'audit_logs', 'UPDATE'),
		       has_table_privilege(current_user, 'audit_logs', 'DELETE'),
		       has_table_privilege(current_user, 'audit_logs', 'TRUNCATE')
	`).Scan(&currentUser, &tableOwner, &canSelect, &canInsert, &canUpdate, &canDelete, &canTruncate); err != nil {
		t.Fatal(err)
	}
	if currentUser == tableOwner {
		t.Fatalf("product runtime %q unexpectedly owns audit_logs", currentUser)
	}
	if !canSelect || !canInsert || canUpdate || canDelete || canTruncate {
		t.Fatalf("audit privileges select=%v insert=%v update=%v delete=%v truncate=%v",
			canSelect, canInsert, canUpdate, canDelete, canTruncate)
	}

	var auditID pgtype.UUID
	if err := runtime.QueryRow(ctx, `INSERT INTO audit_logs
		(category, action, result, request_id)
		VALUES ('session', 'session.create', 'success', $1) RETURNING audit_id`,
		"runtime-role-"+hex.EncodeToString(randomBytes(t, 6))).Scan(&auditID); err != nil {
		t.Fatalf("runtime INSERT audit: %v", err)
	}
	var selected pgtype.UUID
	if err := runtime.QueryRow(ctx, `SELECT audit_id FROM audit_logs WHERE audit_id=$1`, auditID).Scan(&selected); err != nil {
		t.Fatalf("runtime SELECT audit: %v", err)
	}

	for name, testCase := range map[string]struct {
		statement string
		args      []any
	}{
		"update":          {statement: `UPDATE audit_logs SET result='failure' WHERE audit_id=$1`, args: []any{auditID}},
		"delete":          {statement: `DELETE FROM audit_logs WHERE audit_id=$1`, args: []any{auditID}},
		"truncate":        {statement: `TRUNCATE audit_logs`},
		"disable trigger": {statement: `ALTER TABLE audit_logs DISABLE TRIGGER audit_logs_reject_update_delete`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runtime.Exec(ctx, testCase.statement, testCase.args...)
			requireRejected(t, err, "runtime "+name+" audit")
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("runtime %s SQLSTATE = %v, want insufficient_privilege 42501", name, err)
			}
		})
	}
}
