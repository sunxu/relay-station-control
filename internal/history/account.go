package history

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

var ErrNoSamples = errors.New("history: no account samples")

type BasicStatus string

const (
	BasicStatusDisabled    BasicStatus = "disabled"
	BasicStatusUnavailable BasicStatus = "unavailable"
	BasicStatusError       BasicStatus = "error"
	BasicStatusActive      BasicStatus = "active"
	BasicStatusUnknown     BasicStatus = "unknown"
)

func (status BasicStatus) valid() bool {
	switch status {
	case BasicStatusDisabled, BasicStatusUnavailable, BasicStatusError, BasicStatusActive, BasicStatusUnknown:
		return true
	default:
		return false
	}
}

type StatusCounts struct {
	Disabled    uint64
	Unavailable uint64
	Error       uint64
	Active      uint64
	Unknown     uint64
}

func (counts *StatusCounts) add(status BasicStatus) error {
	var target *uint64
	switch status {
	case BasicStatusDisabled:
		target = &counts.Disabled
	case BasicStatusUnavailable:
		target = &counts.Unavailable
	case BasicStatusError:
		target = &counts.Error
	case BasicStatusActive:
		target = &counts.Active
	case BasicStatusUnknown:
		target = &counts.Unknown
	default:
		return ErrInvalidCount
	}
	return addUint64(target, 1)
}

func (counts *StatusCounts) addCounts(other StatusCounts) error {
	for _, pair := range [][2]*uint64{
		{&counts.Disabled, &other.Disabled}, {&counts.Unavailable, &other.Unavailable},
		{&counts.Error, &other.Error}, {&counts.Active, &other.Active}, {&counts.Unknown, &other.Unknown},
	} {
		if err := addUint64(pair[0], *pair[1]); err != nil {
			return err
		}
	}
	return nil
}

type AccountSample struct {
	ScheduledAt time.Time
	ObservedAt  time.Time
	TieKey      string
	BasicStatus BasicStatus
	Success     uint64
	Failed      uint64
}

// AccountAggregate is shared by immutable policy segments and final daily
// account rollups. First/last counters are observations, not inferred usage.
type AccountAggregate struct {
	FirstScheduledAt time.Time
	LastScheduledAt  time.Time
	FirstObservedAt  time.Time
	LastObservedAt   time.Time
	LastBasicStatus  BasicStatus
	SampleCount      uint64
	StatusCounts     StatusCounts
	FirstSuccess     uint64
	LastSuccess      uint64
	SuccessResets    uint64
	FirstFailed      uint64
	LastFailed       uint64
	FailedResets     uint64
}

func AggregateAccountSegment(samples []AccountSample) (AccountAggregate, error) {
	if len(samples) == 0 {
		return AccountAggregate{}, ErrNoSamples
	}
	ordered := append([]AccountSample(nil), samples...)
	for index, sample := range ordered {
		if sample.ScheduledAt.IsZero() || sample.ObservedAt.IsZero() || sample.TieKey == "" || !sample.BasicStatus.valid() ||
			sample.Success > math.MaxInt64 || sample.Failed > math.MaxInt64 {
			return AccountAggregate{}, fmt.Errorf("account sample %d: %w", index, ErrInvalidCount)
		}
		ordered[index].ScheduledAt = sample.ScheduledAt.UTC()
		ordered[index].ObservedAt = sample.ObservedAt.UTC()
	}
	sort.Slice(ordered, func(i, j int) bool { return accountSampleLess(ordered[i], ordered[j]) })
	for index := 1; index < len(ordered); index++ {
		if !accountSampleLess(ordered[index-1], ordered[index]) {
			return AccountAggregate{}, fmt.Errorf("account sample %d duplicate order key: %w", index, ErrInvalidCount)
		}
	}

	first, last := ordered[0], ordered[len(ordered)-1]
	result := AccountAggregate{
		FirstScheduledAt: first.ScheduledAt,
		LastScheduledAt:  last.ScheduledAt,
		FirstObservedAt:  first.ObservedAt,
		LastObservedAt:   first.ObservedAt,
		LastBasicStatus:  last.BasicStatus,
		FirstSuccess:     first.Success,
		LastSuccess:      last.Success,
		FirstFailed:      first.Failed,
		LastFailed:       last.Failed,
	}
	for index, sample := range ordered {
		if sample.ObservedAt.Before(result.FirstObservedAt) {
			result.FirstObservedAt = sample.ObservedAt
		}
		if sample.ObservedAt.After(result.LastObservedAt) {
			result.LastObservedAt = sample.ObservedAt
		}
		if err := addUint64(&result.SampleCount, 1); err != nil {
			return AccountAggregate{}, err
		}
		if err := result.StatusCounts.add(sample.BasicStatus); err != nil {
			return AccountAggregate{}, err
		}
		if index > 0 {
			if sample.Success < ordered[index-1].Success {
				result.SuccessResets++
			}
			if sample.Failed < ordered[index-1].Failed {
				result.FailedResets++
			}
		}
	}
	return result, nil
}

type AccountSegment struct {
	PolicyVersion uuid.UUID
	AccountAggregate
}

// RollupAccountSegments sums immutable segment counts and adds reset events at
// deterministic segment boundaries. Boundary resets use chronological first
// and last scheduled time plus policy UUID; last values independently use the
// latest last_scheduled_at plus policy UUID tie-break.
func RollupAccountSegments(segments []AccountSegment) (AccountAggregate, error) {
	if len(segments) == 0 {
		return AccountAggregate{}, ErrNoSamples
	}
	ordered := append([]AccountSegment(nil), segments...)
	seenPolicies := make(map[uuid.UUID]struct{}, len(ordered))
	for index, segment := range ordered {
		if segment.PolicyVersion == uuid.Nil || !validAccountAggregate(segment.AccountAggregate) {
			return AccountAggregate{}, fmt.Errorf("account segment %d: %w", index, ErrInvalidCount)
		}
		if _, duplicate := seenPolicies[segment.PolicyVersion]; duplicate {
			return AccountAggregate{}, fmt.Errorf("account segment %d duplicate policy: %w", index, ErrInvalidCount)
		}
		seenPolicies[segment.PolicyVersion] = struct{}{}
		ordered[index].FirstScheduledAt = segment.FirstScheduledAt.UTC()
		ordered[index].LastScheduledAt = segment.LastScheduledAt.UTC()
		ordered[index].FirstObservedAt = segment.FirstObservedAt.UTC()
		ordered[index].LastObservedAt = segment.LastObservedAt.UTC()
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].FirstScheduledAt.Equal(ordered[j].FirstScheduledAt) {
			return ordered[i].FirstScheduledAt.Before(ordered[j].FirstScheduledAt)
		}
		if !ordered[i].LastScheduledAt.Equal(ordered[j].LastScheduledAt) {
			return ordered[i].LastScheduledAt.Before(ordered[j].LastScheduledAt)
		}
		return uuidLess(ordered[i].PolicyVersion, ordered[j].PolicyVersion)
	})
	first, last := ordered[0], ordered[0]
	for _, candidate := range ordered[1:] {
		if candidate.LastScheduledAt.After(last.LastScheduledAt) ||
			candidate.LastScheduledAt.Equal(last.LastScheduledAt) &&
				uuidLess(last.PolicyVersion, candidate.PolicyVersion) {
			last = candidate
		}
	}
	result := AccountAggregate{
		FirstScheduledAt: first.FirstScheduledAt,
		LastScheduledAt:  last.LastScheduledAt,
		FirstObservedAt:  first.FirstObservedAt,
		LastObservedAt:   first.LastObservedAt,
		LastBasicStatus:  last.LastBasicStatus,
		FirstSuccess:     first.FirstSuccess,
		LastSuccess:      last.LastSuccess,
		FirstFailed:      first.FirstFailed,
		LastFailed:       last.LastFailed,
	}
	for index, segment := range ordered {
		if segment.FirstObservedAt.Before(result.FirstObservedAt) {
			result.FirstObservedAt = segment.FirstObservedAt
		}
		if segment.LastObservedAt.After(result.LastObservedAt) {
			result.LastObservedAt = segment.LastObservedAt
		}
		for _, pair := range []struct {
			destination *uint64
			value       uint64
		}{
			{&result.SampleCount, segment.SampleCount},
			{&result.SuccessResets, segment.SuccessResets},
			{&result.FailedResets, segment.FailedResets},
		} {
			if err := addUint64(pair.destination, pair.value); err != nil {
				return AccountAggregate{}, err
			}
		}
		if err := result.StatusCounts.addCounts(segment.StatusCounts); err != nil {
			return AccountAggregate{}, err
		}
		if index > 0 {
			if segment.FirstSuccess < ordered[index-1].LastSuccess {
				if err := addUint64(&result.SuccessResets, 1); err != nil {
					return AccountAggregate{}, err
				}
			}
			if segment.FirstFailed < ordered[index-1].LastFailed {
				if err := addUint64(&result.FailedResets, 1); err != nil {
					return AccountAggregate{}, err
				}
			}
		}
	}
	return result, nil
}

func validAccountAggregate(value AccountAggregate) bool {
	statusTotal, statusErr := value.StatusCounts.total()
	return value.SampleCount > 0 && value.LastBasicStatus.valid() &&
		!value.FirstScheduledAt.IsZero() && !value.LastScheduledAt.Before(value.FirstScheduledAt) &&
		!value.FirstObservedAt.IsZero() && !value.LastObservedAt.Before(value.FirstObservedAt) &&
		statusErr == nil && statusTotal == value.SampleCount &&
		value.SuccessResets < value.SampleCount && value.FailedResets < value.SampleCount &&
		value.FirstSuccess <= math.MaxInt64 && value.LastSuccess <= math.MaxInt64 &&
		value.FirstFailed <= math.MaxInt64 && value.LastFailed <= math.MaxInt64
}

func uuidLess(left, right uuid.UUID) bool {
	return bytes.Compare(left[:], right[:]) < 0
}

func (counts StatusCounts) total() (uint64, error) {
	var result uint64
	for _, value := range []uint64{counts.Disabled, counts.Unavailable, counts.Error, counts.Active, counts.Unknown} {
		if err := addUint64(&result, value); err != nil {
			return 0, err
		}
	}
	return result, nil
}

func accountSampleLess(left, right AccountSample) bool {
	if !left.ScheduledAt.Equal(right.ScheduledAt) {
		return left.ScheduledAt.Before(right.ScheduledAt)
	}
	if !left.ObservedAt.Equal(right.ObservedAt) {
		return left.ObservedAt.Before(right.ObservedAt)
	}
	return left.TieKey < right.TieKey
}

func addUint64(destination *uint64, value uint64) error {
	if value > math.MaxUint64-*destination {
		return ErrCountOverflow
	}
	*destination += value
	return nil
}
