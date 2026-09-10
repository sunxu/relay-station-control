package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

type cancellationEvidenceFixture struct {
	database   *isolatedJobDatabase
	repository *jobstore.JobRepository
	job        jobcore.EnqueueResult
}

type cancellationEventSnapshot struct {
	status         string
	jobError       string
	jobErrorNull   bool
	eventType      string
	fromStatus     string
	toStatus       string
	reason         string
	errorCode      string
	eventErrorNull bool
	actor          string
}

func newCancellationEvidenceFixture(t *testing.T, label string, unknownReplay, directSuccess bool) cancellationEvidenceFixture {
	return newCancellationEvidenceFixtureWithMaxAttempts(t, label, unknownReplay, directSuccess, 5)
}

func newCancellationEvidenceFixtureWithMaxAttempts(t *testing.T, label string, unknownReplay, directSuccess bool, maxAttempts int) cancellationEvidenceFixture {
	t.Helper()
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	definition := executionPolicyDefinition(
		fmt.Sprintf("test.cancel.%s.%s", label, assetFixtureSuffix(t)),
		unknownReplay, directSuccess, true,
	)
	definition.MaxAttempts = maxAttempts
	installExecutionPolicyKind(t, ctx, database.owner, definition)
	registry, err := jobcore.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	created := enqueueCommitted(t, ctx, database.owner, registry, jobcore.EnqueueRequest{
		Kind: definition.Kind, SchemaVersion: definition.SchemaVersion, OperationID: uuid.New(),
		IdempotencyKey: "test:cancel-evidence:" + uuid.NewString(), Payload: executionPolicyPayload(),
		Priority: 50, PublisherEnabled: true, Actor: jobcore.ActorService,
	})
	repository, err := jobstore.NewJobRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	return cancellationEvidenceFixture{database: database, repository: repository, job: created}
}

func claimCancellationJob(t *testing.T, fixture cancellationEvidenceFixture, owner string) *jobcore.Lease {
	t.Helper()
	lease, err := fixture.repository.ClaimRunnable(context.Background(), jobcore.ClaimRequest{
		Owner: owner, Token: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.ID != fixture.job.Job.ID || lease.Status != jobcore.StatusRunning {
		t.Fatalf("claimed job = %+v, want job %s running", lease, fixture.job.Job.ID)
	}
	return lease
}

func transitionCancellationEvidence(t *testing.T, fixture cancellationEvidenceFixture, lease *jobcore.Lease,
	to jobcore.Status, event jobcore.EventType, actor jobcore.ActorType, reason, errorCode string) error {
	t.Helper()
	_, err := transitionCancellationEvidenceOutcome(fixture, lease, to, event, actor, reason, errorCode)
	return err
}

func transitionCancellationEvidenceOutcome(fixture cancellationEvidenceFixture, lease *jobcore.Lease,
	to jobcore.Status, event jobcore.EventType, actor jobcore.ActorType, reason, errorCode string) (jobcore.TransitionOutcome, error) {
	return fixture.repository.TransitionFenced(context.Background(), jobcore.Transition{
		JobID: lease.ID, Token: lease.Token, From: []jobcore.Status{jobcore.StatusRunning},
		To: to, Event: event, Actor: actor, ReasonCode: reason, ErrorCode: errorCode,
		ReleaseLease: true,
	})
}

func requestCancellation(t *testing.T, fixture cancellationEvidenceFixture) jobcore.Job {
	t.Helper()
	job, err := cancelJobCommitted(context.Background(), fixture.database.runtime, fixture.job.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func eventCount(t *testing.T, fixture cancellationEvidenceFixture) int {
	t.Helper()
	var count int
	if err := fixture.database.owner.QueryRow(context.Background(),
		`SELECT count(*) FROM async_job_events WHERE job_id=$1`, fixture.job.Job.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func latestCancellationEvent(t *testing.T, fixture cancellationEvidenceFixture) cancellationEventSnapshot {
	t.Helper()
	var snapshot cancellationEventSnapshot
	err := fixture.database.owner.QueryRow(context.Background(), `
		SELECT j.status, coalesce(j.error_code,''), j.error_code IS NULL,
		       e.event_type, coalesce(e.from_status,''), e.to_status,
		       coalesce(e.reason_code,''), coalesce(e.error_code,''), e.error_code IS NULL, e.actor_type
		FROM async_jobs j
		JOIN LATERAL (
			SELECT event_type, from_status, to_status, reason_code, error_code, actor_type
			FROM async_job_events
			WHERE job_id=j.job_id
			ORDER BY sequence DESC
			LIMIT 1
		) e ON true
		WHERE j.job_id=$1`, fixture.job.Job.ID).Scan(
		&snapshot.status, &snapshot.jobError, &snapshot.jobErrorNull, &snapshot.eventType, &snapshot.fromStatus,
		&snapshot.toStatus, &snapshot.reason, &snapshot.errorCode, &snapshot.eventErrorNull, &snapshot.actor,
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertCancellationStateEvent(t *testing.T, fixture cancellationEvidenceFixture, wantStatus, wantJobError,
	wantEvent, wantFrom, wantTo, wantReason, wantEventError, wantActor string, wantEventCount int) {
	t.Helper()
	snapshot := latestCancellationEvent(t, fixture)
	if snapshot.status != wantStatus || snapshot.jobError != wantJobError || snapshot.eventType != wantEvent ||
		snapshot.fromStatus != wantFrom || snapshot.toStatus != wantTo || snapshot.reason != wantReason ||
		snapshot.errorCode != wantEventError || snapshot.actor != wantActor {
		t.Fatalf("job/event snapshot = %+v, want status=%s job_error=%s event=%s %s->%s reason=%s error=%s actor=%s",
			snapshot, wantStatus, wantJobError, wantEvent, wantFrom, wantTo, wantReason, wantEventError, wantActor)
	}
	if wantJobError == "" && !snapshot.jobErrorNull {
		t.Fatalf("job error code = %q, want SQL NULL", snapshot.jobError)
	}
	if wantEventError == "" && !snapshot.eventErrorNull {
		t.Fatalf("event error code = %q, want SQL NULL", snapshot.errorCode)
	}
	if got := eventCount(t, fixture); got != wantEventCount {
		t.Fatalf("event count = %d, want %d", got, wantEventCount)
	}
}

func assertCancellationUnchanged(t *testing.T, fixture cancellationEvidenceFixture, wantStatus string, wantEvents int) {
	t.Helper()
	snapshot := latestCancellationEvent(t, fixture)
	if snapshot.status != wantStatus {
		t.Fatalf("rejected transition changed status to %s, want %s", snapshot.status, wantStatus)
	}
	if got := eventCount(t, fixture); got != wantEvents {
		t.Fatalf("rejected transition changed event count to %d, want %d", got, wantEvents)
	}
}

func enterCancellationRetryWait(t *testing.T, fixture cancellationEvidenceFixture, lease *jobcore.Lease,
	reason, errorCode string) {
	t.Helper()
	if err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusRetryWait,
		jobcore.EventRetryScheduled, jobcore.ActorWorker, reason, errorCode); err != nil {
		t.Fatal(err)
	}
}

// Extra case: a current unknown proof is unsafe to cancel after retry_wait,
// even though the job is opted in to unknown-effect replay.
func TestDurableJobCancellationEvidenceCurrentUnknownAfterCommit(t *testing.T) {
	fixture := newCancellationEvidenceFixture(t, "a", true, false)
	lease := claimCancellationJob(t, fixture, "cancel-a-worker")
	enterCancellationRetryWait(t, fixture, lease, "effect_unknown_unverified", "execution_result_unknown")
	assertCancellationStateEvent(t, fixture, "retry_wait", "execution_result_unknown", "retry_scheduled",
		"running", "retry_wait", "effect_unknown_unverified", "execution_result_unknown", "worker", 3)

	requestCancellation(t, fixture)
	assertCancellationStateEvent(t, fixture, "failed", "cancel_after_unknown_effect", "failed",
		"retry_wait", "failed", "operator_cancelled", "cancel_after_unknown_effect", "service", 4)
}

func TestDurableJobCancellationEvidencePendingPreservesRequestSemantics(t *testing.T) {
	fixture := newCancellationEvidenceFixture(t, "pending", true, true)
	requestCancellation(t, fixture)
	assertCancellationStateEvent(t, fixture, "cancelled", "", "cancelled",
		"pending", "cancelled", "operator_cancelled", "", "service", 2)
}

// Test A: policy is capability, not evidence. A worker's current no-effect
// proof permits the narrow running->cancelled transition for both policy values.
func TestDurableJobCancellationEvidenceARunningKnownNoEffectWithCancel(t *testing.T) {
	for _, unknownReplay := range []bool{false, true} {
		t.Run(fmt.Sprintf("policy-%t", unknownReplay), func(t *testing.T) {
			fixture := newCancellationEvidenceFixture(t, "b", unknownReplay, false)
			lease := claimCancellationJob(t, fixture, "cancel-b-worker")
			requestCancellation(t, fixture)
			outcome, err := transitionCancellationEvidenceOutcome(fixture, lease, jobcore.StatusRetryWait,
				jobcore.EventRetryScheduled, jobcore.ActorWorker, "execute_retryable_no_effect", "known_no_effect")
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Status != jobcore.StatusCancelled || outcome.ErrorCode != "cancel_verified_safe" {
				t.Fatalf("known-no-effect cancellation outcome = %+v, want cancelled/cancel_verified_safe", outcome)
			}
			assertCancellationStateEvent(t, fixture, "cancelled", "cancel_verified_safe", "cancelled",
				"running", "cancelled", "execute_retryable_no_effect", "cancel_verified_safe", "worker", 4)
		})
	}
}

// Test B: a current unknown proof plus cancellation visible before the fenced
// retry commit is unsafe; the same path also preserves the attempt budget guard.
func TestDurableJobCancellationEvidenceBCurrentUnknownBeforeCommit(t *testing.T) {
	t.Run("cancel-after-final-attempt-result", func(t *testing.T) {
		fixture := newCancellationEvidenceFixtureWithMaxAttempts(t, "b_final_cancel", true, false, 1)
		lease := claimCancellationJob(t, fixture, "cancel-final-worker")
		requestCancellation(t, fixture)
		// Execute already selected a budget failure, but cancellation became
		// visible before its fenced commit. The current unknown still matters.
		if err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusFailed,
			jobcore.EventFailed, jobcore.ActorWorker, "effect_unknown_unverified", "max_attempts_exhausted"); err != nil {
			t.Fatal(err)
		}
		assertCancellationStateEvent(t, fixture, "failed", "cancel_after_unknown_effect", "failed",
			"running", "failed", "effect_unknown_unverified", "cancel_after_unknown_effect", "worker", 4)
	})

	t.Run("cancel-race", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "b_race", true, false)
		lease := claimCancellationJob(t, fixture, "cancel-b-worker")
		requestCancellation(t, fixture)
		outcome, err := transitionCancellationEvidenceOutcome(fixture, lease, jobcore.StatusRetryWait,
			jobcore.EventRetryScheduled, jobcore.ActorWorker, "effect_unknown_unverified", "requested_retry")
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Status != jobcore.StatusFailed || outcome.ErrorCode != "cancel_after_unknown_effect" {
			t.Fatalf("current-unknown cancellation outcome = %+v, want failed/cancel_after_unknown_effect", outcome)
		}
		assertCancellationStateEvent(t, fixture, "failed", "cancel_after_unknown_effect", "failed",
			"running", "failed", "effect_unknown_unverified", "cancel_after_unknown_effect", "worker", 4)
	})

	t.Run("max-attempts", func(t *testing.T) {
		fixture := newCancellationEvidenceFixtureWithMaxAttempts(t, "b_budget", true, false, 1)
		lease := claimCancellationJob(t, fixture, "cancel-b-worker")
		if err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusRetryWait,
			jobcore.EventRetryScheduled, jobcore.ActorWorker, "effect_unknown_unverified", "not_relevant"); err != nil {
			t.Fatal(err)
		}
		assertCancellationStateEvent(t, fixture, "failed", "max_attempts_exhausted", "failed",
			"running", "failed", "effect_unknown_unverified", "max_attempts_exhausted", "worker", 3)
	})
}

// Test C: an unresolved prior unknown remains unsafe whether cancellation is
// visible before the later no-effect fenced transition or after it commits.
func TestDurableJobCancellationEvidenceCUnknownThenKnownNoEffectTwoCancelOrders(t *testing.T) {
	t.Run("cancel-before-known-no-effect-commit", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "c_before", true, false)
		first := claimCancellationJob(t, fixture, "cancel-c-worker")
		enterCancellationRetryWait(t, fixture, first, "effect_unknown_unverified", "execution_result_unknown")
		second := claimCancellationJob(t, fixture, "cancel-c-worker")
		requestCancellation(t, fixture)
		if err := transitionCancellationEvidence(t, fixture, second, jobcore.StatusRetryWait,
			jobcore.EventRetryScheduled, jobcore.ActorWorker, "execute_retryable_no_effect", "known_no_effect"); err != nil {
			t.Fatal(err)
		}
		assertCancellationStateEvent(t, fixture, "failed", "cancel_after_unknown_effect", "failed",
			"running", "failed", "execute_retryable_no_effect", "cancel_after_unknown_effect", "worker", 6)
	})

	t.Run("cancel-after-known-no-effect-commit", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "c_after", true, false)
		first := claimCancellationJob(t, fixture, "cancel-c-worker")
		enterCancellationRetryWait(t, fixture, first, "effect_unknown_unverified", "execution_result_unknown")
		second := claimCancellationJob(t, fixture, "cancel-c-worker")
		enterCancellationRetryWait(t, fixture, second, "execute_retryable_no_effect", "known_no_effect")
		requestCancellation(t, fixture)
		assertCancellationStateEvent(t, fixture, "failed", "cancel_after_unknown_effect", "failed",
			"retry_wait", "failed", "operator_cancelled", "cancel_after_unknown_effect", "service", 6)
	})
}

// enterVerifiedReset creates unknown -> verifying -> VerifyEffectAbsent and
// records the framework marker that resets the unresolved-unknown window.
func enterVerifiedReset(t *testing.T, fixture cancellationEvidenceFixture) {
	t.Helper()
	first := claimCancellationJob(t, fixture, "cancel-reset-worker")
	enterCancellationRetryWait(t, fixture, first, "effect_unknown_unverified", "execution_result_unknown")
	second := claimCancellationJob(t, fixture, "cancel-reset-worker")
	if err := transitionCancellationEvidence(t, fixture, second, jobcore.StatusVerifying,
		jobcore.EventVerification, jobcore.ActorWorker, "", "execution_result_unknown"); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.repository.ClaimRecoverable(context.Background(), jobcore.ClaimRequest{
		Owner: "cancel-reset-reconciler", Token: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ID != fixture.job.Job.ID || recovered.Status != jobcore.StatusVerifying {
		t.Fatalf("verification recovery = %+v", recovered)
	}
	if err := transitionFencedError(fixture.repository, context.Background(), jobcore.Transition{
		JobID: recovered.ID, Token: recovered.Token, From: []jobcore.Status{jobcore.StatusVerifying},
		To: jobcore.StatusRetryWait, Event: jobcore.EventRetryScheduled, Actor: jobcore.ActorReconciler,
		ReasonCode: "effect_absent_verified", ErrorCode: "effect_not_applied", ReleaseLease: true,
	}); err != nil {
		t.Fatal(err)
	}
}

// Test D: VerifyEffectAbsent resets an earlier unknown, allowing verified-safe
// cancellation when no newer unknown proof exists.
func TestDurableJobCancellationEvidenceDVerifyAbsentResetsUnknown(t *testing.T) {
	fixture := newCancellationEvidenceFixture(t, "d", true, false)
	enterVerifiedReset(t, fixture)
	requestCancellation(t, fixture)
	assertCancellationStateEvent(t, fixture, "cancelled", "cancel_verified_safe", "cancelled",
		"retry_wait", "cancelled", "operator_cancelled", "cancel_verified_safe", "service", 8)
}

// Test D2: the reset is not sticky; an unknown proof after the marker is again
// unsafe to cancel.
func TestDurableJobCancellationEvidenceD2UnknownAfterVerifyReset(t *testing.T) {
	fixture := newCancellationEvidenceFixture(t, "d2", true, false)
	enterVerifiedReset(t, fixture)
	second := claimCancellationJob(t, fixture, "cancel-d2-worker")
	enterCancellationRetryWait(t, fixture, second, "effect_unknown_unverified", "execution_result_unknown")
	requestCancellation(t, fixture)
	assertCancellationStateEvent(t, fixture, "failed", "cancel_after_unknown_effect", "failed",
		"retry_wait", "failed", "operator_cancelled", "cancel_after_unknown_effect", "service", 10)
}

// Test E: requestcancel keeps the ordinary retry_wait cancellation behavior;
// enabling unknown replay does not manufacture unknown evidence.
func TestDurableJobCancellationEvidenceEOrdinaryRetryWaitCancellation(t *testing.T) {
	for _, unknownReplay := range []bool{false, true} {
		t.Run(fmt.Sprintf("policy-%t", unknownReplay), func(t *testing.T) {
			fixture := newCancellationEvidenceFixture(t, "e", unknownReplay, false)
			lease := claimCancellationJob(t, fixture, "cancel-e-worker")
			enterCancellationRetryWait(t, fixture, lease, "execute_retryable_no_effect", "known_no_effect")
			requestCancellation(t, fixture)
			assertCancellationStateEvent(t, fixture, "cancelled", "", "cancelled",
				"retry_wait", "cancelled", "operator_cancelled", "", "service", 4)
		})
	}
}

// Test F: direct success is not allowed to win a fenced cancellation race.
func TestDurableJobCancellationEvidenceFDirectSuccessAfterCancel(t *testing.T) {
	for _, unknownReplay := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown-policy-%t", unknownReplay), func(t *testing.T) {
			fixture := newCancellationEvidenceFixture(t, "f", unknownReplay, true)
			lease := claimCancellationJob(t, fixture, "cancel-f-worker")
			requestCancellation(t, fixture)
			outcome, err := transitionCancellationEvidenceOutcome(fixture, lease, jobcore.StatusSucceeded,
				jobcore.EventSucceeded, jobcore.ActorWorker, "", "requested_success")
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Status != jobcore.StatusFailed || outcome.ErrorCode != "cancel_after_effect_applied" {
				t.Fatalf("direct-success cancellation outcome = %+v, want failed/cancel_after_effect_applied", outcome)
			}
			assertCancellationStateEvent(t, fixture, "failed", "cancel_after_effect_applied", "failed",
				"running", "failed", "", "cancel_after_effect_applied", "worker", 4)
		})
	}
}

// Guard matrix: only a valid live fence, visible cancellation, worker actor, known
// no-effect proof, and no unresolved unknown may produce running->cancelled.
func TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix(t *testing.T) {
	t.Run("without-cancel", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "g_no_cancel", false, false)
		lease := claimCancellationJob(t, fixture, "cancel-g-worker")
		before := eventCount(t, fixture)
		err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusCancelled,
			jobcore.EventCancelled, jobcore.ActorWorker, "execute_retryable_no_effect", "cancel_verified_safe")
		requirePostgresCode(t, err, "23514")
		assertCancellationUnchanged(t, fixture, "running", before)
	})

	t.Run("without-proof", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "g_no_proof", false, false)
		lease := claimCancellationJob(t, fixture, "cancel-g-worker")
		requestCancellation(t, fixture)
		before := eventCount(t, fixture)
		err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusCancelled,
			jobcore.EventCancelled, jobcore.ActorWorker, "", "cancel_verified_safe")
		requirePostgresCode(t, err, "23514")
		assertCancellationUnchanged(t, fixture, "running", before)
	})

	t.Run("invalid-role-and-reason", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "g_invalid_role", true, false)
		lease := claimCancellationJob(t, fixture, "cancel-g-worker")
		before := eventCount(t, fixture)
		err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusRetryWait,
			jobcore.EventRetryScheduled, jobcore.ActorReconciler, "execute_retryable_no_effect", "known_no_effect")
		requirePostgresCode(t, err, "23514")
		assertCancellationUnchanged(t, fixture, "running", before)

		err = transitionCancellationEvidence(t, fixture, lease, jobcore.StatusRetryWait,
			jobcore.EventRetryScheduled, jobcore.ActorWorker, "effect_absent_verified", "known_no_effect")
		requirePostgresCode(t, err, "23514")
		assertCancellationUnchanged(t, fixture, "running", before)
	})

	t.Run("stale-fence", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "g_stale_fence", false, false)
		lease := claimCancellationJob(t, fixture, "cancel-g-worker")
		before := eventCount(t, fixture)
		if _, err := fixture.database.owner.Exec(context.Background(),
			`UPDATE async_jobs SET lease_fencing_token=$2 WHERE job_id=$1`, fixture.job.Job.ID, uuid.New()); err != nil {
			t.Fatal(err)
		}
		err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusRetryWait,
			jobcore.EventRetryScheduled, jobcore.ActorWorker, "execute_retryable_no_effect", "known_no_effect")
		if !errors.Is(err, jobcore.ErrLostLease) {
			t.Fatalf("stale fence error = %v, want lost lease", err)
		}
		assertCancellationUnchanged(t, fixture, "running", before)
	})

	t.Run("expired-lease", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "g_expired", false, false)
		lease := claimCancellationJob(t, fixture, "cancel-g-worker")
		before := eventCount(t, fixture)
		if _, err := fixture.database.owner.Exec(context.Background(),
			`UPDATE async_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, fixture.job.Job.ID); err != nil {
			t.Fatal(err)
		}
		err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusRetryWait,
			jobcore.EventRetryScheduled, jobcore.ActorWorker, "execute_retryable_no_effect", "known_no_effect")
		if !errors.Is(err, jobcore.ErrLostLease) {
			t.Fatalf("expired lease error = %v, want lost lease", err)
		}
		assertCancellationUnchanged(t, fixture, "running", before)
	})

	t.Run("prior-unknown-explicit-cancel", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "g_prior_unknown", true, false)
		first := claimCancellationJob(t, fixture, "cancel-g-worker")
		enterCancellationRetryWait(t, fixture, first, "effect_unknown_unverified", "execution_result_unknown")
		second := claimCancellationJob(t, fixture, "cancel-g-worker")
		requestCancellation(t, fixture)
		before := eventCount(t, fixture)
		err := transitionCancellationEvidence(t, fixture, second, jobcore.StatusCancelled,
			jobcore.EventCancelled, jobcore.ActorWorker, "execute_retryable_no_effect", "cancel_verified_safe")
		requirePostgresCode(t, err, "23514")
		assertCancellationUnchanged(t, fixture, "running", before)
	})

	t.Run("reserved-cancel-reason", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "g_reserved", false, false)
		before := eventCount(t, fixture)
		_, err := fixture.database.runtime.Exec(context.Background(),
			`SELECT * FROM public.control_request_async_job_cancel($1::uuid,$2::text)`,
			fixture.job.Job.ID, "effect_unknown_unverified")
		requirePostgresCode(t, err, "22023")
		assertCancellationUnchanged(t, fixture, "pending", before)
	})
}

// Test G: ExecuteNeedsVerification keeps cancellation as a request; the later
// verified-absent outcome performs the safe cancellation.
func TestDurableJobCancellationEvidenceGNeedsVerificationWithCancel(t *testing.T) {
	for _, unknownReplay := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown-policy-%t", unknownReplay), func(t *testing.T) {
			fixture := newCancellationEvidenceFixture(t, "g_needs_verification", unknownReplay, false)
			lease := claimCancellationJob(t, fixture, "cancel-g-worker")
			requestCancellation(t, fixture)
			if err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusVerifying,
				jobcore.EventVerification, jobcore.ActorWorker, "", "execution_result_unknown"); err != nil {
				t.Fatal(err)
			}
			assertCancellationStateEvent(t, fixture, "verifying", "execution_result_unknown", "verification_started",
				"running", "verifying", "", "execution_result_unknown", "worker", 4)
			recovered, err := fixture.repository.ClaimRecoverable(context.Background(), jobcore.ClaimRequest{
				Owner: "cancel-g-reconciler", Token: uuid.New(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := transitionFencedError(fixture.repository, context.Background(), jobcore.Transition{
				JobID: recovered.ID, Token: recovered.Token, From: []jobcore.Status{jobcore.StatusVerifying},
				To: jobcore.StatusCancelled, Event: jobcore.EventCancelled, Actor: jobcore.ActorReconciler,
				ReasonCode: "effect_absent_verified", ErrorCode: "cancel_verified_safe", ReleaseLease: true,
			}); err != nil {
				t.Fatal(err)
			}
			assertCancellationStateEvent(t, fixture, "cancelled", "cancel_verified_safe", "cancelled",
				"verifying", "cancelled", "effect_absent_verified", "cancel_verified_safe", "reconciler", 6)
		})
	}
}

// Test H: ExecutePermanentFailure stays fail-closed and does not get
// reclassified as either safe no-effect cancellation or unknown effect.
func TestDurableJobCancellationEvidenceHPermanentFailureWithCancel(t *testing.T) {
	for _, unknownReplay := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown-policy-%t", unknownReplay), func(t *testing.T) {
			fixture := newCancellationEvidenceFixture(t, "h_permanent_failure", unknownReplay, false)
			lease := claimCancellationJob(t, fixture, "cancel-h-worker")
			requestCancellation(t, fixture)
			if err := transitionCancellationEvidence(t, fixture, lease, jobcore.StatusFailed,
				jobcore.EventFailed, jobcore.ActorWorker, "", "permanent_execution_failure"); err != nil {
				t.Fatal(err)
			}
			assertCancellationStateEvent(t, fixture, "failed", "permanent_execution_failure", "failed",
				"running", "failed", "", "permanent_execution_failure", "worker", 4)
		})
	}
}

// Reason matrix: requestcancel uses the immutable reason sequence, not policy or
// error_code. The same policy=true job is safe with a known proof even when
// its error code says unknown, and unsafe with an unknown proof otherwise.
func TestDurableJobCancellationEvidenceHReasonSequenceNotPolicyOrError(t *testing.T) {
	t.Run("known-reason-wins-over-error-code", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "h_known", true, false)
		lease := claimCancellationJob(t, fixture, "cancel-h-worker")
		enterCancellationRetryWait(t, fixture, lease, "execute_retryable_no_effect", "execution_result_unknown")
		requestCancellation(t, fixture)
		assertCancellationStateEvent(t, fixture, "cancelled", "", "cancelled",
			"retry_wait", "cancelled", "operator_cancelled", "", "service", 4)
	})

	t.Run("unknown-reason-wins-over-error-code", func(t *testing.T) {
		fixture := newCancellationEvidenceFixture(t, "h_unknown", true, false)
		lease := claimCancellationJob(t, fixture, "cancel-h-worker")
		enterCancellationRetryWait(t, fixture, lease, "effect_unknown_unverified", "known_no_effect")
		requestCancellation(t, fixture)
		assertCancellationStateEvent(t, fixture, "failed", "cancel_after_unknown_effect", "failed",
			"retry_wait", "failed", "operator_cancelled", "cancel_after_unknown_effect", "service", 4)
	})
}

// This uses a real row-lock wait: the cancellation transaction holds the job
// row until commit while a fenced transition is already blocked on that row.
// The transition must re-read the committed cancellation and atomically emit
// the worker cancellation event.
func TestDurableJobCancellationEvidenceLockWaitRechecksCommittedCancellation(t *testing.T) {
	fixture := newCancellationEvidenceFixture(t, "lock_wait", false, false)
	lease := claimCancellationJob(t, fixture, "cancel-lock-worker")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cancelTx, err := fixture.database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelTx.Rollback(context.Background())
	txStore, err := jobstore.NewJobTxStore(cancelTx)
	if err != nil {
		_ = cancelTx.Rollback(context.Background())
		t.Fatal(err)
	}
	if _, err := jobcore.RequestCancelTx(ctx, txStore, fixture.job.Job.ID, jobcore.ActorService, "operator_cancelled"); err != nil {
		t.Fatal(err)
	}

	connection, err := fixture.database.runtime.Acquire(ctx)
	if err != nil {
		_ = cancelTx.Rollback(context.Background())
		t.Fatal(err)
	}
	transitionDone := make(chan error, 1)
	defer func() {
		cancel()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = cancelTx.Rollback(cleanupCtx)
		cleanupCancel()
		connection.Release()
	}()
	var backendPID int
	if err := connection.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, transitionErr := connection.Exec(ctx, `SELECT * FROM public.control_transition_async_job_fenced(
			$1::uuid, 'running'::text, $2::uuid, 'retry_wait'::text,
			'retry_scheduled'::text, 0::integer, 'execute_retryable_no_effect'::text,
			'known_no_effect'::text, NULL::text, 'worker'::text, true::boolean
		)`, fixture.job.Job.ID, lease.Token)
		transitionDone <- transitionErr
	}()

	waitDeadline := time.Now().Add(2 * time.Second)
	for {
		var waitingForLock bool
		if err := fixture.database.owner.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE pid=$1 AND wait_event_type='Lock'
			)`, backendPID).Scan(&waitingForLock); err != nil {
			t.Fatal(err)
		}
		if waitingForLock {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("fenced transition did not enter a PostgreSQL row-lock wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cancelTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-transitionDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatalf("fenced transition did not finish: %v", ctx.Err())
	}
	assertCancellationStateEvent(t, fixture, "cancelled", "cancel_verified_safe", "cancelled",
		"running", "cancelled", "execute_retryable_no_effect", "cancel_verified_safe", "worker", 4)
}
