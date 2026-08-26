package jobs

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
)

var (
	idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$`)
	ownerPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	codePattern        = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type EnqueueRequest struct {
	Kind             string
	SchemaVersion    int
	OperationID      uuid.UUID
	IdempotencyKey   string
	Payload          []byte
	Priority         int
	PublisherEnabled bool
	Actor            ActorType
}

type EnqueueBundle struct {
	Job             Job
	InitialEvent    Event
	OutboxEventID   uuid.UUID
	OutboxEventKey  string
	OutboxStatus    OutboxStatus
	OutboxErrorCode string
	Envelope        WakeEnvelope
}

type Event struct {
	JobID      uuid.UUID
	Type       EventType
	From       *Status
	To         Status
	Attempt    int
	Actor      ActorType
	ReasonCode string
	ErrorCode  string
}

// EnqueueTxStore is implemented by a caller-owned transaction. InsertBundle
// must atomically create the job, initial event and Outbox row; it must never
// commit or publish a wake notification itself.
type EnqueueTxStore interface {
	FindJobByIdempotencyKey(context.Context, string) (Job, error)
	InsertBundle(context.Context, EnqueueBundle) (Job, error)
}

type EnqueueResult struct {
	Job     Job
	Created bool
}

func EnqueueTx(ctx context.Context, tx EnqueueTxStore, registry *Registry, request EnqueueRequest) (EnqueueResult, error) {
	if tx == nil || registry == nil || request.OperationID == uuid.Nil ||
		!idempotencyPattern.MatchString(request.IdempotencyKey) || request.Priority < 0 || request.Priority > 100 ||
		request.Actor != ActorService {
		return EnqueueResult{}, fmt.Errorf("%w: enqueue metadata", ErrInvalidPayload)
	}
	canonical, hash, definition, err := registry.ValidateAndHash(request.Kind, request.SchemaVersion, request.Payload)
	if err != nil {
		return EnqueueResult{}, err
	}
	if existing, lookupErr := tx.FindJobByIdempotencyKey(ctx, request.IdempotencyKey); lookupErr == nil {
		return matchExisting(existing, request, canonical, hash, definition)
	} else if !errorsIs(lookupErr, ErrNotFound) {
		return EnqueueResult{}, lookupErr
	}

	jobID := uuid.New()
	eventID := uuid.New()
	status := OutboxSuppressed
	errorCode := "publisher_disabled"
	if request.PublisherEnabled {
		status = OutboxPending
		errorCode = ""
	}
	job := Job{
		ID: jobID, OperationID: request.OperationID, IdempotencyKey: request.IdempotencyKey,
		Kind: request.Kind, SchemaVersion: request.SchemaVersion, Payload: canonical,
		PayloadHash: hash, Status: StatusPending, Priority: request.Priority,
		MaxAttempts: definition.MaxAttempts, MaxVerifyAttempts: definition.MaxVerifyAttempts,
		Timeout: definition.Timeout, LeaseDuration: definition.LeaseDuration,
		HeartbeatInterval: definition.HeartbeatInterval, ReplaySafe: definition.ReplaySafe,
		AllowRollback: definition.AllowRollback,
	}
	bundle := EnqueueBundle{
		Job:           job,
		InitialEvent:  Event{JobID: jobID, Type: EventEnqueued, To: StatusPending, Actor: request.Actor, ReasonCode: "job_enqueued"},
		OutboxEventID: eventID, OutboxEventKey: "job:" + jobID.String() + ":wake:v1",
		OutboxStatus: status, OutboxErrorCode: errorCode,
		Envelope: WakeEnvelope{SchemaVersion: 1, EventID: eventID, JobID: jobID, OperationID: request.OperationID, Topic: "async_job_wake"},
	}
	created, err := tx.InsertBundle(ctx, bundle)
	if err == nil {
		return EnqueueResult{Job: created, Created: true}, nil
	}
	if !errorsIs(err, ErrConflict) {
		return EnqueueResult{}, err
	}
	existing, lookupErr := tx.FindJobByIdempotencyKey(ctx, request.IdempotencyKey)
	if lookupErr != nil {
		return EnqueueResult{}, lookupErr
	}
	return matchExisting(existing, request, canonical, hash, definition)
}

func matchExisting(existing Job, request EnqueueRequest, canonical []byte, hash [32]byte, definition Definition) (EnqueueResult, error) {
	if existing.Kind != request.Kind || existing.SchemaVersion != request.SchemaVersion ||
		existing.OperationID != request.OperationID || existing.Priority != request.Priority ||
		existing.MaxAttempts != definition.MaxAttempts || !policyMatches(existing, definition) ||
		subtle.ConstantTimeCompare(existing.PayloadHash[:], hash[:]) != 1 || !bytes.Equal(existing.Payload, canonical) {
		return EnqueueResult{}, ErrConflict
	}
	return EnqueueResult{Job: existing, Created: false}, nil
}

func policyMatches(job Job, definition Definition) bool {
	return job.Timeout == definition.Timeout &&
		job.LeaseDuration == definition.LeaseDuration &&
		job.HeartbeatInterval == definition.HeartbeatInterval &&
		job.MaxVerifyAttempts == definition.MaxVerifyAttempts &&
		job.ReplaySafe == definition.ReplaySafe &&
		job.AllowRollback == definition.AllowRollback
}

type CancelTxStore interface {
	LockJob(context.Context, uuid.UUID) (Job, error)
	ApplyCancellation(context.Context, CancelMutation) (Job, error)
}

type CancelMutation struct {
	JobID       uuid.UUID
	From        Status
	To          Status
	RequestOnly bool
	Actor       ActorType
	ReasonCode  string
	Event       EventType
}

func RequestCancelTx(ctx context.Context, tx CancelTxStore, jobID uuid.UUID, actor ActorType, reasonCode string) (Job, error) {
	if tx == nil || jobID == uuid.Nil || actor != ActorService || !codePattern.MatchString(reasonCode) {
		return Job{}, ErrInvalidTransition
	}
	job, err := tx.LockJob(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusCancelled && job.CancelRequested {
		return job, nil
	}
	if job.Status.Terminal() {
		return Job{}, ErrInvalidTransition
	}
	if job.CancelRequested {
		return job, nil
	}
	mutation := CancelMutation{
		JobID: jobID, From: job.Status, Actor: actor, ReasonCode: reasonCode,
		Event: EventCancelRequested, RequestOnly: true, To: job.Status,
	}
	if job.Status == StatusPending || job.Status == StatusRetryWait {
		mutation.RequestOnly = false
		mutation.To = StatusCancelled
		mutation.Event = EventCancelled
	}
	return tx.ApplyCancellation(ctx, mutation)
}

func errorsIs(err, target error) bool {
	return errors.Is(err, target)
}
