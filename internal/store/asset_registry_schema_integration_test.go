package store_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	assetHealthCapability    = "management_health_read"
	assetInventoryCapability = "management_account_inventory_read"
)

func assetFixtureSuffix(t *testing.T) string {
	t.Helper()
	return strings.ToLower(randomLogin(t, "asset"))
}

func assetSavepoint(t *testing.T, ctx context.Context, tx pgx.Tx, operation string, fn func() error) error {
	t.Helper()
	if _, err := tx.Exec(ctx, "SAVEPOINT asset_case"); err != nil {
		t.Fatalf("savepoint for %s: %v", operation, err)
	}
	err := fn()
	if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT asset_case"); rollbackErr != nil {
		t.Fatalf("rollback %s: %v", operation, rollbackErr)
	}
	return err
}

func requireSQLState(t *testing.T, err error, expected string) {
	t.Helper()
	if err == nil {
		t.Fatalf("operation unexpectedly succeeded; want SQLSTATE %s", expected)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error %T is not a PostgreSQL error: %v", err, err)
	}
	if pgErr.Code != expected {
		t.Fatalf("SQLSTATE = %s, want %s: %v", pgErr.Code, expected, err)
	}
}

func registerAssetDriver(t *testing.T, ctx context.Context, tx pgx.Tx, nodeType string) {
	t.Helper()
	_, err := tx.Exec(ctx, `SELECT public.control_register_node_driver(
		$1, 'v1', 'Test Driver', 'active', $2::text[]
	)`, nodeType, []string{assetInventoryCapability, assetHealthCapability})
	if err != nil {
		t.Fatalf("register driver: %v", err)
	}
}

func TestAssetRegistryIdentityEndpointAndSecretConstraints(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `INSERT INTO environments(
		singleton_id,environment_id,name,environment_type
	) VALUES (1,'asset-registry-test','Asset Registry Test','dev')
	ON CONFLICT (singleton_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	valid := map[string]string{
		"http://a":                    "http://a",
		"HTTPS://Example.COM:443/api": "https://example.com/api",
		"http://Example.COM:80":       "http://example.com",
		"https://[2001:DB8::1]:8443":  "https://[2001:db8::1]:8443",
		"http://example.com/%7euser":  "http://example.com/%7Euser",
	}
	for input, expected := range valid {
		var actual string
		if err := tx.QueryRow(ctx, `SELECT public.control_normalize_asset_endpoint($1)`, input).Scan(&actual); err != nil {
			t.Fatalf("normalize valid endpoint %q: %v", input, err)
		}
		if actual != expected {
			t.Fatalf("normalize %q = %q, want %q", input, actual, expected)
		}
		parsed, parseErr := url.Parse(actual)
		if parseErr != nil || !parsed.IsAbs() || parsed.Host == "" {
			t.Fatalf("database-accepted endpoint is not accepted by Go net/url: endpoint=%q parsed=%v err=%v", actual, parsed, parseErr)
		}
	}

	invalid := []string{
		"http://", "ftp://example.com", "https://user:password@example.com",
		"https://example.com?q=secret", " https://example.com", "https://example.com:0",
		"http://[::::]", "http://[1.2.3.4]",
		"http://host/%ZZ", "http://host/%", "http://host/%0a",
		"http://host/%7f", `http://host/a\b`, "http://host/%5c",
		"http://host/./x", "http://host/a/../b", "http://host/%2e/x",
		"http://host/%2E%2e/x", "http://host/a/%2e./b",
	}
	for _, input := range invalid {
		err := assetSavepoint(t, ctx, tx, "invalid endpoint", func() error {
			_, err := tx.Exec(ctx, `SELECT public.control_normalize_asset_endpoint($1)`, input)
			return err
		})
		requireSQLState(t, err, "22023")
		if strings.Contains(err.Error(), input) {
			t.Fatalf("endpoint error reflected rejected input %q: %v", input, err)
		}
	}

	for _, reference := range []string{
		"vault://control/gateway-reader",
		"secret://relay/node-reader",
		"docker-secret://relay_node_reader",
	} {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT public.control_valid_secret_reference($1)`, reference).Scan(&valid); err != nil || !valid {
			t.Fatalf("valid opaque reference %q: valid=%v err=%v", reference, valid, err)
		}
	}
	for _, reference := range []string{
		"https://user:password@example.com/secret",
		"https://secret-manager.example/item",
		"vault://user:password@secret/path",
		"vault://secret/path?token=credential",
		"plain-password",
	} {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT public.control_valid_secret_reference($1)`, reference).Scan(&valid); err != nil {
			t.Fatalf("validate rejected reference: %v", err)
		}
		if valid {
			t.Fatalf("credential-shaped reference unexpectedly accepted: %q", reference)
		}
	}

	err = assetSavepoint(t, ctx, tx, "mutate environment identity", func() error {
		_, err := tx.Exec(ctx, `UPDATE environments SET environment_id = environment_id || '-changed' WHERE singleton_id=1`)
		return err
	})
	requireSQLState(t, err, "23514")
	err = assetSavepoint(t, ctx, tx, "delete environment identity", func() error {
		_, err := tx.Exec(ctx, `DELETE FROM environments WHERE singleton_id=1`)
		return err
	})
	requireSQLState(t, err, "23514")
	err = assetSavepoint(t, ctx, tx, "truncate environment identity", func() error {
		_, err := tx.Exec(ctx, `TRUNCATE environments`)
		return err
	})
	requireSQLState(t, err, "23514")
	for _, invalidName := range []string{" leading", "trailing ", "line\nbreak", strings.Repeat("x", 101)} {
		err = assetSavepoint(t, ctx, tx, "invalid environment name", func() error {
			_, err := tx.Exec(ctx, `UPDATE environments SET name=$1 WHERE singleton_id=1`, invalidName)
			return err
		})
		requireSQLState(t, err, "23514")
	}
}

func TestAssetRegistrationIsAtomicIdempotentAndCapabilityBound(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	// The Gateway is a deployment-wide singleton. Clear it only inside this
	// rollback-only owner transaction so this test remains repeatable against a
	// shared integration database without changing the committed fixture.
	if _, err := tx.Exec(ctx, `DELETE FROM gateway_instances`); err != nil {
		t.Fatalf("isolate gateway singleton fixture: %v", err)
	}

	suffix := assetFixtureSuffix(t)
	nodeType := "relay-" + suffix
	registerAssetDriver(t, ctx, tx, nodeType)
	syntheticSecret := "vault://synthetic/secret-canary"
	for _, testCase := range []struct {
		name      string
		statement string
		arguments []any
	}{
		{"NULL gateway UUID", `SELECT public.control_register_gateway(
			NULL, 'Gateway', 'https://gateway.example', $1
		)`, []any{syntheticSecret}},
		{"invalid gateway display", `SELECT public.control_register_gateway(
			gen_random_uuid(), E'Gateway\nName', 'https://gateway.example', $1
		)`, []any{syntheticSecret}},
		{"NULL node UUID", `SELECT public.control_register_relay_node(
			NULL, 'Relay Node', $2, 'v1', 'http://node.example', $1,
			ARRAY['management_health_read']::text[]
		)`, []any{syntheticSecret, nodeType}},
		{"invalid node display", `SELECT public.control_register_relay_node(
			gen_random_uuid(), ' ', $2, 'v1', 'http://node.example', $1,
			ARRAY['management_health_read']::text[]
		)`, []any{syntheticSecret, nodeType}},
	} {
		err := assetSavepoint(t, ctx, tx, testCase.name, func() error {
			_, err := tx.Exec(ctx, testCase.statement, testCase.arguments...)
			return err
		})
		requireSQLState(t, err, "22023")
		var pgErr *pgconn.PgError
		if strings.Contains(err.Error(), syntheticSecret) ||
			(errors.As(err, &pgErr) && strings.Contains(pgErr.Detail, syntheticSecret)) {
			t.Fatalf("%s reflected Secret reference in error: %v", testCase.name, err)
		}
	}

	var gatewayID, nodeID string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&gatewayID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	register := func() error {
		if _, err := tx.Exec(ctx, `SELECT public.control_register_gateway($1, 'Gateway', 'HTTPS://Gateway.EXAMPLE:443/api', 'vault://control/gateway')`, gatewayID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT public.control_register_relay_node(
			$1, 'Relay Node', $2, 'v1', 'http://node.example:80',
			'docker-secret://relay/node', $3::text[]
		)`, nodeID, nodeType, []string{assetHealthCapability})
		return err
	}
	if err := register(); err != nil {
		t.Fatalf("initial registration: %v", err)
	}
	if err := register(); err != nil {
		t.Fatalf("identical registration replay: %v", err)
	}

	var gatewayEndpoint, nodeEndpoint string
	var gatewaySecret, nodeSecret bool
	if err := tx.QueryRow(ctx, `SELECT management_endpoint, reader_secret_configured FROM gateway_instances WHERE singleton_id=1`).Scan(&gatewayEndpoint, &gatewaySecret); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT management_endpoint, reader_secret_configured FROM relay_node_assets WHERE instance_id=$1`, nodeID).Scan(&nodeEndpoint, &nodeSecret); err != nil {
		t.Fatal(err)
	}
	if gatewayEndpoint != "https://gateway.example/api" || nodeEndpoint != "http://node.example" || !gatewaySecret || !nodeSecret {
		t.Fatalf("canonical registration mismatch: gateway=%q/%v node=%q/%v", gatewayEndpoint, gatewaySecret, nodeEndpoint, nodeSecret)
	}

	err = assetSavepoint(t, ctx, tx, "conflicting gateway replay", func() error {
		_, err := tx.Exec(ctx, `SELECT public.control_register_gateway($1, 'Changed Gateway', 'https://gateway.example/api', 'vault://control/gateway')`, gatewayID)
		return err
	})
	requireSQLState(t, err, "23505")

	err = assetSavepoint(t, ctx, tx, "unsupported node capability", func() error {
		_, err := tx.Exec(ctx, `SELECT public.control_register_relay_node(
			gen_random_uuid(), 'Invalid Node', $1, 'v1', 'http://invalid-node', NULL,
			ARRAY['management_write']::text[]
		)`, nodeType)
		return err
	})
	requireSQLState(t, err, "22023")

	var invalidNodes int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM relay_node_assets WHERE display_name='Invalid Node'`).Scan(&invalidNodes); err != nil {
		t.Fatal(err)
	}
	if invalidNodes != 0 {
		t.Fatalf("failed registration left %d partial node rows", invalidNodes)
	}
}

func TestProviderPolicyHistoryIsImmutableCanonicalAndDatabaseTimed(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	nodeType := "policy-" + assetFixtureSuffix(t)
	registerAssetDriver(t, ctx, tx, nodeType)

	var firstActivation, replayActivation string
	if err := tx.QueryRow(ctx, `SELECT public.control_activate_provider_policy(
		$1, 'v1', ARRAY['anthropic','openai'], ARRAY['gemini'], 'integration-test', NULL
	)::text`, nodeType).Scan(&firstActivation); err != nil {
		t.Fatalf("activate initial policy: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT public.control_activate_provider_policy(
		$1, 'v1', ARRAY['anthropic','openai'], ARRAY['gemini'], 'integration-test', NULL
	)::text`, nodeType).Scan(&replayActivation); err != nil {
		t.Fatalf("replay current policy: %v", err)
	}
	if replayActivation != firstActivation {
		t.Fatalf("policy replay activation = %s, want %s", replayActivation, firstActivation)
	}

	var versionCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM provider_inventory_policy_versions WHERE node_type=$1`, nodeType).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 1 {
		t.Fatalf("policy replay created %d versions, want 1", versionCount)
	}

	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	lateBoundary := databaseNow.Add(2 * time.Hour)
	earlyBoundary := databaseNow.Add(time.Hour)
	for _, activation := range []struct {
		active   []string
		inactive []string
		at       time.Time
	}{
		{[]string{"gemini"}, []string{"anthropic", "openai"}, lateBoundary},
		{[]string{"openai"}, []string{"anthropic", "gemini"}, earlyBoundary},
	} {
		if _, err := tx.Exec(ctx, `SELECT public.control_activate_provider_policy($1, 'v1', $2::text[], $3::text[], 'integration-test', $4)`, nodeType, activation.active, activation.inactive, activation.at); err != nil {
			t.Fatalf("schedule policy at %s: %v", activation.at, err)
		}
	}

	var intervalCount, overlapCount int
	if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE upper(active_range) IS NULL)
		FROM provider_inventory_policy_activations WHERE node_type=$1`, nodeType).Scan(&intervalCount, &overlapCount); err != nil {
		t.Fatal(err)
	}
	if intervalCount != 3 || overlapCount != 1 {
		t.Fatalf("activation history intervals=%d open=%d, want 3/1", intervalCount, overlapCount)
	}

	err = assetSavepoint(t, ctx, tx, "noncanonical provider set", func() error {
		_, err := tx.Exec(ctx, `INSERT INTO provider_inventory_policy_versions (
			node_type, driver_contract_version, active_providers, out_of_scope_providers, created_by
		) VALUES ($1, 'v1', ARRAY['openai','openai'], ARRAY['gemini'], 'integration-test')`, nodeType)
		return err
	})
	requireSQLState(t, err, "23514")
	err = assetSavepoint(t, ctx, tx, "overlapping provider sets", func() error {
		_, err := tx.Exec(ctx, `SELECT public.control_activate_provider_policy($1, 'v1', ARRAY['openai'], ARRAY['openai'], 'integration-test', NULL)`, nodeType)
		return err
	})
	requireSQLState(t, err, "22023")
	err = assetSavepoint(t, ctx, tx, "backfilled policy", func() error {
		_, err := tx.Exec(ctx, `SELECT public.control_activate_provider_policy($1, 'v1', ARRAY['openai'], ARRAY['gemini'], 'integration-test', CURRENT_TIMESTAMP - interval '1 minute')`, nodeType)
		return err
	})
	requireSQLState(t, err, "22023")
	err = assetSavepoint(t, ctx, tx, "mutate immutable policy", func() error {
		_, err := tx.Exec(ctx, `UPDATE provider_inventory_policy_versions SET created_by='mutator' WHERE node_type=$1`, nodeType)
		return err
	})
	requireSQLState(t, err, "42501")
	err = assetSavepoint(t, ctx, tx, "truncate immutable policy", func() error {
		_, err := tx.Exec(ctx, `TRUNCATE provider_inventory_policy_versions CASCADE`)
		return err
	})
	requireSQLState(t, err, "42501")
}

func TestMonitoringIntervalsAndRegistrarLeastPrivilege(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	nodeType := "monitor-" + assetFixtureSuffix(t)
	registerAssetDriver(t, ctx, tx, nodeType)
	var nodeID string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT public.control_register_relay_node(
		$1, 'Monitored Node', $2, 'v1', 'http://monitor-node', NULL, ARRAY[$3]::text[]
	)`, nodeID, nodeType, assetHealthCapability); err != nil {
		t.Fatalf("register monitoring node: %v", err)
	}

	var enabledID, replayID string
	if err := tx.QueryRow(ctx, `SELECT public.control_set_node_inventory_monitoring($1, true, NULL, 'deployment_enable', 'integration-test')::text`, nodeID).Scan(&enabledID); err != nil {
		t.Fatalf("enable monitoring: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT public.control_set_node_inventory_monitoring($1, true, NULL, 'deployment_enable', 'integration-test')::text`, nodeID).Scan(&replayID); err != nil {
		t.Fatalf("replay monitoring enable: %v", err)
	}
	if enabledID != replayID {
		t.Fatalf("monitoring replay ID = %s, want %s", replayID, enabledID)
	}
	var disabledID *string
	if err := tx.QueryRow(ctx, `SELECT public.control_set_node_inventory_monitoring($1, false, CURRENT_TIMESTAMP + interval '1 hour', 'scheduled_disable', 'integration-test')::text`, nodeID).Scan(&disabledID); err != nil {
		t.Fatalf("schedule monitoring disable: %v", err)
	}
	if disabledID == nil || *disabledID != enabledID {
		t.Fatalf("scheduled disable ID = %v, want %s", disabledID, enabledID)
	}
	var replayDisabledID *string
	if err := tx.QueryRow(ctx, `SELECT public.control_set_node_inventory_monitoring($1, false, CURRENT_TIMESTAMP + interval '1 hour', 'scheduled_disable', 'integration-test')::text`, nodeID).Scan(&replayDisabledID); err != nil {
		t.Fatalf("replay monitoring disable: %v", err)
	}
	if replayDisabledID == nil || *replayDisabledID != enabledID {
		t.Fatalf("scheduled disable replay ID = %v, want %s", replayDisabledID, enabledID)
	}

	var start, finish, endRecordedAt time.Time
	var endReason, endActor string
	if err := tx.QueryRow(ctx, `SELECT effective_from, effective_to, end_reason, end_actor, end_recorded_at
		FROM relay_node_inventory_monitoring_activations WHERE monitoring_activation_id=$1`, enabledID).
		Scan(&start, &finish, &endReason, &endActor, &endRecordedAt); err != nil {
		t.Fatal(err)
	}
	if !finish.After(start) {
		t.Fatalf("monitoring interval is not half-open and positive: [%s,%s)", start, finish)
	}
	if endReason != "scheduled_disable" || endActor != "integration-test" || endRecordedAt.IsZero() {
		t.Fatalf("monitoring close metadata = %q/%q/%s", endReason, endActor, endRecordedAt)
	}

	for _, testCase := range []struct {
		name      string
		statement string
	}{
		{"NULL enabled", `SELECT public.control_set_node_inventory_monitoring($1, NULL, NULL, 'deployment_enable', 'integration-test')`},
		{"NULL reason", `SELECT public.control_set_node_inventory_monitoring($1, true, NULL, NULL, 'integration-test')`},
		{"NULL actor", `SELECT public.control_set_node_inventory_monitoring($1, true, NULL, 'deployment_enable', NULL)`},
		{"enable with disable reason", `SELECT public.control_set_node_inventory_monitoring($1, true, NULL, 'deployment_disable', 'integration-test')`},
		{"disable with enable reason", `SELECT public.control_set_node_inventory_monitoring($1, false, NULL, 'deployment_enable', 'integration-test')`},
	} {
		err = assetSavepoint(t, ctx, tx, testCase.name, func() error {
			_, err := tx.Exec(ctx, testCase.statement, nodeID)
			return err
		})
		requireSQLState(t, err, "22023")
	}
	err = assetSavepoint(t, ctx, tx, "conflicting disable replay", func() error {
		_, err := tx.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
			$1, false, CURRENT_TIMESTAMP + interval '1 hour', 'scheduled_disable', 'different-actor'
		)`, nodeID)
		return err
	})
	requireSQLState(t, err, "23505")

	err = assetSavepoint(t, ctx, tx, "monitoring backfill", func() error {
		_, err := tx.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring($1, true, CURRENT_TIMESTAMP - interval '1 minute', 'scheduled_enable', 'integration-test')`, nodeID)
		return err
	})
	requireSQLState(t, err, "22023")

	if _, err := tx.Exec(ctx, `SET LOCAL ROLE relay_control_runtime`); err != nil {
		t.Fatalf("assume runtime role: %v", err)
	}
	var configured bool
	if err := tx.QueryRow(ctx, `SELECT reader_secret_configured FROM relay_node_assets WHERE instance_id=$1`, nodeID).Scan(&configured); err != nil {
		t.Fatalf("runtime read generated Secret state: %v", err)
	}
	err = assetSavepoint(t, ctx, tx, "runtime secret read", func() error {
		_, err := tx.Exec(ctx, `SELECT reader_secret_ref FROM relay_node_assets LIMIT 1`)
		return err
	})
	requireSQLState(t, err, "42501")
	err = assetSavepoint(t, ctx, tx, "runtime direct write", func() error {
		_, err := tx.Exec(ctx, `UPDATE relay_node_assets SET display_name='Compromised' WHERE instance_id=$1`, nodeID)
		return err
	})
	requireSQLState(t, err, "42501")
}

func TestAssetRegistryConcurrentPolicyActivationSerializesPerScope(t *testing.T) {
	ownerURL := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	setup, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close(context.Background())

	suffix := assetFixtureSuffix(t)
	nodeType := "concurrent-" + suffix
	if _, err := setup.Exec(ctx, `SELECT public.control_register_node_driver($1, 'v1', 'Concurrent Driver', 'active', ARRAY[$2]::text[])`, nodeType, assetHealthCapability); err != nil {
		t.Fatalf("register concurrent fixture driver: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cleanupErr := pgx.Connect(context.Background(), ownerURL)
		if cleanupErr != nil {
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM provider_inventory_policy_activations WHERE node_type=$1`, nodeType)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM provider_inventory_policy_bindings WHERE node_type=$1`, nodeType)
		_, _ = cleanup.Exec(context.Background(), `ALTER TABLE provider_inventory_policy_versions DISABLE TRIGGER provider_inventory_policy_versions_immutable`)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM provider_inventory_policy_versions WHERE node_type=$1`, nodeType)
		_, _ = cleanup.Exec(context.Background(), `ALTER TABLE provider_inventory_policy_versions ENABLE TRIGGER provider_inventory_policy_versions_immutable`)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM driver_capabilities WHERE node_type=$1`, nodeType)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM node_drivers WHERE node_type=$1`, nodeType)
	})

	var databaseNow time.Time
	if err := setup.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	boundary := databaseNow.Add(time.Hour)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for index, provider := range []string{"openai", "anthropic"} {
		wg.Add(1)
		go func(index int, provider string) {
			defer wg.Done()
			conn, err := pgx.Connect(ctx, ownerURL)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close(context.Background())
			<-start
			_, err = conn.Exec(ctx, `SELECT public.control_activate_provider_policy(
				$1, 'v1', ARRAY[$2]::text[], ARRAY['other']::text[], $3, $4
			)`, nodeType, provider, fmt.Sprintf("concurrent-%d", index), boundary)
			errs <- err
		}(index, provider)
	}
	close(start)
	wg.Wait()
	close(errs)

	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23P01" || pgErr.Code == "23505") {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent activation error: %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent same-boundary activations: successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}

	var activationCount, bindingCount int
	if err := setup.QueryRow(ctx, `SELECT count(*) FROM provider_inventory_policy_activations WHERE node_type=$1`, nodeType).Scan(&activationCount); err != nil {
		t.Fatal(err)
	}
	if err := setup.QueryRow(ctx, `SELECT count(*) FROM provider_inventory_policy_bindings WHERE node_type=$1`, nodeType).Scan(&bindingCount); err != nil {
		t.Fatal(err)
	}
	if activationCount != 1 || bindingCount != 1 {
		t.Fatalf("serialized state activations=%d bindings=%d, want 1/1", activationCount, bindingCount)
	}
}

func TestAssetRegistryConcurrentMonitoringEnableIsIdempotent(t *testing.T) {
	ownerURL := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	setup, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close(context.Background())

	suffix := assetFixtureSuffix(t)
	nodeType := "monitor-concurrent-" + suffix
	var nodeID string
	if err := setup.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `SELECT public.control_register_node_driver($1, 'v1', 'Concurrent Monitor Driver', 'active', ARRAY[$2]::text[])`, nodeType, assetHealthCapability); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `SELECT public.control_register_relay_node(
		$1, 'Concurrent Monitor Node', $2, 'v1', 'http://concurrent-monitor', NULL, ARRAY[$3]::text[]
	)`, nodeID, nodeType, assetHealthCapability); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupErr := pgx.Connect(context.Background(), ownerURL)
		if cleanupErr != nil {
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1`, nodeID)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM node_capabilities WHERE instance_id=$1`, nodeID)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM relay_node_assets WHERE instance_id=$1`, nodeID)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM driver_capabilities WHERE node_type=$1`, nodeType)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM node_drivers WHERE node_type=$1`, nodeType)
	})

	start := make(chan struct{})
	results := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := pgx.Connect(ctx, ownerURL)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close(context.Background())
			<-start
			var activationID string
			err = conn.QueryRow(ctx, `SELECT public.control_set_node_inventory_monitoring(
				$1, true, NULL, 'deployment_enable', 'concurrent-test'
			)::text`, nodeID).Scan(&activationID)
			if err != nil {
				errs <- err
				return
			}
			results <- activationID
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent monitoring enable: %v", err)
	}
	var activationIDs []string
	for activationID := range results {
		activationIDs = append(activationIDs, activationID)
	}
	if len(activationIDs) != 2 || activationIDs[0] != activationIDs[1] {
		t.Fatalf("concurrent monitoring IDs = %v, want two identical IDs", activationIDs)
	}
	var activationCount int
	if err := setup.QueryRow(ctx, `SELECT count(*) FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1`, nodeID).Scan(&activationCount); err != nil {
		t.Fatal(err)
	}
	if activationCount != 1 {
		t.Fatalf("concurrent monitoring created %d intervals, want 1", activationCount)
	}
}

func TestAssetRegistryDeploymentTemplatesProtectSecretsAndTransactions(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	registerSQL, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "asset-registry", "register-assets.sql"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(registerSQL)
	for _, required := range []string{
		"BEGIN ISOLATION LEVEL SERIALIZABLE",
		`\getenv gateway_reader_secret_ref CONTROL_GATEWAY_READER_SECRET_REF`,
		`\getenv node_reader_secret_ref CONTROL_NODE_READER_SECRET_REF`,
		`\bind x:gateway_reader_secret_ref`,
		`\bind x:node_reader_secret_ref`,
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("register-assets.sql missing %q", required)
		}
	}
	if strings.Contains(content, "--set=gateway_reader_secret_ref") || strings.Contains(content, "--set=node_reader_secret_ref") {
		t.Fatal("register-assets.sql exposes a Secret reference through a command-line variable")
	}
	if strings.Contains(content, `NULLIF(:'gateway_reader_secret_ref'`) || strings.Contains(content, `NULLIF(:'node_reader_secret_ref'`) {
		t.Fatal("register-assets.sql interpolates a Secret reference into PostgreSQL statement text")
	}
	for _, name := range []string{"activate-provider-policy.sql", "set-node-monitoring.sql"} {
		body, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "asset-registry", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "BEGIN ISOLATION LEVEL SERIALIZABLE") {
			t.Fatalf("%s lacks an explicit SERIALIZABLE transaction", name)
		}
		if !strings.Contains(string(body), "SET LOCAL TIME ZONE 'UTC'") {
			t.Fatalf("%s does not pin timestamp parsing to UTC", name)
		}
		if !strings.Contains(string(body), "effective_at_valid") ||
			!strings.Contains(string(body), "(Z|[+-][0-9]{2}:[0-9]{2})") {
			t.Fatalf("%s does not reject offset-free effective_at values", name)
		}
	}
	reconcile, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "asset-registry", "reconcile.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reconcile), "REPEATABLE READ READ ONLY") || !strings.Contains(string(reconcile), "control_reconcile_asset_registry") {
		t.Fatal("reconcile.sql lacks the read-only definer reconciliation contract")
	}
}

func TestAssetRegistrarCanOnlyUseControlledWriteAndReconciliationFunctions(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `INSERT INTO environments(
		singleton_id,environment_id,name,environment_type
	) VALUES (1,'asset-registrar-test','Asset Registrar Test','dev')
	ON CONFLICT (singleton_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
		t.Fatalf("assume registrar role: %v", err)
	}
	nodeType := "registrar-" + assetFixtureSuffix(t)
	if _, err := tx.Exec(ctx, `SELECT public.control_register_node_driver(
		$1, 'v1', 'Registrar Driver', 'active', ARRAY[$2]::text[]
	)`, nodeType, assetHealthCapability); err != nil {
		t.Fatalf("registrar controlled driver registration: %v", err)
	}
	var nodeID string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT public.control_register_relay_node(
		$1, 'Registrar Node', $2, 'v1', 'http://registrar-node',
		'vault://relay/reader', ARRAY[$3]::text[]
	)`, nodeID, nodeType, assetHealthCapability); err != nil {
		t.Fatalf("registrar controlled node registration: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1, 'v1', ARRAY['openai'], ARRAY['other'], 'registrar-test',
		'initial registrar policy activation', NULL
	)`, nodeType); err != nil {
		t.Fatalf("registrar controlled policy activation: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1, true, NULL, 'deployment_enable', 'registrar-test'
	)`, nodeID); err != nil {
		t.Fatalf("registrar controlled monitoring activation: %v", err)
	}

	var issueRows int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.control_reconcile_asset_registry(
		(SELECT environment_id FROM environments WHERE singleton_id=1),
		(SELECT environment_type FROM environments WHERE singleton_id=1)
	)`).Scan(&issueRows); err != nil {
		t.Fatalf("registrar reconciliation: %v", err)
	}
	if issueRows == 0 {
		t.Fatal("reconciliation returned no fixed issue rows")
	}

	for name, statement := range map[string]string{
		"direct asset read":  `SELECT display_name FROM relay_node_assets LIMIT 1`,
		"Secret column read": `SELECT reader_secret_ref FROM relay_node_assets LIMIT 1`,
		"direct asset write": `UPDATE relay_node_assets SET display_name='Compromised' WHERE instance_id='` + nodeID + `'`,
		"auth table read":    `SELECT login_name FROM control_admin_users LIMIT 1`,
	} {
		err := assetSavepoint(t, ctx, tx, name, func() error {
			_, err := tx.Exec(ctx, statement)
			return err
		})
		requireSQLState(t, err, "42501")
	}
}

func TestAssetRegistryBoundSecretIsAbsentFromPGStatActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observer := connectTestDatabase(t)
	worker, err := pgx.Connect(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close(context.Background())
	if _, err := worker.Exec(ctx, `SET ROLE relay_control_asset_registrar`); err != nil {
		t.Fatalf("assume registrar role: %v", err)
	}

	secretCanary := "vault://synthetic/pg-stat-secret-canary"
	done := make(chan error, 1)
	go func() {
		_, err := worker.Exec(ctx, `SELECT pg_sleep(1), $1::text IS NOT NULL /* asset_secret_bind_canary */`, secretCanary)
		done <- err
	}()

	var statementText string
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		err := observer.QueryRow(ctx, `SELECT query FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND state='active'
			  AND query LIKE '%asset_secret_bind_canary%'
			LIMIT 1`).Scan(&statementText)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if statementText == "" {
		t.Fatal("did not observe bound registrar statement in pg_stat_activity")
	}
	if strings.Contains(statementText, secretCanary) {
		t.Fatalf("pg_stat_activity exposed Secret bind value: %s", statementText)
	}
	if !strings.Contains(statementText, "$1") {
		t.Fatalf("pg_stat_activity did not retain the bind placeholder: %s", statementText)
	}
	if err := <-done; err != nil {
		t.Fatalf("bound registrar statement: %v", err)
	}
}

func proxyFreeEnvironment() []string {
	blocked := map[string]bool{
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true,
		"http_proxy": true, "https_proxy": true, "all_proxy": true,
	}
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !blocked[name] {
			environment = append(environment, entry)
		}
	}
	if os.Getenv("GOCACHE") == "" {
		environment = append(environment, "GOCACHE="+filepath.Join(os.TempDir(), "relay-control-asset-migration-go-build"))
	}
	return environment
}

func runAssetGoose(t *testing.T, ctx context.Context, repositoryRoot, databaseURL string, arguments ...string) error {
	t.Helper()
	commandArguments := []string{"tool", "goose", "-dir", "../migrations", "postgres", databaseURL}
	commandArguments = append(commandArguments, arguments...)
	command := exec.CommandContext(ctx, "go", commandArguments...)
	command.Dir = filepath.Join(repositoryRoot, "tools")
	command.Env = proxyFreeEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("goose %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func TestAssetRegistryProtectedDownPreservesEnvironmentIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ownerConfig, err := pgx.ParseConfig(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	databaseName := strings.ReplaceAll(assetFixtureSuffix(t), "-", "_")
	if len(databaseName) > 60 {
		databaseName = databaseName[:60]
	}
	maintenanceConfig := ownerConfig.Copy()
	maintenanceConfig.Database = "postgres"
	maintenance, err := pgx.ConnectConfig(ctx, maintenanceConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	defer maintenance.Close(context.Background())
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+identifier); err != nil {
		t.Fatalf("create isolated migration database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = maintenance.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, databaseName)
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+identifier)
	})

	isolatedConfig := ownerConfig.Copy()
	isolatedConfig.Database = databaseName
	isolatedLocation, err := url.Parse(testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	isolatedLocation.Path = "/" + databaseName
	isolatedURL := isolatedLocation.String()
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	if err := runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "up"); err != nil {
		t.Fatal(err)
	}
	isolated, err := pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, `INSERT INTO environments (environment_id, name, environment_type)
		VALUES ('protected-down', 'Protected Down', 'dev')`); err != nil {
		isolated.Close(ctx)
		t.Fatal(err)
	}
	isolated.Close(ctx)

	if err := runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "down-to", "2"); err != nil {
		t.Fatalf("empty-registry protected down: %v", err)
	}
	isolated, err = pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	var environmentID string
	if err := isolated.QueryRow(ctx, `SELECT environment_id FROM environments WHERE singleton_id=1`).Scan(&environmentID); err != nil {
		isolated.Close(ctx)
		t.Fatalf("environment after empty down: %v", err)
	}
	if environmentID != "protected-down" {
		isolated.Close(ctx)
		t.Fatalf("environment ID after down = %q", environmentID)
	}
	var runtimeCanInsert, registrarCanSelect bool
	if err := isolated.QueryRow(ctx, `SELECT
		has_table_privilege('relay_control_runtime', 'environments', 'INSERT'),
		has_table_privilege('relay_control_asset_registrar', 'environments', 'SELECT')`).
		Scan(&runtimeCanInsert, &registrarCanSelect); err != nil {
		isolated.Close(ctx)
		t.Fatalf("read baseline ACL after down: %v", err)
	}
	if !runtimeCanInsert || registrarCanSelect {
		isolated.Close(ctx)
		t.Fatalf("down ACL runtime_insert=%v registrar_select=%v, want true/false", runtimeCanInsert, registrarCanSelect)
	}
	isolated.Close(ctx)

	if err := runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "up"); err != nil {
		t.Fatal(err)
	}
	isolated, err = pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := isolated.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		isolated.Close(ctx)
		t.Fatal(err)
	}
	if _, err := writer.Exec(ctx, `SELECT public.control_register_node_driver(
		'protected-down-node', 'v1', 'Protected Driver', 'active', ARRAY['management_health_read']::text[]
	)`); err != nil {
		_ = writer.Rollback(ctx)
		isolated.Close(ctx)
		t.Fatalf("seed concurrent registrar write: %v", err)
	}
	err = runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "down-to", "2")
	if err == nil || !strings.Contains(err.Error(), "could not obtain lock") {
		_ = writer.Rollback(ctx)
		isolated.Close(ctx)
		t.Fatalf("down racing an in-flight registrar did not fail immediately on its NOWAIT lock: %v", err)
	}
	if err := writer.Commit(ctx); err != nil {
		isolated.Close(ctx)
		t.Fatalf("commit concurrent registrar write: %v", err)
	}
	isolated.Close(ctx)

	err = runAssetGoose(t, ctx, repositoryRoot, isolatedURL, "down-to", "2")
	if err == nil || !strings.Contains(err.Error(), "asset registry migration down requires an empty registry") {
		t.Fatalf("down racing registrar write error = %v", err)
	}
	isolated, err = pgx.ConnectConfig(ctx, isolatedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close(context.Background())
	var version int
	if err := isolated.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("migration version after refused down = %d, want 3", version)
	}
	if err := isolated.QueryRow(ctx, `SELECT environment_id FROM environments WHERE singleton_id=1`).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	if environmentID != "protected-down" {
		t.Fatalf("environment ID after refused down = %q", environmentID)
	}
}
