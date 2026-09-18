package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/gatewaydirectory"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

type stage0GatewayDirectoryTestResolver struct{}

func (stage0GatewayDirectoryTestResolver) ResolveAssetCredential(context.Context, assetcredential.CredentialKind, uuid.UUID) (*drivers.Secret, error) {
	return drivers.NewSecretFromBytes([]byte("reader-token")), nil
}

func (stage0GatewayDirectoryTestResolver) ResolveGatewayDirectoryCredential(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*drivers.Secret, error) {
	return drivers.NewSecretFromBytes([]byte("reader-token")), nil
}

func TestGatewayDirectoryCoordinatorUsesStage0FencedCredential(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	gatewayID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer reader-token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(gatewayDirectoryJSON(t, time.Now().UTC(), "Stage0"))
	}))
	defer server.Close()
	insertGatewayInstance(t, ctx, database.owner, gatewayID, server.URL, "file://stage0/unused")
	if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET directory_credential_sealed=$1 WHERE instance_id=$2`, make([]byte, 29), gatewayID); err != nil {
		t.Fatal(err)
	}
	requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
	repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}
	service, err := jobstore.NewGatewayDirectoryIngestionServiceWithGatewayDirectoryCredentialResolver(repository, stage0GatewayDirectoryTestResolver{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunGatewayOnce(ctx, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != jobstore.GatewayDirectoryWorkStatusSucceeded {
		t.Fatalf("result=%#v", result)
	}
}

func gatewayDirectoryJSON(t *testing.T, generatedAt time.Time, name string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"generated_at":   generatedAt.UTC().Format(time.RFC3339Nano),
		"accounts": []map[string]any{{
			"id":       1,
			"name":     name,
			"platform": "linux",
			"type":     "apikey",
			"url":      nil,
			"status":   "active",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestGatewayDirectoryCoordinatorNoWorkAndShapeValidation(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	gatewayID := uuid.New()
	secretRef := "file://gateway-directory/reader"
	insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-directory.test", secretRef)

	runID := uuid.New()
	currentSlot := requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
	insertGatewayDirectoryRunningRun(t, ctx, database.owner, runID, gatewayID, uuid.New(),
		currentSlot, currentSlot, 1, "")

	repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}
	service, err := jobstore.NewGatewayDirectoryIngestionService(
		repository,
		writeGatewayDirectorySecretResolver(t, secretRef, "reader-token"),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.RunGatewayOnce(ctx, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != jobstore.GatewayDirectoryWorkStatusNoWork {
		t.Fatalf("result = %#v", result)
	}
}

func TestGatewayDirectoryCoordinatorSuccessChangedAndUnchanged(t *testing.T) {
	t.Run("changed", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		ctx := context.Background()
		repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
		if err != nil {
			t.Fatal(err)
		}
		gatewayID := uuid.New()
		secretRef := "file://gateway-directory/reader"
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-directory.test", secretRef)
		requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
		fixedAt := time.Now().UTC().Add(-time.Minute)
		body := gatewayDirectoryJSON(t, fixedAt, "Alpha")
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(body)
		}))
		defer server.Close()
		if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET management_endpoint=$1 WHERE instance_id=$2`, server.URL, gatewayID); err != nil {
			t.Fatal(err)
		}
		service, err := jobstore.NewGatewayDirectoryIngestionService(repository, writeGatewayDirectorySecretResolver(t, secretRef, "reader-token"))
		if err != nil {
			t.Fatal(err)
		}
		first, err := service.RunGatewayOnce(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if first.Status != jobstore.GatewayDirectoryWorkStatusSucceeded || first.Outcome != string(jobstore.GatewayDirectoryFinalizeOutcomeChanged) {
			t.Fatalf("first run = %#v", first)
		}
		result, err := service.RunGatewayOnce(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != jobstore.GatewayDirectoryWorkStatusNoWork {
			t.Fatalf("second run should be no_work after terminal current slot = %#v", result)
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		ctx := context.Background()
		repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
		if err != nil {
			t.Fatal(err)
		}
		gatewayID := uuid.New()
		secretRef := "file://gateway-directory/reader"
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-directory.test", secretRef)
		currentSlot := requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
		fixedAt := time.Now().UTC().Add(-time.Minute)
		previous := gatewaydirectory.DirectoryResponse{
			SchemaVersion: 1,
			GeneratedAt:   fixedAt,
			Accounts: []gatewaydirectory.Account{{
				ID: 1, Name: "Alpha", Platform: "linux", Type: "apikey", Status: "active",
			}},
		}
		seedRunID := uuid.New()
		seedLeaseToken := uuid.New()
		seedScheduledAt := currentSlot.Add(-180 * time.Second)
		seedStartedAt := time.Now().UTC().Add(-5 * time.Second)
		if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(
			ingestion_run_id, gateway_instance_id, scheduled_at, status, attempt_count,
			created_at, first_started_at, last_started_at, lease_expires_at, lease_fencing_token
		) VALUES ($1,$2,$3,'running',1,$4,$4,$4,$5,$6)`,
			seedRunID, gatewayID, seedScheduledAt.UTC(), seedStartedAt, seedStartedAt.Add(15*time.Second), seedLeaseToken); err != nil {
			t.Fatal(err)
		}
		failure, err := repository.FinalizeSuccessfulAttempt(ctx, jobstore.GatewayDirectoryAttemptSuccess{
			Request: jobstore.GatewayDirectoryAttemptRequest{
				IngestionRunID:    seedRunID,
				GatewayInstanceID: gatewayID,
				LeaseFencingToken: seedLeaseToken,
			},
			Directory:    previous,
			GeneratedAt:  previous.GeneratedAt,
			Fingerprint:  previous.FingerprintV1(),
			AccountCount: len(previous.Accounts),
		})
		if err != nil || failure.Success == nil {
			t.Fatalf("seed finalize = %#v, %v", failure, err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(gatewayDirectoryJSON(t, fixedAt, "Alpha"))
		}))
		defer server.Close()
		if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET management_endpoint=$1 WHERE instance_id=$2`, server.URL, gatewayID); err != nil {
			t.Fatal(err)
		}
		service, err := jobstore.NewGatewayDirectoryIngestionService(repository, writeGatewayDirectorySecretResolver(t, secretRef, "reader-token"))
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.RunGatewayOnce(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != jobstore.GatewayDirectoryWorkStatusSucceeded || result.Outcome != string(jobstore.GatewayDirectoryFinalizeOutcomeUnchanged) {
			t.Fatalf("result = %#v", result)
		}
	})
}

func TestGatewayDirectoryCoordinatorSourceTimeInvalid(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}
	gatewayID := uuid.New()
	secretRef := "file://gateway-directory/reader"
	insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-directory.test", secretRef)
	requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(gatewayDirectoryJSON(t, time.Now().UTC().Add(-25*time.Hour), "Alpha"))
	}))
	defer server.Close()
	if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET management_endpoint=$1 WHERE instance_id=$2`, server.URL, gatewayID); err != nil {
		t.Fatal(err)
	}
	service, err := jobstore.NewGatewayDirectoryIngestionService(repository, writeGatewayDirectorySecretResolver(t, secretRef, "reader-token"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunGatewayOnce(ctx, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != jobstore.GatewayDirectoryWorkStatusFailed || result.FailureClass != "source_time_invalid" {
		t.Fatalf("result = %#v", result)
	}
}
