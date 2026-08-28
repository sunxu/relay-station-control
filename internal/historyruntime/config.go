package historyruntime

import (
	"errors"
	"time"
)

const (
	// These policy constants are deliberately not configurable. Changing one
	// changes the persisted history contract rather than runtime capacity.
	SnapshotEligibilityDelay = 72 * time.Hour
	HistoryRetention         = 30 * 24 * time.Hour
	CompleteCoverageBasisPts = 9500

	DefaultScanInterval           = 30 * time.Second
	DefaultClaimLease             = 30 * time.Second
	DefaultConcurrency            = 1
	DefaultDeleteBatchSize        = 500
	DefaultStatementTimeout       = 10 * time.Second
	DefaultDatabaseBackoffInitial = time.Second
	DefaultDatabaseBackoffMaximum = 30 * time.Second
	DefaultShutdownGrace          = 15 * time.Second

	MinimumScanInterval     = time.Second
	MaximumScanInterval     = 5 * time.Minute
	MinimumClaimLease       = 5 * time.Second
	MaximumClaimLease       = 5 * time.Minute
	MaximumConcurrency      = 8
	MaximumDeleteBatchSize  = 5000
	MinimumStatementTimeout = time.Second
	MaximumStatementTimeout = 30 * time.Second
	MinimumDatabaseBackoff  = 100 * time.Millisecond
	MaximumDatabaseBackoff  = 5 * time.Minute
	MaximumShutdownGrace    = time.Minute
)

var ErrInvalidConfig = errors.New("account inventory history configuration is invalid")

// Config contains capacity and lifecycle controls only. Eligibility,
// retention, and coverage publication policy are compile-time constants.
type Config struct {
	Enabled                bool
	ScanInterval           time.Duration
	ClaimLease             time.Duration
	Concurrency            int
	DeleteBatchSize        int
	StatementTimeout       time.Duration
	DatabaseBackoffInitial time.Duration
	DatabaseBackoffMaximum time.Duration
	ShutdownGrace          time.Duration
}

type ValidatedConfig struct {
	enabled                bool
	scanInterval           time.Duration
	claimLease             time.Duration
	concurrency            int
	deleteBatchSize        int
	statementTimeout       time.Duration
	databaseBackoffInitial time.Duration
	databaseBackoffMaximum time.Duration
	shutdownGrace          time.Duration
}

func (configuration Config) Validate() (ValidatedConfig, error) {
	configuration.applyDefaults()
	if configuration.ScanInterval < MinimumScanInterval || configuration.ScanInterval > MaximumScanInterval ||
		configuration.ClaimLease < MinimumClaimLease || configuration.ClaimLease > MaximumClaimLease ||
		configuration.Concurrency < 1 || configuration.Concurrency > MaximumConcurrency ||
		configuration.DeleteBatchSize < 1 || configuration.DeleteBatchSize > MaximumDeleteBatchSize ||
		configuration.StatementTimeout < MinimumStatementTimeout || configuration.StatementTimeout > MaximumStatementTimeout ||
		configuration.StatementTimeout >= configuration.ClaimLease ||
		configuration.DatabaseBackoffInitial < MinimumDatabaseBackoff || configuration.DatabaseBackoffInitial > MaximumDatabaseBackoff ||
		configuration.DatabaseBackoffMaximum < configuration.DatabaseBackoffInitial ||
		configuration.DatabaseBackoffMaximum > MaximumDatabaseBackoff ||
		configuration.ShutdownGrace < configuration.StatementTimeout || configuration.ShutdownGrace > MaximumShutdownGrace {
		return ValidatedConfig{}, ErrInvalidConfig
	}
	return ValidatedConfig{
		enabled: configuration.Enabled, scanInterval: configuration.ScanInterval,
		claimLease: configuration.ClaimLease, concurrency: configuration.Concurrency,
		deleteBatchSize: configuration.DeleteBatchSize, statementTimeout: configuration.StatementTimeout,
		databaseBackoffInitial: configuration.DatabaseBackoffInitial,
		databaseBackoffMaximum: configuration.DatabaseBackoffMaximum,
		shutdownGrace:          configuration.ShutdownGrace,
	}, nil
}

func (configuration *Config) applyDefaults() {
	if configuration.ScanInterval == 0 {
		configuration.ScanInterval = DefaultScanInterval
	}
	if configuration.ClaimLease == 0 {
		configuration.ClaimLease = DefaultClaimLease
	}
	if configuration.Concurrency == 0 {
		configuration.Concurrency = DefaultConcurrency
	}
	if configuration.DeleteBatchSize == 0 {
		configuration.DeleteBatchSize = DefaultDeleteBatchSize
	}
	if configuration.StatementTimeout == 0 {
		configuration.StatementTimeout = DefaultStatementTimeout
	}
	if configuration.DatabaseBackoffInitial == 0 {
		configuration.DatabaseBackoffInitial = DefaultDatabaseBackoffInitial
	}
	if configuration.DatabaseBackoffMaximum == 0 {
		configuration.DatabaseBackoffMaximum = DefaultDatabaseBackoffMaximum
	}
	if configuration.ShutdownGrace == 0 {
		configuration.ShutdownGrace = DefaultShutdownGrace
	}
}

func (configuration ValidatedConfig) Enabled() bool               { return configuration.enabled }
func (configuration ValidatedConfig) ScanInterval() time.Duration { return configuration.scanInterval }
func (configuration ValidatedConfig) ClaimLease() time.Duration   { return configuration.claimLease }
func (configuration ValidatedConfig) Concurrency() int            { return configuration.concurrency }
func (configuration ValidatedConfig) DeleteBatchSize() int        { return configuration.deleteBatchSize }
func (configuration ValidatedConfig) StatementTimeout() time.Duration {
	return configuration.statementTimeout
}
func (configuration ValidatedConfig) DatabaseBackoffInitial() time.Duration {
	return configuration.databaseBackoffInitial
}
func (configuration ValidatedConfig) DatabaseBackoffMaximum() time.Duration {
	return configuration.databaseBackoffMaximum
}
func (configuration ValidatedConfig) ShutdownGrace() time.Duration {
	return configuration.shutdownGrace
}
