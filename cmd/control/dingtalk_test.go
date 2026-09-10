package main

import (
	"testing"

	"github.com/sunxu/relay-station-control/internal/dingtalk"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func dingtalkConfigForTest(values map[string]string) (dingtalk.Config, error) {
	return dingtalk.LoadConfig(func(key string) string { return values[key] })
}

func TestDingTalkProductionRegistryKeepsKindWhenURLAbsent(t *testing.T) {
	config, err := dingtalkConfigForTest(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled() {
		t.Fatal("absent URL unexpectedly enabled DingTalk")
	}
	registry, err := newProductionJobRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Len() != 1 {
		t.Fatalf("registry length = %d, want 1", registry.Len())
	}
	entry := registry.Catalog()[0]
	if entry.Kind != "dingtalk_alert_delivery" || !entry.ReplaySafe ||
		!entry.AllowUnknownEffectReplay || !entry.AllowDirectSuccess || entry.AllowRollback {
		t.Fatalf("DingTalk catalog entry = %+v", entry)
	}
}

func TestDingTalkConfiguredCatalogMatchesDatabasePolicy(t *testing.T) {
	config, err := dingtalkConfigForTest(map[string]string{
		"DINGTALK_WEBHOOK_URL":    "https://oapi.dingtalk.com/robot/send?access_token=test",
		"DINGTALK_SIGNING_SECRET": "test-signing-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled() {
		t.Fatal("configured URL did not enable DingTalk")
	}
	registry, err := newProductionJobRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	if !jobCatalogMatches([]assetstore.JobKindPolicy{
		{JobKind: "dingtalk_alert_delivery", PayloadSchemaVersion: 1,
			Timeout: 10e9, LeaseDuration: 30e9, HeartbeatInterval: 5e9,
			MaxAttempts: 5, MaxVerificationAttempts: 1,
			ReplaySafe: true, AllowUnknownEffectReplay: true,
			AllowDirectSuccess: true, RollbackAllowed: false},
	}, registry.Catalog()) {
		t.Fatalf("configured catalog does not match database policy: %+v", registry.Catalog())
	}
}

func TestDingTalkInvalidHTTPConfigFailsClosed(t *testing.T) {
	_, err := dingtalkConfigForTest(map[string]string{"DINGTALK_WEBHOOK_URL": "http://example.com/?access_token=test"})
	if err == nil {
		t.Fatal("invalid HTTP config unexpectedly accepted")
	}
}

func TestDingTalkCatalogPolicyMismatchFailsClosed(t *testing.T) {
	config, err := dingtalkConfigForTest(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newProductionJobRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	policy := assetstore.JobKindPolicy{JobKind: "dingtalk_alert_delivery", PayloadSchemaVersion: 1,
		Timeout: 10e9, LeaseDuration: 30e9, HeartbeatInterval: 5e9,
		MaxAttempts: 5, MaxVerificationAttempts: 1, ReplaySafe: true,
		AllowUnknownEffectReplay: true, AllowDirectSuccess: true}
	for name, mutate := range map[string]func(*assetstore.JobKindPolicy){
		"timeout":        func(p *assetstore.JobKindPolicy) { p.Timeout++ },
		"unknown replay": func(p *assetstore.JobKindPolicy) { p.AllowUnknownEffectReplay = false },
		"direct success": func(p *assetstore.JobKindPolicy) { p.AllowDirectSuccess = false },
		"rollback":       func(p *assetstore.JobKindPolicy) { p.RollbackAllowed = true },
	} {
		t.Run(name, func(t *testing.T) {
			copy := policy
			mutate(&copy)
			if jobCatalogMatches([]assetstore.JobKindPolicy{copy}, registry.Catalog()) {
				t.Fatal("catalog policy mismatch was accepted")
			}
		})
	}
}
