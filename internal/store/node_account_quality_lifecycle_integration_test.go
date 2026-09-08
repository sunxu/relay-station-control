package store_test

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"reflect"
	"testing"
	"time"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestNodeAccountQualityLifecycleFilterPostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES($1,$2,'management_account_inventory_read')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,$2,$3,'management_account_inventory_read')`, fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	accounts := []lifecycleAccount{{email: "lifecycle@example.invalid"}}
	fixture.finalize(t, ctx, database, accounts)
	fixture.finalize(t, ctx, database, nil)
	fixture.finalize(t, ctx, database, nil)
	repo, err := productstore.NewAccountRequestQualityRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	all, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: fixture.instanceID, Window: 15 * time.Minute, Lifecycle: "", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	present, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: fixture.instanceID, Window: 15 * time.Minute, Lifecycle: "present", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	missing, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: fixture.instanceID, Window: 15 * time.Minute, Lifecycle: "missing", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 1 || len(present.Items) != 0 || len(missing.Items) != 1 {
		t.Fatalf("all=%d present=%d missing=%d", len(all.Items), len(present.Items), len(missing.Items))
	}
	if _, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: fixture.instanceID, Window: 15 * time.Minute, Lifecycle: "invalid", Limit: 100}); err == nil {
		t.Fatal("invalid lifecycle accepted")
	}
}

func TestNodeAccountQualityLifecycleFilterAndACLPostgres(t *testing.T) {
	ctx := context.Background()
	f := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "a@example.invalid"}, {email: "b@example.invalid"}, {email: "c@example.invalid"}, {email: "d@example.invalid"}})
	db := f.database
	f.lifecycle.finalize(t, ctx, db, []lifecycleAccount{{email: "a@example.invalid"}, {email: "c@example.invalid"}})
	f.lifecycle.finalize(t, ctx, db, []lifecycleAccount{{email: "a@example.invalid"}, {email: "c@example.invalid"}})
	repo, err := productstore.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	query := func(life, after string) productstore.AccountQualityPage {
		t.Helper()
		page, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: f.lifecycle.instanceID, Window: 15 * time.Minute, Lifecycle: life, Quality: "unknown", AfterAccountKey: after, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	for _, tc := range []struct {
		life string
		keys []string
	}{
		{"present", []string{"openai:a@example.invalid", "openai:c@example.invalid"}},
		{"missing", []string{"openai:b@example.invalid", "openai:d@example.invalid"}},
		{"", []string{"openai:a@example.invalid", "openai:b@example.invalid", "openai:c@example.invalid", "openai:d@example.invalid"}},
	} {
		after := ""
		for i, key := range tc.keys {
			p := query(tc.life, after)
			if len(p.Items) != 1 || p.Items[0].AccountKey != key || p.HasMore != (i < len(tc.keys)-1) {
				t.Fatalf("wrong lifecycle page: %s %d %+v", tc.life, i, p)
			}
			after = key
		}
		if p := query(tc.life, after); len(p.Items) != 0 || p.HasMore {
			t.Fatal("last cursor not exhausted")
		}
	}
	sig := "public.control_query_node_account_quality_v2(uuid,text,text,text,text,interval,integer)"
	var definer, exec, public bool
	var owner, vol string
	var config []string
	if err := db.owner.QueryRow(ctx, `SELECT p.prosecdef,p.provolatile::text,r.rolname,p.proconfig,has_function_privilege('relay_control_runtime',p.oid,'EXECUTE'),EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1::regprocedure`, sig).Scan(&definer, &vol, &owner, &config, &exec, &public); err != nil {
		t.Fatal(err)
	}
	if !definer || vol != "s" || owner != "relay_control_migrator" || !exec || public || len(config) != 1 || config[0] != "search_path=pg_catalog" {
		t.Fatal("unsafe function ACL")
	}
	for _, table := range []string{"account_inventory", "account_inventory_provider_states", "account_request_quality_events"} {
		_, err := db.runtime.Exec(ctx, "SELECT * FROM public."+table+" LIMIT 1")
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatalf("direct access not denied: %v", err)
		}
	}
	before := captureReadonlyQueryMigrationState(t, ctx, db)
	var v1Before, v1After string
	const v1SQL = `SELECT pg_get_functiondef('public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer)'::regprocedure)`
	if err := db.owner.QueryRow(ctx, v1SQL).Scan(&v1Before); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	var removed bool
	if err := db.owner.QueryRow(ctx, `SELECT to_regprocedure($1) IS NULL`, sig).Scan(&removed); err != nil || !removed {
		t.Fatal("v2 not removed")
	}
	var retained int
	if err := db.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_query_node_account_quality_v1($1,'','','','15 minutes'::interval,100)`, f.lifecycle.instanceID).Scan(&retained); err != nil || retained != 4 {
		t.Fatalf("v1 compatibility %d %v", retained, err)
	}
	if !reflect.DeepEqual(before, captureReadonlyQueryMigrationState(t, ctx, db)) {
		t.Fatal("down changed persistence")
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	if err := db.owner.QueryRow(ctx, v1SQL).Scan(&v1After); err != nil || v1After != v1Before {
		t.Fatal("v1 modified")
	}
	if p := query("missing", ""); len(p.Items) != 1 || p.Items[0].AccountKey != "openai:b@example.invalid" || !p.HasMore {
		t.Fatal("v2 recovery failed")
	}
	if !reflect.DeepEqual(before, captureReadonlyQueryMigrationState(t, ctx, db)) {
		t.Fatal("up changed persistence")
	}
}
