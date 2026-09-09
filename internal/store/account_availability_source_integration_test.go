package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestAntigravityAvailabilityMetadataFinalize(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixtureWithProviders(t, ctx, database, []string{"antigravity"})
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	claimToken := fixtureToken()
	setAvailabilityPollRunning(t, ctx, database, fixture.pollRunID, claimToken)
	active := "file_active"
	reason := "token_invalid"
	email := "availability@example.invalid"
	request := availabilityFinalizeRequest(fixture, claimToken, email, &active, &reason)
	if err := repository.FinalizeFenced(ctx, request); err != nil {
		t.Fatal(err)
	}
	var snapshotEvidence, snapshotReason, currentEvidence, currentReason *string
	if err := database.owner.QueryRow(ctx, `SELECT s.availability_runtime_evidence,s.auth_failure_reason,
		i.availability_runtime_evidence,i.auth_failure_reason
		FROM account_inventory_snapshot_items s JOIN account_inventory i
		ON i.instance_id=s.instance_id AND i.account_key=s.account_key
		WHERE s.poll_run_id=$1`, fixture.pollRunID).Scan(&snapshotEvidence, &snapshotReason, &currentEvidence, &currentReason); err != nil {
		t.Fatal(err)
	}
	if snapshotEvidence == nil || *snapshotEvidence != active || snapshotReason == nil || *snapshotReason != reason || currentEvidence == nil || *currentEvidence != active || currentReason == nil || *currentReason != reason {
		t.Fatalf("metadata snapshot/current = %v/%v %v/%v", snapshotEvidence, snapshotReason, currentEvidence, currentReason)
	}

}

func TestLegacyV1FinalizeClearsAvailabilityMetadataPostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixtureWithProviders(t, ctx, database, []string{"antigravity"})
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := fixtureToken()
	setAvailabilityPollRunning(t, ctx, database, fixture.pollRunID, firstToken)
	active, reason := "file_active", "token_invalid"
	if err := repository.FinalizeFenced(ctx, availabilityFinalizeRequest(fixture, firstToken, "legacy@example.invalid", &active, &reason)); err != nil {
		t.Fatal(err)
	}
	// A fenced/stale legacy finalize is a strict no-op: it must not clear
	// metadata left by the successful v2 promotion above.
	providers := `[ {"provider":"antigravity","identifiable_count":1,"missing_identity_count":0,"duplicate_identity_count":0,"identity_complete":true,"snapshot_complete":true,"degraded":false,"reason":"complete"} ]`
	items := `[ {"provider":"antigravity","account_key":"antigravity:legacy@example.invalid","email":"legacy@example.invalid","basic_status":"active","success_count":0,"failed_count":0,"recent_request_count":0,"last_refresh_unix":null,"next_retry_unix":null,"updated_at_unix":null} ]`
	var staleFinalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
		$1,$2,true,true,true,'runtime',true,true,false,'success','none',1,1,0,0,0,'v1.0.0','abcdef1',$3::jsonb,$4::jsonb,'[]'::jsonb)`, fixture.pollRunID, uuid.New(), providers, items).Scan(&staleFinalized); err != nil {
		t.Fatal(err)
	}
	if staleFinalized != 0 {
		t.Fatalf("stale legacy finalize rows = %d", staleFinalized)
	}
	var staleEvidence, staleReason *string
	if err := database.owner.QueryRow(ctx, `SELECT availability_runtime_evidence,auth_failure_reason FROM account_inventory WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID, "antigravity:legacy@example.invalid").Scan(&staleEvidence, &staleReason); err != nil {
		t.Fatal(err)
	}
	if staleEvidence == nil || *staleEvidence != active || staleReason == nil || *staleReason != reason {
		t.Fatalf("stale legacy finalize changed metadata: %v/%v", staleEvidence, staleReason)
	}
	secondRun := uuid.New()
	secondSlot := currentPollSlot(t, ctx, database.owner).Add(-5 * time.Minute)
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at)
		VALUES($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, secondRun, fixture.instanceID,
		fixture.nodeType, fixture.contract, secondSlot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	secondToken := fixtureToken()
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),last_started_at=clock_timestamp(),
		lease_expires_at=clock_timestamp()+interval '60 seconds',lease_fencing_token=$2 WHERE poll_run_id=$1`, secondRun, secondToken); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory SET current_poll_run_id=NULL,current_scheduled_at=$2 WHERE instance_id=$1`, fixture.instanceID, secondSlot.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states SET current_poll_run_id=NULL,current_scheduled_at=$2 WHERE instance_id=$1 AND provider='antigravity'`, fixture.instanceID, secondSlot.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory SET current_poll_run_id=$2 WHERE instance_id=$1`, fixture.instanceID, secondRun); err != nil {
		t.Fatal(err)
	}
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
		$1,$2,true,true,true,'runtime',true,true,false,'success','none',1,1,0,0,0,'v1.0.0','abcdef1',$3::jsonb,$4::jsonb,'[]'::jsonb)`, secondRun, secondToken, providers, items).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("legacy finalize rows = %d", finalized)
	}
	var evidence, authReason *string
	if err := database.owner.QueryRow(ctx, `SELECT availability_runtime_evidence,auth_failure_reason FROM account_inventory WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID, "antigravity:legacy@example.invalid").Scan(&evidence, &authReason); err != nil {
		t.Fatal(err)
	}
	if evidence != nil || authReason != nil {
		t.Fatalf("legacy v1 retained availability metadata: %v/%v", evidence, authReason)
	}
	availability, err := pollstore.NewAccountAvailabilityRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := availability.BatchAccountAvailability(ctx, fixture.instanceID, []string{"antigravity:legacy@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if rows["antigravity:legacy@example.invalid"].State == "AVAILABLE" {
		t.Fatal("legacy v1 metadata clear was interpreted as AVAILABLE")
	}
}

func TestLegacyV1FinalizeDoesNotClearUnpromotedMetadataPostgres(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := insertSnapshotPollFixtureWithProviders(t, ctx, database, []string{"antigravity"})
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := fixtureToken()
	setAvailabilityPollRunning(t, ctx, database, fixture.pollRunID, firstToken)
	active, reason := "file_active", "token_invalid"
	if err := repository.FinalizeFenced(ctx, availabilityFinalizeRequest(fixture, firstToken, "unpromoted@example.invalid", &active, &reason)); err != nil {
		t.Fatal(err)
	}
	secondRun := uuid.New()
	secondSlot := currentPollSlot(t, ctx, database.owner).Add(-5 * time.Minute)
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,max_attempts,poll_start_grace_seconds,created_at)
		VALUES($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, secondRun, fixture.instanceID, fixture.nodeType, fixture.contract, secondSlot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	secondToken := fixtureToken()
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs SET status='running',attempt_count=1,first_started_at=clock_timestamp(),last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',lease_fencing_token=$2 WHERE poll_run_id=$1`, secondRun, secondToken); err != nil {
		t.Fatal(err)
	}
	providers := `[ {"provider":"antigravity","identifiable_count":1,"missing_identity_count":0,"duplicate_identity_count":0,"identity_complete":true,"snapshot_complete":true,"degraded":false,"reason":"complete"} ]`
	items := `[ {"provider":"antigravity","account_key":"antigravity:unpromoted@example.invalid","email":"unpromoted@example.invalid","basic_status":"active","success_count":0,"failed_count":0,"recent_request_count":0,"last_refresh_unix":null,"next_retry_unix":null,"updated_at_unix":null} ]`
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_finalize_account_inventory_poll_run_with_lifecycle($1,$2,true,true,true,'disk_fallback',true,true,false,'success','none',1,1,0,0,0,'v1.0.0','abcdef1',$3::jsonb,$4::jsonb,'[]'::jsonb)`, secondRun, secondToken, providers, items).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("unpromoted legacy finalize rows = %d", finalized)
	}
	var evidence, authReason *string
	if err := database.owner.QueryRow(ctx, `SELECT availability_runtime_evidence,auth_failure_reason FROM account_inventory WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID, "antigravity:unpromoted@example.invalid").Scan(&evidence, &authReason); err != nil {
		t.Fatal(err)
	}
	if evidence == nil || *evidence != active || authReason == nil || *authReason != reason {
		t.Fatalf("unpromoted legacy finalize cleared metadata: %v/%v", evidence, authReason)
	}
}

func setAvailabilityPollRunning(t *testing.T, ctx context.Context, database *isolatedJobDatabase, pollID, token uuid.UUID) {
	t.Helper()
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs SET status='running',attempt_count=1,first_started_at=clock_timestamp(),last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',lease_fencing_token=$2 WHERE poll_run_id=$1`, pollID, token); err != nil {
		t.Fatal(err)
	}
}

func fixtureToken() uuid.UUID { return uuid.New() }

func availabilityFinalizeRequest(f snapshotPollFixture, token uuid.UUID, email string, evidence, reason *string) inventorypoll.FinalizeRequest {
	return inventorypoll.FinalizeRequest{PollRunID: f.pollRunID, FencingToken: token,
		Node:          inventorypoll.NodeEvidence{TransportSuccess: true, ResponseShapeValid: true, ContractValid: true, InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true, SnapshotComplete: true, RecognizedRecordCount: 1, Result: drivers.ResultSuccess, Reason: drivers.ReasonNone, Version: "v1.0.0", Commit: "abcdef1"},
		Providers:     []inventorypoll.ProviderEvidence{{Provider: f.provider, RecognizedRecordCount: 1, IdentityComplete: true, SnapshotComplete: true, Reason: inventorypoll.ProviderReasonComplete}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{Provider: f.provider, AccountKey: f.provider + ":" + email, Email: email, BasicStatus: drivers.AccountStateActive, AvailabilityRuntimeEvidence: evidence, AuthFailureReason: reason}}}
}
