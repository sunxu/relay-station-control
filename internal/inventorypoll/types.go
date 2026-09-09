// Package inventorypoll implements the database-independent orchestration core
// for account-inventory poll runs. PostgreSQL remains the source of truth for
// UTC slots, eligibility, claims, leases, fencing, and terminal evidence.
package inventorypoll

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

var ErrCapacityExceeded = errors.New("account inventory poll capacity exceeded")

var (
	ErrInvalidConfig           = errors.New("account inventory poll: invalid configuration")
	ErrNoWork                  = errors.New("account inventory poll: no work")
	ErrLostLease               = errors.New("account inventory poll: lease lost")
	ErrInvalidRepositoryResult = errors.New("account inventory poll: invalid repository result")
	ErrInvalidObservation      = errors.New("account inventory poll: invalid driver observation")
	ErrShutdownTimedOut        = errors.New("account inventory poll: shutdown timed out")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusRetryWait Status = "retry_wait"
	StatusFinalized Status = "finalized"
	StatusAbandoned Status = "abandoned"
)

var AllStatuses = [...]Status{
	StatusPending, StatusRunning, StatusRetryWait, StatusFinalized, StatusAbandoned,
}

func (status Status) Valid() bool {
	for _, allowed := range AllStatuses {
		if status == allowed {
			return true
		}
	}
	return false
}

func (status Status) Terminal() bool {
	return status == StatusFinalized || status == StatusAbandoned
}

func CanTransition(from, to Status) bool {
	switch from {
	case StatusPending:
		return to == StatusRunning || to == StatusAbandoned
	case StatusRunning:
		return to == StatusFinalized || to == StatusRetryWait || to == StatusAbandoned
	case StatusRetryWait:
		return to == StatusRunning || to == StatusAbandoned
	default:
		return false
	}
}

type ControlReason string

const (
	ControlReasonNone                ControlReason = "none"
	ControlReasonDatabaseUnavailable ControlReason = "database_unavailable"
	ControlReasonCapacityExceeded    ControlReason = "capacity_exceeded"
	ControlReasonInvalidClaim        ControlReason = "invalid_claim"
	ControlReasonGraceExhausted      ControlReason = "grace_exhausted"
	ControlReasonLostLease           ControlReason = "lost_lease"
	ControlReasonAttemptsExhausted   ControlReason = "attempts_exhausted"
	ControlReasonShutdown            ControlReason = "shutdown"
)

func (reason ControlReason) Valid() bool {
	switch reason {
	case ControlReasonNone, ControlReasonDatabaseUnavailable, ControlReasonCapacityExceeded, ControlReasonInvalidClaim,
		ControlReasonGraceExhausted, ControlReasonLostLease,
		ControlReasonAttemptsExhausted, ControlReasonShutdown:
		return true
	default:
		return false
	}
}

type EventComponent string
type EventAction string
type EventResult string
type AttemptBucket string

const (
	EventComponentScheduler  EventComponent = "poll_scheduler"
	EventComponentWorker     EventComponent = "poll_worker"
	EventComponentReconciler EventComponent = "poll_reconciler"

	EventActionSchedule  EventAction = "schedule"
	EventActionClaim     EventAction = "claim"
	EventActionDispatch  EventAction = "dispatch"
	EventActionFinalize  EventAction = "finalize"
	EventActionReconcile EventAction = "reconcile"
	EventActionShutdown  EventAction = "shutdown"

	EventResultSuccess EventResult = "success"
	EventResultFailure EventResult = "failure"
	EventResultSkipped EventResult = "skipped"

	AttemptNone      AttemptBucket = ""
	AttemptFirst     AttemptBucket = "first"
	AttemptRetry     AttemptBucket = "retry"
	AttemptExhausted AttemptBucket = "exhausted"
)

// Event is a closed aggregate projection. It has no poll ID, policy version,
// endpoint, provider/account, Secret, response, version or raw error field.
type Event struct {
	Component     EventComponent
	Action        EventAction
	Result        EventResult
	Reason        ControlReason
	State         Status
	AttemptBucket AttemptBucket
	InstanceID    uuid.UUID
}

type Observer interface {
	Observe(context.Context, Event)
}

type discardObserver struct{}

func (discardObserver) Observe(context.Context, Event) {}

// Repository methods are atomic database operations. Implementations must use
// PostgreSQL clock_timestamp() for every persistent time predicate and value.
// ScheduleCurrent alone computes the current UTC slot; Go never supplies one.
type Repository interface {
	ScheduleCurrent(context.Context, ScheduleRequest) (ScheduleResult, error)
	ClaimRunnable(context.Context, ClaimRequest) (*ClaimedRun, error)
	FinalizeFenced(context.Context, FinalizeRequest) error
	ReconcileExpired(context.Context, ReconcileRequest) (ReconcileResult, error)
}

// ScheduleRequest contains policy durations only. It deliberately has no time,
// slot, instance, endpoint, or policy-version input.
type ScheduleRequest struct {
	Period         time.Duration
	PollStartGrace time.Duration
	MaxAttempts    int
	Limit          int
}

type ScheduleResult struct {
	ScheduledAt time.Time
	Eligible    int
	Created     int
	Existing    int
}

type ClaimRequest struct {
	Token         uuid.UUID
	LeaseDuration time.Duration
}

// ClaimedRun is returned only after the short claim transaction has committed.
// GraceRemaining is computed by PostgreSQL from scheduled_at and db_now.
type ClaimedRun struct {
	PollRunID       uuid.UUID
	InstanceID      uuid.UUID
	PolicyVersionID uuid.UUID
	ScheduledAt     time.Time
	Attempt         int
	MaxAttempts     int
	FencingToken    uuid.UUID
	GraceRemaining  time.Duration
	Target          drivers.NodeTarget
	ProviderPolicy  drivers.ProviderPolicySnapshot
}

type FinalizeRequest struct {
	PollRunID     uuid.UUID
	FencingToken  uuid.UUID
	Node          NodeEvidence
	Providers     []ProviderEvidence
	SnapshotItems []SnapshotCandidate
	Duplicates    []DuplicateEvidence
}

// Format prevents the account-bearing finalize payload from being projected
// through any fmt/log verb. Repository implementations must select only the
// allowlisted fields when constructing database parameters.
func (FinalizeRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED FinalizeRequest]"))
}

// SnapshotCandidate is the complete account-level persistence allowlist. Email
// and AccountKey are sensitive identity data and may only enter the protected
// snapshot columns. A nil source time represents SQL NULL; observed/promotion
// times are supplied exclusively by PostgreSQL.
type SnapshotCandidate struct {
	Provider                    string
	AccountKey                  string
	Email                       string
	BasicStatus                 drivers.AccountState
	SuccessCount                uint64
	FailedCount                 uint64
	RecentRequestCount          uint64
	LastRefreshUnix             *int64
	NextRetryUnix               *int64
	UpdatedAtUnix               *int64
	AvailabilityRuntimeEvidence *string
	AuthFailureReason           *string
}

func (SnapshotCandidate) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED SnapshotCandidate]"))
}

// DuplicateEvidence contains only the minimum identity needed for protected
// duplicate evidence. It deliberately excludes conflicting account state,
// counters, source times and raw records.
type DuplicateEvidence struct {
	Provider        string
	AccountKey      string
	OccurrenceCount uint32
}

func (DuplicateEvidence) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED DuplicateEvidence]"))
}

// NodeEvidence is the complete persistence allowlist. It cannot carry account
// observations, email, endpoint, Secret material, headers, body, or raw errors.
type NodeEvidence struct {
	TransportSuccess         bool
	ResponseShapeValid       bool
	ContractValid            bool
	InventoryMode            drivers.InventoryMode
	NodeIdentityComplete     bool
	SnapshotComplete         bool
	Degraded                 bool
	RecognizedRecordCount    uint32
	UnidentifiedRecordCount  uint32
	UnsupportedProviderCount uint32
	OutOfScopeProviderCount  uint32
	Result                   drivers.Result
	Reason                   drivers.Reason
	Version                  string
	Commit                   string
}

type ProviderReason string

const (
	ProviderReasonComplete               ProviderReason = "complete"
	ProviderReasonTransportFailed        ProviderReason = "transport_failed"
	ProviderReasonContractInvalid        ProviderReason = "contract_invalid"
	ProviderReasonDiskFallback           ProviderReason = "disk_fallback"
	ProviderReasonNodeIdentityIncomplete ProviderReason = "node_identity_incomplete"
	ProviderReasonIdentityIncomplete     ProviderReason = "identity_incomplete"
)

func (reason ProviderReason) Valid() bool {
	switch reason {
	case ProviderReasonComplete, ProviderReasonTransportFailed, ProviderReasonContractInvalid,
		ProviderReasonDiskFallback, ProviderReasonNodeIdentityIncomplete, ProviderReasonIdentityIncomplete:
		return true
	default:
		return false
	}
}

type ProviderEvidence struct {
	Provider               string
	RecognizedRecordCount  uint32
	MissingIdentityCount   uint32
	DuplicateIdentityCount uint32
	IdentityComplete       bool
	SnapshotComplete       bool
	Degraded               bool
	Reason                 ProviderReason
}

type ReconcileRequest struct {
	PollStartGrace time.Duration
	Limit          int
}

type ReconcileResult struct {
	RetryWait int
	Abandoned int
}

// DriverInvoker is deliberately the existing fixed account-inventory method.
// It cannot express Probe, an arbitrary HTTP method/path, or a request body.
type DriverInvoker interface {
	ListAccountInventory(context.Context, drivers.InventoryRequest) (drivers.InventoryObservation, error)
}
