package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestLoadGatewayDirectoryRuntimeConfigDefaultsDisabled(t *testing.T) {
	t.Setenv(gatewayDirectoryEnabledEnvironment, "")
	t.Setenv(gatewayDirectorySecretMappingEnvironment, "")
	configuration, err := loadGatewayDirectoryRuntimeConfig()
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if configuration.enabled || configuration.mappingFile != "" {
		t.Fatalf("unexpected default configuration: %+v", configuration)
	}
}

func TestLoadGatewayDirectoryRuntimeConfigRequiresMappingWhenEnabled(t *testing.T) {
	t.Setenv(gatewayDirectoryEnabledEnvironment, "true")
	t.Setenv(gatewayDirectorySecretMappingEnvironment, "")
	if _, err := loadGatewayDirectoryRuntimeConfig(); err == nil {
		t.Fatal("expected enabled runtime without mapping to fail")
	}
}

func TestGatewayDirectoryRuntimeRejectsMalformedMappingAtStartup(t *testing.T) {
	directory := t.TempDir()
	mapping := filepath.Join(directory, "mapping.json")
	if err := os.WriteFile(mapping, []byte(`{"provider":"file","references":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), "postgres://invalid.invalid/test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	registry := prometheus.NewRegistry()
	_, err = newGatewayDirectoryRuntime(registry, pool, gatewayDirectoryRuntimeConfig{enabled: true, mappingFile: mapping})
	if err == nil || !strings.Contains(err.Error(), "secret configuration is invalid") {
		t.Fatalf("unexpected startup result: %v", err)
	}
}

func TestGatewayDirectoryRuntimeUsesIndependentSecretMappingAndMetrics(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "reader.token")
	mapping := filepath.Join(directory, "mapping.json")
	if err := os.WriteFile(secret, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapping, []byte(`{"provider":"file","references":[{"reference":"file://gateway-directory","path":"`+secret+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), "postgres://invalid.invalid/test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	registry := prometheus.NewRegistry()
	runtime, err := newGatewayDirectoryRuntime(registry, pool, gatewayDirectoryRuntimeConfig{enabled: true, mappingFile: mapping})
	if err != nil {
		t.Fatalf("construct runtime: %v", err)
	}
	if !runtime.enabled || runtime.service == nil || runtime.collector == nil {
		t.Fatalf("runtime was not fully constructed: %+v", runtime)
	}
	descriptions := make(chan *prometheus.Desc, 3)
	runtime.collector.Describe(descriptions)
	close(descriptions)
	for description := range descriptions {
		value := description.String()
		if strings.Contains(value, "instance_id") || strings.Contains(value, "endpoint") || strings.Contains(value, "secret") {
			t.Fatalf("directory metric has sensitive or identity label: %s", value)
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
	select {
	case service.called <- struct{}{}:
	default:
	}
	return nil, nil
}

func (service *fakeGatewayDirectoryService) WorkOnce(context.Context) ([]controlstore.GatewayDirectoryWorkResult, error) {
	service.workCalls.Add(1)
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
