package history

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAggregateAccountSegmentGolden(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	got, err := AggregateAccountSegment([]AccountSample{
		{ScheduledAt: base.Add(10 * time.Minute), ObservedAt: base.Add(10*time.Minute + 4*time.Second), TieKey: "poll-c", BasicStatus: BasicStatusError, Success: 5, Failed: 1},
		{ScheduledAt: base, ObservedAt: base.Add(20 * time.Second), TieKey: "poll-a", BasicStatus: BasicStatusActive, Success: 10, Failed: 0},
		{ScheduledAt: base.Add(5 * time.Minute), ObservedAt: base.Add(5*time.Minute + 8*time.Second), TieKey: "poll-b", BasicStatus: BasicStatusUnavailable, Success: 3, Failed: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := StatusCounts{Active: 1, Unavailable: 1, Error: 1}
	if got.FirstSuccess != 10 || got.LastSuccess != 5 || got.SuccessResets != 1 ||
		got.FirstFailed != 0 || got.LastFailed != 1 || got.FailedResets != 1 ||
		got.SampleCount != 3 || got.StatusCounts != wantCounts || got.LastBasicStatus != BasicStatusError ||
		!got.FirstScheduledAt.Equal(base) || !got.LastScheduledAt.Equal(base.Add(10*time.Minute)) {
		t.Fatalf("aggregate = %+v", got)
	}
}

func TestAggregateAccountSegmentUsesStableTieBreaker(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	got, err := AggregateAccountSegment([]AccountSample{
		{ScheduledAt: base, ObservedAt: base, TieKey: "a", BasicStatus: BasicStatusActive, Success: 10},
		{ScheduledAt: base, ObservedAt: base, TieKey: "b", BasicStatus: BasicStatusError, Success: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.LastBasicStatus != BasicStatusError || got.LastSuccess != 1 || got.SuccessResets != 1 {
		t.Fatalf("stable tie aggregate = %+v", got)
	}
}

func TestRollupAccountSegmentsAddsBoundaryResets(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	segments := []AccountSegment{
		{PolicyVersion: testPolicyUUID(2), AccountAggregate: AccountAggregate{
			FirstScheduledAt: base.Add(10 * time.Minute), LastScheduledAt: base.Add(15 * time.Minute),
			FirstObservedAt: base.Add(10 * time.Minute), LastObservedAt: base.Add(15 * time.Minute),
			LastBasicStatus: BasicStatusError, SampleCount: 3, StatusCounts: StatusCounts{Error: 3},
			FirstSuccess: 90, LastSuccess: 130, SuccessResets: 2,
			FirstFailed: 11, LastFailed: 3, FailedResets: 1,
		}},
		{PolicyVersion: testPolicyUUID(1), AccountAggregate: AccountAggregate{
			FirstScheduledAt: base, LastScheduledAt: base.Add(5 * time.Minute),
			FirstObservedAt: base, LastObservedAt: base.Add(5 * time.Minute),
			LastBasicStatus: BasicStatusActive, SampleCount: 2, StatusCounts: StatusCounts{Active: 2},
			FirstSuccess: 100, LastSuccess: 120, SuccessResets: 1,
			FirstFailed: 1, LastFailed: 10,
		}},
	}
	got, err := RollupAccountSegments(segments)
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleCount != 5 || got.SuccessResets != 4 || got.FailedResets != 1 ||
		got.FirstSuccess != 100 || got.LastSuccess != 130 || got.FirstFailed != 1 || got.LastFailed != 3 ||
		got.LastBasicStatus != BasicStatusError || got.StatusCounts != (StatusCounts{Active: 2, Error: 3}) {
		t.Fatalf("rollup = %+v", got)
	}
}

func TestRollupAccountSegmentsUsesLastScheduledAndPolicyUUIDTieBreak(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	segment := func(policy uuid.UUID, status BasicStatus, lastSuccess uint64) AccountSegment {
		counts := StatusCounts{Active: 1}
		if status == BasicStatusError {
			counts = StatusCounts{Error: 1}
		}
		return AccountSegment{PolicyVersion: policy, AccountAggregate: AccountAggregate{
			FirstScheduledAt: base, LastScheduledAt: base.Add(5 * time.Minute),
			FirstObservedAt: base, LastObservedAt: base.Add(5 * time.Minute),
			LastBasicStatus: status, SampleCount: 1, FirstSuccess: lastSuccess,
			LastSuccess: lastSuccess, StatusCounts: counts,
		}}
	}
	// Both segments have the same last_scheduled_at. PostgreSQL UUID ascending
	// order is the fixed tie-break, so the larger policy UUID supplies last.
	got, err := RollupAccountSegments([]AccountSegment{
		segment(testPolicyUUID(2), BasicStatusError, 2),
		segment(testPolicyUUID(1), BasicStatusActive, 10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.LastBasicStatus != BasicStatusError || got.LastSuccess != 2 || got.SuccessResets != 1 {
		t.Fatalf("UUID tie-break rollup = %+v", got)
	}
}

func TestRollupAccountSegmentsSeparatesBoundaryAndLastValueOrder(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	earlierStartLaterEnd := AccountSegment{PolicyVersion: testPolicyUUID(1), AccountAggregate: AccountAggregate{
		FirstScheduledAt: base, LastScheduledAt: base.Add(20 * time.Minute),
		FirstObservedAt: base, LastObservedAt: base.Add(20 * time.Minute),
		LastBasicStatus: BasicStatusActive, SampleCount: 2, StatusCounts: StatusCounts{Active: 2},
		FirstSuccess: 100, LastSuccess: 120,
	}}
	laterStartEarlierEnd := AccountSegment{PolicyVersion: testPolicyUUID(2), AccountAggregate: AccountAggregate{
		FirstScheduledAt: base.Add(10 * time.Minute), LastScheduledAt: base.Add(15 * time.Minute),
		FirstObservedAt: base.Add(10 * time.Minute), LastObservedAt: base.Add(15 * time.Minute),
		LastBasicStatus: BasicStatusError, SampleCount: 2, StatusCounts: StatusCounts{Error: 2},
		FirstSuccess: 90, LastSuccess: 95,
	}}
	got, err := RollupAccountSegments([]AccountSegment{laterStartEarlierEnd, earlierStartLaterEnd})
	if err != nil {
		t.Fatal(err)
	}
	// Reset boundaries follow first_scheduled_at, so 90 < 120 adds one reset;
	// last values follow last_scheduled_at, so policy 1 supplies the final row.
	if got.SuccessResets != 1 || got.FirstSuccess != 100 || got.LastSuccess != 120 ||
		got.LastBasicStatus != BasicStatusActive {
		t.Fatalf("boundary/last ordering rollup = %+v", got)
	}
}

func TestRollupAccountSegmentsRejectsOverflow(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	segment := func(policy uuid.UUID, count uint64) AccountSegment {
		return AccountSegment{PolicyVersion: policy, AccountAggregate: AccountAggregate{
			FirstScheduledAt: base, LastScheduledAt: base, FirstObservedAt: base, LastObservedAt: base,
			LastBasicStatus: BasicStatusActive, SampleCount: count, StatusCounts: StatusCounts{Active: count},
		}}
	}
	if _, err := RollupAccountSegments([]AccountSegment{
		segment(testPolicyUUID(1), math.MaxUint64), segment(testPolicyUUID(2), 1),
	}); !errors.Is(err, ErrCountOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}

func testPolicyUUID(value byte) uuid.UUID {
	var result uuid.UUID
	result[15] = value
	return result
}
