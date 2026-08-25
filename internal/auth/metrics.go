package auth

import (
	"errors"
	"sort"
	"sync"
)

type MetricOperation string

const (
	MetricOperationLogin        MetricOperation = "login"
	MetricOperationMFA          MetricOperation = "mfa"
	MetricOperationRecoveryCode MetricOperation = "recovery_code"
	MetricOperationReauth       MetricOperation = "reauthenticate"
)

func (v MetricOperation) Valid() bool {
	return v == MetricOperationLogin || v == MetricOperationMFA || v == MetricOperationRecoveryCode || v == MetricOperationReauth
}

type MetricResult string

const (
	MetricResultSuccess     MetricResult = "success"
	MetricResultFailure     MetricResult = "failure"
	MetricResultRateLimited MetricResult = "rate_limited"
	MetricResultInternal    MetricResult = "internal_error"
)

func (v MetricResult) Valid() bool {
	return v == MetricResultSuccess || v == MetricResultFailure || v == MetricResultRateLimited || v == MetricResultInternal
}

type RateLimitDimension string

// FailureDimension is the persistence-facing name for the same closed
// account/source dimension used by rate-limit metrics.
type FailureDimension = RateLimitDimension

const (
	RateLimitAccount RateLimitDimension = "account"
	RateLimitSource  RateLimitDimension = "source"

	FailureDimensionAccount = RateLimitAccount
	FailureDimensionSource  = RateLimitSource
)

func (v RateLimitDimension) Valid() bool {
	return v == RateLimitAccount || v == RateLimitSource
}

type MetricDescriptor struct {
	Name   string
	Labels []string
}

var metricDescriptors = []MetricDescriptor{
	{Name: "relay_control_auth_attempts_total", Labels: []string{"environment", "operation", "result"}},
	{Name: "relay_control_auth_rate_limit_total", Labels: []string{"dimension", "environment"}},
	{Name: "relay_control_auth_active_sessions", Labels: []string{"environment"}},
}

func AuthMetricDescriptors() []MetricDescriptor {
	result := make([]MetricDescriptor, len(metricDescriptors))
	for index, descriptor := range metricDescriptors {
		result[index] = MetricDescriptor{Name: descriptor.Name, Labels: append([]string(nil), descriptor.Labels...)}
	}
	return result
}

type authMetricKey struct {
	environment Environment
	operation   MetricOperation
	result      MetricResult
}

type rateMetricKey struct {
	environment Environment
	dimension   RateLimitDimension
}

// AuthMetrics accepts only closed enum types. Snapshot is suitable for an
// exporter adapter without ever accepting caller-provided label names/values.
type AuthMetrics struct {
	mu             sync.RWMutex
	authAttempts   map[authMetricKey]uint64
	rateLimits     map[rateMetricKey]uint64
	activeSessions map[Environment]int64
}

func NewAuthMetrics() *AuthMetrics {
	return &AuthMetrics{
		authAttempts:   make(map[authMetricKey]uint64),
		rateLimits:     make(map[rateMetricKey]uint64),
		activeSessions: make(map[Environment]int64),
	}
}

func (m *AuthMetrics) RecordAttempt(environment Environment, operation MetricOperation, result MetricResult) error {
	if !environment.Valid() || !operation.Valid() || !result.Valid() {
		return errors.New("auth: invalid authentication metric label")
	}
	m.mu.Lock()
	m.authAttempts[authMetricKey{environment: environment, operation: operation, result: result}]++
	m.mu.Unlock()
	return nil
}

func (m *AuthMetrics) RecordRateLimit(environment Environment, dimension RateLimitDimension) error {
	if !environment.Valid() || !dimension.Valid() {
		return errors.New("auth: invalid rate-limit metric label")
	}
	m.mu.Lock()
	m.rateLimits[rateMetricKey{environment: environment, dimension: dimension}]++
	m.mu.Unlock()
	return nil
}

func (m *AuthMetrics) SetActiveSessions(environment Environment, count int64) error {
	if !environment.Valid() || count < 0 {
		return errors.New("auth: invalid active-session metric")
	}
	m.mu.Lock()
	m.activeSessions[environment] = count
	m.mu.Unlock()
	return nil
}

type AuthMetricSample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

func (m *AuthMetrics) Snapshot() []AuthMetricSample {
	m.mu.RLock()
	defer m.mu.RUnlock()
	samples := make([]AuthMetricSample, 0, len(m.authAttempts)+len(m.rateLimits)+len(m.activeSessions))
	for key, value := range m.authAttempts {
		samples = append(samples, AuthMetricSample{
			Name: "relay_control_auth_attempts_total",
			Labels: map[string]string{
				"environment": string(key.environment), "operation": string(key.operation), "result": string(key.result),
			},
			Value: float64(value),
		})
	}
	for key, value := range m.rateLimits {
		samples = append(samples, AuthMetricSample{
			Name: "relay_control_auth_rate_limit_total",
			Labels: map[string]string{
				"environment": string(key.environment), "dimension": string(key.dimension),
			},
			Value: float64(value),
		})
	}
	for environment, value := range m.activeSessions {
		samples = append(samples, AuthMetricSample{
			Name:   "relay_control_auth_active_sessions",
			Labels: map[string]string{"environment": string(environment)},
			Value:  float64(value),
		})
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].Name != samples[j].Name {
			return samples[i].Name < samples[j].Name
		}
		return canonicalLabels(samples[i].Labels) < canonicalLabels(samples[j].Labels)
	})
	return samples
}

func canonicalLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := ""
	for _, key := range keys {
		result += key + "=" + labels[key] + ";"
	}
	return result
}
