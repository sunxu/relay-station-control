package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	"github.com/sunxu/relay-station-control/internal/jobs"
)

func TestCrossNodeDuplicateNotificationRequestUsesFrozenTransitionContract(t *testing.T) {
	registry, err := jobs.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	first := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	second := uuid.MustParse("20000000-0000-4000-8000-000000000002")
	snapshot := notificationTransition{
		OccurrenceID:   uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a"),
		OccurrenceType: "CROSS_NODE_DUPLICATE_OWNERSHIP", Transition: "ACTIVE",
		Reason: "cross_node_duplicate_ownership", Severity: "Critical",
		EnvironmentID: "env-test", EnvironmentName: "Test", AccountKey: "antigravity:a@example.invalid",
		Email: "a@example.invalid", Provider: "antigravity",
		InstanceIDs: []uuid.UUID{second, first}, NodeNames: []string{"Zulu", "Alpha"},
		StartedAt:      time.Date(2026, 9, 11, 9, 0, 0, 123456789, time.FixedZone("test", 8*60*60)),
		TransitionedAt: time.Date(2026, 9, 11, 9, 1, 0, 0, time.FixedZone("test", 8*60*60)),
	}
	request, err := notificationEnqueueRequest(snapshot, false)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := "dingtalk:duplicate:" + snapshot.OccurrenceID.String() + ":active"
	if request.IdempotencyKey != wantKey || request.OperationID != uuid.NewSHA1(notificationNamespace, []byte(wantKey)) || request.Priority != 50 || request.PublisherEnabled {
		t.Fatalf("request identity or publisher state = %+v", request)
	}
	var payload dingtalk.Payload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.InstanceIDs) != 2 || payload.InstanceIDs[0] != first.String() || payload.NodeNames[0] != "Alpha" ||
		payload.InstanceIDs[1] != second.String() || payload.NodeNames[1] != "Zulu" ||
		payload.StartedAt != "2026-09-11T01:00:00.123456789Z" || payload.TransitionedAt != "2026-09-11T01:01:00Z" {
		t.Fatalf("payload snapshot = %+v", payload)
	}
	if _, _, _, err := registry.ValidateAndHash(request.Kind, request.SchemaVersion, request.Payload); err != nil {
		t.Fatalf("payload rejected by production registry: %v", err)
	}
}
