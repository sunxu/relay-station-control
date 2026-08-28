package historyruntime

import (
	"errors"
	"testing"
	"time"
)

func TestConfigDefaultsAreDisabledAndBounded(t *testing.T) {
	configuration, err := (Config{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Enabled() || configuration.ScanInterval() != DefaultScanInterval ||
		configuration.ClaimLease() != DefaultClaimLease || configuration.Concurrency() != 1 ||
		configuration.DeleteBatchSize() != 500 || configuration.StatementTimeout() != 10*time.Second ||
		configuration.DatabaseBackoffInitial() != time.Second ||
		configuration.DatabaseBackoffMaximum() != 30*time.Second ||
		configuration.ShutdownGrace() != 15*time.Second {
		t.Fatalf("unexpected defaults: %#v", configuration)
	}
	if SnapshotEligibilityDelay != 72*time.Hour || HistoryRetention != 30*24*time.Hour ||
		CompleteCoverageBasisPts != 9500 {
		t.Fatal("fixed history policy changed")
	}
}

func TestConfigRejectsClosedInvalidMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"scan below minimum", func(config *Config) { config.ScanInterval = MinimumScanInterval - time.Nanosecond }},
		{"scan above maximum", func(config *Config) { config.ScanInterval = MaximumScanInterval + time.Nanosecond }},
		{"lease below minimum", func(config *Config) { config.ClaimLease = MinimumClaimLease - time.Nanosecond }},
		{"lease above maximum", func(config *Config) { config.ClaimLease = MaximumClaimLease + time.Nanosecond }},
		{"zero after defaults impossible concurrency", func(config *Config) { config.Concurrency = -1 }},
		{"concurrency above maximum", func(config *Config) { config.Concurrency = MaximumConcurrency + 1 }},
		{"negative batch", func(config *Config) { config.DeleteBatchSize = -1 }},
		{"batch above maximum", func(config *Config) { config.DeleteBatchSize = MaximumDeleteBatchSize + 1 }},
		{"timeout below minimum", func(config *Config) { config.StatementTimeout = MinimumStatementTimeout - time.Nanosecond }},
		{"timeout above maximum", func(config *Config) { config.StatementTimeout = MaximumStatementTimeout + time.Nanosecond }},
		{"timeout equals lease", func(config *Config) {
			config.StatementTimeout = 5 * time.Second
			config.ClaimLease = 5 * time.Second
		}},
		{"backoff initial below minimum", func(config *Config) { config.DatabaseBackoffInitial = MinimumDatabaseBackoff - time.Nanosecond }},
		{"backoff inverted", func(config *Config) {
			config.DatabaseBackoffInitial = 10 * time.Second
			config.DatabaseBackoffMaximum = 5 * time.Second
		}},
		{"backoff above maximum", func(config *Config) { config.DatabaseBackoffMaximum = MaximumDatabaseBackoff + time.Second }},
		{"shutdown below transaction timeout", func(config *Config) {
			config.StatementTimeout = 10 * time.Second
			config.ShutdownGrace = 9 * time.Second
		}},
		{"shutdown above maximum", func(config *Config) { config.ShutdownGrace = MaximumShutdownGrace + time.Second }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := Config{}
			test.mutate(&configuration)
			_, err := configuration.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestConfigAcceptsInclusiveCapacityBounds(t *testing.T) {
	for _, configuration := range []Config{
		{
			Enabled: true, ScanInterval: MinimumScanInterval, ClaimLease: MinimumClaimLease,
			Concurrency: 1, DeleteBatchSize: 1, StatementTimeout: time.Second,
			DatabaseBackoffInitial: MinimumDatabaseBackoff, DatabaseBackoffMaximum: MinimumDatabaseBackoff,
			ShutdownGrace: time.Second,
		},
		{
			Enabled: true, ScanInterval: MaximumScanInterval, ClaimLease: MaximumClaimLease,
			Concurrency: MaximumConcurrency, DeleteBatchSize: MaximumDeleteBatchSize,
			StatementTimeout:       MaximumStatementTimeout,
			DatabaseBackoffInitial: MaximumDatabaseBackoff, DatabaseBackoffMaximum: MaximumDatabaseBackoff,
			ShutdownGrace: MaximumShutdownGrace,
		},
	} {
		if _, err := configuration.Validate(); err != nil {
			t.Fatalf("inclusive bound rejected: %+v: %v", configuration, err)
		}
	}
}
