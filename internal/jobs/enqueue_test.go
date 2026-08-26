package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type memoryTx struct {
	jobs      map[string]Job
	bundles   []EnqueueBundle
	locked    Job
	cancels   []CancelMutation
	insertErr error
	cancelErr error
}

func (tx *memoryTx) FindJobByIdempotencyKey(_ context.Context, key string) (Job, error) {
	job, ok := tx.jobs[key]
	if !ok {
		return Job{}, ErrNotFound
	}
	return job, nil
}

func (tx *memoryTx) InsertBundle(_ context.Context, bundle EnqueueBundle) (Job, error) {
	if tx.insertErr != nil {
		return Job{}, tx.insertErr
	}
	if _, exists := tx.jobs[bundle.Job.IdempotencyKey]; exists {
		return Job{}, ErrConflict
	}
	tx.jobs[bundle.Job.IdempotencyKey] = bundle.Job
	tx.bundles = append(tx.bundles, bundle)
	return bundle.Job, nil
}

func (tx *memoryTx) LockJob(context.Context, uuid.UUID) (Job, error) { return tx.locked, nil }
func (tx *memoryTx) ApplyCancellation(_ context.Context, mutation CancelMutation) (Job, error) {
	if tx.cancelErr != nil {
		return Job{}, tx.cancelErr
	}
	tx.cancels = append(tx.cancels, mutation)
	tx.locked.Status = mutation.To
	tx.locked.CancelRequested = true
	return tx.locked, nil
}

func TestEnqueueTxCreatesAtomicMinimalBundleAndReplays(t *testing.T) {
	registry, err := NewRegistry(testDefinition(nil))
	if err != nil {
		t.Fatal(err)
	}
	tx := &memoryTx{jobs: map[string]Job{}}
	request := EnqueueRequest{Kind: "test.synthetic", SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:operation:1", Payload: validPayload(), Priority: 10, Actor: ActorService}
	created, err := EnqueueTx(context.Background(), tx, registry, request)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || len(tx.bundles) != 1 {
		t.Fatalf("created=%v bundles=%d", created.Created, len(tx.bundles))
	}
	bundle := tx.bundles[0]
	if bundle.InitialEvent.Type != EventEnqueued || bundle.InitialEvent.To != StatusPending || bundle.OutboxStatus != OutboxSuppressed || bundle.OutboxErrorCode != "publisher_disabled" {
		t.Fatalf("unexpected atomic bundle: %+v", bundle)
	}
	if bundle.Envelope.Topic != "async_job_wake" || bundle.Envelope.JobID != created.Job.ID || bundle.Envelope.OperationID != request.OperationID {
		t.Fatalf("unsafe or inconsistent wake envelope: %+v", bundle.Envelope)
	}
	replayed, err := EnqueueTx(context.Background(), tx, registry, request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Created || replayed.Job.ID != created.Job.ID || len(tx.bundles) != 1 {
		t.Fatalf("idempotent replay = %+v bundles=%d", replayed, len(tx.bundles))
	}
}

func TestEnqueueTxConflictsFailClosed(t *testing.T) {
	registry, _ := NewRegistry(testDefinition(nil))
	tx := &memoryTx{jobs: map[string]Job{}}
	request := EnqueueRequest{Kind: "test.synthetic", SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "test:operation:2", Payload: validPayload(), Priority: 1, Actor: ActorService}
	if _, err := EnqueueTx(context.Background(), tx, registry, request); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*EnqueueRequest){
		func(value *EnqueueRequest) { value.OperationID = uuid.New() },
		func(value *EnqueueRequest) { value.Priority++ },
		func(value *EnqueueRequest) {
			value.Payload = bytesReplace(value.Payload, `"revision":1`, `"revision":2`)
		},
	}
	for _, mutate := range mutations {
		conflict := request
		mutate(&conflict)
		if _, err := EnqueueTx(context.Background(), tx, registry, conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflict error = %v", err)
		}
	}
	if len(tx.bundles) != 1 {
		t.Fatalf("conflicts created %d bundles", len(tx.bundles))
	}
}

func bytesReplace(input []byte, old, replacement string) []byte {
	result := string(input)
	result = strings.Replace(result, old, replacement, 1)
	return []byte(result)
}

func TestRequestCancelTxClosedStateBehavior(t *testing.T) {
	for _, testCase := range []struct {
		status      Status
		to          Status
		requestOnly bool
		wantError   bool
	}{
		{StatusPending, StatusCancelled, false, false},
		{StatusRetryWait, StatusCancelled, false, false},
		{StatusRunning, StatusRunning, true, false},
		{StatusVerifying, StatusVerifying, true, false},
		{StatusRollingBack, StatusRollingBack, true, false},
		{StatusSucceeded, "", false, true},
		{StatusFailed, "", false, true},
		{StatusRolledBack, "", false, true},
		{StatusCancelled, "", false, true},
	} {
		t.Run(string(testCase.status), func(t *testing.T) {
			tx := &memoryTx{locked: Job{ID: uuid.New(), Status: testCase.status}}
			_, err := RequestCancelTx(context.Background(), tx, tx.locked.ID, ActorService, "operator_request")
			if (err != nil) != testCase.wantError {
				t.Fatalf("error=%v wantError=%v", err, testCase.wantError)
			}
			if testCase.wantError {
				return
			}
			if len(tx.cancels) != 1 || tx.cancels[0].To != testCase.to || tx.cancels[0].RequestOnly != testCase.requestOnly {
				t.Fatalf("cancel mutation = %+v", tx.cancels)
			}
		})
	}
}

func TestRequestCancelTxIsIdempotentAfterCancellation(t *testing.T) {
	job := Job{ID: uuid.New(), Status: StatusCancelled, CancelRequested: true}
	tx := &memoryTx{locked: job}
	result, err := RequestCancelTx(context.Background(), tx, job.ID, ActorService, "operator_request")
	if err != nil || result.ID != job.ID {
		t.Fatalf("cancel replay result=%+v err=%v", result, err)
	}
	if len(tx.cancels) != 0 {
		t.Fatal("cancel replay wrote a second event")
	}
}
