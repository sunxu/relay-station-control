package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

const providerStateQueryFunction = "public.control_query_account_inventory_provider_states_v1(uuid)"

func TestAccountInventoryProviderStateQueryRuntimeEnvelopeAndACL(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)

	var runtimeCanExecute, registrarCanExecute bool
	if err := database.owner.QueryRow(ctx, `SELECT
		has_function_privilege('relay_control_runtime',$1,'EXECUTE'),
		has_function_privilege('relay_control_asset_registrar',$1,'EXECUTE')`,
		providerStateQueryFunction).Scan(&runtimeCanExecute, &registrarCanExecute); err != nil {
		t.Fatal(err)
	}
	if !runtimeCanExecute || registrarCanExecute {
		t.Fatalf("provider state query privilege mismatch: runtime=%v registrar=%v", runtimeCanExecute, registrarCanExecute)
	}

	repository, err := productstore.NewAccountInventoryProviderStateRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	page, err := repository.GetProviderStates(ctx, fixture.instanceID)
	if err != nil {
		t.Fatalf("runtime provider state query: %v", err)
	}
	if page.InstanceID != fixture.instanceID || page.ObservedAt.IsZero() {
		t.Fatalf("envelope = %+v, want instance and observed_at", page)
	}
	if len(page.Providers) != 0 {
		t.Fatalf("empty provider set = %+v, want []", page.Providers)
	}

	if _, err := database.runtime.Exec(ctx, `SELECT * FROM public.account_inventory_provider_states LIMIT 1`); err == nil {
		t.Fatal("runtime directly selected provider states")
	} else {
		requirePostgresCode(t, err, "42501")
	}
	if _, err := database.runtime.Exec(ctx, `SELECT * FROM public.account_inventory LIMIT 1`); err == nil {
		t.Fatal("runtime directly selected account inventory")
	} else {
		requirePostgresCode(t, err, "42501")
	}

	if _, err := repository.GetProviderStates(ctx, [16]byte{}); err == nil {
		t.Fatal("zero UUID was accepted")
	}
	if _, err := repository.GetProviderStates(ctx, uuid.New()); !errors.Is(err, productstore.ErrAccountInventoryProviderStateNotFound) {
		t.Fatalf("unknown Node error = %v, want ErrAccountInventoryProviderStateNotFound", err)
	}
}

func TestAccountInventoryProviderStateQueryExpectedHeldAndHealthBadges(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES ($1,CURRENT_TIMESTAMP,'deployment_enable','integration-test',CURRENT_TIMESTAMP)`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewAccountInventoryProviderStateRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	page, err := repository.GetProviderStates(ctx, fixture.instanceID)
	if err != nil || len(page.Providers) != 1 {
		t.Fatalf("expected missing-state Provider row: page=%+v err=%v", page, err)
	}
	if page.Providers[0].Provider != "openai" || page.Providers[0].SnapshotFreshness != "unknown" || page.Providers[0].State != nil {
		t.Fatalf("missing-state projection = %+v", page.Providers[0])
	}

	fixture.finalize(t, ctx, database, nil)
	page, err = repository.GetProviderStates(ctx, fixture.instanceID)
	if err != nil || len(page.Providers) != 1 {
		t.Fatalf("expected promoted Provider row: page=%+v err=%v", page, err)
	}
	provider := page.Providers[0]
	if provider.State == nil || *provider.State != "current" || provider.SnapshotFreshness != "fresh" || provider.HealthDegraded == nil || *provider.HealthDegraded || provider.HealthReason == nil || *provider.HealthReason != "none" {
		t.Fatalf("fresh normal projection = %+v", provider)
	}

	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states
		SET health_degraded=true,health_reason='transport_failed',
			health_scheduled_at=current_scheduled_at+interval '5 minutes'
		WHERE instance_id=$1 AND provider='openai'`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	page, err = repository.GetProviderStates(ctx, fixture.instanceID)
	if err != nil || len(page.Providers) != 1 || page.Providers[0].SnapshotFreshness != "fresh" || page.Providers[0].HealthDegraded == nil || !*page.Providers[0].HealthDegraded || page.Providers[0].HealthReason == nil || *page.Providers[0].HealthReason != "transport_failed" {
		t.Fatalf("fresh degraded projection = page=%+v err=%v", page, err)
	}
}

func TestAccountInventoryProviderStateQueryStaleHealthAndHeldOutOfScope(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES ($1,CURRENT_TIMESTAMP,'deployment_enable','integration-test',CURRENT_TIMESTAMP)`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	fixture.finalize(t, ctx, database, nil)
	repository, err := productstore.NewAccountInventoryProviderStateRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `WITH as_of AS (SELECT statement_timestamp() AS value)
		UPDATE account_inventory_provider_states AS state
		SET last_complete_at=as_of.value-interval '16 minutes',
			source_observed_at=as_of.value-interval '16 minutes',
			updated_at=as_of.value
		FROM as_of
		WHERE state.instance_id=$1 AND state.provider='openai'`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	page, err := repository.GetProviderStates(ctx, fixture.instanceID)
	if err != nil || len(page.Providers) != 1 || page.Providers[0].SnapshotFreshness != "stale" || page.Providers[0].HealthDegraded == nil || *page.Providers[0].HealthDegraded {
		t.Fatalf("stale normal projection = page=%+v err=%v", page, err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states
		SET health_degraded=true,health_reason='transport_failed'
		WHERE instance_id=$1 AND provider='openai'`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	page, err = repository.GetProviderStates(ctx, fixture.instanceID)
	if err != nil || len(page.Providers) != 1 || page.Providers[0].SnapshotFreshness != "stale" || page.Providers[0].HealthDegraded == nil || !*page.Providers[0].HealthDegraded {
		t.Fatalf("stale degraded projection = page=%+v err=%v", page, err)
	}
	openaiHealthAt := page.Providers[0].HealthScheduledAt
	var accountRows int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory WHERE instance_id=$1`, fixture.instanceID).Scan(&accountRows); err != nil {
		t.Fatal(err)
	}
	if accountRows != 0 {
		t.Fatalf("zero-account fixture has %d account rows", accountRows)
	}
	if _, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['legacy']::text[],ARRAY['openai']::text[],'integration-test','held-test',NULL)`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	page, err = repository.GetProviderStates(ctx, fixture.instanceID)
	if err != nil || len(page.Providers) != 2 {
		t.Fatalf("held out-of-scope projection = page=%+v err=%v", page, err)
	}
	var foundLegacy, foundOpenAI bool
	for _, provider := range page.Providers {
		switch provider.Provider {
		case "legacy":
			foundLegacy = provider.State == nil && provider.SnapshotFreshness == "unknown" && provider.MonitoringStatus == "active"
		case "openai":
			foundOpenAI = provider.State != nil && provider.MonitoringStatus == "out_of_scope" && provider.SnapshotFreshness == "out_of_scope" && provider.HealthScheduledAt != nil && openaiHealthAt != nil && provider.HealthScheduledAt.Equal(*openaiHealthAt)
		}
	}
	if !foundLegacy || !foundOpenAI {
		t.Fatalf("Expected UNION Held rows = %+v", page.Providers)
	}
}
