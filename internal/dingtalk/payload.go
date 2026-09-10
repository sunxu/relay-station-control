package dingtalk

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/jobs"
)

// Payload is the immutable, transition-time display snapshot carried by a
// DingTalk delivery job. It deliberately contains no credentials or dynamic
// health diagnostics.
type Payload struct {
	OccurrenceID    string   `json:"occurrence_id"`
	OccurrenceType  string   `json:"occurrence_type"`
	Transition      string   `json:"transition"`
	Reason          string   `json:"reason"`
	Severity        string   `json:"severity"`
	EnvironmentID   string   `json:"environment_id"`
	EnvironmentName string   `json:"environment_name"`
	AccountKey      string   `json:"account_key"`
	Email           string   `json:"email"`
	Provider        string   `json:"provider"`
	InstanceIDs     []string `json:"instance_ids"`
	NodeNames       []string `json:"node_names"`
	StartedAt       string   `json:"started_at"`
	TransitionedAt  string   `json:"transitioned_at"`
}

var (
	issueTypePattern  = regexp.MustCompile(`^(TOKEN_INVALID|ACCOUNT_BLOCKED|FORBIDDEN|CROSS_NODE_DUPLICATE_OWNERSHIP)$`)
	transitionPattern = regexp.MustCompile(`^(ACTIVE|RESOLVED)$`)
	reasonPattern     = regexp.MustCompile(`^(token_invalid|account_blocked|forbidden|cross_node_duplicate_ownership)$`)
	severityPattern   = regexp.MustCompile(`^(Critical|Warning)$`)
	providerPattern   = regexp.MustCompile(`^antigravity$`)
)

var payloadSchema = jobs.Schema{Fields: map[string]jobs.Field{
	"occurrence_id":   {Type: jobs.FieldUUID, Required: true},
	"occurrence_type": {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 32, Pattern: issueTypePattern},
	"transition":      {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 8, Pattern: transitionPattern},
	"reason":          {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 64, Pattern: reasonPattern},
	"severity":        {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 8, Pattern: severityPattern},
	"environment_id":  {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 128},
	// Asset names permit 100 Unicode characters; schema bounds count UTF-8
	// bytes. Account identity uses the existing Inventory 385-byte bound.
	"environment_name": {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 400},
	"account_key":      {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 385},
	"email":            {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 320},
	"provider":         {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 16, Pattern: providerPattern},
	"instance_ids":     {Type: jobs.FieldStringArray, Required: true, MinLength: 1, MaxLength: 128, MaxItems: 128},
	"node_names":       {Type: jobs.FieldStringArray, Required: true, MinLength: 1, MaxLength: 400, MaxItems: 128, AllowDuplicates: true, PreserveOrder: true},
	"started_at":       {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 64},
	"transitioned_at":  {Type: jobs.FieldString, Required: true, MinLength: 1, MaxLength: 64},
}}

func renderPayload(raw []byte) ([]byte, error) {
	canonical, err := payloadSchema.Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	var payload Payload
	if err := json.Unmarshal(canonical, &payload); err != nil {
		return nil, jobs.ErrInvalidPayload
	}
	if err := validatePayload(payload); err != nil {
		return nil, err
	}

	status := payload.OccurrenceType
	nodes := strings.Join(payload.NodeNames, ", ")
	var content string
	if payload.Transition == "RESOLVED" {
		content = fmt.Sprintf("[Resolved] %s\n\nAccount: %s\nProvider: %s\nNode: %s\nStarted: %s\nResolved: %s\nOccurrence ID: %s",
			status, payload.Email, payload.Provider, nodes, payload.StartedAt, payload.TransitionedAt, payload.OccurrenceID)
	} else {
		content = fmt.Sprintf("[%s] %s\n\nAccount: %s\nProvider: %s\nNode: %s\nSince: %s\nOccurrence ID: %s",
			payload.Severity, status, payload.Email, payload.Provider, nodes, payload.StartedAt, payload.OccurrenceID)
	}
	return json.Marshal(struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
	}{MsgType: "text", Text: struct {
		Content string `json:"content"`
	}{Content: content}})
}

func validateCanonicalPayload(raw []byte) error {
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return jobs.ErrInvalidPayload
	}
	return validatePayload(payload)
}

func validatePayload(payload Payload) error {
	validTuples := map[string]struct {
		reason   string
		severity string
	}{
		"TOKEN_INVALID":                  {reason: "token_invalid", severity: "Critical"},
		"ACCOUNT_BLOCKED":                {reason: "account_blocked", severity: "Critical"},
		"FORBIDDEN":                      {reason: "forbidden", severity: "Warning"},
		"CROSS_NODE_DUPLICATE_OWNERSHIP": {reason: "cross_node_duplicate_ownership", severity: "Critical"},
	}
	if tuple, ok := validTuples[payload.OccurrenceType]; !ok || payload.Reason != tuple.reason || payload.Severity != tuple.severity {
		return jobs.ErrInvalidPayload
	}
	started, err := parseCanonicalTimestamp(payload.StartedAt)
	if err != nil {
		return jobs.ErrInvalidPayload
	}
	transitioned, err := parseCanonicalTimestamp(payload.TransitionedAt)
	if err != nil || transitioned.Before(started) {
		return jobs.ErrInvalidPayload
	}
	if payload.InstanceIDs == nil || payload.NodeNames == nil || len(payload.InstanceIDs) != len(payload.NodeNames) {
		return jobs.ErrInvalidPayload
	}
	// A resolved duplicate may have zero remaining confirmed owners. Preserve
	// that authoritative membership instead of substituting historical nodes.
	if len(payload.InstanceIDs) == 0 && (payload.OccurrenceType != "CROSS_NODE_DUPLICATE_OWNERSHIP" || payload.Transition != "RESOLVED") {
		return jobs.ErrInvalidPayload
	}
	for _, instanceID := range payload.InstanceIDs {
		parsed, err := uuid.Parse(instanceID)
		if err != nil || parsed == uuid.Nil || parsed.String() != instanceID {
			return jobs.ErrInvalidPayload
		}
	}
	return nil
}

func parseCanonicalTimestamp(raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || parsed.UTC().Format(time.RFC3339Nano) != raw {
		return time.Time{}, jobs.ErrInvalidPayload
	}
	return parsed, nil
}
