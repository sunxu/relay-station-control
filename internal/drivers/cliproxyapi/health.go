package cliproxyapi

import (
	"encoding/json"
	"io"
)

const (
	DefaultHealthBodyLimit int64 = 64 << 10
	maximumHealthBodyLimit int64 = 1 << 20
)

type HealthReason string

const (
	HealthReasonOK               HealthReason = "ok"
	HealthReasonHTTPStatus       HealthReason = "http_status"
	HealthReasonResponseInvalid  HealthReason = "response_invalid"
	HealthReasonResponseTooLarge HealthReason = "response_too_large"
)

// HealthObservation deliberately reports only management-process reachability.
// It says nothing about account inventory, provider availability, or scheduling.
type HealthObservation struct {
	TransportSuccess bool
	ResponseValid    bool
	Reachable        bool
	Reason           HealthReason
}

// parseHealth parses a completed /healthz response without retaining its body.
// Callers must pass the HTTP status separately so non-200 bodies are never read.
func parseHealth(statusCode int, body io.Reader, maxBytes int64) HealthObservation {
	if statusCode != 200 {
		return HealthObservation{Reason: HealthReasonHTTPStatus}
	}
	observation := HealthObservation{TransportSuccess: true}
	if maxBytes <= 0 {
		maxBytes = DefaultHealthBodyLimit
	}
	if maxBytes > maximumHealthBodyLimit {
		observation.Reason = HealthReasonResponseInvalid
		return observation
	}

	encoded, tooLarge, err := readBounded(body, maxBytes)
	if tooLarge {
		observation.Reason = HealthReasonResponseTooLarge
		return observation
	}
	if err != nil {
		observation.Reason = HealthReasonResponseInvalid
		return observation
	}

	object, err := decodeJSONObject(encoded)
	if err != nil {
		observation.Reason = HealthReasonResponseInvalid
		return observation
	}
	var status string
	rawStatus, ok := object["status"]
	if !ok || json.Unmarshal(rawStatus, &status) != nil || status != "ok" {
		observation.Reason = HealthReasonResponseInvalid
		return observation
	}

	observation.ResponseValid = true
	observation.Reachable = true
	observation.Reason = HealthReasonOK
	return observation
}
