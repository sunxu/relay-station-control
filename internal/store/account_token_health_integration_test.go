package store_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

type accountTokenHealthRow struct {
	AccountKey         string
	TokenState         string
	ExpectedValidUntil *time.Time
}

func queryAccountTokenHealth(t *testing.T, database *isolatedJobDatabase, instanceID uuid.UUID, accountKeys ...string) []accountTokenHealthRow {
	t.Helper()
	rows, err := database.runtime.Query(context.Background(), `
		SELECT account_key, token_state, expected_valid_until
		FROM public.control_query_account_token_health_v1($1,$2::text[])
		ORDER BY account_key`, instanceID, accountKeys)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]accountTokenHealthRow, 0, len(accountKeys))
	for rows.Next() {
		var row accountTokenHealthRow
		if err := rows.Scan(&row.AccountKey, &row.TokenState, &row.ExpectedValidUntil); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func requireTokenHealth(t *testing.T, database *isolatedJobDatabase, instanceID uuid.UUID, accountKey, state string) accountTokenHealthRow {
	t.Helper()
	rows := queryAccountTokenHealth(t, database, instanceID, accountKey)
	if len(rows) != 1 || rows[0].AccountKey != accountKey || rows[0].TokenState != state {
		t.Fatalf("token health = %+v, want account=%s state=%s", rows, accountKey, state)
	}
	return rows[0]
}

func setTokenRefreshAt(t *testing.T, database *isolatedJobDatabase, instanceID uuid.UUID, accountKey, offset, wantState string) *time.Time {
	t.Helper()
	ctx := context.Background()
	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('phase5_token.instance_id',$1,true), set_config('phase5_token.account_key',$2,true), set_config('phase5_token.offset',$3,true), set_config('phase5_token.want_state',$4,true)`, instanceID.String(), accountKey, offset, wantState); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE account_inventory DISABLE TRIGGER USER; ALTER TABLE account_inventory_provider_states DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	// UPDATE and the projection query must be separate commands inside this
	// DO block. A data-modifying CTE joined to a STABLE function uses the
	// statement snapshot from before the UPDATE and cannot observe the change.
	if _, err := tx.Exec(ctx, `DO $$
	DECLARE
		v_instance_id uuid := current_setting('phase5_token.instance_id')::uuid;
		v_account_key text := current_setting('phase5_token.account_key');
		v_want_state text := current_setting('phase5_token.want_state');
		v_got_state text;
		v_got_expected timestamptz;
		v_raw_refresh timestamptz;
	BEGIN
		UPDATE account_inventory
		SET last_refresh_at = statement_timestamp() + current_setting('phase5_token.offset')::interval
		WHERE account_inventory.instance_id=v_instance_id
		  AND account_inventory.account_key=v_account_key;

		SELECT token_state, expected_valid_until
		INTO v_got_state, v_got_expected
		FROM public.control_query_account_token_health_v1(v_instance_id, ARRAY[v_account_key]::text[]);
		SELECT ai.last_refresh_at INTO v_raw_refresh
		FROM account_inventory
		AS ai
		WHERE ai.instance_id=v_instance_id
		  AND ai.account_key=v_account_key;

		IF v_got_state IS DISTINCT FROM v_want_state THEN
			RAISE EXCEPTION 'token state = %, want %', v_got_state, v_want_state;
		END IF;
		IF v_got_expected IS DISTINCT FROM v_raw_refresh + interval '3599 seconds' THEN
			RAISE EXCEPTION 'expected_valid_until = %, want refresh + 3599 seconds', v_got_expected;
		END IF;
	END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE account_inventory_provider_states ENABLE TRIGGER USER; ALTER TABLE account_inventory ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var expected *time.Time
	if err := database.owner.QueryRow(ctx, `SELECT last_refresh_at + interval '3599 seconds' FROM account_inventory WHERE instance_id=$1 AND account_key=$2`, instanceID, accountKey).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	return expected
}

func setTokenRefreshNull(t *testing.T, database *isolatedJobDatabase, instanceID uuid.UUID, accountKey string) {
	t.Helper()
	execInventoryOwnerMutation(t, database, `UPDATE account_inventory SET last_refresh_at=NULL WHERE instance_id=$1 AND account_key=$2`, instanceID, accountKey)
}

func setAvailabilityEvidenceNull(t *testing.T, database *isolatedJobDatabase, instanceID uuid.UUID, accountKey string) {
	t.Helper()
	execInventoryOwnerMutation(t, database, `UPDATE account_inventory SET availability_runtime_evidence=NULL WHERE instance_id=$1 AND account_key=$2`, instanceID, accountKey)
}

func execInventoryOwnerMutation(t *testing.T, database *isolatedJobDatabase, statement string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `ALTER TABLE account_inventory DISABLE TRIGGER USER; ALTER TABLE account_inventory_provider_states DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE account_inventory_provider_states ENABLE TRIGGER USER; ALTER TABLE account_inventory ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func activateTokenInvalid(t *testing.T, fixture *availabilityFixture) {
	t.Helper()
	base := fixture.now.Add(-2 * time.Minute)
	fixture.event(t, 0, "token-invalid-1", "token-invalid-1", "token_invalid", base)
	fixture.event(t, 0, "token-invalid-2", "token-invalid-2", "token_invalid", base.Add(time.Second))
	fixture.reconcile(t)
	fixture.state(t, 0, "TOKEN_INVALID")
}

func resolveTokenInvalid(t *testing.T, fixture *availabilityFixture) {
	t.Helper()
	fixture.event(t, 0, "token-recovery", "token-recovery", "", fixture.now)
	fixture.reconcile(t)
	fixture.state(t, 0, "AVAILABLE")
}

func TestAccountTokenHealthProjectionTTLAndExpectedValidUntilPostgres(t *testing.T) {
	fixture := newAvailabilityFixture(t, 1)
	key := fixture.keys[0]

	for _, tc := range []struct {
		name   string
		offset string
		state  string
	}{
		{name: "equal database now", offset: "0 seconds", state: "VALID"},
		{name: "3598.9 seconds", offset: "-3598.9 seconds", state: "VALID"},
		{name: "exact ttl", offset: "-3599 seconds", state: "UNKNOWN"},
		{name: "expired", offset: "-3600 seconds", state: "UNKNOWN"},
		{name: "future", offset: "+1 second", state: "UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := setTokenRefreshAt(t, fixture.db, fixture.node, key, tc.offset, tc.state)
			// State was asserted at the exact DB time inside the DO statement.
			// A later runtime read may legitimately cross the 3598.9s/future
			// boundary; only the diagnostic timestamp must remain unchanged.
			rows := queryAccountTokenHealth(t, fixture.db, fixture.node, key)
			if len(rows) != 1 {
				t.Fatalf("expected one token row, got %d", len(rows))
			}
			row := rows[0]
			if expected == nil || row.ExpectedValidUntil == nil || !row.ExpectedValidUntil.Equal(*expected) {
				t.Fatalf("expected_valid_until=%v helper=%v row=%v", row.ExpectedValidUntil, expected, row)
			}
		})
	}

	setTokenRefreshNull(t, fixture.db, fixture.node, key)
	row := requireTokenHealth(t, fixture.db, fixture.node, key, "UNKNOWN")
	if row.ExpectedValidUntil != nil {
		t.Fatalf("NULL refresh returned expected_valid_until=%v", row.ExpectedValidUntil)
	}
}

func TestAccountTokenHealthActiveInvalidPrecedenceAndRecoveryPostgres(t *testing.T) {
	fixture := newAvailabilityFixture(t, 1)
	key := fixture.keys[0]
	activateTokenInvalid(t, fixture)
	setTokenRefreshAt(t, fixture.db, fixture.node, key, "0 seconds", "INVALID")
	row := requireTokenHealth(t, fixture.db, fixture.node, key, "INVALID")
	if row.ExpectedValidUntil == nil {
		t.Fatal("INVALID projection hid expected_valid_until")
	}
	setTokenRefreshAt(t, fixture.db, fixture.node, key, "+1 hour", "INVALID")
	setTokenRefreshAt(t, fixture.db, fixture.node, key, "-3600 seconds", "INVALID")
	setTokenRefreshNull(t, fixture.db, fixture.node, key)
	if requireTokenHealth(t, fixture.db, fixture.node, key, "INVALID").ExpectedValidUntil != nil {
		t.Fatal("null refresh must remain null even with ACTIVE invalid")
	}
	setTokenRefreshAt(t, fixture.db, fixture.node, key, "0 seconds", "INVALID")

	resolveTokenInvalid(t, fixture)
	requireTokenHealth(t, fixture.db, fixture.node, key, "VALID")
}

func TestAccountTokenHealthAvailabilityOtherReasonsDoNotImplyInvalidPostgres(t *testing.T) {
	for _, reason := range []string{"account_blocked", "forbidden"} {
		t.Run(reason, func(t *testing.T) {
			fixture := newAvailabilityFixture(t, 1)
			key := fixture.keys[0]
			base := fixture.now.Add(-time.Minute)
			fixture.event(t, 0, reason+"-1", reason+"-1", reason, base)
			fixture.event(t, 0, reason+"-2", reason+"-2", reason, base.Add(time.Second))
			fixture.reconcile(t)
			setTokenRefreshAt(t, fixture.db, fixture.node, key, "0 seconds", "VALID")
			requireTokenHealth(t, fixture.db, fixture.node, key, "VALID")
		})
	}
}

func TestAccountTokenHealthQualificationGatesRemainConservativePostgres(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *availabilityFixture)
	}{
		{
			name: "stale source",
			setup: func(t *testing.T, f *availabilityFixture) {
				f.clock(t, "file_active", nil, f.now.Add(-16*time.Minute), f.now.Add(-16*time.Minute).Truncate(5*time.Minute))
			},
		},
		{
			name: "provider degraded",
			setup: func(t *testing.T, f *availabilityFixture) {
				execInventoryOwnerMutation(t, f.db, `UPDATE account_inventory_provider_states SET health_degraded=true, health_reason='transport_failed' WHERE instance_id=$1 AND provider='antigravity'`, f.node)
			},
		},
		{
			name: "monitoring disabled",
			setup: func(t *testing.T, f *availabilityFixture) {
				if _, err := f.db.owner.Exec(context.Background(), `DELETE FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1`, f.node); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unsupported source mode",
			setup: func(t *testing.T, f *availabilityFixture) {
				setAvailabilityEvidenceNull(t, f.db, f.node, f.keys[0])
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAvailabilityFixture(t, 1)
			tc.setup(t, fixture)
			setTokenRefreshAt(t, fixture.db, fixture.node, fixture.keys[0], "-1 second", "UNKNOWN")
			requireTokenHealth(t, fixture.db, fixture.node, fixture.keys[0], "UNKNOWN")
			invalid := newAvailabilityFixture(t, 1)
			activateTokenInvalid(t, invalid)
			tc.setup(t, invalid)
			setTokenRefreshAt(t, invalid.db, invalid.node, invalid.keys[0], "0 seconds", "INVALID")
		})
	}

	for _, lifecycle := range []string{"missing", "out_of_scope"} {
		t.Run(lifecycle, func(t *testing.T) {
			fixture := newAvailabilityFixture(t, 1)
			execInventoryOwnerMutation(t, fixture.db, `UPDATE account_inventory SET lifecycle=$2,
                consecutive_missing_count=CASE WHEN $2='missing' THEN 2 ELSE 0 END,
                missing_since=CASE WHEN $2='missing' THEN statement_timestamp() ELSE NULL END,
                out_of_scope_since=CASE WHEN $2='out_of_scope' THEN statement_timestamp() ELSE NULL END
                WHERE instance_id=$1 AND account_key=$3`, fixture.node, lifecycle, fixture.keys[0])
			setTokenRefreshAt(t, fixture.db, fixture.node, fixture.keys[0], "-1 second", "UNKNOWN")
			requireTokenHealth(t, fixture.db, fixture.node, fixture.keys[0], "UNKNOWN")
		})
	}
}

func TestAccountTokenHealthEmptyAccountsAndIncompleteSourcePostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixtureWithProviders(t, ctx, database, []string{"antigravity"})
	unsupported := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref) VALUES($1,'Unsupported Token Node',$2,$3,'http://unsupported-token.example','docker-secret://synthetic/token-reader')`, unsupported, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	rows, err := database.runtime.Query(ctx, `SELECT account_key,token_state,expected_valid_until FROM public.control_query_account_token_health_v1($1,ARRAY[]::text[])`, unsupported)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("empty account list must produce no token rows")
	}
	rows.Close()

	// A current source that explicitly reports incomplete evidence must not be
	// upgraded to VALID by a recent refresh timestamp.
	availability := newAvailabilityFixture(t, 1)
	for _, reason := range []string{"identity_incomplete", "node_identity_incomplete", "disk_fallback", "contract_invalid"} {
		t.Run(reason, func(t *testing.T) {
			execInventoryOwnerMutation(t, availability.db, `UPDATE account_inventory_provider_states SET health_degraded=true,health_reason=$2 WHERE instance_id=$1 AND provider='antigravity'`, availability.node, reason)
			setTokenRefreshAt(t, availability.db, availability.node, availability.keys[0], "-1 second", "UNKNOWN")
			requireTokenHealth(t, availability.db, availability.node, availability.keys[0], "UNKNOWN")
		})
	}
}

func TestNodeAccountQualityV4AddsTokenProjectionWithoutChangingV1V2V3Postgres(t *testing.T) {
	ctx := context.Background()
	// This fixture verifies migration 29's down contract, not the latest
	// additive read function introduced by later slices.
	fixture := newAvailabilityFixture(t, 1, "up-to", "29")
	key := fixture.keys[0]
	enableTokenQualityFixture(t, fixture.db, fixture.node)
	setTokenRefreshAt(t, fixture.db, fixture.node, key, "0 seconds", "VALID")

	var v1, v2, v3 string
	for _, check := range []struct {
		name string
		into *string
		sig  string
	}{
		{name: "v1", into: &v1, sig: "public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer)"},
		{name: "v2", into: &v2, sig: "public.control_query_node_account_quality_v2(uuid,text,text,text,text,interval,integer)"},
		{name: "v3", into: &v3, sig: "public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer)"},
	} {
		if err := databaseQueryFunctionDefinition(ctx, fixture.db, check.sig, check.into); err != nil {
			t.Fatal(err)
		}
	}

	var accountKey, email, provider, quality string
	var requestCount, successCount, failureCount int64
	var successRate, p95 *float64
	var lastSuccess, lastFailure *time.Time
	var lastFailureClass *string
	var inventory, recent []byte
	var tokenState string
	var expected *time.Time
	if err := fixture.db.runtime.QueryRow(ctx, `
		SELECT account_key, normalized_email, provider, quality, request_count,
			success_count, failure_count, success_rate, p95_latency_ms,
			last_success_at, last_failure_at, last_failure_class, inventory,
			recent_requests, token_state, expected_valid_until
		FROM public.control_query_node_account_quality_v4($1,'','','','','','', '15 minutes'::interval,100)`, fixture.node).
		Scan(&accountKey, &email, &provider, &quality, &requestCount, &successCount, &failureCount, &successRate, &p95, &lastSuccess, &lastFailure, &lastFailureClass, &inventory, &recent, &tokenState, &expected); err != nil {
		t.Fatal(err)
	}
	if accountKey != key || email == "" || provider != "antigravity" || tokenState != "VALID" || expected == nil {
		t.Fatalf("v4 projection = key=%s email=%s provider=%s state=%s expected=%v", accountKey, email, provider, tokenState, expected)
	}
	if requestCount < 0 || successCount < 0 || failureCount < 0 || successRate != nil && (*successRate < 0 || *successRate > 1) || p95 != nil && *p95 < 0 || len(inventory) == 0 || len(recent) == 0 {
		t.Fatal("v4 changed the existing quality projection shape")
	}

	before := map[string]string{"v1": v1, "v2": v2, "v3": v3}
	if err := runAssetGoose(t, ctx, "../..", fixture.db.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	var v4Removed bool
	if err := fixture.db.owner.QueryRow(ctx, `SELECT to_regprocedure('public.control_query_node_account_quality_v4(uuid,text,text,text,text,text,text,interval,integer)') IS NULL`).Scan(&v4Removed); err != nil || !v4Removed {
		t.Fatalf("v4 was not removed on down: %v", err)
	}
	for name, want := range before {
		sig := map[string]string{"v1": "public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer)", "v2": "public.control_query_node_account_quality_v2(uuid,text,text,text,text,interval,integer)", "v3": "public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer)"}[name]
		var got string
		if err := databaseQueryFunctionDefinition(ctx, fixture.db, sig, &got); err != nil || got != want {
			t.Fatalf("%s definition changed across v4 down/up: err=%v", name, err)
		}
	}
	if err := runAssetGoose(t, ctx, "../..", fixture.db.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
}

func TestNodeAccountQualityV4ForwardUpgradePreservesV1V2V3AndColumnsPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newAvailabilityFixture(t, 1, "up-to", "28")
	oldDefinitions := make(map[string]string)
	for _, signature := range []string{
		"public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer)",
		"public.control_query_node_account_quality_v2(uuid,text,text,text,text,interval,integer)",
		"public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer)",
	} {
		var definition string
		if err := databaseQueryFunctionDefinition(ctx, fixture.db, signature, &definition); err != nil {
			t.Fatal(err)
		}
		oldDefinitions[signature] = definition
	}
	oldColumns := accountQualityColumnCatalog(t, ctx, fixture.db)

	if err := runAssetGoose(t, ctx, "../..", fixture.db.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	for signature, want := range oldDefinitions {
		var got string
		if err := databaseQueryFunctionDefinition(ctx, fixture.db, signature, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("function definition changed during 28->29 upgrade: %s", signature)
		}
	}
	if got := accountQualityColumnCatalog(t, ctx, fixture.db); !reflect.DeepEqual(got, oldColumns) {
		t.Fatalf("column catalog changed during 28->29 upgrade:\nold=%v\nnew=%v", oldColumns, got)
	}
}

func accountQualityColumnCatalog(t *testing.T, ctx context.Context, database *isolatedJobDatabase) []string {
	t.Helper()
	rows, err := database.owner.Query(ctx, `
		SELECT table_name || '.' || column_name || ':' || ordinal_position::text || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default,'')
		FROM information_schema.columns
		WHERE table_schema='public'
		ORDER BY table_name, ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func databaseQueryFunctionDefinition(ctx context.Context, database *isolatedJobDatabase, signature string, destination *string) error {
	return database.owner.QueryRow(ctx, `SELECT pg_get_functiondef($1::regprocedure)`, signature).Scan(destination)
}

func TestNodeAccountQualityV4ACLAndNoNewDomainPersistencePostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	for _, signature := range []string{
		"public.control_query_account_token_health_v1(uuid,text[])",
		"public.control_query_node_account_quality_v4(uuid,text,text,text,text,text,text,interval,integer)",
	} {
		assertTokenProjectionFunctionACL(t, ctx, database, signature)
	}
	for _, relation := range []string{"account_token_health", "token_health", "problem_accounts"} {
		var exists bool
		if err := database.owner.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+relation).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("unexpected new domain table %s", relation)
		}
	}
}

func assertTokenProjectionFunctionACL(t *testing.T, ctx context.Context, database *isolatedJobDatabase, signature string) {
	t.Helper()
	var definer bool
	var volatility, owner string
	var config []string
	var runtimeExecute, publicExecute bool
	if err := database.owner.QueryRow(ctx, `
		SELECT p.prosecdef, p.provolatile::text, r.rolname, p.proconfig,
			has_function_privilege('relay_control_runtime',p.oid,'EXECUTE'),
			EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')
		FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner
		WHERE p.oid=$1::regprocedure`, signature).
		Scan(&definer, &volatility, &owner, &config, &runtimeExecute, &publicExecute); err != nil {
		t.Fatal(err)
	}
	if !definer || volatility != "s" || owner != "relay_control_migrator" || !runtimeExecute || publicExecute || !reflect.DeepEqual(config, []string{"search_path=pg_catalog"}) {
		t.Fatalf("unsafe projection ACL/owner contract for %s: definer=%t volatility=%s owner=%s config=%v runtime=%t public=%t", signature, definer, volatility, owner, config, runtimeExecute, publicExecute)
	}
}

func TestNodeAccountQualityV4BatchProjectionIsSetBased(t *testing.T) {
	ctx := context.Background()
	fixture := newAvailabilityFixture(t, 101)
	database := fixture.db
	enableTokenQualityFixture(t, database, fixture.node)
	execInventoryOwnerMutation(t, database, `UPDATE account_inventory SET last_refresh_at=statement_timestamp() WHERE instance_id=$1`, fixture.node)
	var plan []byte
	if err := database.owner.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT * FROM public.control_query_node_account_quality_v4($1,'','','','','','', '15 minutes'::interval,100)`, fixture.node).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if len(plan) == 0 || string(plan) == "null" {
		t.Fatal("empty v4 query plan")
	}
	definition := ""
	if err := databaseQueryFunctionDefinition(ctx, database, "public.control_query_node_account_quality_v4(uuid,text,text,text,text,text,text,interval,integer)", &definition); err != nil {
		t.Fatal(err)
	}
	if strings.Count(definition, "public.control_query_account_token_health_v1(") != 1 || !strings.Contains(definition, "token_page AS MATERIALIZED") || !strings.Contains(definition, "ARRAY(") {
		t.Fatal("v4 does not show the expected set-based token projection source")
	}
	t.Logf("101-account bounded v4 EXPLAIN ANALYZE: %s", plan)
	repository, err := store.NewAccountRequestQualityRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.ListAccountQuality(ctx, store.AccountQualityQuery{InstanceID: fixture.node, Window: 15 * time.Minute, Limit: 100})
	if err != nil || len(first.Items) != 100 || !first.HasMore {
		t.Fatalf("first batched page: items=%d has_more=%t err=%v", len(first.Items), first.HasMore, err)
	}
	for _, item := range first.Items {
		if item.TokenState == nil || *item.TokenState != "VALID" || item.ExpectedValidUntil == nil {
			t.Fatalf("missing adapter token projection: %+v", item)
		}
	}
	second, err := repository.ListAccountQuality(ctx, store.AccountQualityQuery{InstanceID: fixture.node, Window: 15 * time.Minute, AfterAccountKey: first.Items[len(first.Items)-1].AccountKey, Limit: 100})
	if err != nil || len(second.Items) != 1 || second.HasMore {
		t.Fatalf("second batched page: items=%d has_more=%t err=%v", len(second.Items), second.HasMore, err)
	}
}

func TestNodeAccountQualityAdapterLeavesOtherProviderTokenFieldsNullPostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	enableTokenQualityFixture(t, database, fixture.instanceID)
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) VALUES($1,CURRENT_TIMESTAMP,'deployment_enable','token-health-test',CURRENT_TIMESTAMP)`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	fixture.finalize(t, ctx, database, []lifecycleAccount{{email: "other-provider@example.invalid"}})
	repository, err := store.NewAccountRequestQualityRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	page, err := repository.ListAccountQuality(ctx, store.AccountQualityQuery{InstanceID: fixture.instanceID, Window: 15 * time.Minute, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("other-provider page=%+v err=%v", page, err)
	}
	if page.Items[0].Provider != "openai" || page.Items[0].TokenState != nil || page.Items[0].ExpectedValidUntil != nil {
		t.Fatalf("other-provider token fields were not NULL: %+v", page.Items[0])
	}
}

func enableTokenQualityFixture(t *testing.T, database *isolatedJobDatabase, node uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
        SELECT node_type,driver_contract_version,'management_account_inventory_read' FROM relay_node_assets WHERE instance_id=$1
        ON CONFLICT DO NOTHING`, node); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
        SELECT instance_id,node_type,driver_contract_version,'management_account_inventory_read' FROM relay_node_assets WHERE instance_id=$1
        ON CONFLICT DO NOTHING`, node); err != nil {
		t.Fatal(err)
	}
}
