package history

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

const MaximumDailyExpectedSlots = uint64(24 * time.Hour / SlotInterval)

type ProviderPoll struct {
	ScheduledAt      time.Time
	Abandoned        bool
	TransportOK      bool
	ContractOK       bool
	SnapshotComplete bool
	PromotionApplied bool
	PromotionSkipped bool
	Degraded         bool
	SkipReason       string
}

type ProviderAggregate struct {
	ExpectedCount         uint64
	TransportSuccessCount uint64
	ContractValidCount    uint64
	SnapshotCompleteCount uint64
	PromotionAppliedCount uint64
	PromotionSkippedCount uint64
	AbandonedCount        uint64
	DegradedCount         uint64
	PolicyChangedCount    uint64
	FirstPromotionAt      *time.Time
	LastPromotionAt       *time.Time
	Coverage              Coverage
}

type ProviderSegment struct {
	PolicyVersion uuid.UUID
	ProviderAggregate
}

// AggregateProviderSegment counts one Provider independently over the planned
// slots. Missing slots remain in ExpectedCount; abandoned slots have no
// fabricated provider result; and only applied promotions enter coverage.
func AggregateProviderSegment(expectedSlots []time.Time, polls []ProviderPoll) (ProviderAggregate, error) {
	if len(expectedSlots) == 0 {
		return ProviderAggregate{}, ErrNoExpectedSlots
	}
	if uint64(len(expectedSlots)) > MaximumDailyExpectedSlots {
		return ProviderAggregate{}, ErrInvalidCount
	}
	expected := make(map[int64]struct{}, len(expectedSlots))
	for index, slot := range expectedSlots {
		if slot.IsZero() || !slot.Equal(slot.UTC().Truncate(SlotInterval)) {
			return ProviderAggregate{}, fmt.Errorf("expected slot %d: %w", index, ErrInvalidTime)
		}
		key := slot.Unix()
		if _, duplicate := expected[key]; duplicate {
			return ProviderAggregate{}, fmt.Errorf("expected slot %d duplicate: %w", index, ErrInvalidCount)
		}
		expected[key] = struct{}{}
	}
	ordered := append([]ProviderPoll(nil), polls...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ScheduledAt.Before(ordered[j].ScheduledAt) })
	seen := make(map[int64]struct{}, len(ordered))
	result := ProviderAggregate{ExpectedCount: uint64(len(expectedSlots))}
	for index, poll := range ordered {
		poll.ScheduledAt = poll.ScheduledAt.UTC()
		if poll.ScheduledAt.IsZero() || !poll.ScheduledAt.Equal(poll.ScheduledAt.Truncate(SlotInterval)) {
			return ProviderAggregate{}, fmt.Errorf("provider poll %d slot: %w", index, ErrInvalidTime)
		}
		key := poll.ScheduledAt.Unix()
		if _, exists := expected[key]; !exists {
			return ProviderAggregate{}, fmt.Errorf("provider poll %d outside plan: %w", index, ErrInvalidTime)
		}
		if _, duplicate := seen[key]; duplicate {
			return ProviderAggregate{}, fmt.Errorf("provider poll %d duplicate: %w", index, ErrInvalidCount)
		}
		seen[key] = struct{}{}
		if poll.Abandoned {
			if poll.TransportOK || poll.ContractOK || poll.SnapshotComplete || poll.PromotionApplied ||
				poll.PromotionSkipped || poll.Degraded || poll.SkipReason != "" {
				return ProviderAggregate{}, fmt.Errorf("provider poll %d abandoned shape: %w", index, ErrInvalidCount)
			}
			result.AbandonedCount++
			continue
		}
		if poll.ContractOK && !poll.TransportOK || poll.SnapshotComplete && !poll.ContractOK ||
			poll.PromotionApplied && (!poll.SnapshotComplete || poll.PromotionSkipped || poll.Degraded) ||
			!poll.PromotionApplied && !poll.PromotionSkipped || !poll.PromotionSkipped && poll.SkipReason != "" ||
			!validSkipReason(poll.SkipReason) {
			return ProviderAggregate{}, fmt.Errorf("provider poll %d finalized shape: %w", index, ErrInvalidCount)
		}
		if poll.TransportOK {
			result.TransportSuccessCount++
		}
		if poll.ContractOK {
			result.ContractValidCount++
		}
		if poll.SnapshotComplete {
			result.SnapshotCompleteCount++
		}
		if poll.PromotionApplied {
			result.PromotionAppliedCount++
			promotionAt := poll.ScheduledAt
			if result.FirstPromotionAt == nil {
				result.FirstPromotionAt = &promotionAt
			}
			result.LastPromotionAt = &promotionAt
		}
		if poll.PromotionSkipped {
			result.PromotionSkippedCount++
		}
		if poll.Degraded {
			result.DegradedCount++
		}
		if poll.SkipReason == "policy_changed" {
			result.PolicyChangedCount++
		}
	}
	coverage, err := CalculateCoverage(result.PromotionAppliedCount, result.ExpectedCount)
	if err != nil {
		return ProviderAggregate{}, err
	}
	result.Coverage = coverage
	return result, nil
}

// RollupProviderSegments sums counts and recalculates coverage from the totals;
// it never averages segment percentages.
func RollupProviderSegments(segments []ProviderSegment) (ProviderAggregate, error) {
	if len(segments) == 0 {
		return ProviderAggregate{}, ErrNoExpectedSlots
	}
	ordered := append([]ProviderSegment(nil), segments...)
	seenPolicies := make(map[uuid.UUID]struct{}, len(ordered))
	for index, segment := range ordered {
		if segment.PolicyVersion == uuid.Nil || !validProviderAggregate(segment.ProviderAggregate) {
			return ProviderAggregate{}, fmt.Errorf("provider segment %d: %w", index, ErrInvalidCount)
		}
		if _, duplicate := seenPolicies[segment.PolicyVersion]; duplicate {
			return ProviderAggregate{}, fmt.Errorf("provider segment %d duplicate policy: %w", index, ErrInvalidCount)
		}
		seenPolicies[segment.PolicyVersion] = struct{}{}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return uuidLess(ordered[i].PolicyVersion, ordered[j].PolicyVersion)
	})
	var result ProviderAggregate
	for _, providerSegment := range ordered {
		segment := providerSegment.ProviderAggregate
		if segment.FirstPromotionAt != nil {
			first := segment.FirstPromotionAt.UTC()
			segment.FirstPromotionAt = &first
		}
		if segment.LastPromotionAt != nil {
			last := segment.LastPromotionAt.UTC()
			segment.LastPromotionAt = &last
		}
		for _, pair := range []struct {
			destination *uint64
			value       uint64
		}{
			{&result.ExpectedCount, segment.ExpectedCount},
			{&result.TransportSuccessCount, segment.TransportSuccessCount},
			{&result.ContractValidCount, segment.ContractValidCount},
			{&result.SnapshotCompleteCount, segment.SnapshotCompleteCount},
			{&result.PromotionAppliedCount, segment.PromotionAppliedCount},
			{&result.PromotionSkippedCount, segment.PromotionSkippedCount},
			{&result.AbandonedCount, segment.AbandonedCount},
			{&result.DegradedCount, segment.DegradedCount},
			{&result.PolicyChangedCount, segment.PolicyChangedCount},
		} {
			if err := addUint64(pair.destination, pair.value); err != nil {
				return ProviderAggregate{}, err
			}
		}
		result.FirstPromotionAt = earlierTime(result.FirstPromotionAt, segment.FirstPromotionAt)
		result.LastPromotionAt = laterTime(result.LastPromotionAt, segment.LastPromotionAt)
	}
	if result.ExpectedCount > MaximumDailyExpectedSlots {
		return ProviderAggregate{}, ErrInvalidCount
	}
	coverage, err := CalculateCoverage(result.PromotionAppliedCount, result.ExpectedCount)
	if err != nil {
		return ProviderAggregate{}, err
	}
	result.Coverage = coverage
	return result, nil
}

func validProviderAggregate(value ProviderAggregate) bool {
	if value.ExpectedCount == 0 || value.ExpectedCount > MaximumDailyExpectedSlots ||
		value.TransportSuccessCount > value.ExpectedCount ||
		value.ContractValidCount > value.TransportSuccessCount ||
		value.SnapshotCompleteCount > value.ContractValidCount ||
		value.PromotionAppliedCount > value.SnapshotCompleteCount ||
		value.PromotionSkippedCount > value.ExpectedCount ||
		value.AbandonedCount > value.ExpectedCount || value.DegradedCount > value.ExpectedCount ||
		value.PolicyChangedCount > value.PromotionSkippedCount ||
		value.TransportSuccessCount > value.ExpectedCount-value.AbandonedCount ||
		value.PromotionAppliedCount > value.ExpectedCount-value.AbandonedCount ||
		value.PromotionSkippedCount > value.ExpectedCount-value.AbandonedCount-value.PromotionAppliedCount {
		return false
	}
	coverage, err := CalculateCoverage(value.PromotionAppliedCount, value.ExpectedCount)
	if err != nil || coverage != value.Coverage {
		return false
	}
	if value.PromotionAppliedCount == 0 {
		return value.FirstPromotionAt == nil && value.LastPromotionAt == nil
	}
	return value.FirstPromotionAt != nil && value.LastPromotionAt != nil &&
		!value.LastPromotionAt.Before(*value.FirstPromotionAt)
}

func validSkipReason(value string) bool {
	switch value {
	case "", "policy_changed", "transport_failed", "contract_invalid", "disk_fallback",
		"provider_identity_incomplete", "provider_duplicate", "stale_poll":
		return true
	default:
		return false
	}
}

func earlierTime(left, right *time.Time) *time.Time {
	if right == nil || left != nil && !right.Before(*left) {
		return left
	}
	value := right.UTC()
	return &value
}

func laterTime(left, right *time.Time) *time.Time {
	if right == nil || left != nil && !right.After(*left) {
		return left
	}
	value := right.UTC()
	return &value
}
