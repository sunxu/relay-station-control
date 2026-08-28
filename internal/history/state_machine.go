package history

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	MaximumStateMachineAttempts = uint64(1_000_000)
	HistoryRowSchemaVersionV1   = uint16(1)
	MinimumClaimLease           = 5 * time.Second
	MaximumClaimLease           = 5 * time.Minute
)

var (
	ErrInvalidStateTransition = errors.New("history: invalid state transition")
	ErrLeaseHeld              = errors.New("history: lease is held")
	ErrLeaseExpired           = errors.New("history: lease expired")
	ErrStaleFencing           = errors.New("history: stale fencing token")
	ErrCompletedImmutable     = errors.New("history: completed state is immutable")
	ErrInvalidState           = errors.New("history: invalid state")
)

type CompactionStatus string

const (
	CompactionPending    CompactionStatus = "pending"
	CompactionSummarized CompactionStatus = "summarized"
	CompactionDeleting   CompactionStatus = "deleting"
	CompactionCompleted  CompactionStatus = "completed"
	CompactionFailed     CompactionStatus = "failed"
)

var AllCompactionStatuses = [...]CompactionStatus{
	CompactionPending,
	CompactionSummarized,
	CompactionDeleting,
	CompactionCompleted,
	CompactionFailed,
}

type ResumeAction string

const (
	ResumeSummarize ResumeAction = "summarize"
	ResumeDelete    ResumeAction = "delete"
	ResumeRollup    ResumeAction = "rollup"
	ResumeDone      ResumeAction = "done"
	ResumeBlocked   ResumeAction = "blocked"
)

type CompactionState struct {
	Status           CompactionStatus
	FailedFrom       CompactionStatus
	FailureReason    string
	LeaseOwner       string
	LeaseExpiresAt   time.Time
	FencingToken     uuid.UUID
	Attempt          uint64
	SummaryCommitted bool
	SourceRows       uint64
	DeletedRows      uint64
}

func NewCompactionState() CompactionState {
	return CompactionState{Status: CompactionPending}
}

func CanCompactionTransition(from, to CompactionStatus) bool {
	switch from {
	case CompactionPending:
		return to == CompactionSummarized || to == CompactionFailed
	case CompactionSummarized:
		return to == CompactionDeleting || to == CompactionFailed
	case CompactionDeleting:
		return to == CompactionDeleting || to == CompactionCompleted || to == CompactionFailed
	default:
		return false
	}
}

func (state CompactionState) ResumeAction() (ResumeAction, error) {
	if err := state.validate(); err != nil {
		return ResumeBlocked, err
	}
	switch state.Status {
	case CompactionPending:
		return ResumeSummarize, nil
	case CompactionSummarized, CompactionDeleting:
		return ResumeDelete, nil
	case CompactionCompleted:
		return ResumeDone, nil
	case CompactionFailed:
		switch state.FailedFrom {
		case CompactionPending:
			return ResumeSummarize, nil
		case CompactionSummarized, CompactionDeleting:
			return ResumeDelete, nil
		}
	}
	return ResumeBlocked, ErrInvalidState
}

// Claim installs a new fencing generation. An expired generation can be
// replaced directly; its old token immediately becomes stale. A failed run is
// first restored to failed_from, which is the only source of its resume phase.
func (state CompactionState) Claim(now time.Time, owner string, token uuid.UUID, duration time.Duration) (CompactionState, error) {
	before := state
	if err := state.validate(); err != nil {
		return before, err
	}
	if state.Status == CompactionCompleted {
		return before, ErrCompletedImmutable
	}
	if now.IsZero() || owner == "" || len(owner) > 128 || token == uuid.Nil ||
		duration < MinimumClaimLease || duration > MaximumClaimLease || state.Attempt >= MaximumStateMachineAttempts {
		return before, ErrInvalidState
	}
	if state.hasLease() && state.LeaseExpiresAt.After(now.UTC()) {
		return before, ErrLeaseHeld
	}
	if state.Status == CompactionFailed {
		state.Status = state.FailedFrom
		state.FailedFrom = ""
		state.FailureReason = ""
	}
	state.LeaseOwner = owner
	state.LeaseExpiresAt = now.UTC().Add(duration)
	state.FencingToken = token
	state.Attempt++
	return state, nil
}

// Summarize is deliberately available only before any summary has committed.
// A summarized/deleting recovery can therefore never reaggregate residual rows.
func (state CompactionState) Summarize(now time.Time, token uuid.UUID, sourceRows uint64) (CompactionState, error) {
	before := state
	if state.Status == CompactionCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if !CanCompactionTransition(state.Status, CompactionSummarized) {
		return before, transitionError(state.Status, CompactionSummarized)
	}
	state.Status = CompactionSummarized
	state.SummaryCommitted = true
	state.SourceRows = sourceRows
	state.DeletedRows = 0
	return state, nil
}

func (state CompactionState) RenewLease(now time.Time, token uuid.UUID, duration time.Duration) (CompactionState, error) {
	before := state
	if state.Status == CompactionCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if state.Status != CompactionPending && state.Status != CompactionSummarized && state.Status != CompactionDeleting ||
		duration < MinimumClaimLease || duration > MaximumClaimLease {
		return before, ErrInvalidStateTransition
	}
	state.LeaseExpiresAt = now.UTC().Add(duration)
	return state, nil
}

func (state CompactionState) BeginDeleting(now time.Time, token uuid.UUID) (CompactionState, error) {
	before := state
	if state.Status == CompactionCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if !CanCompactionTransition(state.Status, CompactionDeleting) {
		return before, transitionError(state.Status, CompactionDeleting)
	}
	state.Status = CompactionDeleting
	return state, nil
}

// RecordDeleted models DELETE RETURNING conservation. It stays in deleting and
// does not allow the actual count to exceed the immutable summarized source.
func (state CompactionState) RecordDeleted(now time.Time, token uuid.UUID, actual uint64) (CompactionState, error) {
	before := state
	if state.Status == CompactionCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if state.Status != CompactionDeleting || actual > state.SourceRows-state.DeletedRows {
		return before, transitionError(state.Status, CompactionDeleting)
	}
	state.DeletedRows += actual
	return state, nil
}

func (state CompactionState) Complete(now time.Time, token uuid.UUID) (CompactionState, error) {
	before := state
	if state.Status == CompactionCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if !CanCompactionTransition(state.Status, CompactionCompleted) || state.DeletedRows != state.SourceRows {
		return before, transitionError(state.Status, CompactionCompleted)
	}
	state.Status = CompactionCompleted
	state.clearLease()
	return state, nil
}

func (state CompactionState) Fail(now time.Time, token uuid.UUID, reason string) (CompactionState, error) {
	before := state
	if state.Status == CompactionCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if reason == "" || !CanCompactionTransition(state.Status, CompactionFailed) {
		return before, transitionError(state.Status, CompactionFailed)
	}
	state.FailedFrom = state.Status
	state.Status = CompactionFailed
	state.FailureReason = reason
	state.clearLease()
	return state, nil
}

func (state CompactionState) requireFence(now time.Time, token uuid.UUID) error {
	if err := state.validate(); err != nil {
		return err
	}
	if token == uuid.Nil || token != state.FencingToken || !state.hasLease() {
		return ErrStaleFencing
	}
	if now.IsZero() || !state.LeaseExpiresAt.After(now.UTC()) {
		return ErrLeaseExpired
	}
	return nil
}

func (state CompactionState) hasLease() bool {
	return state.LeaseOwner != "" && !state.LeaseExpiresAt.IsZero() && state.FencingToken != uuid.Nil
}

func (state *CompactionState) clearLease() {
	state.LeaseOwner = ""
	state.LeaseExpiresAt = time.Time{}
	state.FencingToken = uuid.Nil
}

func (state CompactionState) validate() error {
	leaseFields := 0
	if state.LeaseOwner != "" {
		leaseFields++
	}
	if !state.LeaseExpiresAt.IsZero() {
		leaseFields++
	}
	if state.FencingToken != uuid.Nil {
		leaseFields++
	}
	if leaseFields != 0 && leaseFields != 3 || state.DeletedRows > state.SourceRows {
		return ErrInvalidState
	}
	if state.Status == CompactionFailed {
		if state.FailureReason == "" || state.FailedFrom != CompactionPending &&
			state.FailedFrom != CompactionSummarized && state.FailedFrom != CompactionDeleting || state.hasLease() {
			return ErrInvalidState
		}
		if (state.FailedFrom == CompactionPending) == state.SummaryCommitted {
			return ErrInvalidState
		}
		return nil
	}
	if state.FailedFrom != "" || state.FailureReason != "" {
		return ErrInvalidState
	}
	switch state.Status {
	case CompactionPending:
		if state.SummaryCommitted || state.SourceRows != 0 || state.DeletedRows != 0 {
			return ErrInvalidState
		}
	case CompactionSummarized, CompactionDeleting:
		if !state.SummaryCommitted {
			return ErrInvalidState
		}
	case CompactionCompleted:
		if !state.SummaryCommitted || state.hasLease() || state.DeletedRows != state.SourceRows {
			return ErrInvalidState
		}
	default:
		return ErrInvalidState
	}
	return nil
}

type RollupStatus string

const (
	RollupPending   RollupStatus = "pending"
	RollupCompleted RollupStatus = "completed"
	RollupFailed    RollupStatus = "failed"
)

var AllRollupStatuses = [...]RollupStatus{RollupPending, RollupCompleted, RollupFailed}

const (
	RollupFailureSegmentIncomplete       = "segment_incomplete"
	RollupFailureSegmentCountMismatch    = "segment_count_mismatch"
	RollupFailureSegmentChecksumMismatch = "segment_checksum_mismatch"
	RollupFailureActivationInconsistent  = "activation_inconsistent"
	RollupFailureStatementTimeout        = "statement_timeout"
	RollupFailureLeaseExpired            = "lease_expired"
	RollupFailureDatabaseUnavailable     = "database_unavailable"
	RollupFailureInternal                = "internal"
)

func CanRollupTransition(from, to RollupStatus) bool {
	return from == RollupPending && (to == RollupCompleted || to == RollupFailed) ||
		from == RollupFailed && to == RollupPending
}

type RollupState struct {
	Status            RollupStatus
	FailureReason     string
	LeaseOwner        string
	LeaseExpiresAt    time.Time
	FencingToken      uuid.UUID
	Attempt           uint64
	ExpectedSegments  uint64
	CompletedSegments uint64
	ChecksumVersion   uint16
	SegmentChecksum   [32]byte
}

func NewRollupState() RollupState { return RollupState{Status: RollupPending} }

// ResumeAction always recomputes a non-completed rollup from immutable
// segments. An unknown commit must reload the run first; a persisted completed
// row is terminal. Only fixed transient failed reasons safely return to the
// rollup phase; integrity and activation failures remain fail-closed.
func (state RollupState) ResumeAction() (ResumeAction, error) {
	if err := state.validate(); err != nil {
		return ResumeBlocked, err
	}
	switch state.Status {
	case RollupPending:
		return ResumeRollup, nil
	case RollupFailed:
		if recoverableRollupFailure(state.FailureReason) {
			return ResumeRollup, nil
		}
		return ResumeBlocked, nil
	case RollupCompleted:
		return ResumeDone, nil
	default:
		return ResumeBlocked, ErrInvalidState
	}
}

func (state RollupState) Claim(now time.Time, owner string, token uuid.UUID, duration time.Duration) (RollupState, error) {
	before := state
	if err := state.validate(); err != nil {
		return before, err
	}
	if state.Status == RollupCompleted {
		return before, ErrCompletedImmutable
	}
	if state.Status != RollupPending && state.Status != RollupFailed ||
		now.IsZero() || owner == "" || len(owner) > 128 || token == uuid.Nil ||
		duration < MinimumClaimLease || duration > MaximumClaimLease ||
		state.Attempt >= MaximumStateMachineAttempts {
		return before, ErrInvalidStateTransition
	}
	if state.hasLease() && state.LeaseExpiresAt.After(now.UTC()) {
		return before, ErrLeaseHeld
	}
	if state.Status == RollupFailed {
		if !recoverableRollupFailure(state.FailureReason) {
			return before, ErrInvalidStateTransition
		}
		state.Status = RollupPending
		state.FailureReason = ""
	}
	state.LeaseOwner = owner
	state.LeaseExpiresAt = now.UTC().Add(duration)
	state.FencingToken = token
	state.Attempt++
	return state, nil
}

func (state RollupState) Complete(
	now time.Time, token uuid.UUID, expected, completed uint64, segmentChecksum [32]byte,
) (RollupState, error) {
	before := state
	if state.Status == RollupCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if !CanRollupTransition(state.Status, RollupCompleted) || expected == 0 || completed != expected {
		return before, transitionError(state.Status, RollupCompleted)
	}
	state.Status = RollupCompleted
	state.ExpectedSegments = expected
	state.CompletedSegments = completed
	state.ChecksumVersion = ChecksumVersionV1
	state.SegmentChecksum = segmentChecksum
	state.clearLease()
	return state, nil
}

func (state RollupState) RenewLease(now time.Time, token uuid.UUID, duration time.Duration) (RollupState, error) {
	before := state
	if state.Status == RollupCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if state.Status != RollupPending || duration < MinimumClaimLease || duration > MaximumClaimLease {
		return before, ErrInvalidStateTransition
	}
	state.LeaseExpiresAt = now.UTC().Add(duration)
	return state, nil
}

func (state RollupState) Fail(now time.Time, token uuid.UUID, reason string) (RollupState, error) {
	before := state
	if state.Status == RollupCompleted {
		return before, ErrCompletedImmutable
	}
	if err := state.requireFence(now, token); err != nil {
		return before, err
	}
	if !CanRollupTransition(state.Status, RollupFailed) || !validRollupFailure(reason) {
		return before, transitionError(state.Status, RollupFailed)
	}
	state.Status = RollupFailed
	state.FailureReason = reason
	state.clearLease()
	return state, nil
}

func (state RollupState) requireFence(now time.Time, token uuid.UUID) error {
	if err := state.validate(); err != nil {
		return err
	}
	if token == uuid.Nil || token != state.FencingToken || !state.hasLease() {
		return ErrStaleFencing
	}
	if now.IsZero() || !state.LeaseExpiresAt.After(now.UTC()) {
		return ErrLeaseExpired
	}
	return nil
}

func (state RollupState) hasLease() bool {
	return state.LeaseOwner != "" && !state.LeaseExpiresAt.IsZero() && state.FencingToken != uuid.Nil
}

func (state *RollupState) clearLease() {
	state.LeaseOwner = ""
	state.LeaseExpiresAt = time.Time{}
	state.FencingToken = uuid.Nil
}

func (state RollupState) validate() error {
	leaseFields := 0
	if state.LeaseOwner != "" {
		leaseFields++
	}
	if !state.LeaseExpiresAt.IsZero() {
		leaseFields++
	}
	if state.FencingToken != uuid.Nil {
		leaseFields++
	}
	if leaseFields != 0 && leaseFields != 3 || state.CompletedSegments > state.ExpectedSegments {
		return ErrInvalidState
	}
	checksumAbsent := state.ChecksumVersion == 0 && state.SegmentChecksum == ([32]byte{})
	switch state.Status {
	case RollupPending:
		if state.FailureReason != "" || state.ExpectedSegments != 0 || state.CompletedSegments != 0 ||
			!checksumAbsent {
			return ErrInvalidState
		}
	case RollupCompleted:
		if state.hasLease() || state.FailureReason != "" || state.ExpectedSegments == 0 ||
			state.CompletedSegments != state.ExpectedSegments || state.ChecksumVersion != ChecksumVersionV1 {
			return ErrInvalidState
		}
	case RollupFailed:
		if state.hasLease() || !validRollupFailure(state.FailureReason) || state.ExpectedSegments != 0 ||
			state.CompletedSegments != 0 || !checksumAbsent {
			return ErrInvalidState
		}
	default:
		return ErrInvalidState
	}
	return nil
}

func validRollupFailure(reason string) bool {
	switch reason {
	case RollupFailureSegmentIncomplete, RollupFailureSegmentCountMismatch,
		RollupFailureSegmentChecksumMismatch, RollupFailureActivationInconsistent,
		RollupFailureStatementTimeout, RollupFailureLeaseExpired,
		RollupFailureDatabaseUnavailable, RollupFailureInternal:
		return true
	default:
		return false
	}
}

func recoverableRollupFailure(reason string) bool {
	switch reason {
	case RollupFailureSegmentIncomplete, RollupFailureStatementTimeout,
		RollupFailureLeaseExpired, RollupFailureDatabaseUnavailable:
		return true
	default:
		return false
	}
}

type CommitKnowledge string

const (
	CommitConfirmed   CommitKnowledge = "committed"
	RollbackConfirmed CommitKnowledge = "rolled_back"
	CommitUnknown     CommitKnowledge = "unknown"
)

type CommitResolution string

const (
	ContinueFromPersistentState CommitResolution = "continue_from_persistent_state"
	RetrySameFencedOperation    CommitResolution = "retry_same_fenced_operation"
	ReloadPersistentState       CommitResolution = "reload_persistent_state"
)

// ResolveCommit never guesses an unknown transaction result. The caller must
// reload the run and then use ResumeAction; a persisted summarized/deleting
// state consequently routes only to deletion and never to reaggregation.
func ResolveCommit(knowledge CommitKnowledge) (CommitResolution, error) {
	switch knowledge {
	case CommitConfirmed:
		return ContinueFromPersistentState, nil
	case RollbackConfirmed:
		return RetrySameFencedOperation, nil
	case CommitUnknown:
		return ReloadPersistentState, nil
	default:
		return "", ErrInvalidState
	}
}

type OrderedRowSchema struct {
	Version uint16
	Name    string
	OrderBy []string
	Fields  []string
}

// SourceChecksumReadSchemasV1 fixes both cursor order and canonical field order.
// Slice order is itself stable: poll, provider result, snapshot, duplicate.
var sourceChecksumReadSchemasV1 = [...]OrderedRowSchema{
	{Version: HistoryRowSchemaVersionV1, Name: "poll", OrderBy: []string{"scheduled_at", "poll_run_id"}, Fields: []string{"row_kind", "scheduled_at", "poll_run_id", "status", "observed_at", "transport_success", "contract_valid", "snapshot_complete", "degraded"}},
	{Version: HistoryRowSchemaVersionV1, Name: "provider_result", OrderBy: []string{"scheduled_at", "poll_run_id", "provider"}, Fields: []string{"row_kind", "scheduled_at", "poll_run_id", "provider", "snapshot_complete", "promotion_applied", "promotion_skipped_reason", "degraded", "reason"}},
	{Version: HistoryRowSchemaVersionV1, Name: "snapshot", OrderBy: []string{"scheduled_at", "poll_run_id", "instance_id", "account_key"}, Fields: []string{"row_kind", "scheduled_at", "poll_run_id", "instance_id", "provider", "account_key", "observed_at", "basic_status", "success_count", "failed_count"}},
	{Version: HistoryRowSchemaVersionV1, Name: "duplicate", OrderBy: []string{"scheduled_at", "poll_run_id", "instance_id", "account_key"}, Fields: []string{"row_kind", "scheduled_at", "poll_run_id", "instance_id", "provider", "account_key", "observed_at", "occurrence_count"}},
}

var segmentChecksumReadSchemasV1 = [...]OrderedRowSchema{
	{
		Version: HistoryRowSchemaVersionV1,
		Name:    "account_segment",
		OrderBy: []string{"summary_date", "instance_id", "account_key", "provider_policy_version"},
		Fields: []string{
			"row_kind", "summary_date", "instance_id", "provider", "account_key", "provider_policy_version",
			"first_scheduled_at", "last_scheduled_at", "first_observed_at", "last_observed_at",
			"last_basic_status", "sample_count", "disabled_count", "unavailable_count", "error_count",
			"active_count", "unknown_count", "first_success_count", "last_success_count",
			"success_reset_count", "first_failed_count", "last_failed_count", "failed_reset_count",
		},
	},
	{
		Version: HistoryRowSchemaVersionV1,
		Name:    "provider_segment",
		OrderBy: []string{"summary_date", "instance_id", "provider", "provider_policy_version"},
		Fields: []string{
			"row_kind", "summary_date", "instance_id", "provider", "provider_policy_version",
			"expected_poll_count", "transport_success_count", "contract_valid_count", "snapshot_complete_count",
			"promotion_applied_count", "promotion_skipped_count", "policy_changed_count", "abandoned_count",
			"degraded_count", "first_promotion_at", "last_promotion_at", "coverage_numerator",
			"coverage_denominator", "coverage_threshold_basis_points", "coverage_status",
		},
	},
}

func SourceChecksumReadSchemasV1() []OrderedRowSchema {
	return cloneOrderedSchemas(sourceChecksumReadSchemasV1[:])
}

func SegmentChecksumReadSchemasV1() []OrderedRowSchema {
	return cloneOrderedSchemas(segmentChecksumReadSchemasV1[:])
}

func cloneOrderedSchemas(source []OrderedRowSchema) []OrderedRowSchema {
	result := make([]OrderedRowSchema, len(source))
	for index, schema := range source {
		result[index] = schema
		result[index].OrderBy = append([]string(nil), schema.OrderBy...)
		result[index].Fields = append([]string(nil), schema.Fields...)
	}
	return result
}

type ChecksumRSSObservation struct {
	Rows              uint64
	BeforeMaxRSSBytes uint64
	AfterMaxRSSBytes  uint64
	Sum               [32]byte
}

// ObserveChecksumV1MaxRSS makes the million-row memory observation repeatable
// without asserting a made-up machine-independent RSS threshold. Max RSS is a
// process high-water mark, so callers should record both before and after.
func ObserveChecksumV1MaxRSS(rows uint64, fields func(uint64) []CanonicalField) (ChecksumRSSObservation, error) {
	if fields == nil {
		return ChecksumRSSObservation{}, ErrInvalidState
	}
	before, err := processMaxRSSBytes()
	if err != nil {
		return ChecksumRSSObservation{}, err
	}
	var chain ChainV1
	for index := uint64(0); index < rows; index++ {
		if err := chain.Add(fields(index)...); err != nil {
			return ChecksumRSSObservation{}, fmt.Errorf("checksum row %d: %w", index, err)
		}
	}
	after, err := processMaxRSSBytes()
	if err != nil {
		return ChecksumRSSObservation{}, err
	}
	return ChecksumRSSObservation{Rows: rows, BeforeMaxRSSBytes: before, AfterMaxRSSBytes: after, Sum: chain.Sum()}, nil
}

func processMaxRSSBytes() (uint64, error) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	if usage.Maxrss < 0 {
		return 0, ErrInvalidState
	}
	value := uint64(usage.Maxrss)
	if runtime.GOOS != "darwin" {
		value *= 1024
	}
	return value, nil
}

func transitionError(from, to any) error {
	return fmt.Errorf("%w: %v -> %v", ErrInvalidStateTransition, from, to)
}
