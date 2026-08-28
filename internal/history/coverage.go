package history

import (
	"errors"
	"math/bits"
)

const (
	CoverageScaleBasisPoints     = uint64(10_000)
	CoverageThresholdBasisPoints = uint16(9_500)
)

var (
	ErrNoExpectedSlots = errors.New("history: coverage has no expected slots")
	ErrInvalidCount    = errors.New("history: invalid count")
	ErrCountOverflow   = errors.New("history: count overflow")
)

type CoverageStatus string

const (
	CoverageComplete CoverageStatus = "complete"
	CoveragePartial  CoverageStatus = "partial"
)

// Coverage is the exact persisted coverage projection. BasisPoints is floor of
// Applied*10000/Expected; expected zero is rejected so callers cannot publish a
// synthetic 0/0 rollup.
type Coverage struct {
	Applied     uint64
	Expected    uint64
	BasisPoints uint16
	Status      CoverageStatus
}

func CalculateCoverage(applied, expected uint64) (Coverage, error) {
	if expected == 0 {
		if applied != 0 {
			return Coverage{}, ErrInvalidCount
		}
		return Coverage{}, ErrNoExpectedSlots
	}
	if applied > expected {
		return Coverage{}, ErrInvalidCount
	}
	high, low := bits.Mul64(applied, CoverageScaleBasisPoints)
	quotient, _ := bits.Div64(high, low, expected)
	result := Coverage{
		Applied:     applied,
		Expected:    expected,
		BasisPoints: uint16(quotient),
		Status:      CoveragePartial,
	}
	if result.BasisPoints >= CoverageThresholdBasisPoints {
		result.Status = CoverageComplete
	}
	return result, nil
}
