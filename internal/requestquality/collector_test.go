package requestquality

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testSource struct {
	pulls      atomic.Int32
	identities []Identity
	pop        func(context.Context, int32) ([][]byte, error)
	lookupErr  error
}

func (s *testSource) CurrentIdentities(context.Context, drivers.NodeTarget) ([]Identity, error) {
	return s.identities, s.lookupErr
}
func (s *testSource) PopUsage(ctx context.Context, _ drivers.NodeTarget) ([][]byte, error) {
	return s.pop(ctx, s.pulls.Add(1))
}

type testStore struct {
	insert  func(context.Context, []Event) error
	deletes atomic.Int32
}

func (s *testStore) InsertRequestEvents(ctx context.Context, e []Event) error {
	return s.insert(ctx, e)
}
func (s *testStore) DeleteOldRequestEvents(context.Context) (int64, error) {
	s.deletes.Add(1)
	return 0, nil
}

type testTargets struct {
	list func(context.Context) ([]drivers.NodeTarget, error)
}

func (t testTargets) ListRequestQualityTargets(ctx context.Context) ([]drivers.NodeTarget, error) {
	return t.list(ctx)
}
func collectorForTest(t *testing.T, store *testStore, source *testSource, targets testTargets) *Collector {
	t.Helper()
	c, err := NewCollector(store, source, targets, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	c.poll = time.Millisecond
	c.maxBackoff = 4 * time.Millisecond
	c.refresh = 2 * time.Millisecond
	c.timeout = time.Second
	return c
}
func usageFixture() []byte {
	return []byte(`{"request_id":"request-1","timestamp":"2026-09-08T00:00:00Z","provider":"openai","source":"a@example.invalid","model":"model","failed":false,"latency_ms":100}`)
}
func awaitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("collector did not terminate")
	}
}

func TestCollectorRetainsFailedBatchUntilCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testSource{pop: func(context.Context, int32) ([][]byte, error) {
		return [][]byte{usageFixture(), []byte(`malformed`)}, nil
	}}
	var calls int
	var hash string
	store := &testStore{insert: func(_ context.Context, events []Event) error {
		calls++
		if len(events) != 1 {
			t.Errorf("valid events=%d", len(events))
		}
		if source.pulls.Load() != 1 {
			t.Error("popped before commit")
		}
		if calls == 1 {
			hash = events[0].EventHash
			return errors.New("database unavailable")
		}
		if events[0].EventHash != hash {
			t.Error("retry changed event identity")
		}
		cancel()
		return nil
	}}
	c := collectorForTest(t, store, source, testTargets{})
	c.collectNode(ctx, drivers.NodeTarget{InstanceID: uuid.New()})
	if calls != 2 {
		t.Fatalf("insert calls=%d", calls)
	}
}
func TestCollectorCancellationStopsHTTPAndBackoff(t *testing.T) {
	for _, mode := range []string{"http", "backoff"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			source := &testSource{pop: func(ctx context.Context, _ int32) ([][]byte, error) {
				close(started)
				if mode == "http" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, errors.New("network")
			}}
			store := &testStore{insert: func(context.Context, []Event) error { t.Error("unexpected insert"); return nil }}
			c := collectorForTest(t, store, source, testTargets{})
			c.poll = time.Hour
			done := make(chan struct{})
			go func() { defer close(done); c.collectNode(ctx, drivers.NodeTarget{InstanceID: uuid.New()}) }()
			awaitDone(t, started)
			cancel()
			awaitDone(t, done)
			if source.pulls.Load() != 1 {
				t.Fatal("unexpected repeat pop")
			}
		})
	}
}
func TestCollectorLookupFailurePreservesUnresolved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testSource{lookupErr: errors.New("lookup unavailable"), pop: func(context.Context, int32) ([][]byte, error) {
		return [][]byte{[]byte(`{"timestamp":"2026-09-08T00:00:00Z","provider":"openai","auth_index":"deleted","model":"m","failed":true,"fail_status_code":500}`)}, nil
	}}
	wrote := false
	store := &testStore{insert: func(_ context.Context, events []Event) error {
		wrote = true
		if len(events) != 1 || events[0].AccountKey != nil || events[0].Success {
			t.Error("unresolved event lost or misattributed")
		}
		cancel()
		return nil
	}}
	collectorForTest(t, store, source, testTargets{}).collectNode(ctx, drivers.NodeTarget{InstanceID: uuid.New()})
	if !wrote {
		t.Fatal("event not persisted")
	}
}
func TestCollectorEmptyQueueDoesNotWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testSource{pop: func(context.Context, int32) ([][]byte, error) { cancel(); return nil, nil }}
	store := &testStore{insert: func(context.Context, []Event) error { t.Error("empty queue written"); return nil }}
	collectorForTest(t, store, source, testTargets{}).collectNode(ctx, drivers.NodeTarget{InstanceID: uuid.New()})
}
func TestCollectorSingleOwnerAndMonitoringRemoval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node := drivers.NodeTarget{InstanceID: uuid.New()}
	var active, maximum atomic.Int32
	var listCalls atomic.Int32
	started := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	source := &testSource{pop: func(ctx context.Context, _ int32) ([][]byte, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		once.Do(func() { close(started) })
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}}
	store := &testStore{insert: func(context.Context, []Event) error { return nil }}
	targets := testTargets{list: func(ctx context.Context) ([]drivers.NodeTarget, error) {
		n := listCalls.Add(1)
		if n > 2 {
			return nil, nil
		}
		return []drivers.NodeTarget{node}, nil
	}}
	c := collectorForTest(t, store, source, targets)
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	awaitDone(t, started)
	awaitDone(t, stopped)
	cancel()
	awaitDone(t, done)
	if maximum.Load() != 1 || source.pulls.Load() != 1 {
		t.Fatalf("owners=%d pulls=%d", maximum.Load(), source.pulls.Load())
	}
	if store.deletes.Load() == 0 {
		t.Error("retention not integrated")
	}
}
