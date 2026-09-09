// Package requestquality collects CLIProxy HTTP usage events without changing Node behavior.
package requestquality

import (
	"context"
	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"time"
)

// Event is the entire persisted projection. A nil AccountKey is unresolved.
type Event struct {
	EventHash         string
	RequestID         string
	NodeID            uuid.UUID
	Provider          string
	AccountKey        *string
	Model             string
	OccurredAt        time.Time
	DurationMS        *int64
	Success           bool
	FailureClass      *string
	AuthFailureReason *string
}

// Identity is current auth-files evidence, not an historical assignment.
type Identity struct{ AuthIndex, Provider, Email string }

// Source is deliberately limited to the existing management read and the destructive HTTP queue.
type Source interface {
	CurrentIdentities(context.Context, drivers.NodeTarget) ([]Identity, error)
	PopUsage(context.Context, drivers.NodeTarget) ([][]byte, error)
}

type Store interface {
	InsertRequestEvents(context.Context, []Event) error
	DeleteOldRequestEvents(context.Context) (int64, error)
}

// Quality uses nil rates/latency/timestamps for unknown (no observations).
type Quality struct {
	RequestCount           int64
	SuccessCount           int64
	FailureCount           int64
	UnresolvedRequestCount int64
	SuccessRate            *float64
	P95LatencyMS           *float64
	LastSuccessAt          *time.Time
	LastFailureAt          *time.Time
	LastFailureClass       *string
}
