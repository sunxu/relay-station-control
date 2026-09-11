package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

type deploymentTestBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *deploymentTestBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *deploymentTestBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func reserveLocalPort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), nil
}

func TestGatewayDirectoryMainDeploymentHelper(t *testing.T) {
	if os.Getenv("CONTROL_MAIN_DEPLOYMENT_HELPER") == "1" {
		main()
	}
}

func TestGatewayDirectoryMainDeploymentHTTP(t *testing.T) {
	owner, runtimePool := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	ctx := context.Background()
	environmentID := "main-deployment-" + uuid.NewString()
	if _, err := owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES($1,'Main deployment test','dev')`, environmentID); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/internal/v1/api-account-directory" || r.Header.Get("Authorization") != "Bearer main-deployment-reader" {
			t.Errorf("source request contract violated: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1,
			"generated_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"accounts":       []map[string]any{{"id": int64(9007199254740993), "name": "main", "platform": "openai", "type": "apikey", "url": nil, "status": "active"}},
		})
	}))
	defer source.Close()
	gatewayID := uuid.New()
	if _, err := owner.Exec(ctx, `SELECT control_register_gateway($1,'Main Deployment Gateway',$2,'file://main-deployment/reader')`, gatewayID, source.URL); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "reader-token")
	mappingPath := filepath.Join(directory, "mapping.json")
	if err := os.WriteFile(tokenPath, []byte("main-deployment-reader"), 0600); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"provider": "file", "references": []map[string]string{{"reference": "file://main-deployment/reader", "path": tokenPath}}})
	if err := os.WriteFile(mappingPath, document, 0600); err != nil {
		t.Fatal(err)
	}
	keyringPath := filepath.Join(directory, "keyring.json")
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	keyring := `{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"` + key + `"}]}`
	if err := os.WriteFile(keyringPath, []byte(keyring), 0600); err != nil {
		t.Fatal(err)
	}
	bootstrapPath := filepath.Join(directory, "bootstrap.secret")
	if err := os.WriteFile(bootstrapPath, []byte(strings.Repeat("b", 48)), 0600); err != nil {
		t.Fatal(err)
	}
	start := func(enabled string) (*exec.Cmd, chan error, string, *deploymentTestBuffer) {
		port, err := reserveLocalPort()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(os.Args[0], "-test.run=^TestGatewayDirectoryMainDeploymentHelper$")
		command.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"CONTROL_MAIN_DEPLOYMENT_HELPER=1",
			"DATABASE_URL=" + runtimePool.Config().ConnString(),
			"CONTROL_ENVIRONMENT_ID=" + environmentID,
			"CONTROL_ENVIRONMENT=dev",
			"CONTROL_AUTH_KEYRING_FILE=" + keyringPath,
			"CONTROL_BOOTSTRAP_SECRET_FILE=" + bootstrapPath,
			"CONTROL_HTTP_ADDR=127.0.0.1:" + port,
			"CONTROL_GATEWAY_DIRECTORY_ENABLED=" + enabled,
			"CONTROL_GATEWAY_DIRECTORY_SECRET_MAPPING_FILE=" + mappingPath,
		}
		output := &deploymentTestBuffer{}
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		t.Cleanup(func() { _ = command.Process.Kill() })
		return command, done, "http://127.0.0.1:" + port, output
	}
	client := &http.Client{Timeout: 3 * time.Second}
	disabled, disabledDone, disabledAddress, disabledOutput := start("false")
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(disabledAddress + "/api/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("disabled main health status=%d", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("disabled main did not become healthy: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := disabled.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-disabledDone:
		if err != nil {
			t.Fatalf("disabled main shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("disabled main shutdown exceeded bound")
	}
	var count int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || requests.Load() != 0 {
		t.Fatalf("disabled main had side effects: runs=%d requests=%d", count, requests.Load())
	}
	if strings.Contains(disabledOutput.String(), "main-deployment-reader") {
		t.Fatal("disabled main log leaked synthetic reader token")
	}
	// Use the real DB claim window and leave time for the same-slot restart.
	// Never rewrite scheduled_at or bypass the production scheduler.
	for {
		var remaining float64
		if err := owner.QueryRow(ctx, `SELECT 120-extract(epoch FROM clock_timestamp()-to_timestamp(floor(extract(epoch FROM clock_timestamp())/180)*180))`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining > 60 {
			break
		}
		time.Sleep(time.Second)
	}
	process, done, address, output := start("true")
	deadline = time.Now().Add(15 * time.Second)
	for {
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gatewayID).Scan(&count); err == nil && count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("main did not persist HTTP Directory snapshot; requests=%d logs=%s", requests.Load(), output.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp, err := client.Get(address + "/api/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("main health status=%d", resp.StatusCode)
	}
	metricsResponse, err := client.Get(address + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, readErr := io.ReadAll(io.LimitReader(metricsResponse.Body, 1<<20))
	_ = metricsResponse.Body.Close()
	if readErr != nil || metricsResponse.StatusCode != http.StatusOK || !strings.Contains(string(metrics), "gateway_directory") {
		t.Fatal("main did not expose Directory metrics")
	}
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("main shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("main shutdown exceeded bound")
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gatewayID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("current state rows=%d", count)
	}
	var accountID int64
	if err := owner.QueryRow(ctx, `SELECT item.account_id FROM gateway_directory_snapshot_items AS item JOIN gateway_directory_current_state AS current ON current.current_snapshot_id = item.snapshot_id WHERE current.gateway_instance_id=$1`, gatewayID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if accountID != 9007199254740993 {
		t.Fatalf("account precision lost: %d", accountID)
	}
	var snapshotID uuid.UUID
	var receivedAt time.Time
	if err := owner.QueryRow(ctx, `SELECT current_snapshot_id,last_success_received_at FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gatewayID).Scan(&snapshotID, &receivedAt); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"main-deployment-reader", "file://main-deployment/reader", mappingPath, source.URL, "9007199254740993"} {
		if strings.Contains(output.String()+disabledOutput.String()+string(metrics), forbidden) {
			t.Fatal("main logs or metrics leaked a synthetic canary")
		}
	}
	restarted, restartedDone, restartedAddress, restartedOutput := start("true")
	deadline = time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get(restartedAddress + "/api/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("restarted main health status=%d", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restarted main did not become healthy: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Keep the real main alive beyond a full runtime tick before checking
	// same-slot idempotency; mere listener readiness is not sufficient.
	time.Sleep(gatewayDirectoryTickInterval + time.Second)
	if err := restarted.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-restartedDone:
		if err != nil {
			t.Fatalf("restarted main shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("restarted main shutdown exceeded bound")
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("restart created duplicate run count=%d", count)
	}
	if requests.Load() != 1 {
		t.Fatalf("same-slot restart source requests=%d", requests.Load())
	}
	var restartedSnapshotID uuid.UUID
	var restartedReceivedAt time.Time
	if err := owner.QueryRow(ctx, `SELECT current.current_snapshot_id,current.last_success_received_at,item.account_id FROM gateway_directory_current_state AS current JOIN gateway_directory_snapshot_items AS item ON item.snapshot_id=current.current_snapshot_id WHERE current.gateway_instance_id=$1`, gatewayID).Scan(&restartedSnapshotID, &restartedReceivedAt, &accountID); err != nil {
		t.Fatal(err)
	}
	if restartedSnapshotID != snapshotID || !restartedReceivedAt.Equal(receivedAt) || accountID != 9007199254740993 {
		t.Fatal("restart changed the persisted current snapshot, successful observation or exact account identity")
	}
	for _, forbidden := range []string{"main-deployment-reader", "file://main-deployment/reader", mappingPath, source.URL, "9007199254740993"} {
		if strings.Contains(restartedOutput.String(), forbidden) {
			t.Fatal("restarted main log leaked a synthetic canary")
		}
	}
}
