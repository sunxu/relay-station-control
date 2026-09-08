package store_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	"github.com/sunxu/relay-station-control/internal/store"
)

type qualityHTTPTargets struct{ target drivers.NodeTarget }
type recordingQualityStore struct {
	inner   requestquality.Store
	inserts atomic.Int32
}

func (s *recordingQualityStore) InsertRequestEvents(ctx context.Context, events []requestquality.Event) error {
	err := s.inner.InsertRequestEvents(ctx, events)
	if err == nil {
		s.inserts.Add(1)
	}
	return err
}
func (s *recordingQualityStore) DeleteOldRequestEvents(ctx context.Context) (int64, error) {
	return s.inner.DeleteOldRequestEvents(ctx)
}

func (t qualityHTTPTargets) ListRequestQualityTargets(context.Context) ([]drivers.NodeTarget, error) {
	return []drivers.NodeTarget{t.target}, nil
}

func TestAccountRequestQualityHTTPCollectorPostgresClosedLoop(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	repository, err := store.NewAccountRequestQualityRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			if r.Header.Get("X-Management-Key") != "integration-key" {
				t.Errorf("management key=%q", r.Header.Get("X-Management-Key"))
			}
			_, _ = io.WriteString(w, `{"files":[{"auth_index":"known","provider":"openai","email":"known@example.invalid"}]}`)
		case "/v0/management/usage-queue":
			if r.Header.Get("Authorization") != "Bearer integration-key" {
				t.Errorf("authorization=%q", r.Header.Get("Authorization"))
			}
			_, _ = fmt.Fprintf(w, `[{"event_hash":"resolved-hash","request_id":"r1","timestamp":%q,"provider":"openai","email":"known@example.invalid","model":"gpt","latency_ms":100,"failed":false},{"event_hash":"auth-resolved-hash","request_id":"r-auth","timestamp":%q,"provider":"openai","auth_index":"known","model":"gpt","latency_ms":150,"failed":false},{"event_hash":"unresolved-hash","request_id":"r2","timestamp":%q,"provider":"openai","auth_index":"missing","model":"gpt","latency_ms":300,"failed":true,"fail_status_code":500,"fail_summary":"upstream unavailable"}]`, stamp, stamp, stamp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	resolver := writeGatewayDirectorySecretResolver(t, "file://integration-reader", "integration-key")
	driver, err := cliproxyapi.NewDriver(cliproxyapi.DriverConfig{Management: drivers.ManagementConfig{}, SecretResolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	target := drivers.NodeTarget{InstanceID: nodeID, NodeType: drivers.NodeTypeCLIProxyAPI, DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1, ManagementEndpoint: server.URL, ReaderSecretReference: drivers.NewSecretReference("file://integration-reader"), Capabilities: []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead}}
	recording := &recordingQualityStore{inner: repository}
	collector, err := requestquality.NewCollector(recording, driver, qualityHTTPTargets{target}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	waitForQualityRows(t, database.owner, nodeID, 3)
	firstInserts := recording.inserts.Load()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not stop")
	}

	account := "openai:known@example.invalid"
	quality, err := repository.AccountQuality(context.Background(), nodeID, account, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if quality.RequestCount != 2 || quality.SuccessCount != 2 || quality.FailureCount != 0 {
		t.Fatalf("account quality=%+v", quality)
	}
	nodeQuality, err := repository.NodeProviderQuality(context.Background(), nodeID, "openai", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if nodeQuality.RequestCount != 3 || nodeQuality.SuccessCount != 2 || nodeQuality.FailureCount != 1 || nodeQuality.UnresolvedRequestCount != 1 {
		t.Fatalf("node quality=%+v", nodeQuality)
	}

	// A restart may receive the same destructive batch again; the event hash
	// uniqueness contract keeps the persisted count unchanged.
	collector, err = requestquality.NewCollector(recording, driver, qualityHTTPTargets{target}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	done = make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for recording.inserts.Load() <= firstInserts && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if recording.inserts.Load() <= firstInserts {
		t.Fatal("restarted collector did not pull and insert the repeated batch")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("restarted collector did not stop")
	}
	var count int
	if err := database.owner.QueryRow(context.Background(), `SELECT count(*) FROM account_request_quality_events WHERE node_id=$1`, nodeID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("idempotent row count=%d, want 3", count)
	}
}

func waitForQualityRows(t *testing.T, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, nodeID uuid.UUID, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := query.QueryRow(context.Background(), `SELECT count(*) FROM account_request_quality_events WHERE node_id=$1`, nodeID).Scan(&count); err == nil && count >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("timed out waiting for %d quality rows", want))
}
