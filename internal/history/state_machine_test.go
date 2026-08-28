package history

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	stateTestNow   = time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	stateTestToken = uuid.MustParse("10000000-0000-0000-0000-000000000001")
	staleTestToken = uuid.MustParse("20000000-0000-0000-0000-000000000002")
)

func TestCompactionTransitionMatrix(t *testing.T) {
	allowed := map[[2]CompactionStatus]bool{
		{CompactionPending, CompactionSummarized}:  true,
		{CompactionPending, CompactionFailed}:      true,
		{CompactionSummarized, CompactionDeleting}: true,
		{CompactionSummarized, CompactionFailed}:   true,
		{CompactionDeleting, CompactionDeleting}:   true,
		{CompactionDeleting, CompactionCompleted}:  true,
		{CompactionDeleting, CompactionFailed}:     true,
	}
	for _, from := range AllCompactionStatuses {
		for _, to := range AllCompactionStatuses {
			if got := CanCompactionTransition(from, to); got != allowed[[2]CompactionStatus{from, to}] {
				t.Errorf("CanCompactionTransition(%s, %s) = %v", from, to, got)
			}
		}
	}
}

func TestCompactionHappyPathAndCompletedImmutability(t *testing.T) {
	state, err := NewCompactionState().Claim(stateTestNow, "worker-a", stateTestToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.Summarize(stateTestNow, stateTestToken, 3)
	if err != nil || state.Status != CompactionSummarized || !state.hasLease() {
		t.Fatalf("summarize = %+v, %v", state, err)
	}
	state, err = state.RenewLease(stateTestNow.Add(10*time.Second), stateTestToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.BeginDeleting(stateTestNow.Add(10*time.Second), stateTestToken)
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.RecordDeleted(stateTestNow.Add(10*time.Second), stateTestToken, 2)
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.RecordDeleted(stateTestNow.Add(10*time.Second), stateTestToken, 1)
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.Complete(stateTestNow.Add(10*time.Second), stateTestToken)
	if err != nil || state.Status != CompactionCompleted || state.DeletedRows != 3 || state.hasLease() {
		t.Fatalf("complete = %+v, %v", state, err)
	}
	before := state
	if got, err := state.Claim(stateTestNow, "worker-c", uuid.New(), time.Minute); !errors.Is(err, ErrCompletedImmutable) || !reflect.DeepEqual(got, before) {
		t.Fatalf("completed claim changed state: %+v, %v", got, err)
	}
	if got, err := state.Complete(stateTestNow, uuid.New()); !errors.Is(err, ErrCompletedImmutable) || !reflect.DeepEqual(got, before) {
		t.Fatalf("completed replay changed state: %+v, %v", got, err)
	}
	for name, operation := range map[string]func() (CompactionState, error){
		"summarize":      func() (CompactionState, error) { return state.Summarize(stateTestNow, uuid.New(), 1) },
		"renew":          func() (CompactionState, error) { return state.RenewLease(stateTestNow, uuid.New(), time.Minute) },
		"begin deleting": func() (CompactionState, error) { return state.BeginDeleting(stateTestNow, uuid.New()) },
		"record deleted": func() (CompactionState, error) { return state.RecordDeleted(stateTestNow, uuid.New(), 1) },
		"fail":           func() (CompactionState, error) { return state.Fail(stateTestNow, uuid.New(), "internal") },
	} {
		t.Run("completed "+name, func(t *testing.T) {
			got, err := operation()
			if !errors.Is(err, ErrCompletedImmutable) || !reflect.DeepEqual(got, before) {
				t.Fatalf("completed mutation = %+v, %v", got, err)
			}
		})
	}
}

func TestCompactionFailedFromResumeMatrix(t *testing.T) {
	for _, test := range []struct {
		failedFrom CompactionStatus
		action     ResumeAction
	}{
		{failedFrom: CompactionPending, action: ResumeSummarize},
		{failedFrom: CompactionSummarized, action: ResumeDelete},
		{failedFrom: CompactionDeleting, action: ResumeDelete},
	} {
		t.Run(string(test.failedFrom), func(t *testing.T) {
			state := CompactionState{Status: CompactionFailed, FailedFrom: test.failedFrom, FailureReason: "database_unavailable"}
			if test.failedFrom != CompactionPending {
				state.SummaryCommitted = true
				state.SourceRows = 5
			}
			if test.failedFrom == CompactionDeleting {
				state.DeletedRows = 2
			}
			action, err := state.ResumeAction()
			if err != nil || action != test.action {
				t.Fatalf("ResumeAction = %s, %v", action, err)
			}
			claimed, err := state.Claim(stateTestNow, "worker", stateTestToken, time.Minute)
			if err != nil || claimed.Status != test.failedFrom || claimed.FailedFrom != "" || claimed.FailureReason != "" {
				t.Fatalf("failed claim = %+v, %v", claimed, err)
			}
		})
	}
}

func TestSummarizedAndDeletingCanNeverReaggregate(t *testing.T) {
	for _, status := range []CompactionStatus{CompactionSummarized, CompactionDeleting} {
		state := CompactionState{Status: status, SummaryCommitted: true, SourceRows: 10}
		claimed, err := state.Claim(stateTestNow, "worker", stateTestToken, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		before := claimed
		got, err := claimed.Summarize(stateTestNow, stateTestToken, 1)
		if !errors.Is(err, ErrInvalidStateTransition) || !reflect.DeepEqual(got, before) {
			t.Fatalf("%s reaggregation changed state: %+v, %v", status, got, err)
		}
	}
}

func TestCompactionStaleFenceAndActiveLeaseHaveZeroEffect(t *testing.T) {
	state, err := NewCompactionState().Claim(stateTestNow, "worker-a", stateTestToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	before := state
	for name, operation := range map[string]func() (CompactionState, error){
		"summarize": func() (CompactionState, error) { return state.Summarize(stateTestNow, staleTestToken, 4) },
		"fail":      func() (CompactionState, error) { return state.Fail(stateTestNow, staleTestToken, "internal") },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := operation()
			if !errors.Is(err, ErrStaleFencing) || !reflect.DeepEqual(got, before) {
				t.Fatalf("stale fence = %+v, %v", got, err)
			}
		})
	}
	if got, err := state.Claim(stateTestNow.Add(time.Second), "worker-b", staleTestToken, time.Minute); !errors.Is(err, ErrLeaseHeld) || !reflect.DeepEqual(got, before) {
		t.Fatalf("active lease replacement = %+v, %v", got, err)
	}
	reclaimed, err := state.Claim(stateTestNow.Add(time.Minute), "worker-b", staleTestToken, time.Minute)
	if err != nil || reclaimed.FencingToken != staleTestToken || reclaimed.Attempt != 2 {
		t.Fatalf("expired lease reclaim = %+v, %v", reclaimed, err)
	}
	if got, err := reclaimed.Summarize(stateTestNow.Add(time.Minute), stateTestToken, 4); !errors.Is(err, ErrStaleFencing) || !reflect.DeepEqual(got, reclaimed) {
		t.Fatalf("superseded fence = %+v, %v", got, err)
	}
	if got, err := reclaimed.RenewLease(stateTestNow.Add(2*time.Minute), staleTestToken, time.Minute); !errors.Is(err, ErrLeaseExpired) || !reflect.DeepEqual(got, reclaimed) {
		t.Fatalf("expired lease renewal = %+v, %v", got, err)
	}
}

func TestCompactionFailureEdgesAndDeleteConservation(t *testing.T) {
	pending, err := NewCompactionState().Claim(stateTestNow, "worker", stateTestToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := pending.Fail(stateTestNow, stateTestToken, "statement_timeout")
	if err != nil || failed.Status != CompactionFailed || failed.FailedFrom != CompactionPending || failed.hasLease() {
		t.Fatalf("pending failure = %+v, %v", failed, err)
	}

	summarized, _ := pending.Summarize(stateTestNow, stateTestToken, 2)
	deleting, err := summarized.BeginDeleting(stateTestNow, stateTestToken)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := deleting.Complete(stateTestNow, stateTestToken); !errors.Is(err, ErrInvalidStateTransition) || !reflect.DeepEqual(got, deleting) {
		t.Fatalf("incomplete delete completed: %+v, %v", got, err)
	}
	if got, err := deleting.RecordDeleted(stateTestNow, stateTestToken, 3); !errors.Is(err, ErrInvalidStateTransition) || !reflect.DeepEqual(got, deleting) {
		t.Fatalf("delete overcount changed state: %+v, %v", got, err)
	}

	failed, err = summarized.Fail(stateTestNow, stateTestToken, "database_unavailable")
	if err != nil || failed.FailedFrom != CompactionSummarized || !failed.SummaryCommitted || failed.DeletedRows != 0 {
		t.Fatalf("summarized failure = %+v, %v", failed, err)
	}
	deleting, _ = deleting.RecordDeleted(stateTestNow, stateTestToken, 1)
	failed, err = deleting.Fail(stateTestNow, stateTestToken, "statement_timeout")
	if err != nil || failed.FailedFrom != CompactionDeleting || !failed.SummaryCommitted || failed.DeletedRows != 1 {
		t.Fatalf("deleting failure = %+v, %v", failed, err)
	}
}

func TestUnknownCommitAlwaysReloadsThenUsesPersistedPhase(t *testing.T) {
	for knowledge, want := range map[CommitKnowledge]CommitResolution{
		CommitConfirmed:   ContinueFromPersistentState,
		RollbackConfirmed: RetrySameFencedOperation,
		CommitUnknown:     ReloadPersistentState,
	} {
		got, err := ResolveCommit(knowledge)
		if err != nil || got != want {
			t.Errorf("ResolveCommit(%s) = %s, %v", knowledge, got, err)
		}
	}
	if _, err := ResolveCommit("invalid"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("invalid commit knowledge error = %v", err)
	}
	persisted := CompactionState{Status: CompactionSummarized, SummaryCommitted: true, SourceRows: 9}
	action, err := persisted.ResumeAction()
	if err != nil || action != ResumeDelete {
		t.Fatalf("persisted summarized action = %s, %v", action, err)
	}
}

func TestRollupTransitionMatrix(t *testing.T) {
	allowed := map[[2]RollupStatus]bool{
		{RollupPending, RollupCompleted}: true,
		{RollupPending, RollupFailed}:    true,
		{RollupFailed, RollupPending}:    true,
	}
	for _, from := range AllRollupStatuses {
		for _, to := range AllRollupStatuses {
			if got := CanRollupTransition(from, to); got != allowed[[2]RollupStatus{from, to}] {
				t.Errorf("CanRollupTransition(%s, %s) = %v", from, to, got)
			}
		}
	}
}

func TestRollupFencingCompletionFailureAndImmutability(t *testing.T) {
	checksum := [32]byte{1, 2, 3}
	state, err := NewRollupState().Claim(stateTestNow, "worker", stateTestToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	before := state
	if got, err := state.Complete(stateTestNow, staleTestToken, 2, 2, checksum); !errors.Is(err, ErrStaleFencing) || !reflect.DeepEqual(got, before) {
		t.Fatalf("stale rollup completion = %+v, %v", got, err)
	}
	if got, err := state.Complete(stateTestNow, stateTestToken, 2, 1, checksum); !errors.Is(err, ErrInvalidStateTransition) || !reflect.DeepEqual(got, before) {
		t.Fatalf("partial rollup completion = %+v, %v", got, err)
	}
	completed, err := state.Complete(stateTestNow, stateTestToken, 2, 2, checksum)
	if err != nil || completed.Status != RollupCompleted || completed.hasLease() ||
		completed.ChecksumVersion != ChecksumVersionV1 || completed.SegmentChecksum != checksum {
		t.Fatalf("rollup completion = %+v, %v", completed, err)
	}
	if got, err := completed.Claim(stateTestNow, "worker", uuid.New(), time.Minute); !errors.Is(err, ErrCompletedImmutable) || !reflect.DeepEqual(got, completed) {
		t.Fatalf("completed rollup claim = %+v, %v", got, err)
	}
	if got, err := completed.RenewLease(stateTestNow, uuid.New(), time.Minute); !errors.Is(err, ErrCompletedImmutable) || !reflect.DeepEqual(got, completed) {
		t.Fatalf("completed rollup renew = %+v, %v", got, err)
	}
	if got, err := completed.Complete(stateTestNow, uuid.New(), 2, 2, checksum); !errors.Is(err, ErrCompletedImmutable) || !reflect.DeepEqual(got, completed) {
		t.Fatalf("completed rollup replay changed state = %+v, %v", got, err)
	}
	if got, err := completed.Fail(stateTestNow, uuid.New(), RollupFailureInternal); !errors.Is(err, ErrCompletedImmutable) || !reflect.DeepEqual(got, completed) {
		t.Fatalf("completed rollup failure changed state = %+v, %v", got, err)
	}

	state, _ = NewRollupState().Claim(stateTestNow, "worker", stateTestToken, time.Minute)
	failed, err := state.Fail(stateTestNow, stateTestToken, RollupFailureSegmentIncomplete)
	if err != nil || failed.Status != RollupFailed || failed.FailureReason == "" || failed.hasLease() {
		t.Fatalf("rollup failure = %+v, %v", failed, err)
	}
	recovered, err := failed.Claim(stateTestNow.Add(time.Second), "worker-retry", uuid.New(), time.Minute)
	if err != nil || recovered.Status != RollupPending || recovered.FailureReason != "" ||
		recovered.Attempt != 2 || !recovered.hasLease() {
		t.Fatalf("failed rollup recovery = %+v, %v", recovered, err)
	}
}

func TestRollupFailureRecoveryAllowlistIsFailClosed(t *testing.T) {
	recoverable := []string{
		RollupFailureSegmentIncomplete,
		RollupFailureStatementTimeout,
		RollupFailureLeaseExpired,
		RollupFailureDatabaseUnavailable,
	}
	for _, reason := range recoverable {
		failed := RollupState{Status: RollupFailed, FailureReason: reason}
		action, err := failed.ResumeAction()
		if err != nil || action != ResumeRollup {
			t.Errorf("ResumeAction(%s) = %s, %v", reason, action, err)
		}
		claimed, err := failed.Claim(stateTestNow, "worker", uuid.New(), time.Minute)
		if err != nil || claimed.Status != RollupPending || claimed.FailureReason != "" {
			t.Errorf("Claim(%s) = %+v, %v", reason, claimed, err)
		}
	}

	fatal := []string{
		RollupFailureSegmentCountMismatch,
		RollupFailureSegmentChecksumMismatch,
		RollupFailureActivationInconsistent,
		RollupFailureInternal,
	}
	for _, reason := range fatal {
		failed := RollupState{Status: RollupFailed, FailureReason: reason}
		action, err := failed.ResumeAction()
		if err != nil || action != ResumeBlocked {
			t.Errorf("ResumeAction(%s) = %s, %v", reason, action, err)
		}
		before := failed
		claimed, err := failed.Claim(stateTestNow, "worker", uuid.New(), time.Minute)
		if !errors.Is(err, ErrInvalidStateTransition) || !reflect.DeepEqual(claimed, before) {
			t.Errorf("Claim(%s) = %+v, %v", reason, claimed, err)
		}
	}

	claimed, err := NewRollupState().Claim(stateTestNow, "worker", stateTestToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := claimed.Fail(stateTestNow, stateTestToken, "not_allowlisted"); !errors.Is(err, ErrInvalidStateTransition) || !reflect.DeepEqual(got, claimed) {
		t.Fatalf("unknown failure reason changed state = %+v, %v", got, err)
	}
}

func TestRollupLeaseRenewReclaimAndStaleFence(t *testing.T) {
	state, err := NewRollupState().Claim(stateTestNow, "worker-a", stateTestToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := state.RenewLease(stateTestNow.Add(10*time.Second), stateTestToken, time.Minute)
	if err != nil || !renewed.LeaseExpiresAt.Equal(stateTestNow.Add(70*time.Second)) {
		t.Fatalf("renewed rollup = %+v, %v", renewed, err)
	}
	if got, err := renewed.Claim(stateTestNow.Add(20*time.Second), "worker-b", staleTestToken, time.Minute); !errors.Is(err, ErrLeaseHeld) || !reflect.DeepEqual(got, renewed) {
		t.Fatalf("active rollup lease replaced = %+v, %v", got, err)
	}
	reclaimed, err := renewed.Claim(stateTestNow.Add(70*time.Second), "worker-b", staleTestToken, time.Minute)
	if err != nil || reclaimed.Attempt != 2 || reclaimed.FencingToken != staleTestToken {
		t.Fatalf("reclaimed rollup = %+v, %v", reclaimed, err)
	}
	if got, err := reclaimed.RenewLease(stateTestNow.Add(71*time.Second), stateTestToken, time.Minute); !errors.Is(err, ErrStaleFencing) || !reflect.DeepEqual(got, reclaimed) {
		t.Fatalf("old rollup fence changed state = %+v, %v", got, err)
	}
}

func TestRollupUnknownCommitReloadsPersistentState(t *testing.T) {
	resolution, err := ResolveCommit(CommitUnknown)
	if err != nil || resolution != ReloadPersistentState {
		t.Fatalf("unknown rollup commit = %s, %v", resolution, err)
	}
	pendingAction, err := NewRollupState().ResumeAction()
	if err != nil || pendingAction != ResumeRollup {
		t.Fatalf("persisted pending rollup action = %s, %v", pendingAction, err)
	}
	failedAction, err := (RollupState{Status: RollupFailed, FailureReason: RollupFailureDatabaseUnavailable}).ResumeAction()
	if err != nil || failedAction != ResumeRollup {
		t.Fatalf("persisted failed rollup action = %s, %v", failedAction, err)
	}
	completed := RollupState{
		Status: RollupCompleted, ExpectedSegments: 2, CompletedSegments: 2,
		ChecksumVersion: ChecksumVersionV1, SegmentChecksum: [32]byte{9},
	}
	completedAction, err := completed.ResumeAction()
	if err != nil || completedAction != ResumeDone {
		t.Fatalf("persisted completed rollup action = %s, %v", completedAction, err)
	}
}

func TestChecksumSchemasV1FixOrderAndFields(t *testing.T) {
	for _, schemas := range [][]OrderedRowSchema{SourceChecksumReadSchemasV1(), SegmentChecksumReadSchemasV1()} {
		seen := map[string]bool{}
		for _, schema := range schemas {
			if schema.Version != HistoryRowSchemaVersionV1 || schema.Name == "" || len(schema.OrderBy) == 0 || len(schema.Fields) == 0 || seen[schema.Name] {
				t.Fatalf("invalid ordered schema: %+v", schema)
			}
			seen[schema.Name] = true
			if schema.Fields[0] != "row_kind" {
				t.Fatalf("schema %s has no row-kind domain separator", schema.Name)
			}
		}
	}
	schemas := SourceChecksumReadSchemasV1()
	schemas[0].Fields[0] = "mutated"
	if SourceChecksumReadSchemasV1()[0].Fields[0] != "row_kind" {
		t.Fatal("checksum schema caller mutated the fixed version-one definition")
	}
	segmentSchemas := SegmentChecksumReadSchemasV1()
	segmentSchemas[0].Fields[0] = "mutated"
	segmentSchemas[1].OrderBy[0] = "mutated"
	if SegmentChecksumReadSchemasV1()[0].Fields[0] != "row_kind" ||
		SegmentChecksumReadSchemasV1()[1].OrderBy[0] != "summary_date" {
		t.Fatal("segment checksum schema caller mutated the fixed version-one definition")
	}
}

func TestObserveChecksumV1MillionRowsReportsMaxRSSWithoutThreshold(t *testing.T) {
	observation, err := ObserveChecksumV1MaxRSS(1_000_000, func(index uint64) []CanonicalField {
		return []CanonicalField{TextField("snapshot"), Uint64Field(index), BoolField(index%2 == 0)}
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Rows != 1_000_000 || observation.Sum == ([32]byte{}) ||
		observation.BeforeMaxRSSBytes == 0 || observation.AfterMaxRSSBytes < observation.BeforeMaxRSSBytes {
		t.Fatalf("invalid RSS observation: %+v", observation)
	}
	t.Logf("history_checksum_rows=%d max_rss_before_bytes=%d max_rss_after_bytes=%d", observation.Rows, observation.BeforeMaxRSSBytes, observation.AfterMaxRSSBytes)
}
