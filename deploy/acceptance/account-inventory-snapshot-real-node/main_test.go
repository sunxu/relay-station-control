package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

type fakeInvoker struct {
	calls int
	fail  int
	panic int
}

func (invoker *fakeInvoker) ListAccountInventory(_ context.Context, _ drivers.InventoryRequest) (drivers.InventoryObservation, error) {
	invoker.calls++
	if invoker.calls == invoker.panic {
		panic("identity-and-secret-canary")
	}
	if invoker.calls == invoker.fail {
		return failedObservation(), errors.New("identity-and-secret-canary")
	}
	return drivers.InventoryObservation{Result: drivers.ResultSuccess, Reason: drivers.ReasonNone}, nil
}

func TestSerialCooldownInvokerWaitsAfterBothRequestsIncludingFinalFailure(t *testing.T) {
	inner := &fakeInvoker{fail: 2}
	waits := make([]time.Duration, 0, 2)
	instances := []uuid.UUID{uuid.New(), uuid.New()}
	invoker := &serialCooldownInvoker{
		inner: inner, expectedInstances: instances,
		wait: func(duration time.Duration) { waits = append(waits, duration) },
	}
	for index, instanceID := range instances {
		_, err := invoker.ListAccountInventory(context.Background(), drivers.InventoryRequest{Target: drivers.NodeTarget{InstanceID: instanceID}})
		if (index == 0 && err != nil) || (index == 1 && err == nil) {
			t.Fatalf("call %d err = %v", index+1, err)
		}
	}
	if inner.calls != 2 || invoker.calls.Load() != 2 || len(waits) != 2 {
		t.Fatalf("calls inner=%d gate=%d waits=%v", inner.calls, invoker.calls.Load(), waits)
	}
	for _, wait := range waits {
		if wait != 10*time.Second {
			t.Fatalf("cooldown = %s", wait)
		}
	}
}

func TestSerialCooldownInvokerRecoversPanicAndStillWaits(t *testing.T) {
	instanceID := uuid.New()
	waits := 0
	invoker := &serialCooldownInvoker{
		inner: &fakeInvoker{panic: 1}, expectedInstances: []uuid.UUID{instanceID},
		wait: func(duration time.Duration) {
			if duration != requestCooldown {
				t.Fatalf("cooldown = %s", duration)
			}
			waits++
		},
	}
	observation, err := invoker.ListAccountInventory(context.Background(), drivers.InventoryRequest{Target: drivers.NodeTarget{InstanceID: instanceID}})
	if err == nil || observation.Result != drivers.ResultFailed || waits != 1 {
		t.Fatalf("panic result=%s err=%v waits=%d", observation.Result, err, waits)
	}
}

func TestSuccessSummaryHasFixedAggregateOnlyBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`^account_inventory_snapshot_real_node=success node_count=2 request_count=2 request_wait_seconds=10 runtime_mode_count=2 disk_fallback_mode_count=0 snapshot_items=6 provider_results=2 promotion_applied=2 promotion_skipped=0 management_writes=0 probe_requests=0 gateway_requests=0$`)
	if !pattern.MatchString(successSummary) {
		t.Fatalf("summary outside fixed schema: %q", successSummary)
	}
	for _, forbidden := range []string{"@", "file://", "http://", "127.0.0.1", "relay-phase0", expectedVersion, expectedCommitPrefix, firstInstanceID.String(), firstPollID.String()} {
		if strings.Contains(successSummary, forbidden) {
			t.Fatalf("summary contains forbidden detail %q", forbidden)
		}
	}
}
