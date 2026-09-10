package store_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestProblemAccountsPostgresCoreProjection(t *testing.T) {
	ctx := context.Background()
	f := newAvailabilityFixture(t, 6)

	// Three availability issue kinds, plus one account carrying two issues.
	problemEventPair(t, f, 0, "token_invalid")
	problemEventPair(t, f, 1, "account_blocked")
	f.event(t, 2, "forbidden-1", "problem-forbidden-1", "forbidden", f.now.Add(-2*time.Minute))
	problemEventPair(t, f, 3, "token_invalid")
	problemEventPair(t, f, 3, "account_blocked")
	f.clock(t, "file_unavailable", nil, f.now.Add(-time.Minute), f.now.Truncate(5*time.Minute))
	f.reconcile(t)
	insertSameNodeDuplicateIssue(t, f, "problem-duplicate-environment")

	// A valid resolved occurrence is evidence, not a current problem.
	problemEventPair(t, f, 4, "token_invalid")
	f.reconcile(t)
	f.event(t, 4, "resolved-success", "resolved-success", "", f.now.Add(-30*time.Second))
	f.reconcile(t)
	if active := f.occurrences(t, 4, "ACTIVE"); len(active) != 0 {
		t.Fatalf("resolved account remains active: %+v", active)
	}
	if resolved := f.occurrences(t, 4, "RESOLVED"); len(resolved) != 1 || resolved[0].ResolvedAt == nil {
		t.Fatalf("resolved occurrence=%+v", resolved)
	}

	repo, err := store.NewProblemAccountRepository(f.db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	filters := store.ProblemAccountFilters{}
	beforeDiagnostics := availabilityForProblems(t, f, []int{0, 1, 2, 3})
	page, err := repo.ListProblemAccounts(ctx, store.ProblemAccountQuery{Filters: filters, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 || page.HasMore {
		t.Fatalf("problem page items=%d more=%t", len(page.Items), page.HasMore)
	}
	byKey := make(map[string]store.ProblemAccountItem, len(page.Items))
	for _, item := range page.Items {
		byKey[item.AccountKey] = item
	}
	for _, index := range []int{0, 1, 2, 3} {
		item, ok := byKey[f.keys[index]]
		if !ok {
			t.Fatalf("missing problem account %s", f.keys[index])
		}
		if item.InstanceID != f.node || item.Provider != "antigravity" || item.Email == "" || len(item.Issues) == 0 {
			t.Fatalf("bad projection for %s: %+v", f.keys[index], item)
		}
	}
	if len(byKey[f.keys[3]].Issues) != 2 {
		t.Fatalf("multi-issue account issues=%+v", byKey[f.keys[3]].Issues)
	}
	if len(byKey[f.keys[0]].Issues) != 2 {
		t.Fatalf("mixed availability/duplicate account issues=%+v", byKey[f.keys[0]].Issues)
	}
	if byKey[f.keys[0]].Issues[0].Type != "CROSS_NODE_DUPLICATE_OWNERSHIP" || byKey[f.keys[0]].Issues[1].Type != "TOKEN_INVALID" || byKey[f.keys[3]].Issues[0].Type != "ACCOUNT_BLOCKED" || byKey[f.keys[3]].Issues[1].Type != "TOKEN_INVALID" {
		t.Fatal("unstable issue ordering")
	}
	seenReasons := make(map[string]bool, len(byKey[f.keys[0]].Issues))
	for _, issue := range byKey[f.keys[0]].Issues {
		seenReasons[issue.Reason] = true
	}
	if !seenReasons["token_invalid"] || !seenReasons["cross_node_duplicate_ownership"] {
		t.Fatalf("mixed issue reasons=%v", seenReasons)
	}
	if _, ok := byKey[f.keys[4]]; ok {
		t.Fatal("resolved availability occurrence appeared as a problem")
	}
	if after := availabilityForProblems(t, f, []int{0, 1, 2, 3}); !reflect.DeepEqual(beforeDiagnostics, after) {
		t.Fatalf("problem read changed availability diagnostics: before=%+v after=%+v", beforeDiagnostics, after)
	}
	codec, err := store.NewProblemAccountCursorCodec(problemAccountsTestKeyring(t))
	if err != nil {
		t.Fatal(err)
	}
	last := page.Items[len(page.Items)-1]
	actor := uuid.New()
	encoded, err := codec.Encode(actor, filters, store.ProblemAccountCursor{Severity: last.HighestSeverity, Since: last.OldestActiveSince, Email: last.Email, NodeID: last.InstanceID})
	if err != nil {
		t.Fatalf("encode DB page cursor: %v", err)
	}
	decoded, err := codec.Decode(encoded, actor, filters)
	if err != nil || decoded.Severity != last.HighestSeverity || !decoded.Since.Equal(last.OldestActiveSince) || decoded.Email != last.Email || decoded.NodeID != last.InstanceID {
		t.Fatalf("DB page cursor roundtrip: decoded=%+v last=%+v err=%v", decoded, last, err)
	}

	t.Run("filters", func(t *testing.T) {
		cases := []struct {
			name   string
			filter store.ProblemAccountFilters
			want   int
			key    string
		}{
			{"provider", store.ProblemAccountFilters{Provider: "antigravity"}, 4, ""},
			{"node", store.ProblemAccountFilters{NodeID: f.node}, 4, ""},
			{"critical", store.ProblemAccountFilters{Severity: "Critical"}, 3, ""},
			{"warning", store.ProblemAccountFilters{Severity: "Warning"}, 1, ""},
			{"reason", store.ProblemAccountFilters{Reason: "account_blocked"}, 2, ""},
			{"email", store.ProblemAccountFilters{Email: strings.TrimPrefix(f.keys[1], "antigravity:")}, 1, f.keys[1]},
			{"unsupported provider", store.ProblemAccountFilters{Provider: "openai"}, 0, ""},
			{"combined", store.ProblemAccountFilters{Provider: "antigravity", NodeID: f.node, Severity: "Critical", Reason: "token_invalid", Email: strings.TrimPrefix(f.keys[0], "antigravity:")}, 1, f.keys[0]},
			{"combined conflict", store.ProblemAccountFilters{NodeID: f.node, Severity: "Warning", Reason: "token_invalid"}, 0, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := repo.ListProblemAccounts(ctx, store.ProblemAccountQuery{Filters: tc.filter, Limit: 100})
				if err != nil || len(got.Items) != tc.want {
					t.Fatalf("items=%d err=%v want=%d", len(got.Items), err, tc.want)
				}
				if tc.key != "" && (len(got.Items) != 1 || got.Items[0].AccountKey != tc.key) {
					t.Fatalf("filtered items=%+v", got.Items)
				}
			})
		}
	})

	t.Run("stable keyset", func(t *testing.T) {
		var all []store.ProblemAccountItem
		var after *store.ProblemAccountCursor
		for pageCount := 0; pageCount < 10; pageCount++ {
			query := store.ProblemAccountQuery{Filters: filters, After: after, Limit: 2}
			got, err := repo.ListProblemAccounts(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, got.Items...)
			if !got.HasMore {
				break
			}
			if len(got.Items) != 2 {
				t.Fatalf("page size=%d", len(got.Items))
			}
			last := got.Items[len(got.Items)-1]
			after = &store.ProblemAccountCursor{Severity: last.HighestSeverity, Since: last.OldestActiveSince, Email: last.Email, NodeID: last.InstanceID}
		}
		if len(all) != len(page.Items) {
			t.Fatalf("keyset items=%d first-page=%d", len(all), len(page.Items))
		}
		for i := range page.Items {
			if all[i].AccountKey != page.Items[i].AccountKey || all[i].HighestSeverity != page.Items[i].HighestSeverity || !all[i].OldestActiveSince.Equal(page.Items[i].OldestActiveSince) {
				t.Fatalf("keyset changed order at %d: got=%+v want=%+v", i, all[i], page.Items[i])
			}
		}
		keys := make(map[string]bool, len(all))
		for _, item := range all {
			key := fmt.Sprintf("%s|%s|%s|%s", item.HighestSeverity, item.OldestActiveSince.UTC().Format(time.RFC3339Nano), item.Email, item.InstanceID)
			if keys[key] {
				t.Fatalf("non-unique ordering key %s", key)
			}
			keys[key] = true
		}
	})
}

func TestProblemAccountsPostgresACL(t *testing.T) {
	ctx := context.Background()
	f := newAvailabilityFixture(t, 1)
	sig := "public.control_query_problem_accounts_v1(text,uuid,text,text,text,text,timestamptz,text,uuid,integer)"
	var definer, runtimeExecute, publicExecute bool
	var volatility, owner string
	var config []string
	if err := f.db.owner.QueryRow(ctx, `
		SELECT p.prosecdef,p.provolatile::text,r.rolname,p.proconfig,
			has_function_privilege('relay_control_runtime',p.oid,'EXECUTE'),
			EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
				WHERE a.grantee=0 AND a.privilege_type='EXECUTE')
		FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner
		WHERE p.oid=$1::regprocedure`, sig).
		Scan(&definer, &volatility, &owner, &config, &runtimeExecute, &publicExecute); err != nil {
		t.Fatal(err)
	}
	if !definer || volatility != "s" || owner != "relay_control_migrator" || !runtimeExecute || publicExecute || !reflect.DeepEqual(config, []string{"search_path=pg_catalog"}) {
		t.Fatalf("problem function ACL: definer=%t volatility=%s owner=%s config=%v runtime=%t public=%t", definer, volatility, owner, config, runtimeExecute, publicExecute)
	}
	for _, relation := range []string{"account_availability_occurrences", "account_inventory", "account_request_quality_events"} {
		_, err := f.db.runtime.Exec(ctx, "SELECT * FROM public."+relation+" LIMIT 1")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("runtime direct SELECT %s: %v", relation, err)
		}
	}
	repo, err := store.NewProblemAccountRepository(f.db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListProblemAccounts(ctx, store.ProblemAccountQuery{Limit: 25}); err != nil {
		t.Fatalf("runtime function execute: %v", err)
	}
}

func TestProblemAccountsDiagnosticsNeverClearActiveIssues(t *testing.T) {
	for _, state := range []string{"unknown", "disabled", "stale", "missing", "out_of_scope", "inventory_absent"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			f := newAvailabilityFixture(t, 1)
			problemEventPair(t, f, 0, "token_invalid")
			f.reconcile(t)
			switch state {
			case "inventory_absent":
				execInventoryOwnerMutation(t, f.db, `DELETE FROM account_inventory WHERE instance_id=$1`, f.node)
			case "unknown":
				f.clock(t, "file_unavailable", nil, f.now.Add(-time.Minute), f.now.Truncate(5*time.Minute))
			case "disabled":
				f.clock(t, "file_disabled", nil, f.now.Add(-time.Minute), f.now.Truncate(5*time.Minute))
			case "stale":
				f.clock(t, "file_active", nil, f.now.Add(-16*time.Minute), f.now.Add(-20*time.Minute).Truncate(5*time.Minute))
			case "missing", "out_of_scope":
				lifecycle := state
				if _, err := f.db.owner.Exec(ctx, `ALTER TABLE account_inventory DISABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
				_, err := f.db.owner.Exec(ctx, `UPDATE account_inventory SET lifecycle=$2,consecutive_missing_count=$3,missing_since=$4,out_of_scope_since=$5 WHERE instance_id=$1 AND account_key=$6`, f.node, lifecycle, map[string]int{"missing": 2, "out_of_scope": 0}[state], nullableProblemTime(state == "missing", f.now), nullableProblemTime(state == "out_of_scope", f.now), f.keys[0])
				if enableErr := func() error {
					_, e := f.db.owner.Exec(ctx, `ALTER TABLE account_inventory ENABLE TRIGGER USER`)
					return e
				}(); err != nil || enableErr != nil {
					t.Fatalf("diagnostic lifecycle mutation err=%v enable=%v", err, enableErr)
				}
			}
			repo, err := store.NewProblemAccountRepository(f.db.runtime)
			if err != nil {
				t.Fatal(err)
			}
			page, err := repo.ListProblemAccounts(ctx, store.ProblemAccountQuery{Filters: store.ProblemAccountFilters{}, Limit: 25})
			if err != nil || len(page.Items) != 1 || len(page.Items[0].Issues) == 0 {
				t.Fatalf("diagnostic=%s page=%+v err=%v", state, page, err)
			}
		})
	}
}

func TestProblemAccountsMissingAssetDiagnosticsPreserveOccurrence(t *testing.T) {
	ctx := context.Background()
	db := newIsolatedJobDatabase(t)
	node := uuid.New()
	// Occurrences deliberately have no asset/Inventory FK. Seed retained
	// confirmed truth with no current diagnostic rows; do not invent Retire.
	if _, err := db.owner.Exec(ctx, `INSERT INTO account_availability_occurrences(node_id,account_key,reason,severity,first_seen_at,last_failure_at,confirmed_at)
		VALUES($1,'antigravity:retained@example.invalid','token_invalid','Critical',statement_timestamp(),statement_timestamp(),statement_timestamp())`, node); err != nil {
		t.Fatal(err)
	}
	repo, err := store.NewProblemAccountRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListProblemAccounts(ctx, store.ProblemAccountQuery{Limit: 25})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("retained truth: %+v %v", page, err)
	}
	row := page.Items[0]
	if row.InstanceID != node || row.TokenState != "INVALID" || row.Availability.State != "UNKNOWN" || row.LastRefreshAt != nil || row.ExpectedValidUntil != nil || len(row.Issues) != 1 {
		t.Fatalf("diagnostics overrode truth: %+v", row)
	}
}

func TestProblemAccountsMigration29To30PreservesExistingSchema(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t, "up-to", "29")
	beforeColumns := problemAccountsSchemaSnapshot(t, ctx, database, `SELECT COALESCE(string_agg(table_name||':'||column_name||':'||ordinal_position||':'||COALESCE(data_type,'')||':'||COALESCE(udt_name,''),E'\n' ORDER BY table_name,ordinal_position),'') FROM information_schema.columns WHERE table_schema='public'`)
	beforeFunctions := problemAccountsSchemaSnapshot(t, ctx, database, `SELECT COALESCE(string_agg(p.proname||'('||pg_get_function_identity_arguments(p.oid)||'):'||pg_get_functiondef(p.oid),E'\n' ORDER BY p.proname,pg_get_function_identity_arguments(p.oid)),'') FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname <> 'control_query_problem_accounts_v1'`)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	if after := problemAccountsSchemaSnapshot(t, ctx, database, `SELECT COALESCE(string_agg(table_name||':'||column_name||':'||ordinal_position||':'||COALESCE(data_type,'')||':'||COALESCE(udt_name,''),E'\n' ORDER BY table_name,ordinal_position),'') FROM information_schema.columns WHERE table_schema='public'`); after != beforeColumns {
		t.Fatal("migration 30 changed existing columns")
	}
	if after := problemAccountsSchemaSnapshot(t, ctx, database, `SELECT COALESCE(string_agg(p.proname||'('||pg_get_function_identity_arguments(p.oid)||'):'||pg_get_functiondef(p.oid),E'\n' ORDER BY p.proname,pg_get_function_identity_arguments(p.oid)),'') FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname <> 'control_query_problem_accounts_v1'`); after != beforeFunctions {
		t.Fatal("migration 30 changed existing function definitions")
	}
}

func TestProblemAccountsPostgresExplainPerformance(t *testing.T) {
	ctx := context.Background()
	f := newAvailabilityFixture(t, 1000)
	events := make([]requestquality.Event, 0, 2000)
	for i, key := range f.keys {
		for attempt := 0; attempt < 2; attempt++ {
			accountKey := key
			reason := "token_invalid"
			events = append(events, requestquality.Event{
				EventHash: fmt.Sprintf("problem-perf-%03d-%d", i, attempt), RequestID: fmt.Sprintf("problem-perf-request-%03d-%d", i, attempt),
				NodeID: f.node, Provider: "antigravity", AccountKey: &accountKey, OccurredAt: f.now.Add(-2 * time.Minute).Add(-time.Duration(attempt) * time.Second),
				Success: false, FailureClass: stringPtr("auth"), AuthFailureReason: &reason,
			})
		}
	}
	if err := f.events.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	insertPerformanceDuplicateMembership(t, f, "problem-performance-environment")
	if _, err := f.db.owner.Exec(ctx, `ANALYZE account_availability_occurrences; ANALYZE account_inventory; ANALYZE account_request_quality_events`); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	var plan []byte
	if err := f.db.owner.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
		SELECT * FROM public.control_query_problem_accounts_v1(
			''::text,NULL::uuid,''::text,''::text,''::text,
			NULL::text,NULL::timestamptz,NULL::text,NULL::uuid,25::integer)`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if len(plan) == 0 || string(plan) == "null" {
		t.Fatal("empty problem-account EXPLAIN plan")
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("problem-account EXPLAIN exceeded 5s: %s", elapsed)
	}
	t.Logf("1000-inventory / 1000-occurrence / 1000-membership problem-account EXPLAIN ANALYZE: %s", plan)

	var definition string
	if err := f.db.owner.QueryRow(ctx, `SELECT pg_get_functiondef($1::regprocedure)`, "public.control_query_problem_accounts_v1(text,uuid,text,text,text,text,timestamptz,text,uuid,integer)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if strings.Count(definition, "RETURN QUERY") != 1 ||
		!strings.Contains(definition, "issue_rows AS MATERIALIZED") ||
		!strings.Contains(definition, "grouped AS MATERIALIZED") ||
		!strings.Contains(definition, "availability_diag AS MATERIALIZED") ||
		!strings.Contains(definition, "token_diag AS MATERIALIZED") ||
		!strings.Contains(definition, "quality_diag AS MATERIALIZED") ||
		!strings.Contains(definition, "LIMIT page_limit + 1") {
		t.Fatalf("problem function body lacks single bounded projection proof")
	}
	// Inspect the real RETURN QUERY body, not a hand-written approximation.
	// Only PL/pgSQL input/local variables are substituted with this call's
	// typed constants. Nested diagnostic functions remain opaque Function Scans.
	body := strings.SplitN(strings.SplitN(definition, "RETURN QUERY", 2)[1], "\nEND;", 2)[0]
	constants := map[string]string{
		"target_node": "NULL::uuid", "target_severity": "''::text",
		"reason_filter": "''::text", "email_filter": "''::text",
		"cursor_severity": "NULL::text", "after_since": "NULL::timestamptz",
		"cursor_email": "NULL::text", "after_node": "NULL::uuid", "page_limit": "25",
	}
	for name, value := range constants {
		body = regexp.MustCompile(`\b`+name+`\b`).ReplaceAllString(body, value)
	}
	var nested []byte
	if err := f.db.owner.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+body).Scan(&nested); err != nil {
		t.Fatal(err)
	}
	var plans []map[string]any
	if err := json.Unmarshal(nested, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("nested plan: %v", err)
	}
	root := plans[0]["Plan"].(map[string]any)
	if root["Actual Rows"].(float64) != 26 {
		t.Fatalf("unbounded page: %v", root["Actual Rows"])
	}
	var checkPlan func(map[string]any)
	checkPlan = func(node map[string]any) {
		for _, key := range []string{"Temp Read Blocks", "Temp Written Blocks"} {
			if value, ok := node[key].(float64); ok && value != 0 {
				t.Fatalf("temp spill: %s=%v", key, value)
			}
		}
		// Existing indexed nested-loop probes are legitimate set-based SQL.
		// Reject repeated full scans, not the optimizer's bounded index probes.
		if relation, _ := node["Relation Name"].(string); (relation == "cross_node_duplicate_occurrence_nodes" || relation == "account_availability_occurrences") && node["Node Type"] == "Seq Scan" {
			if loops, _ := node["Actual Loops"].(float64); loops > 1 {
				t.Fatalf("repeated occurrence/membership scan: %s loops=%v", relation, loops)
			}
		}
		if children, ok := node["Plans"].([]any); ok {
			for _, child := range children {
				checkPlan(child.(map[string]any))
			}
		}
	}
	checkPlan(root)
	t.Logf("actual-body plan: execution=%vms estimated_rows=%v actual_rows=%v; no temp spill or repeated full occurrence/membership scan (existing index probes allowed)", plans[0]["Execution Time"], root["Plan Rows"], root["Actual Rows"])
}

func problemEventPair(t *testing.T, f *availabilityFixture, index int, reason string) {
	t.Helper()
	base := f.now.Add(-3 * time.Minute)
	f.event(t, index, reason+"-1", "problem-"+reason+"-1-"+fmt.Sprint(index), reason, base)
	f.event(t, index, reason+"-2", "problem-"+reason+"-2-"+fmt.Sprint(index), reason, base.Add(time.Second))
}

func availabilityForProblems(t *testing.T, f *availabilityFixture, indexes []int) map[string]store.AccountAvailability {
	t.Helper()
	keys := make([]string, 0, len(indexes))
	for _, index := range indexes {
		keys = append(keys, f.keys[index])
	}
	values, err := f.repo.BatchAccountAvailability(context.Background(), f.node, keys)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func nullableProblemTime(set bool, value time.Time) any {
	if !set {
		return nil
	}
	return value
}

func problemAccountsSchemaSnapshot(t *testing.T, ctx context.Context, database *isolatedJobDatabase, query string) string {
	t.Helper()
	var snapshot string
	if err := database.owner.QueryRow(ctx, query).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func insertSameNodeDuplicateIssue(t *testing.T, f *availabilityFixture, environmentID string) {
	t.Helper()
	ctx := context.Background()
	occurrenceID := uuid.New()
	if _, err := f.db.owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES($1,'Problem duplicate test','dev')`, environmentID); err != nil {
		t.Fatal(err)
	}
	seenAt := f.now.Add(-4 * time.Minute)
	if _, err := f.db.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrences(occurrence_id,environment_id,account_key,conflict_type,status,severity,first_seen_at,last_seen_at,evidence_state) VALUES($1,$2,$3,'cross_node_duplicate_ownership','ACTIVE','Critical',$4,$4,'complete')`, occurrenceID, environmentID, f.keys[0], seenAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(occurrence_id,instance_id,first_confirmed_at) VALUES($1,$2,$3)`, occurrenceID, f.node, seenAt); err != nil {
		t.Fatal(err)
	}
}

func insertPerformanceDuplicateMembership(t *testing.T, f *availabilityFixture, environmentID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.db.owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES($1,'Problem performance duplicate test','dev')`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.owner.Exec(ctx, `
		INSERT INTO cross_node_duplicate_occurrences(environment_id,account_key,conflict_type,status,severity,first_seen_at,last_seen_at,evidence_state)
		SELECT $1,'antigravity:account'||lpad(i::text,3,'0')||'@example.invalid','cross_node_duplicate_ownership','ACTIVE','Critical',statement_timestamp()-interval '2 minutes',statement_timestamp()-interval '2 minutes','complete'
		FROM generate_series(0,999) AS s(i)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.owner.Exec(ctx, `
		INSERT INTO cross_node_duplicate_occurrence_nodes(occurrence_id,instance_id,first_confirmed_at)
		SELECT o.occurrence_id,$2,statement_timestamp()
		FROM cross_node_duplicate_occurrences AS o
		WHERE o.environment_id=$1`, environmentID, f.node); err != nil {
		t.Fatal(err)
	}
}

func problemAccountsTestKeyring(t *testing.T) *authn.Keyring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	document := `{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"` + base64.RawStdEncoding.EncodeToString(key) + `"}]}`
	keyring, err := authn.ParseKeyring([]byte(document), authn.EnvironmentDev)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}
