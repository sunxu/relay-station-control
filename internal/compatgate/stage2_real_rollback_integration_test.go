package compatgate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

const stage2ArchiveCommit = "5199b611a99ac36b46a5a0309db1c01d3fe50929"

func TestStage2RealArtifactRollbackAgainstStage3ForwardSchemaPG18(t *testing.T) {
	if testing.Short() {
		t.Skip("requires PostgreSQL 18 and Docker")
	}
	ownerBase := os.Getenv("CONTROL_DATABASE_TEST_URL")
	runtimeBase := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if ownerBase == "" || runtimeBase == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for descriptor-based wrapper execution")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	ownerURL, runtimeURL, cleanupDB := createStage2RollbackDatabase(t, ctx, root, ownerBase, runtimeBase)
	defer cleanupDB()

	temporary, err := os.MkdirTemp("/Volumes/DevRAM/tmp", "stage2-real-rollback-")
	if err != nil {
		temporary = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(temporary) })
	}
	keyringPath, bootstrapPath, keyring := writeStage2RollbackAuth(t, temporary)
	intentKeyPath := filepath.Join(temporary, "asset-intent-key")
	if err = os.WriteFile(intentKeyPath, []byte("01234567890123456789012345678901"), 0o400); err != nil {
		t.Fatal(err)
	}
	adminID, token, csrf := seedStage2RollbackTruth(t, ctx, ownerURL, runtimeURL, keyring)

	oldArtifact, gate, digest := buildStage2RollbackArtifacts(t, ctx, root, temporary)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(temporary, "release-authority.ed25519.pub")
	if err = os.WriteFile(publicPath, publicKey, 0o400); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(temporary, "stage2-manifest.v1.json")
	if err = os.WriteFile(manifestPath, signedManifest(t, privateKey, digest, 2), 0o400); err != nil {
		t.Fatal(err)
	}
	wrapperBytes, err := os.ReadFile(filepath.Join(root, "deploy", "compatibility", "relay-control-compat-wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	wrapperPath := filepath.Join(temporary, "relay-control-compat-wrapper.sh")
	if err = os.WriteFile(wrapperPath, wrapperBytes, 0o700); err != nil {
		t.Fatal(err)
	}

	containerName := "stage2-real-rollback-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	postgresContainer := os.Getenv("CONTROL_POSTGRES_CONTAINER")
	if postgresContainer == "" {
		postgresContainer = "relay-station-dev-control-postgres-1"
	}
	networkOutput, err := exec.CommandContext(ctx, "docker", "inspect", "-f", `{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}`, postgresContainer).CombinedOutput()
	if err != nil || strings.TrimSpace(string(networkOutput)) == "" {
		t.Fatalf("inspect PostgreSQL network: %v %s", err, networkOutput)
	}
	dockerNetwork := strings.Fields(string(networkOutput))[0]
	dockerOwnerURL := rewriteStage2RollbackHost(t, ownerURL, postgresContainer)
	dockerRuntimeURL := rewriteStage2RollbackHost(t, runtimeURL, postgresContainer)
	args := []string{
		"run", "--detach", "--name", containerName,
		"--network", dockerNetwork, "-p", "127.0.0.1::8080",
		"-v", temporary + ":/work:ro",
		"-e", "CONTROL_COMPAT_GATE=/work/" + filepath.Base(gate),
		"-e", "CONTROL_ARTIFACT=/work/" + filepath.Base(oldArtifact),
		"-e", "CONTROL_COMPAT_MANIFEST=/work/" + filepath.Base(manifestPath),
		"-e", "CONTROL_COMPAT_PUBLIC_KEY=/work/" + filepath.Base(publicPath),
		"-e", "CONTROL_COMPAT_DATABASE_URL_ENV=COMPAT_DATABASE_URL",
		"-e", "COMPAT_DATABASE_URL=" + dockerOwnerURL,
		"-e", "DATABASE_URL=" + dockerRuntimeURL,
		"-e", "CONTROL_ENVIRONMENT_ID=stage3-rollback",
		"-e", "CONTROL_ENVIRONMENT=dev",
		"-e", "CONTROL_HTTP_ADDR=0.0.0.0:8080",
		"-e", "CONTROL_AUTH_KEYRING_FILE=/work/" + filepath.Base(keyringPath),
		"-e", "CONTROL_BOOTSTRAP_SECRET_FILE=/work/" + filepath.Base(bootstrapPath),
		"-e", "CONTROL_ASSET_INTENT_KEY_FILE=/work/" + filepath.Base(intentKeyPath),
		"-e", "CONTROL_MFA_REQUIRED=false",
		"-e", "CONTROL_CLIPROXYAPI_DRIVER_ENABLED=false",
		"-e", "CONTROL_GATEWAY_DIRECTORY_ENABLED=false",
		"-e", "CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=false",
		"-e", "CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED=false",
		"-e", "CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED=false",
		"-e", "CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED=false",
		"-e", "CONTROL_JOB_WORKER_CONCURRENCY=1",
		"-e", "CONTROL_JOB_RECONCILER_CONCURRENCY=1",
		"-e", "CONTROL_JOB_POLL_INTERVAL=1s",
		"-e", "CONTROL_JOB_RECONCILE_INTERVAL=1s",
		"-e", "CONTROL_JOB_DATABASE_BACKOFF=1s",
		"-e", "CONTROL_JOB_SHUTDOWN_GRACE=3s",
		"alpine:3.22", "/work/" + filepath.Base(wrapperPath),
	}
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("start real Stage2 artifact through wrapper: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", containerName).Run() })
	baseURL := waitStage2RollbackControl(t, ctx, containerName)

	do := func(method, path, body string, mutation bool) string {
		t.Helper()
		request, requestErr := http.NewRequestWithContext(ctx, method, baseURL+path, strings.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		if mutation {
			request.Header.Set("Origin", baseURL)
			request.Header.Set("X-CSRF-Token", csrf)
		}
		response, requestErr := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			logs, _ := exec.Command("docker", "logs", containerName).CombinedOutput()
			t.Fatalf("Stage2 %s %s status=%d body=%s logs=%s", method, path, response.StatusCode, responseBody, logs)
		}
		return string(responseBody)
	}
	listBody := do(http.MethodGet, "/api/assets/nodes?lifecycle=all", "", false)
	if !strings.Contains(listBody, stage2RollbackNodeA.String()) || !strings.Contains(listBody, stage2RollbackNodeB.String()) {
		t.Fatalf("Stage2 read omitted seeded Nodes: %s", listBody)
	}

	registrar := connectStage2RollbackRole(t, ctx, ownerURL, "relay_control_asset_registrar")
	defer registrar.Close(ctx)
	var currentFence *uuid.UUID
	if err = registrar.QueryRow(ctx, `SELECT public.control_latest_node_disable_fence_v1($1)`, stage2RollbackNodeA).Scan(&currentFence); err != nil || currentFence == nil {
		t.Fatalf("capture post-restart F0: fence=%v err=%v", currentFence, err)
	}
	if _, err = registrar.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,clock_timestamp()+interval '1 hour','scheduled_enable','rollback-registrar',$2)`, stage2RollbackNodeA, currentFence); err != nil {
		t.Fatalf("post-rollback fresh intent: %v", err)
	}
	if _, err = registrar.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,clock_timestamp()+interval '2 hours','scheduled_enable','rollback-registrar')`, stage2RollbackNodeA); err == nil {
		t.Fatal("old five-parameter writer unexpectedly executed")
	}
	var reconcileFailures int
	if err = registrar.QueryRow(ctx, `SELECT count(*) FROM public.control_reconcile_asset_registry('stage3-rollback','dev') WHERE issue_count<>0`).Scan(&reconcileFailures); err != nil {
		t.Fatalf("Stage2 reconcile smoke: %v", err)
	}
	if reconcileFailures != 0 {
		t.Fatalf("Stage2 reconcile reported %d failures", reconcileFailures)
	}

	replaceBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","new_instance_id":"%s","display_name":"Rollback replacement","management_endpoint":"http://replacement.example","node_type":"cliproxyapi","driver_contract_version":"cliproxyapi.auth-files.v1","capabilities":["management_health_read","management_account_inventory_read"]}`,
		uuid.New(), stage2RollbackNodeC)
	if body := do(http.MethodPost, "/api/assets/nodes/"+stage2RollbackNodeB.String()+"/replace", replaceBody, true); !strings.Contains(body, stage2RollbackNodeC.String()) {
		t.Fatalf("Stage2 Replace response omitted replacement: %s", body)
	}
	retireBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1"}`, uuid.New())
	if body := do(http.MethodPost, "/api/assets/nodes/"+stage2RollbackNodeA.String()+"/retire", retireBody, true); !strings.Contains(body, `"lifecycle_status":"retired"`) {
		t.Fatalf("Stage2 Retire response is not retired: %s", body)
	}
	t.Logf("stage2_source_commit=%s artifact_digest=%s manifest_class=2 floor=2 admin=%s", stage2ArchiveCommit, digest, adminID)
}

var (
	stage2RollbackNodeA = uuid.MustParse("00000000-0000-4000-8000-000000003201")
	stage2RollbackNodeB = uuid.MustParse("00000000-0000-4000-8000-000000003202")
	stage2RollbackNodeC = uuid.MustParse("00000000-0000-4000-8000-000000003203")
)

func createStage2RollbackDatabase(t *testing.T, ctx context.Context, root, ownerBase, runtimeBase string) (string, string, func()) {
	t.Helper()
	admin, err := pgx.Connect(ctx, ownerBase)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "control_stage2_rollback_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	}
	ownerURL := stage2RollbackDatabaseName(t, ownerBase, databaseName)
	runtimeURL := stage2RollbackDatabaseName(t, runtimeBase, databaseName)
	migrate := exec.CommandContext(ctx, "go", "tool", "goose", "-dir", "../migrations", "postgres", ownerURL, "up-to", "36")
	migrate.Dir = filepath.Join(root, "tools")
	if output, migrateErr := migrate.CombinedOutput(); migrateErr != nil {
		cleanup()
		t.Fatalf("migrate rollback database: %v\n%s", migrateErr, output)
	}
	verification, err := pgx.ConnectConfig(ctx, mustStage2RollbackDatabaseConfig(t, ownerURL))
	if err != nil {
		cleanup()
		t.Fatalf("connect rollback database for migration 36 verification: %v", err)
	}
	defer verification.Close(ctx)
	var version, floor int
	if err := verification.QueryRow(ctx, `SELECT max(version_id) FILTER (WHERE is_applied),
		(SELECT phase6_evidence_floor FROM control_runtime_compatibility WHERE singleton_id=1)
		FROM goose_db_version`).Scan(&version, &floor); err != nil {
		cleanup()
		t.Fatalf("read rollback schema version/floor: %v", err)
	}
	if version != 36 || floor != 2 {
		cleanup()
		t.Fatalf("rollback schema version/floor=%d/%d, want 36/2", version, floor)
	}
	var httpConstraintValidated bool
	if err := verification.QueryRow(ctx, `SELECT convalidated FROM pg_constraint
		WHERE conrelid='public.relay_node_assets'::regclass
		  AND conname='relay_node_assets_http_only_check'`).Scan(&httpConstraintValidated); err != nil {
		cleanup()
		t.Fatalf("read Node HTTP-only constraint: %v", err)
	}
	if !httpConstraintValidated {
		cleanup()
		t.Fatal("Stage2 rollback database has an unvalidated Node HTTP-only constraint")
	}
	return ownerURL, runtimeURL, cleanup
}

func mustStage2RollbackDatabaseConfig(t *testing.T, databaseURL string) *pgx.ConnConfig {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func stage2RollbackDatabaseName(t *testing.T, raw, name string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func writeStage2RollbackAuth(t *testing.T, directory string) (string, string, *authn.Keyring) {
	t.Helper()
	keyPath := filepath.Join(directory, "auth-keyring.json")
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	contents := `{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"` + key + `"}]}`
	if err := os.WriteFile(keyPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(directory, "bootstrap.secret")
	if err := os.WriteFile(bootstrap, []byte(strings.Repeat("b", 48)), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := (authn.Config{Environment: authn.EnvironmentDev, BindAddress: "127.0.0.1:0", AuthKeyringFile: keyPath, BootstrapSecretFile: bootstrap}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	return keyPath, bootstrap, config.Keyring
}

func seedStage2RollbackTruth(t *testing.T, ctx context.Context, ownerURL, runtimeURL string, keyring *authn.Keyring) (uuid.UUID, string, string) {
	t.Helper()
	owner, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	runtimePool, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	adminID, token, csrf := uuid.New(), "rollback-session-"+uuid.NewString(), "rollback-csrf-"+uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(keyring, authn.DomainCSRFDigest, csrf)
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO environments(environment_id,name,environment_type) VALUES('stage3-rollback','Stage 3 rollback','dev')`, nil},
		{`INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at) VALUES($1,$2,'Rollback Admin','super_admin','enabled',clock_timestamp())`, []any{adminID, "rollback_" + adminID.String()[:8]}},
		{`INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',clock_timestamp()+interval '1 hour')`, []any{uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)}},
		{`INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','cliproxyapi.auth-files.v1','CLIProxyAPI')`, nil},
		{`INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','cliproxyapi.auth-files.v1','management_health_read'),('cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read')`, nil},
	}
	for _, statement := range statements {
		if _, err = owner.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{stage2RollbackNodeA, stage2RollbackNodeB} {
		if _, err = runtimePool.Exec(ctx, `SELECT public.control_create_relay_node_asset($1,$2,'cliproxyapi','cliproxyapi.auth-files.v1','http://node.example',NULL,ARRAY['management_health_read','management_account_inventory_read']::text[])`, id, "Rollback Node "+id.String()[len(id.String())-3:]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) VALUES($1,clock_timestamp()+interval '2 hours','scheduled_enable','rollback-seed',clock_timestamp())`, stage2RollbackNodeA); err != nil {
		t.Fatal(err)
	}
	repository, err := assetstore.NewNodeMonitoringRepository(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.Disable(ctx, assetstore.NodeMonitoringCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "stage2-rollback-disable", InstanceID: stage2RollbackNodeA}); err != nil {
		t.Fatal(err)
	}
	var receipts, cancelled int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts WHERE command_kind='node.monitoring_disable' AND sanitized_result->>'instance_id'=$1`, stage2RollbackNodeA.String()).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1 AND cancelled_at IS NOT NULL AND cancel_reason='administrator_disable'`, stage2RollbackNodeA).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || cancelled != 1 {
		t.Fatalf("Stage3 truth receipts=%d cancelled=%d", receipts, cancelled)
	}
	return adminID, token, csrf
}

func buildStage2RollbackArtifacts(t *testing.T, ctx context.Context, root, temporary string) (string, string, string) {
	t.Helper()
	archive := filepath.Join(temporary, "stage2-source.tar")
	archiveCommand := exec.CommandContext(ctx, "git", "archive", "--format=tar", "--output", archive, stage2ArchiveCommit)
	archiveCommand.Dir = root
	if output, err := archiveCommand.CombinedOutput(); err != nil {
		t.Fatalf("archive Stage2 source: %v\n%s", err, output)
	}
	source := filepath.Join(temporary, "stage2-source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(ctx, "tar", "-xf", archive, "-C", source).CombinedOutput(); err != nil {
		t.Fatalf("extract Stage2 source: %v\n%s", err, output)
	}
	oldArtifact := filepath.Join(temporary, "control-stage2")
	gate := filepath.Join(temporary, "relay-control-compat-gate")
	build := func(directory, outputPath, target string) {
		t.Helper()
		command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", outputPath, target)
		command.Dir = directory
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", target, err, output)
		}
		if err := os.Chmod(outputPath, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	build(source, oldArtifact, "./cmd/control")
	build(root, gate, "./cmd/relay-control-compat-gate")
	artifact, err := os.ReadFile(oldArtifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	return oldArtifact, gate, "sha256:" + hex.EncodeToString(digest[:])
}

func rewriteStage2RollbackHost(t *testing.T, raw, postgresContainer string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = postgresContainer + ":5432"
	return parsed.String()
}

func waitStage2RollbackControl(t *testing.T, ctx context.Context, container string) string {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		output, err := exec.CommandContext(ctx, "docker", "port", container, "8080/tcp").CombinedOutput()
		if err == nil {
			address := strings.TrimSpace(string(output))
			if index := strings.LastIndex(address, ":"); index >= 0 {
				base := "http://127.0.0.1:" + address[index+1:]
				response, requestErr := (&http.Client{Timeout: 500 * time.Millisecond}).Get(base + "/api/healthz")
				if requestErr == nil {
					response.Body.Close()
					if response.StatusCode == http.StatusOK {
						return base
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", container).CombinedOutput()
	t.Fatalf("Stage2 Control did not start through wrapper: %s", logs)
	return ""
}

func connectStage2RollbackRole(t *testing.T, ctx context.Context, ownerURL, role string) *pgx.Conn {
	t.Helper()
	connection, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
		connection.Close(ctx)
		t.Fatal(err)
	}
	return connection
}
