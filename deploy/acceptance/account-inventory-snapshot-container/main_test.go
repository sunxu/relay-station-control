package main

import (
	"testing"
	"time"
)

func TestSafeSlotDelayPreservesSubsecondBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		epochSeconds float64
		want         time.Duration
	}{
		{name: "outside guard window", epochSeconds: 270, want: 0},
		{name: "inside guard window", epochSeconds: 271, want: 29*time.Second + 250*time.Millisecond},
		{name: "final half second", epochSeconds: 299.5, want: 750 * time.Millisecond},
		{name: "new period", epochSeconds: 300, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := safeSlotDelay(test.epochSeconds); got != test.want {
				t.Fatalf("safeSlotDelay(%v) = %v, want %v", test.epochSeconds, got, test.want)
			}
		})
	}
}
