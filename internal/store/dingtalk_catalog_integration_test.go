package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/dingtalk"
	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

func dingtalkTestPayload(t *testing.T, definition jobcore.Definition) []byte {
	t.Helper()
	values := map[string]any{
		"occurrence_id":    uuid.NewString(),
		"occurrence_type":  "TOKEN_INVALID",
		"transition":       "ACTIVE",
		"reason":           "token_invalid",
		"severity":         "Critical",
		"environment_id":   "environment-test",
		"environment_name": "test",
		"account_key":      "antigravity:test@example.invalid",
		"email":            "test@example.invalid",
		"provider":         "antigravity",
		"instance_ids":     []string{uuid.NewString()},
		"node_names":       []string{"node-test"},
		"started_at":       time.Now().UTC().Format(time.RFC3339Nano),
		"transitioned_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if len(definition.Schema.Fields) != 14 {
		t.Fatalf("DingTalk schema field count = %d, want fixed 14", len(definition.Schema.Fields))
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestDingTalkDuplicateDisplayNamesEnqueue(t *testing.T) {
	database := newIsolatedJobDatabase(t, "up")
	ctx := context.Background()
	definition := dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{}))
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	var payload dingtalk.Payload
	if err := json.Unmarshal(dingtalkTestPayload(t, definition), &payload); err != nil {
		t.Fatal(err)
	}
	payload.InstanceIDs = []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
	payload.NodeNames = []string{"Relay", "Relay"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	canonical, hash, _, err := registry.ValidateAndHash(definition.Kind, 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	request := jobcore.EnqueueRequest{Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:dingtalk:duplicate-display", Payload: raw, Priority: 50, Actor: jobcore.ActorService}
	created := enqueueCommitted(t, ctx, database.owner, registry, request)
	request.Payload = canonical
	repeated := enqueueCommitted(t, ctx, database.owner, registry, request)
	if !created.Created || repeated.Created || repeated.Job.ID != created.Job.ID || repeated.Job.PayloadHash != hash {
		t.Fatal("duplicate display snapshot enqueue not idempotent")
	}
	var stored []byte
	if err := database.owner.QueryRow(ctx, `SELECT payload FROM async_jobs WHERE job_id=$1`, created.Job.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var actual dingtalk.Payload
	if err := json.Unmarshal(stored, &actual); err != nil {
		t.Fatal(err)
	}
	if len(actual.NodeNames) != 2 || actual.NodeNames[0] != "Relay" || actual.NodeNames[1] != "Relay" {
		t.Fatal("duplicate names lost during persistence")
	}
	payload.Severity = "Warning"
	request.Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = "test:dingtalk:bad-tuple"
	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	txStore, err := jobstore.NewJobTxStore(tx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = jobcore.EnqueueTx(ctx, txStore, registry, request)
	if !errors.Is(err, jobcore.ErrInvalidPayload) {
		t.Fatalf("invalid tuple enqueue: %v", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM async_jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("invalid tuple inserted job: %d", count)
	}
}

func TestDingTalkCatalogCleanInstallAndForwardUpgrade(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		migration []string
		upgrade   bool
	}{
		{name: "clean install", migration: []string{"up"}},
		{name: "forward upgrade", migration: []string{"up-to", "30"}, upgrade: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			database := newIsolatedJobDatabase(t, scenario.migration...)
			if scenario.upgrade {
				if err := runAssetGoose(t, context.Background(), "../..", database.ownerURL, "up"); err != nil {
					t.Fatal(err)
				}
			}
			testDingTalkCatalogAndSnapshot(t, database)
		})
	}
}

func TestDingTalkCanonicalPayloadEnqueue(t *testing.T) {
	database := newIsolatedJobDatabase(t, "up")
	ctx := context.Background()
	definition := dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{}))
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		mutate func(*dingtalk.Payload)
		valid  bool
	}{
		{"parallel names", func(p *dingtalk.Payload) { p.NodeNames = []string{"Zulu", "Alpha"} }, true},
		{"missing name", func(p *dingtalk.Payload) { p.NodeNames = []string{"Zulu"} }, false},
		{"noncanonical timestamp", func(p *dingtalk.Payload) { p.StartedAt = "2026-09-11T08:00:00+00:00" }, false},
		{"fractional zero", func(p *dingtalk.Payload) { p.StartedAt = "2026-09-11T08:00:00.000Z" }, false},
		{"tuple mismatch", func(p *dingtalk.Payload) { p.Reason = "forbidden" }, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var payload dingtalk.Payload
			if err := json.Unmarshal(dingtalkTestPayload(t, definition), &payload); err != nil {
				t.Fatal(err)
			}
			payload.InstanceIDs = []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
			payload.NodeNames = []string{"Relay", "Relay"}
			payload.StartedAt, payload.TransitionedAt = "2026-09-11T08:00:00Z", "2026-09-11T08:01:00Z"
			scenario.mutate(&payload)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := database.owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			txStore, err := jobstore.NewJobTxStore(tx)
			if err != nil {
				t.Fatal(err)
			}
			result, err := jobcore.EnqueueTx(ctx, txStore, registry, jobcore.EnqueueRequest{
				Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
				IdempotencyKey: "test:dingtalk:canonical", Payload: raw, Priority: 50, Actor: jobcore.ActorService,
			})
			if scenario.valid {
				if err != nil || !result.Created || result.Job.Priority != 50 {
					t.Fatalf("valid payload enqueue: %v", err)
				}
				var stored []byte
				if err := tx.QueryRow(ctx, `SELECT payload FROM async_jobs WHERE job_id=$1`, result.Job.ID).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				var actual dingtalk.Payload
				if err := json.Unmarshal(stored, &actual); err != nil {
					t.Fatal(err)
				}
				if len(actual.NodeNames) != 2 || actual.NodeNames[0] != "Zulu" || actual.NodeNames[1] != "Alpha" || actual.InstanceIDs[0] != payload.InstanceIDs[0] || actual.InstanceIDs[1] != payload.InstanceIDs[1] {
					t.Fatal("ID/name pairing changed in database")
				}
			} else {
				if !errors.Is(err, jobcore.ErrInvalidPayload) {
					t.Fatalf("invalid payload enqueue: %v", err)
				}
				var count int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM async_jobs`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("invalid payload inserted %d jobs", count)
				}
			}
		})
	}
}

func testDingTalkCatalogAndSnapshot(t *testing.T, database *isolatedJobDatabase) {
	t.Helper()
	ctx := context.Background()

	var row struct {
		kind, lifecycle                                      string
		version, timeout, lease, heartbeat, attempts, verify int
		replay, unknown, direct, rollback                    bool
	}
	err := database.owner.QueryRow(ctx, `SELECT job_kind,lifecycle_status,payload_schema_version,
		default_timeout_seconds,lease_seconds,heartbeat_interval_seconds,
		default_max_attempts,default_max_verification_attempts,replay_safe,
		allow_unknown_effect_replay,allow_direct_success,rollback_allowed
		FROM async_job_kinds WHERE job_kind='dingtalk_alert_delivery'`).Scan(
		&row.kind, &row.lifecycle, &row.version, &row.timeout, &row.lease,
		&row.heartbeat, &row.attempts, &row.verify, &row.replay, &row.unknown,
		&row.direct, &row.rollback)
	if err != nil {
		t.Fatal(err)
	}
	if row.kind != "dingtalk_alert_delivery" || row.lifecycle != "active" || row.version != 1 ||
		row.timeout != 10 || row.lease != 30 || row.heartbeat != 5 || row.attempts != 5 ||
		row.verify != 1 || !row.replay || !row.unknown || !row.direct || row.rollback {
		t.Fatalf("DingTalk DB catalog row = %+v", row)
	}
	var jobs int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("initial durable jobs = %d, want 0", jobs)
	}
	runtimeRepository, err := jobstore.NewJobRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := runtimeRepository.JobKinds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]string{
		"absent":     "",
		"configured": "https://oapi.dingtalk.com/robot/send?access_token=test",
	} {
		t.Run(name, func(t *testing.T) {
			config, err := dingtalk.LoadConfig(func(key string) string {
				if key == "DINGTALK_WEBHOOK_URL" {
					return values
				}
				if name == "configured" && key == "DINGTALK_SIGNING_SECRET" {
					return "test-signing-secret"
				}
				return ""
			})
			if err != nil {
				t.Fatal(err)
			}
			registry, err := jobcore.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(config)))
			if err != nil {
				t.Fatal(err)
			}
			if len(policies) != 1 {
				t.Fatalf("runtime JobKinds length = %d, want 1", len(policies))
			}
			policy, entry := policies[0], registry.Catalog()[0]
			if policy.JobKind != entry.Kind || policy.PayloadSchemaVersion != entry.SchemaVersion ||
				policy.Timeout != entry.Timeout || policy.LeaseDuration != entry.LeaseDuration ||
				policy.HeartbeatInterval != entry.HeartbeatInterval || policy.MaxAttempts != entry.MaxAttempts ||
				policy.MaxVerificationAttempts != entry.MaxVerifyAttempts || policy.ReplaySafe != entry.ReplaySafe ||
				policy.AllowUnknownEffectReplay != entry.AllowUnknownEffectReplay ||
				policy.AllowDirectSuccess != entry.AllowDirectSuccess || policy.RollbackAllowed != entry.AllowRollback {
				t.Fatalf("catalog mismatch: db=%+v process=%+v", policy, entry)
			}
			if config.Enabled() != (name == "configured") {
				t.Fatalf("config enabled = %v", config.Enabled())
			}
		})
	}
	config, err := dingtalk.LoadConfig(func(key string) string {
		if key == "DINGTALK_WEBHOOK_URL" {
			return "https://oapi.dingtalk.com/robot/send?access_token=test"
		}
		if key == "DINGTALK_SIGNING_SECRET" {
			return "test-signing-secret"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	definition := dingtalk.Definition(dingtalk.NewExecutor(config))
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "test:dingtalk:configured:" + uuid.NewString(),
		Payload:        dingtalkTestPayload(t, definition), Priority: 50,
		PublisherEnabled: false, Actor: jobcore.ActorService,
	})
	if !created.Created || !created.Job.AllowUnknownEffectReplay || !created.Job.AllowDirectSuccess ||
		!created.Job.ReplaySafe || created.Job.AllowRollback || created.Job.MaxAttempts != 5 ||
		created.Job.MaxVerifyAttempts != 1 || created.Job.Timeout != 10*time.Second ||
		created.Job.LeaseDuration != 30*time.Second || created.Job.HeartbeatInterval != 5*time.Second {
		t.Fatalf("enqueue policy snapshot = %+v", created.Job)
	}
	var storedPayloadText string
	if err := database.owner.QueryRow(ctx, `SELECT payload::text FROM async_jobs WHERE job_id=$1`, created.Job.ID).Scan(&storedPayloadText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedPayloadText, "https://oapi.dingtalk.com/robot/send?access_token=test") ||
		strings.Contains(storedPayloadText, "test-signing-secret") {
		t.Fatalf("DingTalk secret configuration leaked into stored payload: %s", storedPayloadText)
	}
}
