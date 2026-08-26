package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestAccountInventorySnapshotStoreDTOsRedactIdentity(t *testing.T) {
	const canary = "snapshot-store-canary@example.invalid"
	values := []any{
		pollstore.FinalizePollRunInput{},
		pollstore.CurrentAccountInventorySnapshotItem{
			AccountKey: "openai:" + canary, NormalizedEmail: canary,
		},
		generated.AccountInventorySnapshotItem{
			AccountKey: "openai:" + canary, NormalizedEmail: canary,
		},
		generated.AccountInventoryPollDuplicate{AccountKey: "openai:" + canary},
		generated.ListCurrentAccountInventorySnapshotRow{
			AccountKey: "openai:" + canary, NormalizedEmail: canary,
		},
		generated.FinalizeAccountInventoryPollRunParams{
			SnapshotItems: []byte(canary), DuplicateEvidence: []byte(canary),
		},
	}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if output := fmt.Sprintf(format, value); strings.Contains(output, canary) {
				t.Fatalf("format %s exposed snapshot identity", format)
			}
		}
	}
}

type snapshotPollFixture struct {
	instanceID uuid.UUID
	policyID   uuid.UUID
	nodeType   string
	contract   string
	provider   string
	pollRunID  uuid.UUID
}

func insertSnapshotPollFixture(t *testing.T, ctx context.Context, database *isolatedJobDatabase) snapshotPollFixture {
	return insertSnapshotPollFixtureWithProviders(t, ctx, database, []string{"openai"})
}

func insertSnapshotPollFixtureWithProviders(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, activeProviders []string,
) snapshotPollFixture {
	t.Helper()
	fixture := snapshotPollFixture{
		instanceID: uuid.New(), policyID: uuid.New(), nodeType: "snapshot-test",
		contract: "v1", provider: activeProviders[0], pollRunID: uuid.New(),
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Snapshot Test Driver')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,
		management_endpoint,reader_secret_ref
	) VALUES ($1,'Snapshot Test Node',$2,$3,'http://snapshot.example',
		'docker-secret://synthetic/snapshot-reader')`, fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES ($1,$2,$3,$4,ARRAY['legacy'],'integration-test')`, fixture.policyID,
		fixture.nodeType, fixture.contract, activeProviders); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES ($1,$2,$3,'integration-test',clock_timestamp())`, fixture.nodeType,
		fixture.contract, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		activated_by,created_at
	) VALUES ($1,$2,$3,CURRENT_TIMESTAMP,'integration-test',CURRENT_TIMESTAMP)`,
		fixture.nodeType, fixture.contract, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	slot := currentPollSlot(t, ctx, database.owner)
	var databaseNow time.Time
	if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	// A 299-second grace intentionally leaves the last second of every slot
	// ineligible. Use the next aligned slot near that boundary so fixture
	// claims cannot flake while retaining the production constraint shape.
	if !databaseNow.Before(slot.Add(295 * time.Second)) {
		slot = slot.Add(5 * time.Minute)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs (
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, fixture.pollRunID,
		fixture.instanceID, fixture.nodeType, fixture.contract, slot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestInventorySnapshotFinalizePromotesAndReadsCurrentProvider(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	claim, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.PollRunID != fixture.pollRunID {
		t.Fatalf("claimed poll = %s", claim.PollRunID)
	}
	lastRefresh, updatedAt := int64(1735689600), int64(1735689660)
	email := "snapshot@example.invalid"
	request := inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "v1.2.3", Commit: "1234567",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: fixture.provider, AccountKey: fixture.provider + ":" + email,
			Email: email, BasicStatus: drivers.AccountStateActive,
			SuccessCount: 11, FailedCount: 2, RecentRequestCount: 3,
			LastRefreshUnix: &lastRefresh, UpdatedAtUnix: &updatedAt,
		}},
	}
	if err := repository.FinalizeFenced(ctx, request); err != nil {
		t.Fatal(err)
	}

	var status string
	var applied bool
	var skipped *string
	var itemCount, stateCount int
	if err := database.owner.QueryRow(ctx, `SELECT run.status,result.promotion_applied,
		result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=run.poll_run_id),
		(SELECT count(*) FROM account_inventory_provider_states
		 WHERE instance_id=run.instance_id AND provider=$2)
		FROM account_inventory_poll_runs AS run
		JOIN account_inventory_poll_provider_results AS result USING (poll_run_id)
		WHERE run.poll_run_id=$1`, fixture.pollRunID, fixture.provider).
		Scan(&status, &applied, &skipped, &itemCount, &stateCount); err != nil {
		t.Fatal(err)
	}
	if status != "finalized" || !applied || skipped != nil || itemCount != 1 || stateCount != 1 {
		t.Fatalf("promotion evidence = %s/%t/%v/%d/%d", status, applied, skipped, itemCount, stateCount)
	}
	page, err := repository.CurrentProviderSnapshot(ctx, fixture.instanceID, fixture.provider, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].NormalizedEmail != email || page[0].SuccessCount != 11 ||
		page[0].BasicStatus != drivers.AccountStateActive {
		t.Fatalf("current snapshot page mismatch: count=%d", len(page))
	}
	var observationTimesEqual bool
	if err := database.owner.QueryRow(ctx, `SELECT run.observed_at=item.observed_at
		AND run.observed_at=state.source_observed_at
		AND run.observed_at=state.last_complete_at
		FROM account_inventory_poll_runs AS run
		JOIN account_inventory_snapshot_items AS item USING (poll_run_id)
		JOIN account_inventory_provider_states AS state
		  ON state.current_poll_run_id=run.poll_run_id AND state.provider=item.provider
		WHERE run.poll_run_id=$1`, fixture.pollRunID).Scan(&observationTimesEqual); err != nil {
		t.Fatal(err)
	}
	if !observationTimesEqual {
		t.Fatal("poll, snapshot item, and Provider state observation times diverged")
	}
}

func TestInventorySnapshotPromotesCompleteProviderBesideIncompleteProvider(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixtureWithProviders(t, ctx, database, []string{"anthropic", "openai"})
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	email := "independent@example.invalid"
	if err := repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			Degraded: true, RecognizedRecordCount: 1, UnidentifiedRecordCount: 1,
			Result: drivers.ResultDegraded, Reason: drivers.ReasonNone,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{
			{Provider: "anthropic", IdentityComplete: true, Degraded: true,
				Reason: inventorypoll.ProviderReasonIdentityIncomplete},
			{Provider: "openai", RecognizedRecordCount: 1, IdentityComplete: true,
				SnapshotComplete: true, Reason: inventorypoll.ProviderReasonComplete},
		},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: "openai", AccountKey: "openai:" + email, Email: email,
			BasicStatus: drivers.AccountStateActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := database.owner.Query(ctx, `SELECT provider,promotion_applied,
		promotion_skipped_reason FROM account_inventory_poll_provider_results
		WHERE poll_run_id=$1 ORDER BY provider`, fixture.pollRunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type result struct {
		provider string
		applied  bool
		reason   *string
	}
	var results []result
	for rows.Next() {
		var item result
		if err := rows.Scan(&item.provider, &item.applied, &item.reason); err != nil {
			t.Fatal(err)
		}
		results = append(results, item)
	}
	var items, states int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)`,
		fixture.pollRunID, fixture.instanceID).Scan(&items, &states); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].provider != "anthropic" || results[0].applied ||
		results[0].reason == nil || *results[0].reason != "provider_identity_incomplete" ||
		results[1].provider != "openai" || !results[1].applied || results[1].reason != nil ||
		items != 1 || states != 1 {
		t.Fatalf("independent provider promotion mismatch: results=%v items=%d states=%d",
			results, items, states)
	}
}

func TestInventorySnapshotFinalizePersistsDuplicateWithoutPromotion(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	accountKey := fixture.provider + ":duplicate@example.invalid"
	request := inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			Degraded: true, RecognizedRecordCount: 3,
			Result: drivers.ResultDegraded, Reason: drivers.ReasonNone,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, RecognizedRecordCount: 3,
			DuplicateIdentityCount: 1, Degraded: true,
			Reason: inventorypoll.ProviderReasonIdentityIncomplete,
		}},
		Duplicates: []inventorypoll.DuplicateEvidence{{
			Provider: fixture.provider, AccountKey: accountKey, OccurrenceCount: 2,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider:    fixture.provider,
			AccountKey:  fixture.provider + ":unique-beside-duplicate@example.invalid",
			Email:       "unique-beside-duplicate@example.invalid",
			BasicStatus: drivers.AccountStateActive,
		}},
	}
	if err := repository.FinalizeFenced(ctx, request); err != nil {
		t.Fatal(err)
	}
	var applied bool
	var skipped string
	var duplicateCount, snapshotCount, stateCount int
	if err := database.owner.QueryRow(ctx, `SELECT result.promotion_applied,
		result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_poll_duplicates WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
		FROM account_inventory_poll_provider_results AS result
		WHERE result.poll_run_id=$1`, fixture.pollRunID, fixture.instanceID).
		Scan(&applied, &skipped, &duplicateCount, &snapshotCount, &stateCount); err != nil {
		t.Fatal(err)
	}
	if applied || skipped != "provider_duplicate" || duplicateCount != 1 || snapshotCount != 0 || stateCount != 0 {
		t.Fatalf("duplicate promotion evidence = %t/%s/%d/%d/%d",
			applied, skipped, duplicateCount, snapshotCount, stateCount)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_provider_states(
		instance_id,provider,current_poll_run_id,current_scheduled_at,last_complete_at,
		source_observed_at,source_node_version,source_node_commit,state,updated_at
	) SELECT instance_id,$2,poll_run_id,scheduled_at,observed_at,observed_at,
		node_version,node_commit,'current',observed_at
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixture.pollRunID,
		fixture.provider); err == nil {
		t.Fatal("provider state accepted a skipped promotion source")
	}
}

func TestInventorySnapshotFinalizeSkipsChangedPolicyAtomically(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	newPolicyID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES ($1,$2,$3,ARRAY['anthropic'],ARRAY['legacy'],'integration-test')`,
		newPolicyID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	policySwitch, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var boundary time.Time
	if err := policySwitch.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&boundary); err != nil {
		_ = policySwitch.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := policySwitch.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_to=$1 WHERE node_type=$2 AND driver_contract_version=$3
		AND effective_to IS NULL`, boundary, fixture.nodeType, fixture.contract); err != nil {
		_ = policySwitch.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := policySwitch.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		activated_by,created_at
	) VALUES ($2,$3,$1,$4,'integration-test',$4)`, newPolicyID,
		fixture.nodeType, fixture.contract, boundary); err != nil {
		_ = policySwitch.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := policySwitch.Exec(ctx, `UPDATE provider_inventory_policy_bindings
		SET policy_version_id=$1,bound_at=$4
		WHERE node_type=$2 AND driver_contract_version=$3`, newPolicyID,
		fixture.nodeType, fixture.contract, boundary); err != nil {
		_ = policySwitch.Rollback(ctx)
		t.Fatal(err)
	}
	if err := policySwitch.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	email := "policy-change@example.invalid"
	request := inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: fixture.provider, AccountKey: fixture.provider + ":" + email,
			Email: email, BasicStatus: drivers.AccountStateUnknown,
		}},
	}
	if err := repository.FinalizeFenced(ctx, request); err != nil {
		t.Fatal(err)
	}
	var runReason, providerReason string
	var applied bool
	var itemCount, stateCount int
	if err := database.owner.QueryRow(ctx, `SELECT run.promotion_skipped_reason,
		result.promotion_applied,result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
		FROM account_inventory_poll_runs AS run
		JOIN account_inventory_poll_provider_results AS result USING (poll_run_id)
		WHERE run.poll_run_id=$1`, fixture.pollRunID, fixture.instanceID).
		Scan(&runReason, &applied, &providerReason, &itemCount, &stateCount); err != nil {
		t.Fatal(err)
	}
	if runReason != "policy_changed" || applied || providerReason != "policy_changed" || itemCount != 0 || stateCount != 0 {
		t.Fatalf("changed policy evidence = %s/%t/%s/%d/%d",
			runReason, applied, providerReason, itemCount, stateCount)
	}
}

func TestInventorySnapshotFuturePolicyBindingKeepsCurrentActivationEligible(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy(
		$1,$2,ARRAY['anthropic'],ARRAY['legacy'],'integration-test',
		clock_timestamp()+interval '1 minute')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	email := "future-policy@example.invalid"
	if err := repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: fixture.provider, AccountKey: fixture.provider + ":" + email,
			Email: email, BasicStatus: drivers.AccountStateActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var runReason *string
	var applied bool
	if err := database.owner.QueryRow(ctx, `SELECT run.promotion_skipped_reason,
		result.promotion_applied FROM account_inventory_poll_runs AS run
		JOIN account_inventory_poll_provider_results AS result USING (poll_run_id)
		WHERE run.poll_run_id=$1`, fixture.pollRunID).Scan(&runReason, &applied); err != nil {
		t.Fatal(err)
	}
	if runReason != nil || !applied {
		t.Fatalf("future-effective policy incorrectly blocked current promotion: %v/%t", runReason, applied)
	}
}

func TestInventorySnapshotFinalizeAndPolicyActivationHaveOnlyAtomicOutcomes(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	email := "policy-race@example.invalid"
	start := make(chan struct{})
	finalizeResult := make(chan error, 1)
	activationResult := make(chan error, 1)
	go func() {
		<-start
		finalizeResult <- repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
			PollRunID: fixture.pollRunID, FencingToken: token,
			Node: inventorypoll.NodeEvidence{
				TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
				InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
				SnapshotComplete: true, RecognizedRecordCount: 1,
				Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
				Version: "unknown", Commit: "unknown",
			},
			Providers: []inventorypoll.ProviderEvidence{{
				Provider: fixture.provider, RecognizedRecordCount: 1,
				IdentityComplete: true, SnapshotComplete: true,
				Reason: inventorypoll.ProviderReasonComplete,
			}},
			SnapshotItems: []inventorypoll.SnapshotCandidate{{
				Provider: fixture.provider, AccountKey: fixture.provider + ":" + email,
				Email: email, BasicStatus: drivers.AccountStateActive,
			}},
		})
	}()
	go func() {
		<-start
		_, err := database.owner.Exec(ctx, `SELECT public.control_activate_provider_policy(
			$1,$2,ARRAY['anthropic'],ARRAY['legacy'],'integration-test',NULL)`,
			fixture.nodeType, fixture.contract)
		activationResult <- err
	}()
	close(start)
	if err := <-finalizeResult; err != nil {
		t.Fatal(err)
	}
	if err := <-activationResult; err != nil {
		t.Fatal(err)
	}
	var applied bool
	var skipReason *string
	var runReason *string
	var items, states int
	if err := database.owner.QueryRow(ctx, `SELECT result.promotion_applied,
		result.promotion_skipped_reason,run.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
		FROM account_inventory_poll_runs AS run
		JOIN account_inventory_poll_provider_results AS result USING (poll_run_id)
		WHERE run.poll_run_id=$1`, fixture.pollRunID, fixture.instanceID).
		Scan(&applied, &skipReason, &runReason, &items, &states); err != nil {
		t.Fatal(err)
	}
	oldPolicyWon := applied && skipReason == nil && runReason == nil && items == 1 && states == 1
	newPolicyWon := !applied && skipReason != nil && *skipReason == "policy_changed" &&
		runReason != nil && *runReason == "policy_changed" && items == 0 && states == 0
	if !oldPolicyWon && !newPolicyWon {
		t.Fatalf("mixed finalize/policy outcome: %t/%v/%v/%d/%d",
			applied, skipReason, runReason, items, states)
	}
}

func TestInventorySnapshotStalePollCannotMoveProviderPointerBackward(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	olderPollID, newerToken, olderToken := uuid.New(), uuid.New(), uuid.New()
	var currentSlot time.Time
	if err := database.owner.QueryRow(ctx, `SELECT scheduled_at FROM account_inventory_poll_runs
		WHERE poll_run_id=$1`, fixture.pollRunID).Scan(&currentSlot); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, olderPollID,
		fixture.instanceID, fixture.nodeType, fixture.contract,
		currentSlot.Add(-5*time.Minute), fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '30 seconds',
		lease_fencing_token=CASE WHEN poll_run_id=$1 THEN $2::uuid ELSE $3::uuid END
		WHERE poll_run_id IN ($1,$4)`, fixture.pollRunID, newerToken, olderToken, olderPollID); err != nil {
		t.Fatal(err)
	}
	finalize := func(pollRunID, token uuid.UUID, email string) error {
		return repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
			PollRunID: pollRunID, FencingToken: token,
			Node: inventorypoll.NodeEvidence{
				TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
				InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
				SnapshotComplete: true, RecognizedRecordCount: 1,
				Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
				Version: "unknown", Commit: "unknown",
			},
			Providers: []inventorypoll.ProviderEvidence{{
				Provider: fixture.provider, RecognizedRecordCount: 1,
				IdentityComplete: true, SnapshotComplete: true,
				Reason: inventorypoll.ProviderReasonComplete,
			}},
			SnapshotItems: []inventorypoll.SnapshotCandidate{{
				Provider: fixture.provider, AccountKey: fixture.provider + ":" + email,
				Email: email, BasicStatus: drivers.AccountStateActive,
			}},
		})
	}
	if err := finalize(fixture.pollRunID, newerToken, "newer@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err := finalize(olderPollID, olderToken, "older@example.invalid"); err != nil {
		t.Fatal(err)
	}
	var currentPollID uuid.UUID
	var olderApplied bool
	var olderReason string
	var olderItems int
	if err := database.owner.QueryRow(ctx, `SELECT state.current_poll_run_id,
		result.promotion_applied,result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1)
		FROM account_inventory_provider_states AS state
		JOIN account_inventory_poll_provider_results AS result
		  ON result.poll_run_id=$1 AND result.provider=state.provider
		WHERE state.instance_id=$2 AND state.provider=$3`, olderPollID,
		fixture.instanceID, fixture.provider).
		Scan(&currentPollID, &olderApplied, &olderReason, &olderItems); err != nil {
		t.Fatal(err)
	}
	if currentPollID != fixture.pollRunID || olderApplied || olderReason != "stale_poll" || olderItems != 0 {
		t.Fatalf("stale promotion changed pointer: %s/%t/%s/%d",
			currentPollID, olderApplied, olderReason, olderItems)
	}
}

func TestInventorySnapshotNodeIdentityIncompleteSkipsPromotion(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	err = repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, Degraded: true,
			UnidentifiedRecordCount: 1,
			Result:                  drivers.ResultDegraded, Reason: drivers.ReasonNone,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, IdentityComplete: true, Degraded: true,
			Reason: inventorypoll.ProviderReasonNodeIdentityIncomplete,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var applied bool
	var reason string
	var states int
	if err := database.owner.QueryRow(ctx, `SELECT promotion_applied,promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
		FROM account_inventory_poll_provider_results WHERE poll_run_id=$1`,
		fixture.pollRunID, fixture.instanceID).Scan(&applied, &reason, &states); err != nil {
		t.Fatal(err)
	}
	if applied || reason != "provider_identity_incomplete" || states != 0 {
		t.Fatalf("node-incomplete promotion = %t/%s/%d", applied, reason, states)
	}
}

func TestInventorySnapshotContractFailureFinalizesWithoutPromotion(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, Degraded: true,
			Result: drivers.ResultFailed, Reason: drivers.ReasonContractInvalid,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, IdentityComplete: true, Degraded: true,
			Reason: inventorypoll.ProviderReasonContractInvalid,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	var applied bool
	var snapshotRows, stateRows int
	if err := database.owner.QueryRow(ctx, `SELECT run.status,
		result.promotion_applied,result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
		FROM account_inventory_poll_runs AS run
		JOIN account_inventory_poll_provider_results AS result USING (poll_run_id)
		WHERE run.poll_run_id=$1`, fixture.pollRunID, fixture.instanceID).
		Scan(&status, &applied, &reason, &snapshotRows, &stateRows); err != nil {
		t.Fatal(err)
	}
	if status != "finalized" || applied || reason != "contract_invalid" || snapshotRows != 0 || stateRows != 0 {
		t.Fatalf("contract failure evidence = %s/%t/%s/%d/%d",
			status, applied, reason, snapshotRows, stateRows)
	}
}

func TestInventorySnapshotFinalizeCapacityKeepsLeaseAndGraceMargin(t *testing.T) {
	for _, itemCount := range []int{0, 1, 1000} {
		t.Run(fmt.Sprintf("items_%d", itemCount), func(t *testing.T) {
			ctx := context.Background()
			database := newIsolatedJobDatabase(t)
			fixture := insertSnapshotPollFixture(t, ctx, database)
			repository, err := pollstore.NewInventoryPollRepository(database.runtime)
			if err != nil {
				t.Fatal(err)
			}
			token := uuid.New()
			if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
				Token: token, LeaseDuration: 30 * time.Second,
			}); err != nil {
				t.Fatal(err)
			}
			items := make([]inventorypoll.SnapshotCandidate, 0, itemCount)
			for index := 0; index < itemCount; index++ {
				email := fmt.Sprintf("capacity-%04d@example.invalid", index)
				items = append(items, inventorypoll.SnapshotCandidate{
					Provider: fixture.provider, AccountKey: fixture.provider + ":" + email,
					Email: email, BasicStatus: drivers.AccountStateActive,
					SuccessCount: uint64(index), RecentRequestCount: uint64(index % 1001),
				})
			}
			startedAt := time.Now()
			err = repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
				PollRunID: fixture.pollRunID, FencingToken: token,
				Node: inventorypoll.NodeEvidence{
					TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
					InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
					SnapshotComplete: true, RecognizedRecordCount: uint32(itemCount),
					Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
					Version: "unknown", Commit: "unknown",
				},
				Providers: []inventorypoll.ProviderEvidence{{
					Provider: fixture.provider, RecognizedRecordCount: uint32(itemCount),
					IdentityComplete: true, SnapshotComplete: true,
					Reason: inventorypoll.ProviderReasonComplete,
				}},
				SnapshotItems: items,
			})
			elapsed := time.Since(startedAt)
			if err != nil {
				t.Fatal(err)
			}
			var storedItems, providerStates int
			if err := database.owner.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
				(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)`,
				fixture.pollRunID, fixture.instanceID).Scan(&storedItems, &providerStates); err != nil {
				t.Fatal(err)
			}
			if storedItems != itemCount || providerStates != 1 {
				t.Fatalf("capacity finalize counts = %d/%d, want %d/1", storedItems, providerStates, itemCount)
			}
			if elapsed >= 15*time.Second {
				t.Fatalf("capacity finalize exceeded half of 30s lease: items=%d elapsed=%s", itemCount, elapsed)
			}
			t.Logf("snapshot_capacity items=%d elapsed_ms=%d lease_margin_ms=%d grace_margin_ms=%d",
				itemCount, elapsed.Milliseconds(),
				(30*time.Second - elapsed).Milliseconds(),
				(120*time.Second - elapsed).Milliseconds())
		})
	}
}

func TestInventorySnapshotBindingWaitCompletesWithinLeaseMargin(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `SELECT 1 FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2 FOR UPDATE`,
		fixture.nodeType, fixture.contract); err != nil {
		_ = lock.Rollback(ctx)
		t.Fatal(err)
	}
	startedAt := time.Now()
	result := make(chan error, 1)
	go func() {
		result <- repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
			PollRunID: fixture.pollRunID, FencingToken: token,
			Node: inventorypoll.NodeEvidence{
				TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
				InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
				SnapshotComplete: true, Result: drivers.ResultSuccess,
				Reason: drivers.ReasonNone, Version: "unknown", Commit: "unknown",
			},
			Providers: []inventorypoll.ProviderEvidence{{
				Provider: fixture.provider, IdentityComplete: true, SnapshotComplete: true,
				Reason: inventorypoll.ProviderReasonComplete,
			}},
		})
	}()
	time.Sleep(300 * time.Millisecond)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(startedAt)
	if elapsed < 250*time.Millisecond || elapsed >= 15*time.Second {
		t.Fatalf("binding wait finalize outside expected lease margin: %s", elapsed)
	}
	t.Logf("snapshot_binding_wait elapsed_ms=%d lease_margin_ms=%d grace_margin_ms=%d",
		elapsed.Milliseconds(), (30*time.Second - elapsed).Milliseconds(),
		(120*time.Second - elapsed).Milliseconds())
}

func TestInventorySnapshotRejectsContradictoryNodeAndProviderCompleteness(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	email := "contradiction@example.invalid"
	err = repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, Degraded: true,
			RecognizedRecordCount: 1, UnidentifiedRecordCount: 1,
			Result: drivers.ResultDegraded, Reason: drivers.ReasonNone,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: fixture.provider, AccountKey: fixture.provider + ":" + email,
			Email: email, BasicStatus: drivers.AccountStateActive,
		}},
	})
	if err == nil {
		t.Fatal("contradictory node/provider completeness unexpectedly finalized")
	}
	var status string
	var providers, snapshots, states int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixture.pollRunID,
		fixture.instanceID).Scan(&status, &providers, &snapshots, &states); err != nil {
		t.Fatal(err)
	}
	if status != "running" || providers != 0 || snapshots != 0 || states != 0 {
		t.Fatalf("contradictory finalize leaked state: %s/%d/%d/%d", status, providers, snapshots, states)
	}
}

func TestInventorySnapshotFinalizeLeaseExpiryWhileWaitingForPolicyRollsBack(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `SELECT 1 FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2 FOR UPDATE`,
		fixture.nodeType, fixture.contract); err != nil {
		_ = lock.Rollback(ctx)
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
			PollRunID: fixture.pollRunID, FencingToken: token,
			Node: inventorypoll.NodeEvidence{
				Degraded: true, Result: drivers.ResultFailed, Reason: drivers.ReasonTimeout,
				Version: "unknown", Commit: "unknown",
			},
			Providers: []inventorypoll.ProviderEvidence{{
				Provider: fixture.provider, IdentityComplete: true, Degraded: true,
				Reason: inventorypoll.ProviderReasonTransportFailed,
			}},
		})
	}()
	time.Sleep(1200 * time.Millisecond)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, inventorypoll.ErrLostLease) {
		t.Fatalf("lease expiry error = %v", err)
	}
	var status string
	var providerRows, snapshotRows, stateRows int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixture.pollRunID,
		fixture.instanceID).Scan(&status, &providerRows, &snapshotRows, &stateRows); err != nil {
		t.Fatal(err)
	}
	if status != "running" || providerRows != 0 || snapshotRows != 0 || stateRows != 0 {
		t.Fatalf("expired finalize leaked state: %s/%d/%d/%d",
			status, providerRows, snapshotRows, stateRows)
	}
	t.Logf("snapshot_lease_expiry rollback=true provider_rows=%d snapshot_rows=%d state_rows=%d",
		providerRows, snapshotRows, stateRows)
}

func TestAccountInventorySnapshotRuntimeRoleCannotWriteEvidence(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	for name, statement := range map[string]string{
		"select snapshot":  `SELECT * FROM account_inventory_snapshot_items`,
		"select duplicate": `SELECT * FROM account_inventory_poll_duplicates`,
		"select state":     `SELECT * FROM account_inventory_provider_states`,
		"insert snapshot":  `INSERT INTO account_inventory_snapshot_items DEFAULT VALUES`,
		"update snapshot":  `UPDATE account_inventory_snapshot_items SET basic_status='unknown' WHERE false`,
		"delete duplicate": `DELETE FROM account_inventory_poll_duplicates WHERE false`,
		"update pointer":   `UPDATE account_inventory_provider_states SET state='current' WHERE false`,
		"truncate": `TRUNCATE account_inventory_snapshot_items,
			account_inventory_poll_duplicates,account_inventory_provider_states`,
	} {
		if _, err := database.runtime.Exec(ctx, statement); err == nil {
			t.Fatalf("runtime direct %s unexpectedly succeeded", name)
		}
	}
	var oldFinalizeAllowed bool
	if err := database.runtime.QueryRow(ctx, `SELECT has_function_privilege(
		current_user,
		'public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb)',
		'EXECUTE')`).Scan(&oldFinalizeAllowed); err != nil {
		t.Fatal(err)
	}
	if oldFinalizeAllowed {
		t.Fatal("runtime retained EXECUTE on legacy v1 poll finalize")
	}
}

func TestAccountInventorySnapshotMigrationDownRefusesPromotionEvidence(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.FinalizeFenced(ctx, inventorypoll.FinalizeRequest{
		PollRunID: fixture.pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			Degraded: true, Result: drivers.ResultFailed, Reason: drivers.ReasonTimeout,
			Version: "unknown", Commit: "unknown",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixture.provider, IdentityComplete: true, Degraded: true,
			Reason: inventorypoll.ProviderReasonTransportFailed,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err == nil {
		t.Fatal("snapshot migration down unexpectedly removed promotion evidence")
	}
	var version int
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id)
		FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Fatalf("snapshot protected down changed version to %d", version)
	}
}

func TestAccountInventorySnapshotLegacyV5UpgradePreservesUnevaluatedPromotion(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	providers := `[{
		"provider":"openai","identifiable_count":0,"missing_identity_count":0,
		"duplicate_identity_count":0,"identity_complete":true,
		"snapshot_complete":false,"degraded":true,"reason":"transport_failed"
	}]`
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*) FROM
		public.control_finalize_account_inventory_poll_run(
		$1,$2,false,false,false,NULL,false,false,true,'failed','timeout',
		0,0,0,0,0,'unknown','unknown',$3::jsonb
	)`, fixture.pollRunID, token, providers).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("legacy finalize rows = %d", finalized)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	var applied bool
	var skipped *string
	var itemCount, duplicateCount, stateCount int
	if err := database.owner.QueryRow(ctx, `SELECT result.promotion_applied,
		result.promotion_skipped_reason,
		(SELECT count(*) FROM account_inventory_snapshot_items),
		(SELECT count(*) FROM account_inventory_poll_duplicates),
		(SELECT count(*) FROM account_inventory_provider_states)
		FROM account_inventory_poll_provider_results AS result
		WHERE result.poll_run_id=$1 AND result.provider=$2`, fixture.pollRunID, fixture.provider).
		Scan(&applied, &skipped, &itemCount, &duplicateCount, &stateCount); err != nil {
		t.Fatal(err)
	}
	if applied || skipped != nil || itemCount != 0 || duplicateCount != 0 || stateCount != 0 {
		t.Fatalf("legacy promotion changed during upgrade: %t/%v/%d/%d/%d",
			applied, skipped, itemCount, duplicateCount, stateCount)
	}
	_, metrics, err := repository.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].PromotionEvaluated || metrics[0].PromotionApplied ||
		metrics[0].PromotionSkippedReason != "" {
		t.Fatalf("legacy promotion metric was fabricated: count=%d", len(metrics))
	}
}

func callRawSnapshotFinalize(
	ctx context.Context, database *isolatedJobDatabase, fixture snapshotPollFixture,
	token uuid.UUID, sourceCount, identifiableCount, unidentifiedCount int,
	providers, snapshots, duplicates string,
) error {
	var affected int
	return database.runtime.QueryRow(ctx, `SELECT count(*) FROM
		public.control_finalize_account_inventory_poll_run(
		$1,$2,true,true,true,'runtime',true,true,false,'success','none',
		$3,$4,$5,0,0,'unknown','unknown',$6::jsonb,$7::jsonb,$8::jsonb
	)`, fixture.pollRunID, token, sourceCount, identifiableCount, unidentifiedCount,
		providers, snapshots, duplicates).Scan(&affected)
}

func TestInventorySnapshotDatabaseRejectsInflatedAggregateCountsAtomically(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	providers := `[{
		"provider":"openai","identifiable_count":1,"missing_identity_count":0,
		"duplicate_identity_count":0,"identity_complete":true,
		"snapshot_complete":true,"degraded":false,"reason":"complete"
	}]`
	snapshots := `[{
		"provider":"openai","account_key":"openai:count@example.invalid",
		"email":"count@example.invalid","basic_status":"active",
		"success_count":0,"failed_count":0,"recent_request_count":0,
		"last_refresh_unix":null,"next_retry_unix":null,"updated_at_unix":null
	}]`
	if err := callRawSnapshotFinalize(ctx, database, fixture, token, 2, 1, 0,
		providers, snapshots, `[]`); err == nil {
		t.Fatal("database accepted inflated source count")
	}
	var status string
	var providerRows, snapshotRows int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixture.pollRunID).
		Scan(&status, &providerRows, &snapshotRows); err != nil {
		t.Fatal(err)
	}
	if status != "running" || providerRows != 0 || snapshotRows != 0 {
		t.Fatalf("invalid aggregate leaked state: %s/%d/%d", status, providerRows, snapshotRows)
	}
}

func TestInventorySnapshotDatabaseRejectsUnclosedIncompleteProviderCounts(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	providers := `[{
		"provider":"openai","identifiable_count":1000,"missing_identity_count":0,
		"duplicate_identity_count":1,"identity_complete":false,
		"snapshot_complete":false,"degraded":true,"reason":"identity_incomplete"
	}]`
	duplicates := `[{
		"provider":"openai","account_key":"openai:duplicate@example.invalid",
		"occurrence_count":2
	}]`
	var affected int
	err = database.runtime.QueryRow(ctx, `SELECT count(*) FROM
		public.control_finalize_account_inventory_poll_run(
		$1,$2,true,true,true,'runtime',true,false,true,'degraded','none',
		1000,1000,0,0,0,'unknown','unknown',$3::jsonb,'[]'::jsonb,$4::jsonb
	)`, fixture.pollRunID, token, providers, duplicates).Scan(&affected)
	if err == nil {
		t.Fatal("database accepted incomplete Provider counts without candidate/duplicate closure")
	}
	var status string
	var providerRows, duplicateRows, snapshotRows int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_poll_duplicates WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixture.pollRunID).
		Scan(&status, &providerRows, &duplicateRows, &snapshotRows); err != nil {
		t.Fatal(err)
	}
	if status != "running" || providerRows != 0 || duplicateRows != 0 || snapshotRows != 0 {
		t.Fatalf("unclosed incomplete Provider leaked state: %s/%d/%d/%d",
			status, providerRows, duplicateRows, snapshotRows)
	}
}

func TestInventorySnapshotDatabaseRejectsProviderMissingAboveNodeUnidentified(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	providers := `[{
		"provider":"openai","identifiable_count":0,"missing_identity_count":1,
		"duplicate_identity_count":0,"identity_complete":false,
		"snapshot_complete":false,"degraded":true,"reason":"identity_incomplete"
	}]`
	var affected int
	err = database.runtime.QueryRow(ctx, `SELECT count(*) FROM
		public.control_finalize_account_inventory_poll_run(
		$1,$2,true,true,true,'runtime',true,false,true,'degraded','none',
		0,0,0,0,0,'unknown','unknown',$3::jsonb,'[]'::jsonb,'[]'::jsonb
	)`, fixture.pollRunID, token, providers).Scan(&affected)
	if err == nil {
		t.Fatal("database accepted Provider missing count above Node unidentified count")
	}
	var status string
	var providerRows int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixture.pollRunID).
		Scan(&status, &providerRows); err != nil {
		t.Fatal(err)
	}
	if status != "running" || providerRows != 0 {
		t.Fatalf("invalid missing aggregate leaked state: %s/%d", status, providerRows)
	}
}

func TestInventorySnapshotDatabaseRejectsMalformedDuplicateWithoutCanaryReflection(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	const canary = "duplicate-json-canary@example.invalid"
	providers := `[{
		"provider":"openai","identifiable_count":0,"missing_identity_count":0,
		"duplicate_identity_count":0,"identity_complete":true,
		"snapshot_complete":true,"degraded":false,"reason":"complete"
	}]`
	duplicates := `[{"provider":"openai","account_key":"openai:` + canary +
		`","occurrence_count":"not-a-number"}]`
	err = callRawSnapshotFinalize(ctx, database, fixture, token, 0, 0, 0,
		providers, `[]`, duplicates)
	if err == nil {
		t.Fatal("database accepted malformed duplicate occurrence")
	}
	if strings.Contains(err.Error(), canary) || !strings.Contains(err.Error(), "duplicate evidence") {
		t.Fatalf("malformed duplicate returned unsafe or unstable error: %v", err)
	}
}

func TestInventorySnapshotDatabaseRejectsNullWrongTypedAndOversizedJSON(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixture(t, ctx, database)
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	validProviders := `[{
		"provider":"openai","identifiable_count":1,"missing_identity_count":0,
		"duplicate_identity_count":0,"identity_complete":true,
		"snapshot_complete":true,"degraded":false,"reason":"complete"
	}]`
	validSnapshot := `[{
		"provider":"openai","account_key":"openai:typed@example.invalid",
		"email":"typed@example.invalid","basic_status":"active",
		"success_count":0,"failed_count":0,"recent_request_count":0,
		"last_refresh_unix":null,"next_retry_unix":null,"updated_at_unix":null
	}]`
	tests := []struct {
		name       string
		providers  string
		snapshots  string
		duplicates string
	}{
		{name: "null provider array", providers: `null`, snapshots: `[]`, duplicates: `[]`},
		{name: "string boolean", providers: strings.Replace(validProviders, `"identity_complete":true`, `"identity_complete":"true"`, 1), snapshots: validSnapshot, duplicates: `[]`},
		{name: "null email", providers: validProviders, snapshots: strings.Replace(validSnapshot, `"email":"typed@example.invalid"`, `"email":null`, 1), duplicates: `[]`},
		{name: "recent request overflow", providers: validProviders, snapshots: strings.Replace(validSnapshot, `"recent_request_count":0`, `"recent_request_count":1001`, 1), duplicates: `[]`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := callRawSnapshotFinalize(ctx, database, fixture, token, 1, 1, 0,
				testCase.providers, testCase.snapshots, testCase.duplicates); err == nil {
				t.Fatal("database accepted invalid snapshot finalize JSON")
			}
		})
	}
	var status string
	var evidence int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixture.pollRunID).
		Scan(&status, &evidence); err != nil {
		t.Fatal(err)
	}
	if status != "running" || evidence != 0 {
		t.Fatalf("invalid JSON leaked finalize evidence: %s/%d", status, evidence)
	}
}

func TestInventorySnapshotImmutabilityGuardRejectsDirectAndParentCascadeDelete(t *testing.T) {
	ctx := context.Background()
	conn := connectTestDatabase(t)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE snapshot_guard_parent(id integer PRIMARY KEY);
		CREATE TEMP TABLE snapshot_guard_child(
			id integer PRIMARY KEY,
			parent_id integer REFERENCES snapshot_guard_parent(id) ON DELETE CASCADE
		);
		CREATE TRIGGER snapshot_guard_child_immutable
		BEFORE DELETE ON snapshot_guard_child FOR EACH ROW
		EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();
		INSERT INTO snapshot_guard_parent VALUES (1),(2);
		INSERT INTO snapshot_guard_child VALUES (1,1),(2,2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM snapshot_guard_parent WHERE id=1`); err == nil {
		t.Fatal("parent cascade unexpectedly bypassed immutable evidence guard")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE snapshot_guard_direct(id integer PRIMARY KEY);
		CREATE TRIGGER snapshot_guard_direct_immutable
		BEFORE DELETE ON snapshot_guard_direct FOR EACH ROW
		EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();
		INSERT INTO snapshot_guard_direct VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM snapshot_guard_direct WHERE id=1`); err == nil {
		t.Fatal("direct child evidence delete unexpectedly succeeded")
	}
}
