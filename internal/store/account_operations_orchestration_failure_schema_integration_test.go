package store_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/accountadmin"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountCommandFailureMatrixPG18(t *testing.T) {
	if testDatabaseURL(t) == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL to run PostgreSQL orchestration integration tests")
	}
	cases := []struct {
		name       string
		kind       store.AccountOperationKind
		email      string
		setup      failureScenario
		wantState  store.AccountOperationState
		wantCode   string
		wantHTTP   int
		wantMutate int32
	}{
		{name: "reviewed upload 503", kind: store.AccountUploadNew, email: "upload-503@example.invalid", setup: failureReviewedUpload503, wantState: store.AccountFailed, wantCode: "node_management_unavailable", wantHTTP: 503, wantMutate: 1},
		{name: "timeout", kind: store.AccountDisable, email: "timeout@example.invalid", setup: failureTimeout, wantState: store.AccountOutcomeUnknown, wantMutate: 1},
		{name: "connection loss", kind: store.AccountDisable, email: "connection-loss@example.invalid", setup: failureConnectionLoss, wantState: store.AccountOutcomeUnknown, wantMutate: 1},
		{name: "generic 500", kind: store.AccountDisable, email: "generic-500@example.invalid", setup: failureGeneric500, wantState: store.AccountOutcomeUnknown, wantMutate: 1},
		{name: "unsupported runtime", kind: store.AccountDisable, email: "bad-runtime@example.invalid", setup: failureUnsupportedRuntime, wantState: store.AccountFailed, wantCode: "unsupported_node_version", wantHTTP: 503, wantMutate: 0},
		{name: "target not found", kind: store.AccountDisable, email: "missing@example.invalid", setup: failureTargetNotFound, wantState: store.AccountFailed, wantCode: "account_target_not_found", wantHTTP: 404, wantMutate: 0},
		{name: "target ambiguous", kind: store.AccountDisable, email: "ambiguous@example.invalid", setup: failureTargetAmbiguous, wantState: store.AccountFailed, wantCode: "account_target_ambiguous", wantHTTP: 409, wantMutate: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFailureMatrixCase(t, tc)
		})
	}
}

type failureScenario int

const (
	failureReviewedUpload503 failureScenario = iota
	failureTimeout
	failureConnectionLoss
	failureGeneric500
	failureUnsupportedRuntime
	failureTargetNotFound
	failureTargetAmbiguous
)

type failureFixture struct {
	pool   *pgxpool.Pool
	repo   *store.AccountOperationRepository
	admin  uuid.UUID
	node   uuid.UUID
	server *httptest.Server
	gets   atomic.Int32
	mutate atomic.Int32
}

func runFailureMatrixCase(t *testing.T, tc struct {
	name       string
	kind       store.AccountOperationKind
	email      string
	setup      failureScenario
	wantState  store.AccountOperationState
	wantCode   string
	wantHTTP   int
	wantMutate int32
}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "37"); err != nil {
		t.Fatal(err)
	}
	admin, node := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Failure Admin','enabled',clock_timestamp())`, admin, "failure-"+admin.String()[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'Failure Node','cliproxyapi','v1','http://node.example/')`, node); err != nil {
		t.Fatal(err)
	}
	owner.Close(ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "49"); err != nil {
		t.Fatal(err)
	}
	p, err := runtimePool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	repo, err := store.NewAccountOperationRepository(p)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &failureFixture{pool: p, repo: repo, admin: admin, node: node}
	fixture.server = httptest.NewServer(fixture.handler(tc.setup, tc.email))
	defer fixture.server.Close()
	config, err := (drivers.ManagementConfig{ConnectTimeout: time.Second, RequestTimeout: time.Second}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := cliproxyapi.NewNativeAdapter(fixture.server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	service, err := accountadmin.NewService(repo, integrationNodeResolver{state: accountadmin.NodeState{Adapter: adapter, LifecycleActive: true, MonitoringEligible: true, InventoryReadAllowed: true, ProviderPolicyActive: true}})
	if err != nil {
		t.Fatal(err)
	}
	command, keyPath := failureCommand(t, tc.kind, node, admin, tc.email)
	if keyPath != "" {
		defer os.Remove(keyPath)
		command.IntentKeyPath = keyPath
	}
	operation, executeErr := service.Execute(ctx, command)
	if tc.wantState == store.AccountOutcomeUnknown {
		if executeErr == nil && operation.ExecutionState != tc.wantState {
			t.Fatalf("execute state=%s, want %s", operation.ExecutionState, tc.wantState)
		}
	} else if executeErr != nil {
		t.Fatalf("execute: %v", executeErr)
	}
	saved, err := repo.Operation(ctx, command.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ExecutionState != tc.wantState {
		t.Fatalf("durable state=%s, want %s", saved.ExecutionState, tc.wantState)
	}
	if tc.wantCode != "" {
		if saved.RemoteResultCode == nil || *saved.RemoteResultCode != tc.wantCode {
			var actual string
			if saved.RemoteResultCode != nil {
				actual = *saved.RemoteResultCode
			}
			t.Fatalf("durable result=%q, want %s", actual, tc.wantCode)
		}
		receipt, receiptErr := repo.Receipt(ctx, command.CommandID)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		if tc.wantHTTP != 0 && receipt.HTTPStatus != tc.wantHTTP {
			t.Fatalf("receipt status=%d, want %d", receipt.HTTPStatus, tc.wantHTTP)
		}
		var audits int
		if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id=$1`, command.RequestID).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if audits != 1 {
			t.Fatalf("audit count=%d, want 1", audits)
		}
	} else if _, err := repo.Receipt(ctx, command.CommandID); !errors.Is(err, store.ErrAccountOperationNotFound) {
		// outcome_unknown intentionally has no terminal receipt; Receipt returns
		// the repository's no-receipt error for this non-terminal execution truth.
		t.Fatalf("outcome_unknown receipt lookup=%v", err)
	}
	if fixture.mutate.Load() != tc.wantMutate {
		t.Fatalf("native mutation count=%d, want %d", fixture.mutate.Load(), tc.wantMutate)
	}
	if fixture.mutate.Load() > 1 {
		t.Fatal("more than one native mutation attempt")
	}
	var registryCount, operationCount, receiptCount int
	if err := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM admin_command_registry WHERE command_id=$1), (SELECT count(*) FROM account_admin_operations WHERE command_id=$1), (SELECT count(*) FROM account_admin_command_receipts WHERE command_id=$1)`, command.CommandID).Scan(&registryCount, &operationCount, &receiptCount); err != nil {
		t.Fatal(err)
	}
	if registryCount != 1 || operationCount != 1 || ((tc.wantCode != "") != (receiptCount == 1)) {
		t.Fatalf("durable counts registry/operation/receipt=%d/%d/%d", registryCount, operationCount, receiptCount)
	}
}

func runtimePool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	return pgxpool.NewWithConfig(ctx, config)
}

func failureCommand(t *testing.T, kind store.AccountOperationKind, node, admin uuid.UUID, email string) (accountadmin.Command, string) {
	t.Helper()
	accountKey := "antigravity:" + email
	command := accountadmin.Command{CommandID: uuid.New(), ActorAdminID: admin, NodeInstanceID: node, AccountKey: accountKey, Kind: kind, RequestID: "failure-" + uuid.NewString()}
	if kind == store.AccountUploadNew || kind == store.AccountReplaceExisting {
		path := filepath.Join(t.TempDir(), "intent.key")
		if err := os.WriteFile(path, []byte("01234567890123456789012345678901"), 0600); err != nil {
			t.Fatal(err)
		}
		command.Credential = []byte(`{"type":"antigravity","email":"` + email + `"}`)
		_, fingerprint, err := store.AccountUploadIntentFingerprint(path, command.Credential)
		if err != nil {
			t.Fatal(err)
		}
		command.CanonicalIntent, err = json.Marshal([]any{"account-intent-v1", "account." + string(kind), node.String(), accountKey, 1, hex.EncodeToString(fingerprint)})
		if err != nil {
			t.Fatal(err)
		}
		return command, path
	}
	var err error
	command.CanonicalIntent, err = accountadmin.CanonicalIntentV1(kind, node, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	return command, ""
}

func (f *failureFixture) handler(scenario failureScenario, email string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			f.gets.Add(1)
			if scenario == failureUnsupportedRuntime {
				w.Header().Set("X-CPA-VERSION", "7.3.1")
			} else {
				w.Header().Set("X-CPA-VERSION", cliproxyapi.FrozenRuntimeVersion)
			}
			w.Header().Set("X-CPA-COMMIT", cliproxyapi.FrozenRuntimeCommit)
			files := "[]"
			switch scenario {
			case failureTimeout, failureConnectionLoss, failureGeneric500:
				files = snapshotFile(email)
			case failureTargetAmbiguous:
				files = `[{"name":"a.json","provider":"antigravity","email":"ambiguous@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false},{"name":"b.json","provider":"antigravity","email":"ambiguous@example.invalid","source":"file","runtime_only":false,"auth_index":"2","disabled":false}]`
			}
			_, _ = io.WriteString(w, `{"files":`+files+`}`)
			return
		}
		f.mutate.Add(1)
		switch scenario {
		case failureReviewedUpload503:
			w.WriteHeader(http.StatusServiceUnavailable)
		case failureTimeout:
			time.Sleep(2 * time.Second)
		case failureConnectionLoss:
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
			}
		case failureGeneric500:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

func snapshotFile(email string) string {
	return `[{"name":"` + strings.ReplaceAll(email, "@", "-") + `.json","provider":"antigravity","email":"` + email + `","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]`
}
