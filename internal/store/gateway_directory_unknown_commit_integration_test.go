package store_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	store "github.com/sunxu/relay-station-control/internal/store"
)

// loseDirectoryCommitReply drops the server's COMMIT acknowledgement after it
// has actually arrived. The client cannot know whether the transaction committed.
// This wraps only an isolated fixture's plaintext PostgreSQL connection.
type loseDirectoryCommitReply struct {
	net.Conn
	tail    []byte
	dropped *atomic.Bool
}

func (conn *loseDirectoryCommitReply) Read(p []byte) (int, error) {
	n, err := conn.Conn.Read(p)
	if n > 0 {
		combined := append(conn.tail, p[:n]...)
		if bytes.Contains(combined, []byte{'C', 0, 0, 0, 11, 'C', 'O', 'M', 'M', 'I', 'T', 0}) && conn.dropped.CompareAndSwap(false, true) {
			_ = conn.Conn.Close()
			return 0, io.ErrUnexpectedEOF
		}
		start := max(0, len(combined)-11)
		conn.tail = append([]byte(nil), combined[start:]...)
	}
	return n, err
}

func TestGatewayDirectoryRuntimeUnknownCommitRecovery(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	waitDirectoryRuntimeClaimWindow(t, db)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/internal/v1/api-account-directory" || r.Header.Get("Authorization") != "Bearer synthetic-reader" {
			t.Error("fixed source request contract violated")
		}
		_, _ = w.Write(gatewayDirectoryJSON(t, time.Now().UTC(), "Synthetic"))
	}))
	defer server.Close()
	gatewayID := uuid.New()
	ref := "file://unknown-commit/reader"
	insertGatewayInstance(t, ctx, db.owner, gatewayID, server.URL, ref)
	resolver := writeGatewayDirectorySecretResolver(t, ref, "synthetic-reader")
	configuration := db.runtime.Config()
	if configuration.ConnConfig.TLSConfig != nil {
		t.Fatal("lost-reply fixture requires the isolated plaintext PostgreSQL endpoint")
	}
	var dropped atomic.Bool
	dial := configuration.ConnConfig.DialFunc
	configuration.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &loseDirectoryCommitReply{Conn: conn, dropped: &dropped}, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo, err := store.NewGatewayDirectoryIngestionRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.NewGatewayDirectoryIngestionService(repo, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunGatewayOnce(ctx, gatewayID); err == nil {
		t.Fatal("lost commit acknowledgement unexpectedly returned success")
	}
	if !dropped.Load() {
		t.Fatal("COMMIT acknowledgement was not intercepted")
	}
	var runID uuid.UUID
	var status string
	if err := db.owner.QueryRow(ctx, `SELECT ingestion_run_id,status FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&runID, &status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("durable status = %s", status)
	}
	snapshotID, receivedAt, _ := loadGatewayDirectoryCurrentState(t, ctx, db.owner, gatewayID)
	restartedRepo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := store.NewGatewayDirectoryIngestionService(restartedRepo, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled, err := restarted.ReconcileTick(ctx); err != nil || len(reconciled) != 0 {
		t.Fatalf("terminal recovery = %v, %v", reconciled, err)
	}
	result, err := restarted.RunGatewayOnce(ctx, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != store.GatewayDirectoryWorkStatusNoWork || calls.Load() != 1 {
		t.Fatal("unknown commit caused a second execution")
	}
	afterSnapshot, afterReceived, _ := loadGatewayDirectoryCurrentState(t, ctx, db.owner, gatewayID)
	if afterSnapshot != snapshotID || !afterReceived.Equal(receivedAt) {
		t.Fatal("recovery changed committed observation")
	}
	var runs, snapshots int
	if err := db.owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM gateway_directory_ingestion_runs),(SELECT count(*) FROM gateway_directory_snapshots)`).Scan(&runs, &snapshots); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || snapshots != 1 {
		t.Fatalf("runs=%d snapshots=%d", runs, snapshots)
	}
}
