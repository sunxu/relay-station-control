package store_test

import (
	"context"
	"testing"

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
