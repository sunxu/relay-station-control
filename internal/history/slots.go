package history

import (
	"fmt"
	"sort"
	"time"
)

// Interval is a half-open [Start, End) activation interval. Times may carry
// any location; planning compares their instants and emits UTC slots.
type Interval struct {
	Start time.Time
	End   time.Time
}

func normalizeIntervals(intervals []Interval, label string) ([]Interval, error) {
	result := make([]Interval, 0, len(intervals))
	for index, interval := range intervals {
		if interval.Start.IsZero() || interval.End.IsZero() || !interval.Start.Before(interval.End) {
			return nil, fmt.Errorf("%w: %s[%d]", ErrInvalidInterval, label, index)
		}
		result = append(result, Interval{Start: interval.Start.UTC(), End: interval.End.UTC()})
	}
	return result, nil
}

// PlanExpectedSlots intersects policy and monitoring activation history with a
// UTC day, merges overlapping or adjacent intersections, and enumerates the
// actual five-minute grid points inside each half-open intersection.
func PlanExpectedSlots(day UTCDay, policy, monitoring []Interval) ([]time.Time, error) {
	dayStart, dayEnd, err := day.Bounds()
	if err != nil {
		return nil, err
	}
	policy, err = normalizeIntervals(policy, "policy")
	if err != nil {
		return nil, err
	}
	monitoring, err = normalizeIntervals(monitoring, "monitoring")
	if err != nil {
		return nil, err
	}

	intersections := make([]Interval, 0)
	for _, policyInterval := range policy {
		for _, monitoringInterval := range monitoring {
			start := maximumTime(dayStart, policyInterval.Start, monitoringInterval.Start)
			end := minimumTime(dayEnd, policyInterval.End, monitoringInterval.End)
			if start.Before(end) {
				intersections = append(intersections, Interval{Start: start, End: end})
			}
		}
	}
	if len(intersections) == 0 {
		return []time.Time{}, nil
	}

	sort.Slice(intersections, func(i, j int) bool {
		if intersections[i].Start.Equal(intersections[j].Start) {
			return intersections[i].End.Before(intersections[j].End)
		}
		return intersections[i].Start.Before(intersections[j].Start)
	})
	merged := intersections[:1]
	for _, interval := range intersections[1:] {
		last := &merged[len(merged)-1]
		if !interval.Start.After(last.End) {
			if interval.End.After(last.End) {
				last.End = interval.End
			}
			continue
		}
		merged = append(merged, interval)
	}

	slots := make([]time.Time, 0)
	for _, interval := range merged {
		for slot := alignSlotCeiling(interval.Start); slot.Before(interval.End); slot = slot.Add(SlotInterval) {
			slots = append(slots, slot)
		}
	}
	return slots, nil
}

func alignSlotCeiling(value time.Time) time.Time {
	value = value.UTC()
	aligned := value.Truncate(SlotInterval)
	if aligned.Before(value) {
		aligned = aligned.Add(SlotInterval)
	}
	return aligned
}

func maximumTime(values ...time.Time) time.Time {
	result := values[0]
	for _, value := range values[1:] {
		if value.After(result) {
			result = value
		}
	}
	return result
}

func minimumTime(values ...time.Time) time.Time {
	result := values[0]
	for _, value := range values[1:] {
		if value.Before(result) {
			result = value
		}
	}
	return result
}
