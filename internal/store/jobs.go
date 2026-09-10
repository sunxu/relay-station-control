package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrInvalidJobQuery  = errors.New("store: invalid job query")
	ErrInvalidJobCursor = errors.New("store: invalid job cursor")
	ErrJobNotFound      = errors.New("store: job not found")
	ErrJobInconsistent  = errors.New("store: durable job state is inconsistent")
)

type JobListFilters struct {
	JobKind     string
	Status      string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
}

type JobCursor struct {
	CreatedAt time.Time
	JobID     uuid.UUID
}

type JobSummary struct {
	JobID           uuid.UUID
	OperationID     uuid.UUID
	JobKind         string
	Status          string
	AttemptCount    int
	MaxAttempts     int
	AvailableAt     time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
	CancelRequested bool
	ErrorCode       *string
	OutboxStatus    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type JobEvent struct {
	Sequence   int64
	EventType  string
	FromStatus *string
	ToStatus   string
	Attempt    int
	ReasonCode *string
	ErrorCode  *string
	ActorType  string
	OccurredAt time.Time
}

type JobDetail struct {
	JobSummary
	Events []JobEvent
}

type JobPage struct {
	Items   []JobSummary
	HasMore bool
}

type JobReader interface {
	ListJobs(context.Context, JobListFilters, *JobCursor, int) (JobPage, error)
	Job(context.Context, uuid.UUID) (JobDetail, error)
}

type JobKindPolicy struct {
	JobKind                  string
	PayloadSchemaVersion     int
	Timeout                  time.Duration
	LeaseDuration            time.Duration
	HeartbeatInterval        time.Duration
	MaxAttempts              int
	MaxVerificationAttempts  int
	ReplaySafe               bool
	RollbackAllowed          bool
	AllowUnknownEffectReplay bool
	AllowDirectSuccess       bool
}

type JobRepository struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

func NewJobRepository(pool *pgxpool.Pool) (*JobRepository, error) {
	if pool == nil {
		return nil, errors.New("store: job database is unavailable")
	}
	return &JobRepository{pool: pool, queries: generated.New(pool)}, nil
}

func (repository *JobRepository) ListJobs(ctx context.Context, filters JobListFilters, cursor *JobCursor, limit int) (JobPage, error) {
	if limit < 1 || limit > 200 || !validJobCursor(cursor) ||
		(filters.CreatedFrom != nil && filters.CreatedTo != nil && !filters.CreatedFrom.Before(*filters.CreatedTo)) {
		return JobPage{}, ErrInvalidJobQuery
	}
	params := generated.ListAsyncJobsPublicParams{
		JobKind: nullableText(filters.JobKind), Status: nullableText(filters.Status),
		CreatedFrom: nullableTimeValue(filters.CreatedFrom), CreatedTo: nullableTimeValue(filters.CreatedTo),
		PageSize: int32(limit + 1),
	}
	if cursor != nil {
		params.AfterCreatedAt = pgtype.Timestamptz{Time: cursor.CreatedAt.UTC(), Valid: true}
		params.AfterJobID = nullableUUID(cursor.JobID)
	}
	var result JobPage
	err := pgx.BeginTxFunc(ctx, repository.pool, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		rows, err := repository.queries.WithTx(tx).ListAsyncJobsPublic(ctx, params)
		if err != nil {
			return err
		}
		result.HasMore = len(rows) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		result.Items = make([]JobSummary, 0, len(rows))
		for _, row := range rows {
			summary, err := jobSummaryFromList(row)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, summary)
		}
		return nil
	})
	return result, err
}

func (repository *JobRepository) Job(ctx context.Context, jobID uuid.UUID) (JobDetail, error) {
	if jobID == uuid.Nil {
		return JobDetail{}, ErrInvalidJobQuery
	}
	var detail JobDetail
	err := pgx.BeginTxFunc(ctx, repository.pool, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		row, err := queries.GetAsyncJobPublic(ctx, nullableUUID(jobID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrJobNotFound
		}
		if err != nil {
			return err
		}
		events, err := queries.ListAsyncJobEventsPublic(ctx, nullableUUID(jobID))
		if err != nil {
			return err
		}
		detail.JobSummary, err = jobSummaryFromDetail(row)
		if err != nil {
			return err
		}
		detail.Events = make([]JobEvent, 0, len(events))
		for _, event := range events {
			converted := JobEvent{
				Sequence: event.Sequence, EventType: event.EventType,
				FromStatus: textPointer(event.FromStatus), ToStatus: event.ToStatus,
				Attempt: int(event.Attempt), ReasonCode: textPointer(event.ReasonCode),
				ErrorCode: textPointer(event.ErrorCode), ActorType: event.ActorType,
				OccurredAt: event.OccurredAt.Time.UTC(),
			}
			if !validJobEvent(converted) {
				return ErrJobInconsistent
			}
			detail.Events = append(detail.Events, converted)
		}
		return nil
	})
	return detail, err
}

func (repository *JobRepository) JobKinds(ctx context.Context) ([]JobKindPolicy, error) {
	rows, err := repository.queries.ListActiveAsyncJobKinds(ctx)
	if err != nil {
		return nil, err
	}
	policies := make([]JobKindPolicy, 0, len(rows))
	for _, row := range rows {
		policies = append(policies, JobKindPolicy{
			JobKind: row.JobKind, PayloadSchemaVersion: int(row.PayloadSchemaVersion),
			Timeout:                 time.Duration(row.DefaultTimeoutSeconds) * time.Second,
			LeaseDuration:           time.Duration(row.LeaseSeconds) * time.Second,
			HeartbeatInterval:       time.Duration(row.HeartbeatIntervalSeconds) * time.Second,
			MaxAttempts:             int(row.DefaultMaxAttempts),
			MaxVerificationAttempts: int(row.DefaultMaxVerificationAttempts),
			ReplaySafe:              row.ReplaySafe, RollbackAllowed: row.RollbackAllowed,
			AllowUnknownEffectReplay: row.AllowUnknownEffectReplay, AllowDirectSuccess: row.AllowDirectSuccess,
		})
	}
	return policies, nil
}

func validJobCursor(cursor *JobCursor) bool {
	return cursor == nil || (!cursor.CreatedAt.IsZero() && cursor.JobID != uuid.Nil)
}

func nullableTimeValue(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func textPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func jobSummaryFromList(row generated.ListAsyncJobsPublicRow) (JobSummary, error) {
	result := JobSummary{
		JobID: uuidFromPG(row.JobID), OperationID: uuidFromPG(row.OperationID),
		JobKind: row.JobKind, Status: row.Status, AttemptCount: int(row.AttemptCount),
		MaxAttempts: int(row.MaxAttempts), AvailableAt: row.AvailableAt.Time.UTC(),
		StartedAt: nullableTime(row.StartedAt), CompletedAt: nullableTime(row.CompletedAt),
		CancelRequested: row.CancelRequested, ErrorCode: textPointer(row.ErrorCode),
		OutboxStatus: row.OutboxStatus, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if !validJobSummary(result) {
		return JobSummary{}, ErrJobInconsistent
	}
	return result, nil
}

func jobSummaryFromDetail(row generated.GetAsyncJobPublicRow) (JobSummary, error) {
	result := JobSummary{
		JobID: uuidFromPG(row.JobID), OperationID: uuidFromPG(row.OperationID),
		JobKind: row.JobKind, Status: row.Status, AttemptCount: int(row.AttemptCount),
		MaxAttempts: int(row.MaxAttempts), AvailableAt: row.AvailableAt.Time.UTC(),
		StartedAt: nullableTime(row.StartedAt), CompletedAt: nullableTime(row.CompletedAt),
		CancelRequested: row.CancelRequested, ErrorCode: textPointer(row.ErrorCode),
		OutboxStatus: row.OutboxStatus, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if !validJobSummary(result) {
		return JobSummary{}, ErrJobInconsistent
	}
	return result, nil
}

// JobTxStore binds enqueue/cancel primitives to a caller-owned transaction.
// None of its methods commits or emits an in-process wake signal.
type JobTxStore struct{ queries *generated.Queries }

func NewJobTxStore(tx pgx.Tx) (*JobTxStore, error) {
	if tx == nil {
		return nil, errors.New("store: job transaction is unavailable")
	}
	return &JobTxStore{queries: generated.New(tx)}, nil
}

func (store *JobTxStore) FindJobByIdempotencyKey(ctx context.Context, key string) (jobcore.Job, error) {
	row, err := store.queries.GetAsyncJobByIdempotencyKey(ctx, key)
	return coreJob(row, err)
}

func (store *JobTxStore) InsertBundle(ctx context.Context, bundle jobcore.EnqueueBundle) (jobcore.Job, error) {
	// Check the immutable catalog before writing: the database copies its policy,
	// while EnqueueTx supplied the independently validated registry snapshot.
	kind, err := store.queries.GetActiveAsyncJobKind(ctx, generated.GetActiveAsyncJobKindParams{
		JobKind: bundle.Job.Kind, PayloadSchemaVersion: int32(bundle.Job.SchemaVersion),
	})
	if err != nil {
		return jobcore.Job{}, err
	}
	job := bundle.Job
	if job.Timeout != time.Duration(kind.DefaultTimeoutSeconds)*time.Second ||
		job.LeaseDuration != time.Duration(kind.LeaseSeconds)*time.Second ||
		job.HeartbeatInterval != time.Duration(kind.HeartbeatIntervalSeconds)*time.Second ||
		job.MaxAttempts != int(kind.DefaultMaxAttempts) ||
		job.MaxVerifyAttempts != int(kind.DefaultMaxVerificationAttempts) ||
		job.ReplaySafe != kind.ReplaySafe || job.AllowRollback != kind.RollbackAllowed ||
		job.AllowUnknownEffectReplay != kind.AllowUnknownEffectReplay ||
		job.AllowDirectSuccess != kind.AllowDirectSuccess ||
		(kind.AllowUnknownEffectReplay && !kind.ReplaySafe) {
		return jobcore.Job{}, jobcore.ErrConflict
	}
	row, err := store.queries.EnqueueAsyncJob(ctx, generated.EnqueueAsyncJobParams{
		JobID: nullableUUID(bundle.Job.ID), IdempotencyKey: bundle.Job.IdempotencyKey,
		JobKind: bundle.Job.Kind, PayloadSchemaVersion: int32(bundle.Job.SchemaVersion),
		OperationID: nullableUUID(bundle.Job.OperationID), Payload: bundle.Job.Payload,
		PayloadHash: bundle.Job.PayloadHash[:], Priority: int16(bundle.Job.Priority),
		OutboxEventID: nullableUUID(bundle.OutboxEventID), OutboxEventKey: bundle.OutboxEventKey,
		PublisherEnabled: bundle.OutboxStatus == jobcore.OutboxPending,
	})
	if isUniqueViolation(err) {
		return jobcore.Job{}, jobcore.ErrConflict
	}
	return coreJob(row, err)
}

func (store *JobTxStore) LockJob(ctx context.Context, jobID uuid.UUID) (jobcore.Job, error) {
	row, err := store.queries.LockAsyncJob(ctx, nullableUUID(jobID))
	return coreJob(row, err)
}

func (store *JobTxStore) ApplyCancellation(ctx context.Context, mutation jobcore.CancelMutation) (jobcore.Job, error) {
	row, err := store.queries.RequestAsyncJobCancel(ctx, generated.RequestAsyncJobCancelParams{
		JobID: nullableUUID(mutation.JobID), ReasonCode: mutation.ReasonCode,
	})
	return coreJob(row, err)
}

func coreJob(row generated.AsyncJob, err error) (jobcore.Job, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return jobcore.Job{}, jobcore.ErrNotFound
	}
	if err != nil {
		return jobcore.Job{}, err
	}
	if row.AllowUnknownEffectReplay && !row.ReplaySafe {
		return jobcore.Job{}, ErrJobInconsistent
	}
	var hash [32]byte
	if len(row.PayloadHash) != len(hash) {
		return jobcore.Job{}, jobcore.ErrInvalidPayload
	}
	copy(hash[:], row.PayloadHash)
	canonical, err := canonicalStoredPayload(row.Payload)
	if err != nil || sha256.Sum256(canonical) != hash {
		return jobcore.Job{}, jobcore.ErrInvalidPayload
	}
	return jobcore.Job{
		ID: uuidFromPG(row.JobID), OperationID: uuidFromPG(row.OperationID),
		IdempotencyKey: row.IdempotencyKey, Kind: row.JobKind,
		SchemaVersion: int(row.PayloadSchemaVersion), Payload: canonical,
		PayloadHash: hash, Status: jobcore.Status(row.Status), Priority: int(row.Priority),
		Attempt: int(row.AttemptCount), MaxAttempts: int(row.MaxAttempts),
		VerificationAttempt: int(row.VerificationAttempt), CancelRequested: row.CancelRequestedAt.Valid,
		CreatedAt: row.CreatedAt.Time.UTC(), DeadlineAt: row.DeadlineAt.Time.UTC(),
		Timeout:           time.Duration(row.TimeoutSeconds) * time.Second,
		LeaseDuration:     time.Duration(row.LeaseSeconds) * time.Second,
		HeartbeatInterval: time.Duration(row.HeartbeatIntervalSeconds) * time.Second,
		MaxVerifyAttempts: int(row.MaxVerificationAttempts),
		ReplaySafe:        row.ReplaySafe, AllowRollback: row.RollbackAllowed,
		AllowUnknownEffectReplay: row.AllowUnknownEffectReplay, AllowDirectSuccess: row.AllowDirectSuccess,
	}, nil
}

func canonicalStoredPayload(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, jobcore.ErrInvalidPayload
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, jobcore.ErrInvalidPayload
	}
	return json.Marshal(object)
}

func retrySeconds(duration time.Duration) (int32, error) {
	if duration < 0 || duration > 24*time.Hour {
		return 0, jobcore.ErrInvalidTransition
	}
	return int32(math.Ceil(duration.Seconds())), nil
}

func isUniqueViolation(err error) bool {
	var pgError interface{ SQLState() string }
	return errors.As(err, &pgError) && pgError.SQLState() == "23505"
}

func (repository *JobRepository) ClaimRunnable(ctx context.Context, request jobcore.ClaimRequest) (*jobcore.Lease, error) {
	row, err := repository.queries.ClaimRunnableAsyncJob(ctx, generated.ClaimRunnableAsyncJobParams{
		LeaseOwner: request.Owner, LeaseFencingToken: nullableUUID(request.Token),
	})
	return leaseFromAsyncJob(row, false, err)
}

func (repository *JobRepository) RenewLease(ctx context.Context, jobID, token uuid.UUID, duration time.Duration) error {
	seconds, err := leaseSeconds(duration)
	if err != nil {
		return err
	}
	_, err = repository.queries.RenewAsyncJobLease(ctx, generated.RenewAsyncJobLeaseParams{
		JobID: nullableUUID(jobID), LeaseFencingToken: nullableUUID(token), LeaseSeconds: seconds,
	})
	return lostLeaseError(err)
}

func (repository *JobRepository) TransitionFenced(ctx context.Context, transition jobcore.Transition) (jobcore.TransitionOutcome, error) {
	if err := transition.Validate(); err != nil {
		return jobcore.TransitionOutcome{}, err
	}
	if len(transition.From) != 1 {
		return jobcore.TransitionOutcome{}, jobcore.ErrInvalidTransition
	}
	delay, err := retrySeconds(transition.RetryAfter)
	if err != nil {
		return jobcore.TransitionOutcome{}, err
	}
	params := generated.TransitionAsyncJobFencedParams{
		JobID: nullableUUID(transition.JobID), ExpectedStatus: string(transition.From[0]),
		LeaseFencingToken: nullableUUID(transition.Token), TargetStatus: string(transition.To),
		EventType: string(transition.Event), RetryDelaySeconds: delay,
		ReasonCode: nullableText(transition.ReasonCode), ErrorCode: nullableText(transition.ErrorCode),
		ErrorSummary: pgtype.Text{}, ActorType: string(transition.Actor),
		ReleaseLease: transition.ReleaseLease,
	}
	var outcome jobcore.TransitionOutcome
	err = pgx.BeginTxFunc(ctx, repository.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		row, transitionErr := generated.New(tx).TransitionAsyncJobFenced(ctx, params)
		if transitionErr != nil {
			return lostLeaseError(transitionErr)
		}
		outcome.Status = jobcore.Status(row.Status)
		if row.ErrorCode.Valid {
			outcome.ErrorCode = row.ErrorCode.String
		}
		if transition.Mutation != nil {
			return transition.Mutation(ctx, tx)
		}
		return nil
	})
	if err != nil {
		return jobcore.TransitionOutcome{}, err
	}
	// The SQL function may rewrite the requested target under its row lock.
	// Publish that outcome only after the transition, event, and optional
	// mutation have all committed in the same transaction.
	return outcome, nil
}

func (repository *JobRepository) ClaimRecoverable(ctx context.Context, request jobcore.ClaimRequest) (*jobcore.Lease, error) {
	row, err := repository.queries.ClaimExpiredAsyncJob(ctx, generated.ClaimExpiredAsyncJobParams{
		LeaseOwner: request.Owner, LeaseFencingToken: nullableUUID(request.Token),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, jobcore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	converted := generated.AsyncJob{
		JobID: row.JobID, IdempotencyKey: row.IdempotencyKey, JobKind: row.JobKind,
		PayloadSchemaVersion: row.PayloadSchemaVersion, OperationID: row.OperationID,
		Payload: row.Payload, PayloadHash: row.PayloadHash, Status: row.Status,
		Priority: row.Priority, AttemptCount: row.AttemptCount, VerificationAttempt: row.VerificationAttempt,
		MaxAttempts: row.MaxAttempts, MaxVerificationAttempts: row.MaxVerificationAttempts,
		TimeoutSeconds: row.TimeoutSeconds, LeaseSeconds: row.LeaseSeconds,
		HeartbeatIntervalSeconds: row.HeartbeatIntervalSeconds,
		ReplaySafe:               row.ReplaySafe, RollbackAllowed: row.RollbackAllowed,
		AllowUnknownEffectReplay: row.AllowUnknownEffectReplay, AllowDirectSuccess: row.AllowDirectSuccess,
		AvailableAt: row.AvailableAt, DeadlineAt: row.DeadlineAt, StartedAt: row.StartedAt,
		CompletedAt: row.CompletedAt, CancelRequestedAt: row.CancelRequestedAt,
		ErrorCode: row.ErrorCode, ErrorSummary: row.ErrorSummary, LeaseOwner: row.LeaseOwner,
		LeaseFencingToken: row.LeaseFencingToken, LeaseExpiresAt: row.LeaseExpiresAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return leaseFromAsyncJob(converted, row.DeadlineExceeded, nil)
}

func (repository *JobRepository) ClaimOutbox(ctx context.Context, request jobcore.OutboxClaimRequest) (*jobcore.OutboxLease, error) {
	seconds, err := leaseSeconds(request.LeaseDuration)
	if err != nil {
		return nil, err
	}
	row, err := repository.queries.ClaimOutboxEvent(ctx, generated.ClaimOutboxEventParams{
		LeaseOwner: request.Owner, LeaseFencingToken: nullableUUID(request.Token), LeaseSeconds: seconds,
	})
	return outboxLease(row, err)
}

func (repository *JobRepository) RenewOutboxLease(ctx context.Context, eventID, token uuid.UUID, duration time.Duration) error {
	seconds, err := leaseSeconds(duration)
	if err != nil {
		return err
	}
	_, err = repository.queries.RenewOutboxEventLease(ctx, generated.RenewOutboxEventLeaseParams{
		EventID: nullableUUID(eventID), LeaseFencingToken: nullableUUID(token), LeaseSeconds: seconds,
	})
	return lostLeaseError(err)
}

func (repository *JobRepository) TransitionOutboxFenced(ctx context.Context, transition jobcore.OutboxTransition) error {
	delay, err := retrySeconds(transition.RetryAfter)
	if err != nil {
		return err
	}
	_, err = repository.queries.TransitionOutboxEventFenced(ctx, generated.TransitionOutboxEventFencedParams{
		EventID: nullableUUID(transition.EventID), LeaseFencingToken: nullableUUID(transition.Token),
		TargetStatus: string(transition.To), RetryDelaySeconds: delay, ErrorCode: nullableText(transition.ErrorCode),
	})
	return lostLeaseError(err)
}

func (repository *JobRepository) JobMetricsSnapshot(ctx context.Context) (jobcore.MetricsSnapshot, error) {
	row, err := repository.queries.GetAsyncJobMetrics(ctx)
	if err != nil {
		return jobcore.MetricsSnapshot{}, err
	}
	outboxAge, err := repository.queries.GetOutboxOldestPendingSeconds(ctx)
	if err != nil {
		return jobcore.MetricsSnapshot{}, err
	}
	return jobcore.MetricsSnapshot{
		JobsByStatus: map[jobcore.Status]int64{
			jobcore.StatusPending: row.Pending, jobcore.StatusRunning: row.Running,
			jobcore.StatusVerifying: row.Verifying, jobcore.StatusRetryWait: row.RetryWait,
			jobcore.StatusRollingBack: row.RollingBack, jobcore.StatusSucceeded: row.Succeeded,
			jobcore.StatusFailed: row.Failed, jobcore.StatusRolledBack: row.RolledBack,
			jobcore.StatusCancelled: row.Cancelled,
		},
		OldestPendingSeconds: maxFloat(row.OldestPendingSeconds, 0), ExpiredLeases: row.ExpiredLeases,
		OldestOutboxSeconds: maxFloat(outboxAge, 0),
	}, nil
}

func leaseFromAsyncJob(row generated.AsyncJob, deadlineExceeded bool, err error) (*jobcore.Lease, error) {
	job, err := coreJob(row, err)
	if err != nil {
		return nil, err
	}
	if !row.LeaseOwner.Valid || !row.LeaseFencingToken.Valid || !row.LeaseExpiresAt.Valid {
		return nil, ErrJobInconsistent
	}
	return &jobcore.Lease{
		Job: job, Owner: row.LeaseOwner.String, Token: uuidFromPG(row.LeaseFencingToken),
		ExpiresAt: row.LeaseExpiresAt.Time.UTC(), DeadlineExceeded: deadlineExceeded,
	}, nil
}

func outboxLease(row generated.OperationOutbox, err error) (*jobcore.OutboxLease, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, jobcore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	envelope, err := decodeWakeEnvelope(row.Envelope)
	if err != nil || !row.LeaseFencingToken.Valid || !row.LeaseExpiresAt.Valid ||
		envelope.EventID != uuidFromPG(row.EventID) || envelope.JobID != uuidFromPG(row.JobID) ||
		envelope.OperationID != uuidFromPG(row.OperationID) || envelope.Topic != row.Topic {
		return nil, ErrJobInconsistent
	}
	return &jobcore.OutboxLease{
		Envelope: envelope, Status: jobcore.OutboxStatus(row.Status), Attempt: int(row.AttemptCount),
		MaxAttempts: int(row.MaxAttempts), Token: uuidFromPG(row.LeaseFencingToken),
		ExpiresAt: row.LeaseExpiresAt.Time.UTC(),
	}, nil
}

func leaseSeconds(duration time.Duration) (int32, error) {
	if duration < 5*time.Second || duration > time.Hour || duration%time.Second != 0 {
		return 0, jobcore.ErrInvalidTransition
	}
	return int32(duration / time.Second), nil
}

func lostLeaseError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return jobcore.ErrLostLease
	}
	return err
}

func maxFloat(value, minimum float64) float64 {
	if value < minimum {
		return minimum
	}
	return value
}

func decodeWakeEnvelope(raw []byte) (jobcore.WakeEnvelope, error) {
	var envelope jobcore.WakeEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return jobcore.WakeEnvelope{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return jobcore.WakeEnvelope{}, ErrJobInconsistent
	}
	return envelope, nil
}

func validJobSummary(value JobSummary) bool {
	if value.JobID == uuid.Nil || value.OperationID == uuid.Nil || !jobcore.Status(value.Status).Valid() ||
		value.AttemptCount < 0 || value.MaxAttempts < 1 || value.AttemptCount > value.MaxAttempts ||
		value.AvailableAt.IsZero() || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return false
	}
	switch value.OutboxStatus {
	case string(jobcore.OutboxPending), string(jobcore.OutboxPublishing), string(jobcore.OutboxRetryWait),
		string(jobcore.OutboxSent), string(jobcore.OutboxFailed), string(jobcore.OutboxSuppressed):
	default:
		return false
	}
	return true
}

func validJobEvent(value JobEvent) bool {
	if value.Sequence < 1 || !jobcore.Status(value.ToStatus).Valid() || value.Attempt < 0 || value.OccurredAt.IsZero() {
		return false
	}
	if value.FromStatus != nil && !jobcore.Status(*value.FromStatus).Valid() {
		return false
	}
	switch jobcore.EventType(value.EventType) {
	case jobcore.EventEnqueued, jobcore.EventClaimed, jobcore.EventRetryScheduled,
		jobcore.EventVerification, jobcore.EventRollbackStarted, jobcore.EventSucceeded,
		jobcore.EventFailed, jobcore.EventRolledBack, jobcore.EventCancelled, jobcore.EventCancelRequested:
	default:
		return false
	}
	switch jobcore.ActorType(value.ActorType) {
	case jobcore.ActorService, jobcore.ActorWorker, jobcore.ActorReconciler, jobcore.ActorSystem:
	default:
		return false
	}
	return true
}

var _ JobReader = (*JobRepository)(nil)
var _ jobcore.EnqueueTxStore = (*JobTxStore)(nil)
var _ jobcore.CancelTxStore = (*JobTxStore)(nil)
var _ jobcore.Repository = (*JobRepository)(nil)
var _ jobcore.MetricsProvider = (*JobRepository)(nil)
