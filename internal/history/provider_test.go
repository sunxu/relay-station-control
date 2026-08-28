package history

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAggregateProviderSegmentCountsMissingAbandonedAndSkipped(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	expected := []time.Time{base, base.Add(5 * time.Minute), base.Add(10 * time.Minute), base.Add(15 * time.Minute)}
	got, err := AggregateProviderSegment(expected, []ProviderPoll{
		{ScheduledAt: expected[2], Abandoned: true},
		{ScheduledAt: expected[1], TransportOK: true, ContractOK: true, SnapshotComplete: true, PromotionSkipped: true, SkipReason: "policy_changed"},
		{ScheduledAt: expected[0], TransportOK: true, ContractOK: true, SnapshotComplete: true, PromotionApplied: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpectedCount != 4 || got.TransportSuccessCount != 2 || got.ContractValidCount != 2 ||
		got.SnapshotCompleteCount != 2 || got.PromotionAppliedCount != 1 || got.PromotionSkippedCount != 1 ||
		got.AbandonedCount != 1 || got.PolicyChangedCount != 1 || got.DegradedCount != 0 ||
		got.Coverage.BasisPoints != 2_500 || got.Coverage.Status != CoveragePartial ||
		got.FirstPromotionAt == nil || !got.FirstPromotionAt.Equal(base) || got.LastPromotionAt == nil || !got.LastPromotionAt.Equal(base) {
		t.Fatalf("provider aggregate = %+v", got)
	}
}

func TestAggregateProviderSegmentZeroDataIsExplicitPartial(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	got, err := AggregateProviderSegment([]time.Time{base, base.Add(5 * time.Minute)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpectedCount != 2 || got.PromotionAppliedCount != 0 || got.Coverage.Status != CoveragePartial || got.Coverage.BasisPoints != 0 {
		t.Fatalf("zero-data aggregate = %+v", got)
	}
}

func TestRollupProviderSegmentsRecalculatesRatio(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	first, err := CalculateCoverage(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CalculateCoverage(8, 9)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RollupProviderSegments([]ProviderSegment{
		{PolicyVersion: testPolicyUUID(1), ProviderAggregate: ProviderAggregate{
			ExpectedCount: 1, TransportSuccessCount: 1, ContractValidCount: 1, SnapshotCompleteCount: 1,
			PromotionAppliedCount: 1, FirstPromotionAt: &base, LastPromotionAt: &base, Coverage: first}},
		{PolicyVersion: testPolicyUUID(2), ProviderAggregate: ProviderAggregate{
			ExpectedCount: 9, TransportSuccessCount: 8, ContractValidCount: 8, SnapshotCompleteCount: 8,
			PromotionAppliedCount: 8, FirstPromotionAt: ptrTime(base.Add(5 * time.Minute)),
			LastPromotionAt: ptrTime(base.Add(40 * time.Minute)), PromotionSkippedCount: 1,
			Coverage: second}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpectedCount != 10 || got.PromotionAppliedCount != 9 || got.Coverage.BasisPoints != 9_000 || got.Coverage.Status != CoveragePartial {
		t.Fatalf("provider rollup = %+v", got)
	}
}

func TestRollupProviderSegmentsIsPolicyOrderIndependentAtIntegerBoundary(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	segment := func(policyByte byte, expected, applied uint64) ProviderSegment {
		coverage, err := CalculateCoverage(applied, expected)
		if err != nil {
			t.Fatal(err)
		}
		value := ProviderAggregate{
			ExpectedCount: expected, TransportSuccessCount: applied, ContractValidCount: applied,
			SnapshotCompleteCount: applied, PromotionAppliedCount: applied,
			PromotionSkippedCount: expected - applied, Coverage: coverage,
		}
		if applied > 0 {
			value.FirstPromotionAt = &base
			value.LastPromotionAt = &base
		}
		return ProviderSegment{PolicyVersion: testPolicyUUID(policyByte), ProviderAggregate: value}
	}
	segments := []ProviderSegment{segment(3, 10, 9), segment(1, 5, 5), segment(2, 5, 5)}
	got, err := RollupProviderSegments(segments)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := RollupProviderSegments([]ProviderSegment{segments[1], segments[2], segments[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, reordered) || got.ExpectedCount != 20 || got.PromotionAppliedCount != 19 ||
		got.Coverage.BasisPoints != 9_500 || got.Coverage.Status != CoverageComplete {
		t.Fatalf("policy-order rollup = %+v reordered=%+v", got, reordered)
	}
}

func TestRollupProviderSegmentsOmitsZeroExpected(t *testing.T) {
	if _, err := RollupProviderSegments(nil); !errors.Is(err, ErrNoExpectedSlots) {
		t.Fatalf("empty rollup error = %v", err)
	}
	if _, err := RollupProviderSegments([]ProviderSegment{{
		PolicyVersion: testPolicyUUID(1), ProviderAggregate: ProviderAggregate{},
	}}); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("zero-expected segment error = %v", err)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func TestAggregateProviderSegmentRejectsResultOutsidePlan(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if _, err := AggregateProviderSegment([]time.Time{base}, []ProviderPoll{{ScheduledAt: base.Add(5 * time.Minute), Abandoned: true}}); err == nil {
		t.Fatal("poll outside expected slots was accepted")
	}
}
