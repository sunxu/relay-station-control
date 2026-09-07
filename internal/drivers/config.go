package drivers

import (
	"errors"
	"net/netip"
	"time"
)

const (
	DefaultConnectTimeout         = 3 * time.Second
	DefaultRequestTimeout         = 15 * time.Second
	DefaultHealthResponseBytes    = int64(64 << 10)
	DefaultInventoryResponseBytes = int64(5 << 20)
	DefaultInventoryRecords       = 1000
	DefaultSecretBytes            = int64(4 << 10)
	DefaultSecretMappingBytes     = int64(64 << 10)

	minimumConnectTimeout         = 100 * time.Millisecond
	maximumConnectTimeout         = 10 * time.Second
	minimumRequestTimeout         = time.Second
	maximumRequestTimeout         = 30 * time.Second
	minimumHealthResponseBytes    = int64(128)
	maximumHealthResponseBytes    = int64(1 << 20)
	minimumInventoryResponseBytes = int64(1024)
	maximumInventoryResponseBytes = int64(5 << 20)
	minimumSecretBytes            = int64(1)
	maximumSecretBytes            = int64(64 << 10)
	maximumSecretMappingBytes     = int64(1 << 20)
)

var ErrInvalidManagementConfig = errors.New("node driver: invalid management configuration")

// ManagementConfig is startup configuration, not per-call input. Legacy network
// allowlist fields are accepted but ignored; limits and deadlines remain enforced.
type ManagementConfig struct {
	AllowedDNSNames           []string
	AllowedManagementCIDRs    []string
	AllowedPlainHTTPCIDRs     []string
	ConnectTimeout            time.Duration
	RequestTimeout            time.Duration
	MaxHealthResponseBytes    int64
	MaxInventoryResponseBytes int64
	MaxInventoryRecords       int
	MaxSecretBytes            int64
	MaxSecretMappingBytes     int64
}

type ValidatedManagementConfig struct {
	AllowedDNSNames           []string
	AllowedManagementPrefixes []netip.Prefix
	AllowedPlainHTTPPrefixes  []netip.Prefix
	ConnectTimeout            time.Duration
	RequestTimeout            time.Duration
	MaxHealthResponseBytes    int64
	MaxInventoryResponseBytes int64
	MaxInventoryRecords       int
	MaxSecretBytes            int64
	MaxSecretMappingBytes     int64
}

func (configuration ManagementConfig) Validate() (ValidatedManagementConfig, error) {
	configuration.applyDefaults()
	if configuration.ConnectTimeout < minimumConnectTimeout || configuration.ConnectTimeout > maximumConnectTimeout ||
		configuration.RequestTimeout < minimumRequestTimeout || configuration.RequestTimeout > maximumRequestTimeout ||
		configuration.RequestTimeout < configuration.ConnectTimeout ||
		configuration.MaxHealthResponseBytes < minimumHealthResponseBytes || configuration.MaxHealthResponseBytes > maximumHealthResponseBytes ||
		configuration.MaxInventoryResponseBytes < minimumInventoryResponseBytes || configuration.MaxInventoryResponseBytes > maximumInventoryResponseBytes ||
		configuration.MaxInventoryRecords < 1 || configuration.MaxInventoryRecords > DefaultInventoryRecords ||
		configuration.MaxSecretBytes < minimumSecretBytes || configuration.MaxSecretBytes > maximumSecretBytes ||
		configuration.MaxSecretMappingBytes < configuration.MaxSecretBytes || configuration.MaxSecretMappingBytes > maximumSecretMappingBytes {
		return ValidatedManagementConfig{}, ErrInvalidManagementConfig
	}

	return ValidatedManagementConfig{
		// Legacy allowlist fields remain source-compatible but are ignored by
		// the unified management transport policy.
		ConnectTimeout:            configuration.ConnectTimeout,
		RequestTimeout:            configuration.RequestTimeout,
		MaxHealthResponseBytes:    configuration.MaxHealthResponseBytes,
		MaxInventoryResponseBytes: configuration.MaxInventoryResponseBytes,
		MaxInventoryRecords:       configuration.MaxInventoryRecords,
		MaxSecretBytes:            configuration.MaxSecretBytes,
		MaxSecretMappingBytes:     configuration.MaxSecretMappingBytes,
	}, nil
}

func (configuration *ManagementConfig) applyDefaults() {
	if configuration.ConnectTimeout == 0 {
		configuration.ConnectTimeout = DefaultConnectTimeout
	}
	if configuration.RequestTimeout == 0 {
		configuration.RequestTimeout = DefaultRequestTimeout
	}
	if configuration.MaxHealthResponseBytes == 0 {
		configuration.MaxHealthResponseBytes = DefaultHealthResponseBytes
	}
	if configuration.MaxInventoryResponseBytes == 0 {
		configuration.MaxInventoryResponseBytes = DefaultInventoryResponseBytes
	}
	if configuration.MaxInventoryRecords == 0 {
		configuration.MaxInventoryRecords = DefaultInventoryRecords
	}
	if configuration.MaxSecretBytes == 0 {
		configuration.MaxSecretBytes = DefaultSecretBytes
	}
	if configuration.MaxSecretMappingBytes == 0 {
		configuration.MaxSecretMappingBytes = DefaultSecretMappingBytes
	}
}
