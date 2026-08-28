// Package history contains the deterministic, database-independent parts of
// account-inventory history compaction. PostgreSQL remains authoritative for
// the current time, activation history, persistence, locking, and fencing.
package history

import (
	"errors"
	"fmt"
	"time"
)

const (
	EligibilityDelay = 72 * time.Hour
	SlotInterval     = 5 * time.Minute
)

var (
	ErrInvalidTime     = errors.New("history: invalid time")
	ErrInvalidInterval = errors.New("history: invalid half-open interval")
)

// UTCDay identifies one natural UTC day. Its zero value is invalid.
type UTCDay struct {
	start time.Time
}

// UTCDayOf returns the natural UTC day containing timestamp.
func UTCDayOf(timestamp time.Time) (UTCDay, error) {
	if timestamp.IsZero() {
		return UTCDay{}, ErrInvalidTime
	}
	timestamp = timestamp.UTC()
	return UTCDay{start: time.Date(timestamp.Year(), timestamp.Month(), timestamp.Day(), 0, 0, 0, 0, time.UTC)}, nil
}

// NewUTCDay constructs a UTC day without accepting time.Date's normalization
// of invalid calendar dates.
func NewUTCDay(year int, month time.Month, day int) (UTCDay, error) {
	if year < 1 || year > 9999 {
		return UTCDay{}, ErrInvalidTime
	}
	start := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if start.Year() != year || start.Month() != month || start.Day() != day {
		return UTCDay{}, ErrInvalidTime
	}
	return UTCDay{start: start}, nil
}

func (day UTCDay) valid() bool {
	return !day.start.IsZero() && day.start.Location() == time.UTC &&
		day.start.Hour() == 0 && day.start.Minute() == 0 &&
		day.start.Second() == 0 && day.start.Nanosecond() == 0
}

// Bounds returns the half-open [start, end) UTC-day boundary.
func (day UTCDay) Bounds() (time.Time, time.Time, error) {
	if !day.valid() {
		return time.Time{}, time.Time{}, ErrInvalidTime
	}
	return day.start, day.start.AddDate(0, 0, 1), nil
}

func (day UTCDay) String() string {
	if !day.valid() {
		return "invalid"
	}
	return day.start.Format(time.DateOnly)
}

// Eligible applies the fixed day-end plus 72-hour boundary to a PostgreSQL
// clock value supplied by the caller. Equality is eligible.
func (day UTCDay) Eligible(databaseNow time.Time) (bool, error) {
	if databaseNow.IsZero() {
		return false, ErrInvalidTime
	}
	_, end, err := day.Bounds()
	if err != nil {
		return false, err
	}
	return !end.After(databaseNow.UTC().Add(-EligibilityDelay)), nil
}

// Contains reports whether timestamp belongs to this natural UTC day.
func (day UTCDay) Contains(timestamp time.Time) (bool, error) {
	if timestamp.IsZero() {
		return false, ErrInvalidTime
	}
	start, end, err := day.Bounds()
	if err != nil {
		return false, err
	}
	timestamp = timestamp.UTC()
	return !timestamp.Before(start) && timestamp.Before(end), nil
}

// SourceDaysMatch enforces the fail-closed rule that a snapshot's observed
// UTC date and its poll's scheduled UTC date are identical.
func SourceDaysMatch(scheduledAt, observedAt time.Time) (bool, error) {
	scheduledDay, err := UTCDayOf(scheduledAt)
	if err != nil {
		return false, fmt.Errorf("scheduled at: %w", err)
	}
	observedDay, err := UTCDayOf(observedAt)
	if err != nil {
		return false, fmt.Errorf("observed at: %w", err)
	}
	return scheduledDay.start.Equal(observedDay.start), nil
}
