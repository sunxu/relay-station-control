package historyruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRepositoryLoopsRetentionRoundUsesDependencyOrderAndBoundedTransactions(t *testing.T) {
	repository := &fakeHistoryRepository{}
	check := func(ctx context.Context, request RetentionRequest) {
		t.Helper()
		if request.Limit != testLoopsConfig().DeleteBatchSize {
			t.Fatalf("limit=%d", request.Limit)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > MinimumStatementTimeout || time.Until(deadline) <= 0 {
			t.Fatalf("transaction deadline=%v ok=%t", deadline, ok)
		}
	}
	repository.deletePollRetention = func(ctx context.Context, request RetentionRequest) (RetentionResult, error) {
		check(ctx, request)
		return RetentionResult{Processed: 1, DeletedRows: 2}, nil
	}
	repository.deleteRollupRowRetention = func(ctx context.Context, request RetentionRequest) (RetentionResult, error) {
		check(ctx, request)
		return RetentionResult{}, nil
	}
	repository.deleteRollupRunRetention = func(ctx context.Context, request RetentionRequest) (RetentionResult, error) {
		check(ctx, request)
		return RetentionResult{Processed: 2, DeletedRows: 2}, nil
	}
	repository.deleteCompactionRunRetention = func(ctx context.Context, request RetentionRequest) (RetentionResult, error) {
		check(ctx, request)
		return RetentionResult{}, nil
	}

	loops := newTestRepositoryLoops(t, repository)
	progressed, err := loops.runRetentionRound(context.Background(), context.Background())
	if err != nil || !progressed {
		t.Fatalf("progressed=%t error=%v", progressed, err)
	}
	want := []string{"retain-poll", "retain-rollup-row", "retain-rollup-run", "retain-compaction-run"}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events=%v want=%v", got, want)
	}
	if len(loops.workSlots) != 0 {
		t.Fatalf("occupied slots=%d", len(loops.workSlots))
	}
}

func TestRepositoryLoopsRetentionProgressImmediatelyStartsNextOrderedRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pollCalls := 0
	repository := &fakeHistoryRepository{
		deletePollRetention: func(context.Context, RetentionRequest) (RetentionResult, error) {
			pollCalls++
			if pollCalls == 1 {
				return RetentionResult{Processed: 1, DeletedRows: 1}, nil
			}
			return RetentionResult{}, nil
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	waits := 0
	loops.wait = func(_ context.Context, duration time.Duration) error {
		waits++
		if duration != loops.config.scanInterval {
			t.Fatalf("wait=%v", duration)
		}
		cancel()
		return context.Canceled
	}
	if err := loops.RunRetentionWorker(ctx, context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRound := []string{"retain-poll", "retain-rollup-row", "retain-rollup-run", "retain-compaction-run"}
	want := append(append([]string(nil), wantRound...), wantRound...)
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events=%v want=%v", got, want)
	}
	if waits != 1 {
		t.Fatalf("waits=%d", waits)
	}
}

func TestRepositoryLoopsRetentionDatabaseErrorsUseBoundedBackoff(t *testing.T) {
	for _, fixedErr := range []error{ErrHistoryDatabaseUnavailable, ErrHistoryStatementTimeout} {
		t.Run(fixedErr.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repository := &fakeHistoryRepository{
				deletePollRetention: func(context.Context, RetentionRequest) (RetentionResult, error) {
					return RetentionResult{}, fixedErr
				},
			}
			loops := newTestRepositoryLoops(t, repository)
			var waits []time.Duration
			loops.wait = func(_ context.Context, duration time.Duration) error {
				waits = append(waits, duration)
				if len(waits) == 2 {
					cancel()
					return context.Canceled
				}
				return nil
			}
			if err := loops.RunRetentionWorker(ctx, context.Background()); err != nil {
				t.Fatal(err)
			}
			want := []time.Duration{MinimumDatabaseBackoff, 2 * MinimumDatabaseBackoff}
			if fmt.Sprint(waits) != fmt.Sprint(want) {
				t.Fatalf("waits=%v want=%v", waits, want)
			}
			if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{"retain-poll", "retain-poll"}) {
				t.Fatalf("events=%v", got)
			}
		})
	}
}

func TestRepositoryLoopsRetentionUnknownCommitWaitsForNextScanWithoutReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repository := &fakeHistoryRepository{
		deletePollRetention: func(context.Context, RetentionRequest) (RetentionResult, error) {
			return RetentionResult{}, ErrHistoryCommitUnknown
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	waits := 0
	loops.wait = func(_ context.Context, duration time.Duration) error {
		waits++
		if duration != loops.config.scanInterval {
			t.Fatalf("wait=%v", duration)
		}
		cancel()
		return context.Canceled
	}
	if err := loops.RunRetentionWorker(ctx, context.Background()); err != nil {
		t.Fatal(err)
	}
	if waits != 1 || fmt.Sprint(repository.recorded()) != fmt.Sprint([]string{"retain-poll"}) {
		t.Fatalf("waits=%d events=%v", waits, repository.recorded())
	}
}

func TestRepositoryLoopsRetentionShutdownDrainsCurrentTransactionOnly(t *testing.T) {
	claimContext, stopClaims := context.WithCancel(context.Background())
	operationContext, stopOperations := context.WithCancel(context.Background())
	defer stopOperations()
	entered := make(chan struct{})
	release := make(chan struct{})
	repository := &fakeHistoryRepository{
		deletePollRetention: func(ctx context.Context, _ RetentionRequest) (RetentionResult, error) {
			close(entered)
			select {
			case <-release:
				return RetentionResult{Processed: 1, DeletedRows: 1}, nil
			case <-ctx.Done():
				return RetentionResult{}, ctx.Err()
			}
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	done := make(chan error, 1)
	go func() { done <- loops.RunRetentionWorker(claimContext, operationContext) }()
	<-entered
	stopClaims()
	select {
	case err := <-done:
		t.Fatalf("worker exited before transaction drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retention worker did not drain")
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{"retain-poll"}) {
		t.Fatalf("shutdown started another transaction: %v", got)
	}
}

func TestRepositoryLoopsRetentionShutdownWhileQueuedStartsNoTransaction(t *testing.T) {
	for range 100 {
		repository := &fakeHistoryRepository{}
		loops := newTestRepositoryLoops(t, repository)
		// Occupy the sole shared slot before starting retention, forcing its
		// first transaction to queue behind unrelated active work.
		loops.workSlots <- struct{}{}
		claimContext, stopClaims := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- loops.RunRetentionWorker(claimContext, context.Background()) }()
		time.Sleep(time.Millisecond)
		stopClaims()
		loops.releaseWorkSlot()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("queued retention worker did not stop")
		}
		if got := repository.recorded(); len(got) != 0 {
			t.Fatalf("shutdown started a queued retention transaction: %v", got)
		}
		if len(loops.workSlots) != 0 {
			t.Fatalf("shared slot leaked after shutdown: %d", len(loops.workSlots))
		}
	}
}

func TestRepositoryLoopsRetentionInvalidResultAndRepositoryFailureAreFatal(t *testing.T) {
	for _, test := range []struct {
		name       string
		poll       func(context.Context, RetentionRequest) (RetentionResult, error)
		wantEvents []string
	}{
		{
			name: "invalid result",
			poll: func(context.Context, RetentionRequest) (RetentionResult, error) {
				return RetentionResult{Processed: 1}, nil
			},
			wantEvents: []string{"retain-poll"},
		},
		{
			name: "fixed consistency failure stops later categories",
			poll: func(context.Context, RetentionRequest) (RetentionResult, error) {
				return RetentionResult{}, ErrHistoryStateInconsistent
			},
			wantEvents: []string{"retain-poll"},
		},
		{
			name: "raw repository failure is contained",
			poll: func(context.Context, RetentionRequest) (RetentionResult, error) {
				return RetentionResult{}, errors.New("protected-retention-marker")
			},
			wantEvents: []string{"retain-poll"},
		},
		{
			name: "no work error violates zero result contract",
			poll: func(context.Context, RetentionRequest) (RetentionResult, error) {
				return RetentionResult{}, ErrNoHistoryWork
			},
			wantEvents: []string{"retain-poll"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeHistoryRepository{deletePollRetention: test.poll}
			loops := newTestRepositoryLoops(t, repository)
			err := loops.RunRetentionWorker(context.Background(), context.Background())
			if !errors.Is(err, ErrRuntimeStopped) || !loops.runtimeStopped() {
				t.Fatalf("error=%v stopped=%t", err, loops.runtimeStopped())
			}
			if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint(test.wantEvents) {
				t.Fatalf("events=%v", got)
			}
		})
	}
}

func TestRepositoryLoopsRetentionSharesTotalConcurrencyLimit(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			var mu sync.Mutex
			active, peak := 0, 0
			entered := make(chan string, 2)
			release := make(chan struct{}, 2)
			enter := func(kind string) {
				mu.Lock()
				active++
				if active > peak {
					peak = active
				}
				mu.Unlock()
				entered <- kind
				<-release
				mu.Lock()
				active--
				mu.Unlock()
			}
			claimed := false
			repository := &fakeHistoryRepository{
				claim: func(context.Context, ClaimRequest) (*CompactionClaim, error) {
					mu.Lock()
					defer mu.Unlock()
					if claimed {
						return nil, ErrNoHistoryWork
					}
					claimed = true
					claim := validRuntimeCompactionClaim(CompactionSummarized, "")
					return &claim, nil
				},
				deleteBatch: func(context.Context, DeleteBatchRequest) (DeleteBatchResult, error) {
					enter("compaction")
					return DeleteBatchResult{}, nil
				},
				deletePollRetention: func(context.Context, RetentionRequest) (RetentionResult, error) {
					enter("retention")
					return RetentionResult{}, nil
				},
			}
			config := testLoopsConfig()
			config.Concurrency = concurrency
			loops, err := NewRepositoryLoops(config, repository)
			if err != nil {
				t.Fatal(err)
			}
			compactionContext, stopCompaction := context.WithCancel(context.Background())
			retentionContext, stopRetention := context.WithCancel(context.Background())
			compactionDone := make(chan error, 1)
			retentionDone := make(chan error, 1)
			go func() { compactionDone <- loops.RunWorker(compactionContext, context.Background()) }()
			go func() { retentionDone <- loops.RunRetentionWorker(retentionContext, context.Background()) }()

			first := <-entered
			if concurrency == 1 {
				select {
				case second := <-entered:
					t.Fatalf("%s and %s exceeded one shared slot", first, second)
				case <-time.After(20 * time.Millisecond):
				}
				release <- struct{}{}
			}
			var second string
			select {
			case second = <-entered:
			case <-time.After(time.Second):
				t.Fatal("second workload did not acquire shared capacity")
			}
			if first == second {
				t.Fatalf("workload starved: %s then %s", first, second)
			}
			stopCompaction()
			stopRetention()
			if concurrency == 2 {
				release <- struct{}{}
			}
			release <- struct{}{}
			for name, done := range map[string]<-chan error{"compaction": compactionDone, "retention": retentionDone} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("%s error=%v", name, err)
					}
				case <-time.After(time.Second):
					t.Fatalf("%s did not stop", name)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if peak != concurrency || len(loops.workSlots) != 0 {
				t.Fatalf("peak=%d occupied=%d", peak, len(loops.workSlots))
			}
		})
	}
}

func TestRetentionResultBounds(t *testing.T) {
	for _, result := range []RetentionResult{
		{},
		{Processed: 1, DeletedRows: 1},
		{Processed: 2, DeletedRows: 20},
	} {
		if !validRetentionResult(result, 2) {
			t.Fatalf("valid result rejected: %+v", result)
		}
	}
	for _, result := range []RetentionResult{
		{Processed: -1},
		{Processed: 3, DeletedRows: 3},
		{Processed: 1, DeletedRows: 0},
		{Processed: 0, DeletedRows: 1},
	} {
		if validRetentionResult(result, 2) {
			t.Fatalf("invalid result accepted: %+v", result)
		}
	}
}
