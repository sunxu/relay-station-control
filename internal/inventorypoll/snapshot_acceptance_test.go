package inventorypoll

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

func TestSnapshotFakeDriverFinalizesClosedSuccessAndFailureMatrixOnce(t *testing.T) {
	tests := []struct {
		name             string
		providers        []string
		observation      func([]string) drivers.InventoryObservation
		wantItems        int
		wantDuplicates   int
		wantComplete     []bool
		wantTransport    bool
		wantContract     bool
		wantMode         drivers.InventoryMode
		wantUnidentified uint32
		wantUnsupported  uint32
		wantOutOfScope   uint32
	}{
		{
			name: "runtime complete", providers: []string{"antigravity"},
			observation: func(providers []string) drivers.InventoryObservation {
				observation := successfulObservation(providers)
				observation.Providers[0].Accounts = []drivers.AccountObservation{{
					Provider: "antigravity", Email: "runtime@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 1,
				}}
				return observation
			},
			wantItems: 1, wantComplete: []bool{true}, wantTransport: true, wantContract: true, wantMode: drivers.InventoryModeRuntime,
		},
		{
			name: "runtime complete empty", providers: []string{"antigravity"}, observation: successfulObservation,
			wantComplete: []bool{true}, wantTransport: true, wantContract: true, wantMode: drivers.InventoryModeRuntime,
		},
		{
			name: "disk fallback", providers: []string{"antigravity"},
			observation: func(providers []string) drivers.InventoryObservation {
				observation := successfulObservation(providers)
				observation.Mode = drivers.InventoryModeDiskFallback
				observation.Result = drivers.ResultDegraded
				return observation
			},
			wantComplete: []bool{false}, wantTransport: true, wantContract: true, wantMode: drivers.InventoryModeDiskFallback,
		},
		{
			name: "transport failed", providers: []string{"antigravity"},
			observation: func([]string) drivers.InventoryObservation {
				return drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonNetworkUnavailable}
			},
			wantComplete: []bool{false},
		},
		{
			name: "contract invalid", providers: []string{"antigravity"},
			observation: func([]string) drivers.InventoryObservation {
				return drivers.InventoryObservation{
					TransportSuccess: true, ResponseShapeValid: true,
					Result: drivers.ResultFailed, Reason: drivers.ReasonContractInvalid,
				}
			},
			wantComplete: []bool{false}, wantTransport: true,
		},
		{
			name: "missing identity", providers: []string{"antigravity"},
			observation: func(providers []string) drivers.InventoryObservation {
				observation := successfulObservation(providers)
				observation.Result = drivers.ResultDegraded
				observation.Providers[0].Accounts = []drivers.AccountObservation{{Provider: "antigravity", Email: " ", OccurrenceCount: 1}}
				return observation
			},
			wantComplete: []bool{false}, wantTransport: true, wantContract: true, wantMode: drivers.InventoryModeRuntime, wantUnidentified: 1,
		},
		{
			name: "duplicate identity", providers: []string{"antigravity"},
			observation: func(providers []string) drivers.InventoryObservation {
				observation := successfulObservation(providers)
				observation.Result = drivers.ResultDegraded
				observation.Providers[0].Accounts = []drivers.AccountObservation{
					{Provider: "antigravity", Email: "duplicate@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 2},
					{Provider: "antigravity", Email: "DUPLICATE@example.invalid", State: drivers.AccountStateError, OccurrenceCount: 2},
				}
				return observation
			},
			wantDuplicates: 1, wantComplete: []bool{false}, wantTransport: true, wantContract: true, wantMode: drivers.InventoryModeRuntime,
		},
		{
			name: "unsupported and out of scope are aggregate only", providers: []string{"antigravity"},
			observation: func(providers []string) drivers.InventoryObservation {
				observation := successfulObservation(providers)
				observation.Result = drivers.ResultDegraded
				observation.UnsupportedProviderCount = 1
				observation.OutOfScopeProviderCount = 1
				return observation
			},
			wantComplete: []bool{true}, wantTransport: true, wantContract: true, wantMode: drivers.InventoryModeRuntime,
			wantUnsupported: 1, wantOutOfScope: 1,
		},
		{
			name: "one duplicate provider does not block complete provider", providers: []string{"antigravity", "codex"},
			observation: func(providers []string) drivers.InventoryObservation {
				observation := successfulObservation(providers)
				observation.Result = drivers.ResultDegraded
				observation.Providers[0].Accounts = []drivers.AccountObservation{
					{Provider: "antigravity", Email: "duplicate@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 2},
					{Provider: "antigravity", Email: "DUPLICATE@example.invalid", State: drivers.AccountStateError, OccurrenceCount: 2},
				}
				observation.Providers[1].Accounts = []drivers.AccountObservation{{
					Provider: "codex", Email: "complete@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 1,
				}}
				return observation
			},
			wantItems: 1, wantDuplicates: 1, wantComplete: []bool{false, true},
			wantTransport: true, wantContract: true, wantMode: drivers.InventoryModeRuntime,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			driver := &fakeDriver{invoke: func(_ context.Context, _ drivers.InventoryRequest) (drivers.InventoryObservation, error) {
				return test.observation(test.providers), nil
			}}
			configuration, err := smallTestConfig().Validate()
			if err != nil {
				t.Fatal(err)
			}
			newWorker(repository, driver, configuration).execute(context.Background(), testClaim(test.providers...))
			finalizes := repository.finalizeSnapshot()
			if driver.calls.Load() != 1 || len(finalizes) != 1 {
				t.Fatalf("driver_calls=%d finalize_count=%d", driver.calls.Load(), len(finalizes))
			}
			finalize := finalizes[0]
			if len(finalize.SnapshotItems) != test.wantItems || len(finalize.Duplicates) != test.wantDuplicates ||
				finalize.Node.TransportSuccess != test.wantTransport || finalize.Node.ContractValid != test.wantContract ||
				finalize.Node.InventoryMode != test.wantMode || finalize.Node.UnidentifiedRecordCount != test.wantUnidentified ||
				finalize.Node.UnsupportedProviderCount != test.wantUnsupported || finalize.Node.OutOfScopeProviderCount != test.wantOutOfScope ||
				len(finalize.Providers) != len(test.wantComplete) {
				t.Fatalf("unexpected closed finalize projection: items=%d duplicates=%d node=%#v providers=%#v",
					len(finalize.SnapshotItems), len(finalize.Duplicates), finalize.Node, finalize.Providers)
			}
			for index, complete := range test.wantComplete {
				if finalize.Providers[index].SnapshotComplete != complete {
					t.Fatalf("provider %d completeness=%v, want %v", index, finalize.Providers[index].SnapshotComplete, complete)
				}
			}
		})
	}
}

func TestSnapshotCapacityOneTenFiftyUsesApprovedWorstCaseModel(t *testing.T) {
	tests := []struct {
		nodes       int
		concurrency int
	}{{nodes: 1, concurrency: 1}, {nodes: 10, concurrency: 10}, {nodes: 50, concurrency: 10}}

	for _, test := range tests {
		t.Run(capacityName(test.nodes), func(t *testing.T) {
			configuration := Config{MaxMonitoredNodes: test.nodes, Concurrency: test.concurrency, ScheduleLimit: test.nodes}
			validated, err := configuration.Validate()
			if err != nil {
				t.Fatal(err)
			}
			if validated.RequestTimeout() != 15*time.Second || validated.PollStartGrace() != 120*time.Second ||
				validated.LeaseDuration() != 30*time.Second || validated.LastBatchStart()+DefaultDispatchMargin >= validated.PollStartGrace() ||
				validated.LeaseDuration() < validated.RequestTimeout()+DefaultFinalizeMargin {
				t.Fatalf("unsafe approved capacity model: %#v", validated)
			}

			repository := &fakeRepository{}
			for range test.nodes {
				claim := testClaim("antigravity")
				claim.GraceRemaining = validated.PollStartGrace()
				repository.claims = append(repository.claims, claim)
			}
			release := make(chan struct{})
			driver := &fakeDriver{invoke: func(_ context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
				<-release
				return successfulObservation(request.ProviderPolicy.ActiveProviders), nil
			}}
			worker, err := NewWorker(repository, driver, configuration)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			waitFor(t, 2*time.Second, func() bool { return int(driver.calls.Load()) == test.concurrency })
			_, claims, _, _ := repository.counts()
			if claims != test.concurrency {
				t.Fatalf("claimed beyond available HTTP capacity: %d", claims)
			}
			close(release)
			waitFor(t, 3*time.Second, func() bool {
				_, _, finalizes, _ := repository.counts()
				return finalizes == test.nodes
			})
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if driver.maximum.Load() > int32(test.concurrency) || int(driver.calls.Load()) != test.nodes {
				t.Fatalf("maximum=%d calls=%d", driver.maximum.Load(), driver.calls.Load())
			}
		})
	}
}

func capacityName(nodes int) string {
	switch nodes {
	case 1:
		return "one_node"
	case 10:
		return "ten_nodes"
	case 50:
		return "fifty_nodes"
	default:
		return "invalid"
	}
}

type unknownFinalizeRepository struct {
	*fakeRepository
	mu       sync.Mutex
	attempts []FinalizeRequest
}

func (repository *unknownFinalizeRepository) FinalizeFenced(_ context.Context, request FinalizeRequest) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.attempts = append(repository.attempts, request)
	if len(repository.attempts) == 1 {
		return errors.New("opaque finalize outcome unavailable")
	}
	return nil
}

func (repository *unknownFinalizeRepository) finalizeAttempts() []FinalizeRequest {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]FinalizeRequest(nil), repository.attempts...)
}

func TestSnapshotUnknownFinalizeOutcomeReusesOriginalPollAndStopsAtSecondAttempt(t *testing.T) {
	first := testClaim("antigravity")
	second := first
	second.Attempt = 2
	repository := &unknownFinalizeRepository{fakeRepository: &fakeRepository{claims: []ClaimedRun{first, second}}}
	driver := &fakeDriver{}
	worker, err := NewWorker(repository, driver, smallTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitFor(t, 2*time.Second, func() bool { return len(repository.finalizeAttempts()) == 2 })
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	attempts := repository.finalizeAttempts()
	if driver.calls.Load() != 2 || len(attempts) != 2 || attempts[0].PollRunID != first.PollRunID ||
		attempts[1].PollRunID != first.PollRunID || attempts[0].FencingToken == attempts[1].FencingToken ||
		len(attempts[0].Providers) != 1 || len(attempts[1].Providers) != 1 ||
		attempts[0].Providers[0].Provider != attempts[1].Providers[0].Provider {
		t.Fatalf("bounded recovery changed poll/policy evidence: driver_calls=%d attempts=%#v", driver.calls.Load(), attempts)
	}
}

type independentDataPlane struct {
	requests atomic.Int32
	success  atomic.Int32
}

func (plane *independentDataPlane) request() bool {
	plane.requests.Add(1)
	plane.success.Add(1)
	return true
}

func TestSnapshotDatabaseOutageAndPollStopLeaveIndependentDataPlaneAvailable(t *testing.T) {
	repository := &fakeRepository{reconcileErr: errors.New("database unavailable")}
	driver := &fakeDriver{}
	service, err := NewService(repository, driver, smallTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	pollContext, stopPoll := context.WithCancel(context.Background())
	pollDone := make(chan error, 1)
	go func() { pollDone <- service.Run(pollContext) }()
	waitFor(t, time.Second, func() bool {
		_, _, _, reconciles := repository.counts()
		return reconciles >= 2
	})

	dataPlane := &independentDataPlane{}
	for range 25 {
		if !dataPlane.request() {
			t.Fatal("data-plane request failed during PostgreSQL outage")
		}
	}
	stopPoll()
	if err := <-pollDone; err != nil {
		t.Fatal(err)
	}
	for range 25 {
		if !dataPlane.request() {
			t.Fatal("data-plane request failed after poll stop")
		}
	}
	if dataPlane.requests.Load() != 50 || dataPlane.success.Load() != 50 || driver.calls.Load() != 0 {
		t.Fatalf("data-plane=%d/%d management_calls=%d", dataPlane.success.Load(), dataPlane.requests.Load(), driver.calls.Load())
	}
}
