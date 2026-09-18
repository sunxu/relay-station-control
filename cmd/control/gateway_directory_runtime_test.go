package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

type gatewayDirectoryRuntimeErrorService struct {
	reconcileErr error
	workErr      error
}

func (service gatewayDirectoryRuntimeErrorService) ReconcileTick(context.Context) ([]controlstore.GatewayDirectoryReconcileResult, error) {
	return nil, service.reconcileErr
}

func (service gatewayDirectoryRuntimeErrorService) WorkOnce(context.Context) ([]controlstore.GatewayDirectoryWorkResult, error) {
	return nil, service.workErr
}

func TestGatewayDirectoryRuntimeErrorsUseFixedRedactedLogFields(t *testing.T) {
	const (
		endpointCanary  = "https://endpoint-canary.invalid/private"
		referenceCanary = "file://reference-canary/reader"
		tokenCanary     = "token-canary-value"
		rawErrorCanary  = "raw-error-canary"
	)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	runtime := gatewayDirectoryRuntime{
		enabled: true,
		service: gatewayDirectoryRuntimeErrorService{
			reconcileErr: errors.New("reconcile failed " + endpointCanary + " " + referenceCanary + " " + tokenCanary + " " + rawErrorCanary),
			workErr:      errors.New("work failed " + endpointCanary + " " + referenceCanary + " " + tokenCanary + " " + rawErrorCanary),
		},
	}

	runtime.tick(context.Background(), logger)
	output := logs.String()
	if !strings.Contains(output, `"reason":"reconcile_failed"`) || !strings.Contains(output, `"reason":"work_failed"`) {
		t.Fatalf("missing fixed runtime failure classifications: %s", output)
	}
	if !strings.Contains(output, `"component":"gateway_directory"`) {
		t.Fatalf("missing fixed component classification: %s", output)
	}
	for _, canary := range []string{endpointCanary, referenceCanary, tokenCanary, rawErrorCanary} {
		if strings.Contains(output, canary) {
			t.Fatalf("runtime log leaked sensitive canary %q: %s", canary, output)
		}
	}
}

type fakeGatewayDirectoryService struct {
	reconcileCalls atomic.Int32
	workCalls      atomic.Int32
	called         chan struct{}
}

func (service *fakeGatewayDirectoryService) ReconcileTick(context.Context) ([]controlstore.GatewayDirectoryReconcileResult, error) {
	service.reconcileCalls.Add(1)
	return nil, nil
}

func (service *fakeGatewayDirectoryService) WorkOnce(context.Context) ([]controlstore.GatewayDirectoryWorkResult, error) {
	service.workCalls.Add(1)
	select {
	case service.called <- struct{}{}:
	default:
	}
	return nil, nil
}

func TestGatewayDirectoryRuntimeStopsOnCancellation(t *testing.T) {
	service := &fakeGatewayDirectoryService{called: make(chan struct{}, 1)}
	runtime := gatewayDirectoryRuntime{enabled: true, service: service}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runtime.run(ctx, nil)
		close(done)
	}()
	select {
	case <-service.called:
	case <-time.After(time.Second):
		t.Fatal("runtime did not execute a tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after cancellation")
	}
	if service.reconcileCalls.Load() == 0 || service.workCalls.Load() == 0 {
		t.Fatalf("runtime did not run reconcile then work: reconcile=%d work=%d", service.reconcileCalls.Load(), service.workCalls.Load())
	}
}
