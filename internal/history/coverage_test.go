package history

import (
	"errors"
	"math"
	"testing"
)

func TestCalculateCoverageBoundaries(t *testing.T) {
	for _, test := range []struct {
		name     string
		applied  uint64
		expected uint64
		basis    uint16
		status   CoverageStatus
	}{
		{name: "zero percent", applied: 0, expected: 10_000, basis: 0, status: CoveragePartial},
		{name: "94.99 percent", applied: 9_499, expected: 10_000, basis: 9_499, status: CoveragePartial},
		{name: "95 percent", applied: 19, expected: 20, basis: 9_500, status: CoverageComplete},
		{name: "100 percent", applied: 7, expected: 7, basis: 10_000, status: CoverageComplete},
		{name: "maximum counts do not overflow", applied: math.MaxUint64, expected: math.MaxUint64, basis: 10_000, status: CoverageComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := CalculateCoverage(test.applied, test.expected)
			if err != nil || got.BasisPoints != test.basis || got.Status != test.status {
				t.Fatalf("CalculateCoverage = %+v, %v; want basis=%d status=%s", got, err, test.basis, test.status)
			}
		})
	}
}

func TestCalculateCoverageRejectsZeroDenominatorAndImpossibleNumerator(t *testing.T) {
	if _, err := CalculateCoverage(0, 0); !errors.Is(err, ErrNoExpectedSlots) {
		t.Fatalf("0/0 error = %v", err)
	}
	if _, err := CalculateCoverage(1, 0); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("1/0 error = %v", err)
	}
	if _, err := CalculateCoverage(2, 1); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("2/1 error = %v", err)
	}
}
