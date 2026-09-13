package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayLifecycleBindingSerializationPG18(t *testing.T) {
	t.Run("retire commits before bind", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		database, fixture, assets, bindings := newGatewayBindingRaceFixture(t, ctx)

		retireGatewayWithBinding(t, ctx, assets, bindings, fixture, fixture.insertNode(t, ctx, database), fixture.accountIDs[0], "gateway-retire-bind")

		lateNode := fixture.insertNode(t, ctx, database)
		result, err := bindings.Bind(ctx, store.BindParams{
			RelayNodeID:       lateNode,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  fixture.accountIDs[1],
			AdminID:           fixture.adminID,
			RequestID:         "retire-before-bind-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != store.RelayBindingOutcomeGatewayConflict {
			t.Fatalf("late bind outcome=%s, want gateway_conflict", result.Outcome)
		}
		assertNoCurrentBindingToGateway(t, ctx, database, fixture.gatewayID)
		assertHistoricalGatewayBinding(t, ctx, database, fixture.gatewayID, "gateway_retired")
	})

	t.Run("bind commits before retire", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		database, fixture, assets, bindings := newGatewayBindingRaceFixture(t, ctx)
		nodeID := fixture.insertNode(t, ctx, database)
		bindGatewayAccount(t, ctx, bindings, fixture, nodeID, fixture.accountIDs[0], "retire-after-bind")

		retireGatewayWithBinding(t, ctx, assets, bindings, fixture, uuid.Nil, 0, "gateway-retire-after-bind")

		assertNoCurrentBindingToGateway(t, ctx, database, fixture.gatewayID)
		assertHistoricalGatewayBinding(t, ctx, database, fixture.gatewayID, "gateway_retired")
		assertBindingHistoryRetained(t, ctx, database, fixture.gatewayID, 1)
	})

	t.Run("replace commits before rebind", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		database, fixture, assets, bindings := newGatewayBindingRaceFixture(t, ctx)
		nodeID := fixture.insertNode(t, ctx, database)
		bindGatewayAccount(t, ctx, bindings, fixture, nodeID, fixture.accountIDs[0], "replace-before-rebind")

		newGatewayID := uuid.New()
		replaceGateway(t, ctx, assets, fixture, newGatewayID, "replace-before-rebind")

		result, err := bindings.Rebind(ctx, store.RebindParams{
			RelayNodeID:          nodeID,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  fixture.accountIDs[1],
			AdminID:              fixture.adminID,
			RequestID:            "replace-before-rebind-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != store.RelayBindingOutcomeGatewayConflict {
			t.Fatalf("late rebind outcome=%s, want gateway_conflict", result.Outcome)
		}
		assertNoCurrentBindingToGateway(t, ctx, database, fixture.gatewayID)
		assertHistoricalGatewayBinding(t, ctx, database, fixture.gatewayID, "gateway_replaced")
		assertNoCurrentBindingToGateway(t, ctx, database, newGatewayID)
	})

	t.Run("rebind commits before replace", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		database, fixture, assets, bindings := newGatewayBindingRaceFixture(t, ctx)
		nodeID := fixture.insertNode(t, ctx, database)
		bindGatewayAccount(t, ctx, bindings, fixture, nodeID, fixture.accountIDs[0], "replace-after-rebind")

		result, err := bindings.Rebind(ctx, store.RebindParams{
			RelayNodeID:          nodeID,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  fixture.accountIDs[1],
			AdminID:              fixture.adminID,
			RequestID:            "rebind-before-replace-" + uuid.NewString(),
		})
		if err != nil || result.Outcome != store.RelayBindingOutcomeSuccess {
			t.Fatalf("rebind: outcome=%s err=%v", result.Outcome, err)
		}

		newGatewayID := uuid.New()
		replaceGateway(t, ctx, assets, fixture, newGatewayID, "replace-after-rebind")

		assertNoCurrentBindingToGateway(t, ctx, database, fixture.gatewayID)
		assertHistoricalGatewayBinding(t, ctx, database, fixture.gatewayID, "gateway_replaced")
		assertBindingHistoryRetained(t, ctx, database, fixture.gatewayID, 2)
		assertNoCurrentBindingToGateway(t, ctx, database, newGatewayID)
	})
}

func newGatewayBindingRaceFixture(
	t *testing.T,
	ctx context.Context,
) (*isolatedJobDatabase, relayBindingSchemaFixture, *store.GatewayLifecycleRepository, *store.RelayBindingRepository) {
	t.Helper()
	database := newIsolatedJobDatabase(t)
	fixture := newRelayBindingSchemaFixture(t, ctx, database)
	insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, time.Now().UTC().Add(-10*time.Second))
	assets, err := store.NewGatewayLifecycleRepository(database.runtime, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := store.NewRelayBindingRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	return database, fixture, assets, bindings
}

func bindGatewayAccount(
	t *testing.T,
	ctx context.Context,
	bindings *store.RelayBindingRepository,
	fixture relayBindingSchemaFixture,
	nodeID uuid.UUID,
	accountID int64,
	requestID string,
) {
	t.Helper()
	result, err := bindings.Bind(ctx, store.BindParams{
		RelayNodeID:       nodeID,
		GatewayInstanceID: fixture.gatewayID,
		GatewayAccountID:  accountID,
		AdminID:           fixture.adminID,
		RequestID:         requestID,
	})
	if err != nil || result.Outcome != store.RelayBindingOutcomeSuccess {
		t.Fatalf("bind: outcome=%s err=%v", result.Outcome, err)
	}
}

func retireGatewayWithBinding(
	t *testing.T,
	ctx context.Context,
	assets *store.GatewayLifecycleRepository,
	bindings *store.RelayBindingRepository,
	fixture relayBindingSchemaFixture,
	nodeID uuid.UUID,
	accountID int64,
	requestID string,
) {
	t.Helper()
	if nodeID != uuid.Nil {
		bindGatewayAccount(t, ctx, bindings, fixture, nodeID, accountID, requestID+"-bind")
	}
	var revision int64
	if err := assetsCurrentRevision(ctx, assets, fixture.gatewayID, &revision); err != nil {
		t.Fatal(err)
	}
	result, err := assets.Retire(ctx, store.GatewayCommand{
		CommandID:        uuid.New(),
		ActorAdminID:     fixture.adminID,
		RequestID:        requestID,
		InstanceID:       fixture.gatewayID,
		ExpectedRevision: revision,
		Secret:           store.SecretPatch{Operation: store.SecretAbsent},
	})
	if err != nil || result.HTTPStatus != 200 {
		t.Fatalf("retire: status=%d err=%v", result.HTTPStatus, err)
	}
}

func replaceGateway(
	t *testing.T,
	ctx context.Context,
	assets *store.GatewayLifecycleRepository,
	fixture relayBindingSchemaFixture,
	newGatewayID uuid.UUID,
	requestID string,
) {
	t.Helper()
	var revision int64
	if err := assetsCurrentRevision(ctx, assets, fixture.gatewayID, &revision); err != nil {
		t.Fatal(err)
	}
	result, err := assets.Replace(ctx, store.GatewayCommand{
		CommandID:          uuid.New(),
		ActorAdminID:       fixture.adminID,
		RequestID:          requestID,
		InstanceID:         fixture.gatewayID,
		ExpectedRevision:   revision,
		NewInstanceID:      newGatewayID,
		DisplayName:        store.StringPatch{Present: true, Value: "Replacement Gateway"},
		ManagementEndpoint: store.StringPatch{Present: true, Value: "http://replacement-gateway.test"},
		Secret:             store.SecretPatch{Operation: store.SecretAbsent},
	})
	if err != nil || result.HTTPStatus != 200 {
		t.Fatalf("replace: status=%d err=%v", result.HTTPStatus, err)
	}
}

func assetsCurrentRevision(ctx context.Context, assets *store.GatewayLifecycleRepository, gatewayID uuid.UUID, revision *int64) error {
	asset, err := assets.Detail(ctx, gatewayID)
	if err != nil {
		return err
	}
	*revision = asset.Asset.Revision
	return nil
}

func assertNoCurrentBindingToGateway(t *testing.T, ctx context.Context, database *isolatedJobDatabase, gatewayID uuid.UUID) {
	t.Helper()
	var count int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_gateway_account_bindings WHERE gateway_instance_id=$1 AND ended_at IS NULL`, gatewayID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("gateway %s has %d current bindings", gatewayID, count)
	}
}

func assertHistoricalGatewayBinding(t *testing.T, ctx context.Context, database *isolatedJobDatabase, gatewayID uuid.UUID, reason string) {
	t.Helper()
	var count int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_gateway_account_bindings WHERE gateway_instance_id=$1 AND ended_at IS NOT NULL AND end_reason=$2`, gatewayID, reason).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("gateway %s has no historical binding with reason %s", gatewayID, reason)
	}
}

func assertBindingHistoryRetained(t *testing.T, ctx context.Context, database *isolatedJobDatabase, gatewayID uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_gateway_account_bindings WHERE gateway_instance_id=$1`, gatewayID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("gateway %s binding history=%d, want %d", gatewayID, count, want)
	}
}
