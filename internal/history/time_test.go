package history

import (
	"errors"
	"testing"
	"time"
)

func mustDay(t *testing.T, year int, month time.Month, day int) UTCDay {
	t.Helper()
	result, err := NewUTCDay(year, month, day)
	if err != nil {
		t.Fatalf("NewUTCDay: %v", err)
	}
	return result
}

func TestUTCDayEligibilityBoundary(t *testing.T) {
	day := mustDay(t, 2026, time.March, 8)
	boundary := time.Date(2026, time.March, 12, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "one nanosecond early", now: boundary.Add(-time.Nanosecond), want: false},
		{name: "exactly seventy-two hours", now: boundary, want: true},
		{name: "later", now: boundary.Add(time.Hour), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := day.Eligible(test.now)
			if err != nil || got != test.want {
				t.Fatalf("Eligible(%s) = %v, %v; want %v", test.now, got, err, test.want)
			}
		})
	}
}

func TestUTCDayUsesInstantNotHostLocation(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// The local calendar date is March 7, while the UTC date is March 8. This
	// also crosses the 2026 US DST transition without changing UTC semantics.
	instant := time.Date(2026, time.March, 7, 16, 30, 0, 0, losAngeles)
	day, err := UTCDayOf(instant)
	if err != nil {
		t.Fatal(err)
	}
	if got := day.String(); got != "2026-03-08" {
		t.Fatalf("day = %s, want 2026-03-08", got)
	}
	start, end, err := day.Bounds()
	if err != nil || start.Location() != time.UTC || end.Sub(start) != 24*time.Hour {
		t.Fatalf("Bounds = %v, %v, %v", start, end, err)
	}
}

func TestSourceDaysMatchMidnight(t *testing.T) {
	tests := []struct {
		name      string
		scheduled time.Time
		observed  time.Time
		want      bool
	}{
		{
			name:      "same UTC day in different locations",
			scheduled: time.Date(2026, 8, 20, 23, 55, 0, 0, time.UTC),
			observed:  time.Date(2026, 8, 21, 7, 59, 0, 0, time.FixedZone("UTC+8", 8*60*60)),
			want:      true,
		},
		{
			name:      "observed crosses UTC midnight",
			scheduled: time.Date(2026, 8, 20, 23, 55, 0, 0, time.UTC),
			observed:  time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
			want:      false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := SourceDaysMatch(test.scheduled, test.observed)
			if err != nil || got != test.want {
				t.Fatalf("SourceDaysMatch = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestInvalidUTCDayAndTimesFailClosed(t *testing.T) {
	if _, err := NewUTCDay(2026, time.February, 30); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("invalid calendar date error = %v", err)
	}
	if _, err := (UTCDay{}).Eligible(time.Now()); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("zero day error = %v", err)
	}
	if _, err := SourceDaysMatch(time.Time{}, time.Now()); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("zero source error = %v", err)
	}
}
