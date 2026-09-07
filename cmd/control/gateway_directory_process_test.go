package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

func TestGatewayDirectoryRuntimeProcessHelper(t *testing.T) {
	if os.Getenv("CONTROL_DIRECTORY_TEST_HELPER") != "1" {
		return
	}
	cfg, err := loadGatewayDirectoryRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("CONTROL_DIRECTORY_TEST_DATABASE"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	runtime, err := newGatewayDirectoryRuntime(prometheus.NewRegistry(), pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	if marker := os.Getenv("CONTROL_DIRECTORY_TEST_TICK_MARKER"); marker != "" {
		runtime.tick(ctx, nil)
		if err := os.WriteFile(marker, []byte("tick\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runtime.run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGatewayDirectoryRuntimeProcessRecovery(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%t", secure), func(t *testing.T) {
			owner, runtimePool := isolatedCrossNodeDuplicateOwnershipDatabase(t)
			ctx := context.Background()
			// Reserve enough real DB slot time for lease recovery without skipping acceptance.
			for {
				var remaining float64
				if err := owner.QueryRow(ctx, `SELECT 120-extract(epoch FROM clock_timestamp()-to_timestamp(floor(extract(epoch FROM clock_timestamp())/180)*180))`).Scan(&remaining); err != nil {
					t.Fatal(err)
				}
				if remaining > 45 {
					break
				}
				time.Sleep(time.Second)
			}
			var hold atomic.Bool
			hold.Store(true)
			var requests atomic.Int32
			source := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/internal/v1/api-account-directory" || r.Header.Get("Authorization") != "Bearer process-test" {
					t.Error("fixed request or auth violated")
				}
				if hold.Load() {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "generated_at": time.Now().UTC().Format(time.RFC3339Nano), "accounts": []map[string]any{{"id": int64(9007199254740993), "name": "process", "platform": "openai", "type": "apikey", "url": nil, "status": "active"}}})
			})
			var server *httptest.Server
			if secure {
				server = httptest.NewTLSServer(source)
			} else {
				server = httptest.NewServer(source)
			}
			defer server.Close()
			id := uuid.New()
			if _, err := owner.Exec(ctx, `SELECT control_register_gateway($1,'Process Gateway',$2,'file://process/reader')`, id, server.URL); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			token := filepath.Join(dir, "token")
			mapping := filepath.Join(dir, "mapping.json")
			if err := os.WriteFile(token, []byte("process-test"), 0600); err != nil {
				t.Fatal(err)
			}
			document, _ := json.Marshal(map[string]any{"provider": "file", "references": []map[string]string{{"reference": "file://process/reader", "path": token}}})
			if err := os.WriteFile(mapping, document, 0600); err != nil {
				t.Fatal(err)
			}
			start := func(enabled string) (*exec.Cmd, chan error) {
				command := exec.Command(os.Args[0], "-test.run=^TestGatewayDirectoryRuntimeProcessHelper$")
				command.Env = append(os.Environ(), "CONTROL_DIRECTORY_TEST_HELPER=1", "CONTROL_DIRECTORY_TEST_DATABASE="+runtimePool.Config().ConnString(), "CONTROL_GATEWAY_DIRECTORY_ENABLED="+enabled, "CONTROL_GATEWAY_DIRECTORY_SECRET_MAPPING_FILE="+mapping)
				var output bytes.Buffer
				command.Stdout = &output
				command.Stderr = &output
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- command.Wait() }()
				t.Cleanup(func() { _ = command.Process.Kill() })
				return command, done
			}
			_, disabled := start("false")
			select {
			case err := <-disabled:
				if err != nil {
					t.Fatal("disabled process failed")
				}
			case <-time.After(8 * time.Second):
				t.Fatal("disabled process did not exit")
			}
			var count int
			if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 || requests.Load() != 0 {
				t.Fatal("disabled process had ingestion side effects")
			}
			first, firstDone := start("true")
			deadline := time.Now().Add(12 * time.Second)
			for requests.Load() == 0 {
				if time.Now().After(deadline) {
					t.Fatal("child did not fetch")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := first.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-firstDone:
				if err != nil {
					t.Fatal("first process shutdown failed")
				}
			case <-time.After(8 * time.Second):
				t.Fatal("shutdown not bounded")
			}
			hold.Store(false)
			second, secondDone := start("true")
			deadline = time.Now().Add(30 * time.Second)
			for {
				if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("restart did not recover persisted run")
				}
				time.Sleep(100 * time.Millisecond)
			}
			var accountID int64
			if err := owner.QueryRow(ctx, `SELECT account_id FROM gateway_directory_snapshot_items`).Scan(&accountID); err != nil {
				t.Fatal(err)
			}
			if accountID != 9007199254740993 {
				t.Fatal("source precision lost")
			}
			if err := second.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-secondDone:
				if err != nil {
					t.Fatal("second process shutdown failed")
				}
			case <-time.After(8 * time.Second):
				t.Fatal("shutdown not bounded")
			}
		})
	}
}

func TestGatewayDirectoryRuntimeProcessSameSlotCompetition(t *testing.T) {
	owner, runtimePool := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	ctx := context.Background()
	for {
		var remaining float64
		if err := owner.QueryRow(ctx, `SELECT 120-extract(epoch FROM clock_timestamp()-to_timestamp(floor(extract(epoch FROM clock_timestamp())/180)*180))`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining > 45 {
			break
		}
		time.Sleep(time.Second)
	}
	var requests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1,
			"generated_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"accounts":       []map[string]any{{"id": int64(9007199254740993), "name": "competition", "platform": "openai", "type": "apikey", "url": nil, "status": "active"}},
		})
	}))
	defer source.Close()
	id := uuid.New()
	if _, err := owner.Exec(ctx, `SELECT control_register_gateway($1,'Competition Gateway',$2,'file://competition/reader')`, id, source.URL); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	token := filepath.Join(directory, "token")
	mapping := filepath.Join(directory, "mapping.json")
	if err := os.WriteFile(token, []byte("competition-reader"), 0600); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"provider": "file", "references": []map[string]string{{"reference": "file://competition/reader", "path": token}}})
	if err := os.WriteFile(mapping, document, 0600); err != nil {
		t.Fatal(err)
	}
	start := func(marker string) (*exec.Cmd, chan error) {
		command := exec.Command(os.Args[0], "-test.run=^TestGatewayDirectoryRuntimeProcessHelper$")
		command.Env = append(os.Environ(), "CONTROL_DIRECTORY_TEST_HELPER=1", "CONTROL_DIRECTORY_TEST_DATABASE="+runtimePool.Config().ConnString(), "CONTROL_GATEWAY_DIRECTORY_ENABLED=true", "CONTROL_GATEWAY_DIRECTORY_SECRET_MAPPING_FILE="+mapping, "CONTROL_DIRECTORY_TEST_TICK_MARKER="+marker)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		t.Cleanup(func() { _ = command.Process.Kill() })
		return command, done
	}
	firstMarker := filepath.Join(directory, "first.tick")
	secondMarker := filepath.Join(directory, "second.tick")
	first, firstDone := start(firstMarker)
	second, secondDone := start(secondMarker)
	deadline := time.Now().Add(25 * time.Second)
	for {
		_, firstErr := os.Stat(firstMarker)
		_, secondErr := os.Stat(secondMarker)
		if firstErr == nil && secondErr == nil {
			break
		}
		if firstErr != nil && !os.IsNotExist(firstErr) {
			t.Fatal(firstErr)
		}
		if secondErr != nil && !os.IsNotExist(secondErr) {
			t.Fatal(secondErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("both runtime processes did not complete their marked tick")
		}
		time.Sleep(100 * time.Millisecond)
	}
	for {
		var current int
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, id).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if current == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("competing processes did not finalize one current state; requests=%d", requests.Load())
		}
		time.Sleep(100 * time.Millisecond)
	}
	var runs int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, id).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || requests.Load() != 1 {
		t.Fatalf("same-slot competition created duplicate work: runs=%d requests=%d", runs, requests.Load())
	}
	var accountID int64
	if err := owner.QueryRow(ctx, `SELECT account_id FROM gateway_directory_snapshot_items WHERE snapshot_id = (SELECT snapshot_id FROM gateway_directory_current_state WHERE gateway_instance_id=$1)`, id).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if accountID != 9007199254740993 {
		t.Fatalf("same-slot source precision lost: %d", accountID)
	}
	for _, process := range []*exec.Cmd{first, second} {
		if err := process.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
	}
	for _, done := range []chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("competing process shutdown failed: %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("competing process shutdown was not bounded")
		}
	}
}
