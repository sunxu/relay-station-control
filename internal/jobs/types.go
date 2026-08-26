// Package jobs implements the database-independent orchestration core for
// durable Control jobs. PostgreSQL remains the source of truth: repository
// implementations must make each documented claim or transition atomically.
package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	MaxPayloadBytes = 64 * 1024
	MaxConcurrency  = 32
)

var (
	ErrNotFound          = errors.New("durable job not found")
	ErrConflict          = errors.New("durable job conflict")
	ErrLostLease         = errors.New("durable job lease lost")
	ErrInvalidTransition = errors.New("invalid durable job transition")
	ErrInvalidPayload    = errors.New("invalid durable job payload")
	ErrUnknownKind       = errors.New("unknown durable job kind")
	ErrPublisherDisabled = errors.New("publisher disabled")
	ErrShutdownTimedOut  = errors.New("durable job shutdown timed out")
)

type Status string

const (
	StatusPending     Status = "pending"
	StatusRunning     Status = "running"
	StatusVerifying   Status = "verifying"
	StatusRetryWait   Status = "retry_wait"
	StatusRollingBack Status = "rolling_back"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusRolledBack  Status = "rolled_back"
	StatusCancelled   Status = "cancelled"
)

var AllStatuses = [...]Status{
	StatusPending, StatusRunning, StatusVerifying, StatusRetryWait,
	StatusRollingBack, StatusSucceeded, StatusFailed, StatusRolledBack,
	StatusCancelled,
}

func (s Status) Valid() bool {
	for _, allowed := range AllStatuses {
		if s == allowed {
			return true
		}
	}
	return false
}

func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusRolledBack || s == StatusCancelled
}

type EventType string

const (
	EventEnqueued        EventType = "enqueued"
	EventClaimed         EventType = "claimed"
	EventRetryScheduled  EventType = "retry_scheduled"
	EventVerification    EventType = "verification_started"
	EventRollbackStarted EventType = "rollback_started"
	EventSucceeded       EventType = "succeeded"
	EventFailed          EventType = "failed"
	EventRolledBack      EventType = "rolled_back"
	EventCancelled       EventType = "cancelled"
	EventCancelRequested EventType = "cancel_requested"
)

type ActorType string

const (
	ActorService    ActorType = "service"
	ActorWorker     ActorType = "worker"
	ActorReconciler ActorType = "reconciler"
	ActorSystem     ActorType = "system"
)

type Job struct {
	ID                  uuid.UUID
	OperationID         uuid.UUID
	IdempotencyKey      string
	Kind                string
	SchemaVersion       int
	Payload             []byte
	PayloadHash         [32]byte
	Status              Status
	Priority            int
	Attempt             int
	MaxAttempts         int
	VerificationAttempt int
	MaxVerifyAttempts   int
	// The persisted execution policy is copied from the immutable kind
	// definition when a job is enqueued. Workers compare it with their local
	// registry before invoking an executor so a deployment with a mismatched
	// registry fails closed instead of silently changing durable semantics.
	Timeout           time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	ReplaySafe        bool
	AllowRollback     bool
	CancelRequested   bool
	CreatedAt         time.Time
	DeadlineAt        time.Time
}

type Lease struct {
	Job
	Owner     string
	Token     uuid.UUID
	ExpiresAt time.Time
	// DeadlineExceeded is computed by PostgreSQL at claim time. Go wall time
	// must never decide whether a persistent job deadline has elapsed.
	DeadlineExceeded bool
}

type Transition struct {
	JobID        uuid.UUID
	Token        uuid.UUID
	From         []Status
	To           Status
	Event        EventType
	Actor        ActorType
	ReasonCode   string
	ErrorCode    string
	RetryAfter   time.Duration
	ReleaseLease bool
	// Mutation confirms a future business-side effect in the exact pgx
	// transaction that commits the fenced job transition and immutable event.
	// It is permitted only for verified success or completed rollback.
	Mutation TransitionMutation
}

// DBTX matches the interface consumed by sqlc-generated Queries. A repository
// passes the transaction-scoped pgx.Tx to TransitionMutation, allowing future
// business packages to call generated.New(tx) or queries.WithTx(tx) without
// exposing transaction creation or commit to the mutation.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type TransitionMutation func(context.Context, DBTX) error

// Validate checks the transaction-bound mutation invariant. Repository
// implementations must call it before opening their transition transaction.
func (transition Transition) Validate() error {
	if transition.Mutation == nil {
		return nil
	}
	if !transition.ReleaseLease || transition.Actor != ActorReconciler || len(transition.From) != 1 {
		return ErrInvalidTransition
	}
	verifiedSuccess := transition.From[0] == StatusVerifying && transition.To == StatusSucceeded && transition.Event == EventSucceeded
	completedRollback := transition.From[0] == StatusRollingBack && transition.To == StatusRolledBack && transition.Event == EventRolledBack
	if !verifiedSuccess && !completedRollback {
		return ErrInvalidTransition
	}
	return nil
}

// Repository methods are atomic database operations. Implementations must use
// database time for runnable/expiry predicates and must fence renewals and
// transitions by the supplied token and a still-valid lease. TransitionFenced
// must open one pgx transaction, apply the fenced transition and event first,
// then call Transition.Mutation with that same transaction. Any transition,
// event, or mutation error must roll the entire transaction back.
type Repository interface {
	// ClaimRunnable must derive the initial lease expiry from the registered
	// per-kind database policy, not from a process-wide duration.
	ClaimRunnable(context.Context, ClaimRequest) (*Lease, error)
	RenewLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) error
	TransitionFenced(context.Context, Transition) error
	// ClaimRecoverable converts an expired running row to verifying while
	// claiming it, or claims a due verifying/rolling_back row whose prior lease
	// was deliberately released. It returns only verifying/rolling_back work and
	// must never return pending/retry_wait work for Execute.
	ClaimRecoverable(context.Context, ClaimRequest) (*Lease, error)

	ClaimOutbox(context.Context, OutboxClaimRequest) (*OutboxLease, error)
	RenewOutboxLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) error
	TransitionOutboxFenced(context.Context, OutboxTransition) error
}

type ClaimRequest struct {
	Owner string
	Token uuid.UUID
}

type ExecuteDisposition string

const (
	ExecuteNeedsVerification ExecuteDisposition = "needs_verification"
	ExecuteRetryableNoEffect ExecuteDisposition = "retryable_no_effect"
	ExecutePermanentFailure  ExecuteDisposition = "permanent_failure"
	ExecuteResultUnknown     ExecuteDisposition = "result_unknown"
)

type ExecuteResult struct {
	Disposition ExecuteDisposition
	ErrorCode   string
}

type VerifyDisposition string

const (
	VerifyEffectApplied VerifyDisposition = "effect_applied"
	VerifyEffectAbsent  VerifyDisposition = "effect_not_applied"
	VerifyEffectPartial VerifyDisposition = "effect_partial_or_rollback_required"
	VerifyEffectUnknown VerifyDisposition = "effect_unknown"
)

type VerifyResult struct {
	Disposition VerifyDisposition
	ErrorCode   string
	Mutation    TransitionMutation
}

type RollbackDisposition string

const (
	RollbackCompleted RollbackDisposition = "rollback_completed"
	RollbackRetryable RollbackDisposition = "rollback_retryable"
	RollbackFailed    RollbackDisposition = "rollback_failed"
	RollbackUnknown   RollbackDisposition = "rollback_unknown"
)

type RollbackResult struct {
	Disposition RollbackDisposition
	ErrorCode   string
	Mutation    TransitionMutation
}

type Execution struct {
	JobID        uuid.UUID
	OperationID  uuid.UUID
	Kind         string
	Payload      []byte
	Attempt      int
	FencingToken uuid.UUID
}

type Executor interface {
	Execute(context.Context, Execution) ExecuteResult
	Verify(context.Context, Execution) VerifyResult
	Rollback(context.Context, Execution) RollbackResult
}

type WakeEnvelope struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       uuid.UUID `json:"event_id"`
	JobID         uuid.UUID `json:"job_id"`
	OperationID   uuid.UUID `json:"operation_id"`
	Topic         string    `json:"topic"`
}

type OutboxStatus string

const (
	OutboxPending    OutboxStatus = "pending"
	OutboxPublishing OutboxStatus = "publishing"
	OutboxSent       OutboxStatus = "sent"
	OutboxSuppressed OutboxStatus = "suppressed"
	OutboxRetryWait  OutboxStatus = "retry_wait"
	OutboxFailed     OutboxStatus = "failed"
)

type OutboxLease struct {
	Envelope    WakeEnvelope
	Status      OutboxStatus
	Attempt     int
	MaxAttempts int
	Token       uuid.UUID
	ExpiresAt   time.Time
}

type OutboxClaimRequest struct {
	Owner         string
	Token         uuid.UUID
	LeaseDuration time.Duration
}

type OutboxTransition struct {
	EventID      uuid.UUID
	Token        uuid.UUID
	To           OutboxStatus
	ErrorCode    string
	RetryAfter   time.Duration
	ReleaseLease bool
}
