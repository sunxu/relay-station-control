package history

import (
	"reflect"
	"testing"
	"time"
)

func at(day UTCDay, hour, minute int) time.Time {
	start, _, _ := day.Bounds()
	return start.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func TestPlanExpectedSlotsHalfOpenAndAligned(t *testing.T) {
	day := mustDay(t, 2026, time.August, 20)
	got, err := PlanExpectedSlots(day,
		[]Interval{{Start: at(day, 0, 0).Add(-time.Hour), End: at(day, 0, 17)}},
		[]Interval{{Start: at(day, 0, 2), End: at(day, 0, 12)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{at(day, 0, 5), at(day, 0, 10)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}
}

func TestPlanExpectedSlotsMergesOverlapAndAdjacency(t *testing.T) {
	day := mustDay(t, 2026, time.August, 20)
	got, err := PlanExpectedSlots(day,
		[]Interval{
			{Start: at(day, 0, 0), End: at(day, 0, 10)},
			{Start: at(day, 0, 10), End: at(day, 0, 20)},
			{Start: at(day, 0, 5), End: at(day, 0, 15)},
		},
		[]Interval{{Start: at(day, 0, 0), End: at(day, 0, 20)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{at(day, 0, 0), at(day, 0, 5), at(day, 0, 10), at(day, 0, 15)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}
}

func TestPlanExpectedSlotsPauseAndReentry(t *testing.T) {
	day := mustDay(t, 2026, time.August, 20)
	got, err := PlanExpectedSlots(day,
		[]Interval{{Start: at(day, 0, 0), End: at(day, 0, 30)}},
		[]Interval{
			{Start: at(day, 0, 1), End: at(day, 0, 11)},
			{Start: at(day, 0, 19), End: at(day, 0, 26)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{at(day, 0, 5), at(day, 0, 10), at(day, 0, 20), at(day, 0, 25)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}
}

func TestPlanExpectedSlotsDoesNotInventShortIntervalSlot(t *testing.T) {
	day := mustDay(t, 2026, time.August, 20)
	got, err := PlanExpectedSlots(day,
		[]Interval{{Start: at(day, 0, 1), End: at(day, 0, 4)}},
		[]Interval{{Start: at(day, 0, 0), End: at(day, 1, 0)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("short interval slots = %v, want none", got)
	}
}

func TestPlanExpectedSlotsRejectsInvalidInterval(t *testing.T) {
	day := mustDay(t, 2026, time.August, 20)
	if _, err := PlanExpectedSlots(day,
		[]Interval{{Start: at(day, 0, 5), End: at(day, 0, 5)}},
		[]Interval{{Start: at(day, 0, 0), End: at(day, 1, 0)}},
	); err == nil {
		t.Fatal("invalid half-open interval was accepted")
	}
}
