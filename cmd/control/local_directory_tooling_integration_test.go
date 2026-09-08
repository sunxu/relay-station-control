package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/auth"
)

// This opt-in test exercises the new CLI against real main/PG. Only its
// external Directory fixture changes availability; product timestamps and
// Binding state are never rewritten to manufacture freshness transitions.
func TestLocalDirectoryToolingNaturalRecovery(t *testing.T) {
	if os.Getenv("RELAY_DIRECTORY_TOOLING_E2E") != "1" {
		t.Skip("set RELAY_DIRECTORY_TOOLING_E2E=1")
	}
	owner, runtimePool := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	ctx := context.Background()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(home, ".relay-directory-tooling-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	write := func(name string, body []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	environment := "tooling-" + uuid.NewString()
	gateway, node, admin := uuid.New(), uuid.New(), uuid.New()
	password := "Fixture-only-password-" + uuid.NewString()
	token := "fixture-reader-" + uuid.NewString()
	var enabled atomic.Bool
	enabled.Store(true)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/internal/v1/api-account-directory" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "generated_at": time.Now().UTC().Format(time.RFC3339Nano), "accounts": []map[string]any{{"id": int64(9007199254740993), "name": "fixture", "platform": "openai", "type": "apikey", "url": nil, "status": "active"}}})
	}))
	defer source.Close()
	defer enabled.Store(true)
	execSQL := func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(`INSERT INTO environments(environment_id,name,environment_type) VALUES($1,'Tooling test','dev')`, environment)
	execSQL(`SELECT control_register_gateway($1,'Tooling Gateway',$2,'file://tooling/reader')`, gateway, source.URL)
	execSQL(`INSERT INTO node_drivers(node_type,driver_contract_version,display_name,lifecycle_status) VALUES('cliproxy.api','v1','Fixture','active')`)
	execSQL(`INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref) VALUES($1,'Tooling Node','cliproxy.api','v1','http://127.0.0.1:1','file://tooling/node')`, node)
	// Credentials are synthetic fixture data. No session is seeded: the harness
	// must pass the ordinary HTTP password login and CSRF checks itself.
	phc, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(`INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at) VALUES($1,'tooling_admin','Tooling Admin','super_admin','enabled',CURRENT_TIMESTAMP)`, admin)
	execSQL(`INSERT INTO control_admin_passwords(admin_id,password_phc,parameter_version) VALUES($1,$2,1)`, admin, phc)
	tokenPath := write("reader-token", []byte(token))
	mapping, _ := json.Marshal(map[string]any{"provider": "file", "references": []map[string]string{{"reference": "file://tooling/reader", "path": tokenPath}}})
	mappingPath := write("mapping.json", mapping)
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	keyPath := write("keyring.json", []byte(`{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"`+key+`"}]}`))
	bootstrap := write("bootstrap", []byte(strings.Repeat("b", 48)))
	port, err := reserveLocalPort()
	if err != nil {
		t.Fatal(err)
	}
	address := "http://127.0.0.1:" + port
	process := exec.Command(os.Args[0], "-test.run=^TestGatewayDirectoryMainDeploymentHelper$")
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "CONTROL_MAIN_DEPLOYMENT_HELPER=1", "DATABASE_URL=" + runtimePool.Config().ConnString(), "CONTROL_ENVIRONMENT_ID=" + environment, "CONTROL_ENVIRONMENT=dev", "CONTROL_AUTH_KEYRING_FILE=" + keyPath, "CONTROL_BOOTSTRAP_SECRET_FILE=" + bootstrap, "CONTROL_HTTP_ADDR=127.0.0.1:" + port, "CONTROL_COOKIE_SECURE=false", "CONTROL_MFA_REQUIRED=false", "CONTROL_GATEWAY_DIRECTORY_ENABLED=true", "CONTROL_GATEWAY_DIRECTORY_SECRET_MAPPING_FILE=" + mappingPath}
	logs := &deploymentTestBuffer{}
	process.Stdout = logs
	process.Stderr = logs
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	t.Cleanup(func() {
		_ = process.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
		}
	})
	deadline := time.Now().Add(200 * time.Second)
	for {
		var count int
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gateway).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Directory did not become fresh within normal slot")
		}
		time.Sleep(time.Second)
	}
	config := map[string]any{"control_url": address, "login_name": "tooling_admin", "password_file": write("password", []byte(password)), "gateway_instance_id": gateway.String(), "node_instance_id": node.String(), "gateway_account_id": "9007199254740993", "baseline_file": filepath.Join(directory, "baseline.json"), "wait_timeout_seconds": 780, "poll_interval_seconds": 5}
	encoded, _ := json.Marshal(config)
	configPath := write("config.json", encoded)
	harness := filepath.Join(crossNodeDuplicateOwnershipRepositoryRoot(t), "deploy", "acceptance", "directory_http.py")
	run := func(mode string) {
		t.Helper()
		t.Log("harness phase=" + mode)
		command := exec.Command("python3", harness, "--config", configPath, mode)
		output := &deploymentTestBuffer{}
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- command.Wait() }()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case err := <-result:
				for _, canary := range []string{password, token, "9007199254740993", gateway.String(), node.String()} {
					if strings.Contains(output.String(), canary) {
						t.Fatal("harness output leaked fixture canary")
					}
				}
				if err != nil {
					t.Fatalf("harness %s failed (private output withheld): %v", mode, err)
				}
				if !strings.Contains(output.String(), `"result":"success"`) {
					t.Fatal("harness did not report explicit success")
				}
				return
			case <-ticker.C:
				t.Log("harness waiting phase=" + mode)
			}
		}
	}
	run("bind")
	run("baseline")
	var before time.Time
	var binding uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT last_success_received_at FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gateway).Scan(&before); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]any
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	binding, err = uuid.Parse(baseline["binding_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	enabled.Store(false)
	run("wait-stale")
	var after time.Time
	if err := owner.QueryRow(ctx, `SELECT last_success_received_at FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gateway).Scan(&after); err != nil || !before.Equal(after) {
		t.Fatal("failed ingestion advanced observation")
	}
	enabled.Store(true)
	run("wait-recovered")
	raw, err = os.ReadFile(filepath.Join(directory, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), binding.String()) {
		t.Fatal("baseline binding changed")
	}
	if err := owner.QueryRow(ctx, `SELECT last_success_received_at FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gateway).Scan(&after); err != nil || !after.After(before) {
		t.Fatal("normal recovery did not advance observation")
	}
	for _, canary := range []string{password, token, "9007199254740993"} {
		if strings.Contains(logs.String(), canary) {
			t.Fatal("main output leaked fixture canary")
		}
	}
	t.Log("PASS real main/PG, normal HTTP login and bind, exact identity, natural 540s stale and normal-slot recovery")
}
