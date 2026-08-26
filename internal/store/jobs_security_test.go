package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWakeEnvelopeDecoderRejectsUnknownAndTrailingDocuments(t *testing.T) {
	valid := `{"schema_version":1,"event_id":"11111111-1111-4111-8111-111111111111","job_id":"22222222-2222-4222-8222-222222222222","operation_id":"33333333-3333-4333-8333-333333333333","topic":"async_job_wake"}`
	if _, err := decodeWakeEnvelope([]byte(valid)); err != nil {
		t.Fatalf("valid envelope: %v", err)
	}
	for name, raw := range map[string]string{
		"unknown field":     valid[:len(valid)-1] + `,"payload":"canary"}`,
		"trailing document": valid + `{}`,
		"wrong type":        `{"schema_version":"1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWakeEnvelope([]byte(raw)); err == nil {
				t.Fatal("unsafe envelope unexpectedly accepted")
			}
		})
	}
}

func TestPublicJobProjectionFailsClosedOnInvalidEnumsOrMissingOutbox(t *testing.T) {
	now := time.Now().UTC()
	base := JobSummary{
		JobID: uuid.New(), OperationID: uuid.New(), JobKind: "test.synthetic",
		Status: "pending", AttemptCount: 0, MaxAttempts: 3,
		AvailableAt: now, OutboxStatus: "suppressed", CreatedAt: now, UpdatedAt: now,
	}
	if !validJobSummary(base) {
		t.Fatal("valid summary rejected")
	}
	for _, status := range []string{"none", "unknown", ""} {
		candidate := base
		candidate.OutboxStatus = status
		if validJobSummary(candidate) {
			t.Fatalf("outbox status %q accepted", status)
		}
	}
	candidate := base
	candidate.Status = "arbitrary"
	if validJobSummary(candidate) {
		t.Fatal("arbitrary job status accepted")
	}

	event := JobEvent{Sequence: 1, EventType: "enqueued", ToStatus: "pending", Attempt: 0, ActorType: "service", OccurredAt: now}
	if !validJobEvent(event) {
		t.Fatal("valid event rejected")
	}
	event.EventType = "arbitrary"
	if validJobEvent(event) {
		t.Fatal("arbitrary event accepted")
	}
}
