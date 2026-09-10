package store

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	"github.com/sunxu/relay-station-control/internal/jobs"
)

// Test-only seam exercises the exact producer through real caller-owned pgx
// transactions without exposing an internal notification API in production.
type NotificationSnapshotForTest = notificationTransition

func EnqueueNotificationForTest(ctx context.Context, tx pgx.Tx, registry *jobs.Registry, snapshot NotificationSnapshotForTest) error {
	return enqueueNotificationTx(ctx, tx, notificationDeliveryFor(registry, true), snapshot)
}

func TestNotificationIdentityCanonicalSnapshot(t *testing.T) {
	registry, err := jobs.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := notificationTransition{
		OccurrenceID:   uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a"),
		OccurrenceType: "TOKEN_INVALID", Transition: "ACTIVE", Reason: "token_invalid", Severity: "Critical",
		EnvironmentID: "test", EnvironmentName: "Test", AccountKey: "antigravity:a@example.invalid", Email: "a@example.invalid", Provider: "antigravity",
		InstanceIDs:    []uuid.UUID{uuid.MustParse("20000000-0000-4000-8000-000000000002"), uuid.MustParse("10000000-0000-4000-8000-000000000001")},
		NodeNames:      []string{"Alpha", "Zulu"},
		StartedAt:      time.Date(2026, 9, 11, 9, 0, 0, 123456789, time.FixedZone("test", 8*3600)),
		TransitionedAt: time.Date(2026, 9, 11, 9, 1, 0, 0, time.FixedZone("test", 8*3600)),
	}
	seen := map[uuid.UUID]bool{}
	for _, domain := range []string{"availability", "duplicate"} {
		for _, transition := range []string{"ACTIVE", "RESOLVED"} {
			value := snapshot
			value.Transition = transition
			if domain == "duplicate" {
				value.OccurrenceType = "CROSS_NODE_DUPLICATE_OWNERSHIP"
				value.Reason = "cross_node_duplicate_ownership"
			}
			request, err := notificationEnqueueRequest(value, true)
			if err != nil {
				t.Fatal(err)
			}
			suffix := "active"
			if transition == "RESOLVED" {
				suffix = "resolved"
			}
			key := "dingtalk:" + domain + ":" + snapshot.OccurrenceID.String() + ":" + suffix
			if request.IdempotencyKey != key || request.OperationID != uuid.NewSHA1(uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86"), []byte(key)) || request.Priority != 50 || request.SchemaVersion != 1 || request.Kind != dingtalk.JobKind || request.Actor != jobs.ActorService {
				t.Fatalf("incorrect identity/metadata: %+v", request)
			}
			if seen[request.OperationID] {
				t.Fatal("different keys share operation identity")
			}
			seen[request.OperationID] = true
			canonical, hash, _, err := registry.ValidateAndHash(request.Kind, request.SchemaVersion, request.Payload)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := notificationEnqueueRequest(value, true)
			if err != nil || replay.OperationID != request.OperationID || !bytes.Equal(replay.Payload, request.Payload) {
				t.Fatal("same transition identity/payload drift")
			}
			again, againHash, _, err := registry.ValidateAndHash(replay.Kind, replay.SchemaVersion, replay.Payload)
			if err != nil || !bytes.Equal(canonical, again) || hash != againHash {
				t.Fatal("canonical/hash drift")
			}
			var payload dingtalk.Payload
			if err = json.Unmarshal(canonical, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.InstanceIDs[0] != snapshot.InstanceIDs[1].String() || payload.NodeNames[0] != "Zulu" || payload.NodeNames[1] != "Alpha" || payload.StartedAt != "2026-09-11T01:00:00.123456789Z" || payload.TransitionedAt != "2026-09-11T01:01:00Z" {
				t.Fatalf("snapshot pairing/time lost: %+v", payload)
			}
			value.OccurrenceID = uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2b")
			other, err := notificationEnqueueRequest(value, true)
			if err != nil || other.OperationID == request.OperationID {
				t.Fatal("different occurrence reused operation ID")
			}
		}
	}
	if snapshot.NodeNames[0] != "Alpha" {
		t.Fatal("producer mutated domain snapshot")
	}
	// Disabled means no transaction or enqueue call is even needed.
	if err := enqueueNotificationTx(context.Background(), nil, nil, notificationTransition{}); err != nil {
		t.Fatal(err)
	}
}
