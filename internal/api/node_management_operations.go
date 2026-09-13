package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/drivers"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

const nodeManagementBodyLimit int64 = 1 << 10

// NodeProbeRegistry is the only driver surface exposed to the API. Its
// implementation must be an immutable registry of fixed operation-specific
// drivers; callers cannot supply method, path, headers, query, or body.
type NodeProbeRegistry interface {
	Probe(context.Context, drivers.ProbeRequest) (drivers.ProbeObservation, error)
}

// NodeProbeAuthorizer owns the short database authorization transaction. The
// returned target snapshot is copied before the transaction is released; the
// API never holds this authorization lock while the driver performs HTTP.
type NodeProbeAuthorizer interface {
	AuthorizeNodeProbe(context.Context, uuid.UUID) (drivers.NodeTarget, error)
}

// NodeProbeAuditWriter persists only the bounded observation projection. It is
// intentionally separate from the Node asset mutation repository because a
// probe is non-durable remote observation and never creates a command receipt.
type NodeProbeAuditWriter interface {
	RecordNodeProbe(context.Context, uuid.UUID, uuid.UUID, string, string, map[string]any) error
}

// NodeMonitoringOperator owns the shared receipt, transaction, generation and
// Disable fence semantics. The HTTP package only authenticates and validates
// the small command envelope.
type NodeMonitoringOperator interface {
	Enable(context.Context, assetstore.NodeMonitoringCommand) (assetstore.NodeCommandResult, error)
	Disable(context.Context, assetstore.NodeMonitoringCommand) (assetstore.NodeCommandResult, error)
}

func (s *Server) GetNodeHealth(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId) {
	s.handleNodeHealth(w, r, instanceID)
}

func (s *Server) TestNodeConnection(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params TestNodeConnectionParams) {
	s.handleNodeConnectionTest(w, r, instanceID, string(params.XCSRFToken))
}

func (s *Server) EnableNodeMonitoring(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params EnableNodeMonitoringParams) {
	s.runNodeMonitoringCommand(w, r, instanceID, true, string(params.XCSRFToken))
}

func (s *Server) DisableNodeMonitoring(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params DisableNodeMonitoringParams) {
	s.runNodeMonitoringCommand(w, r, instanceID, false, string(params.XCSRFToken))
}

func (s *Server) handleNodeHealth(w http.ResponseWriter, r *http.Request, instanceID uuid.UUID) {
	s.prepare(w, r, true)
	session, ok := s.authorizeNodeRead(w, r)
	if !ok {
		return
	}
	if instanceID == uuid.Nil || r.URL.RawQuery != "" || !emptyNodeBody(w, r) {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return
	}
	s.runNodeProbe(w, r, instanceID, session.AdminID, "node.health")
}

func (s *Server) handleNodeConnectionTest(w http.ResponseWriter, r *http.Request, instanceID uuid.UUID, csrf string) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, csrf)
	if !ok {
		return
	}
	if instanceID == uuid.Nil || r.URL.RawQuery != "" || !decodeNodeEmptyObject(w, r, s) {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return
	}
	s.runNodeProbe(w, r, instanceID, session.AdminID, "node.connection_test")
}

func (s *Server) authorizeNodeRead(w http.ResponseWriter, r *http.Request) (authn.Session, bool) {
	session, ok := s.requireSession(w, r, false, "")
	if !ok {
		return authn.Session{}, false
	}
	if session.Role != "super_admin" {
		s.writeError(w, r, authn.ErrForbidden)
		return authn.Session{}, false
	}
	return session, true
}

func (s *Server) runNodeProbe(w http.ResponseWriter, r *http.Request, instanceID, actor uuid.UUID, action string) {
	if s.nodeProbeAuthorizer == nil || s.nodeProbeRegistry == nil || s.nodeProbeAuditor == nil {
		writeNodeOperationError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
		return
	}
	target, err := s.nodeProbeAuthorizer.AuthorizeNodeProbe(r.Context(), instanceID)
	if err != nil {
		writeNodeProbeError(w, r, s, err)
		return
	}
	started := time.Now()
	observation, probeErr := s.nodeProbeRegistry.Probe(r.Context(), drivers.ProbeRequest{Target: target})
	if probeErr != nil && (observation.Reason == drivers.ReasonCapabilityUnsupported || observation.Reason == drivers.ReasonNodeTypeUnsupported || observation.Reason == drivers.ReasonDriverContractMismatch) {
		writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCode("capability_unsupported"))
		return
	}
	latency := time.Since(started).Milliseconds()
	if latency < 0 {
		latency = 0
	}
	if latency > 30000 {
		latency = 30000
	}
	reason := boundedNodeProbeReason(observation.Reason)
	result := "failure"
	if observation.Reachable && observation.Result == drivers.ResultSuccess {
		result = "success"
		reason = "none"
	}
	if probeErr != nil && reason == "none" {
		reason = "network_unavailable"
	}
	details := map[string]any{
		"instance_id": instanceID.String(),
		"result":      result,
		"reason":      reason,
		"latency_ms":  latency,
	}
	if err = s.nodeProbeAuditor.RecordNodeProbe(r.Context(), actor, instanceID, s.requestID(r), action, details); err != nil {
		writeNodeOperationError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
		return
	}
	if s.assetMetrics != nil {
		metricResult := "failed"
		if result == "success" {
			metricResult = "healthy"
		} else if reason == "timeout" {
			metricResult = "timeout"
		}
		s.assetMetrics.RecordNodeProbe(strings.TrimPrefix(action, "node."), metricResult)
	}
	writeJSON(w, http.StatusOK, NodeProbeResult{
		Result: NodeProbeResultResult(result), Reachable: observation.Reachable, Reason: NodeProbeResultReason(reason), LatencyMs: int(latency),
	})
}

func (s *Server) runNodeMonitoringCommand(w http.ResponseWriter, r *http.Request, instanceID uuid.UUID, enable bool, csrf string) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, csrf)
	if !ok {
		return
	}
	if instanceID == uuid.Nil || r.URL.RawQuery != "" {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return
	}
	fields, ok := decodeNodeObject(w, r, s)
	if !ok {
		return
	}
	raw, exists := fields["command_id"]
	var commandID uuid.UUID
	if !exists || json.Unmarshal(raw, &commandID) != nil || commandID == uuid.Nil || len(fields) != 1 {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return
	}
	if s.nodeMonitoring == nil {
		writeNodeOperationError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
		return
	}
	command := assetstore.NodeMonitoringCommand{CommandID: commandID, ActorAdminID: session.AdminID, InstanceID: instanceID, RequestID: s.requestID(r)}
	var result assetstore.NodeCommandResult
	var err error
	if enable {
		result, err = s.nodeMonitoring.Enable(r.Context(), command)
	} else {
		result, err = s.nodeMonitoring.Disable(r.Context(), command)
	}
	if err != nil {
		if s.assetMetrics != nil {
			action := "monitoring_disable"
			if enable {
				action = "monitoring_enable"
			}
			s.assetMetrics.RecordNodeMutation(action, nodeMonitoringMetricResult(err))
		}
		switch {
		case errors.Is(err, assetstore.ErrInvalidNode):
			writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		case errors.Is(err, assetstore.ErrAssetNotFound), errors.Is(err, assetstore.ErrNodeNotFound):
			writeNodeOperationError(w, r, s, http.StatusNotFound, ErrorCodeAssetNotFound)
		case errors.Is(err, assetstore.ErrCommandConflict):
			writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeCommandConflict)
		case errors.Is(err, assetstore.ErrNodeRetired), errors.Is(err, assetstore.ErrMonitoringTargetRetired):
			writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeAssetRetired)
		case errors.Is(err, assetstore.ErrMonitoringDisableFenceConflict):
			writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeMonitoringStateConflict)
		case errors.Is(err, assetstore.ErrMonitoringFutureConflict):
			writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeMonitoringFutureConflict)
		case errors.Is(err, assetstore.ErrMonitoringBoundaryConflict):
			writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeMonitoringBoundaryConflict)
		case errors.Is(err, assetstore.ErrMonitoringStateConflict):
			writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeMonitoringStateConflict)
		case errors.Is(err, assetstore.ErrNodeGenerationExhausted):
			writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeGenerationExhausted)
		default:
			writeNodeOperationError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
		}
		return
	}
	metricResult := "success"
	if result.Replayed {
		metricResult = "replay"
	} else {
		var projection struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(result.Body, &projection) == nil && strings.HasPrefix(projection.Result, "already_") {
			metricResult = "noop"
		}
	}
	if s.assetMetrics != nil {
		action := "monitoring_disable"
		if enable {
			action = "monitoring_enable"
		}
		s.assetMetrics.RecordNodeMutation(action, metricResult)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.HTTPStatus)
	_, _ = w.Write(result.Body)
}

func nodeMonitoringMetricResult(err error) string {
	if errors.Is(err, assetstore.ErrInvalidNode) {
		return "invalid"
	}
	if errors.Is(err, assetstore.ErrCommandConflict) ||
		errors.Is(err, assetstore.ErrNodeRetired) ||
		errors.Is(err, assetstore.ErrMonitoringTargetRetired) ||
		errors.Is(err, assetstore.ErrMonitoringDisableFenceConflict) ||
		errors.Is(err, assetstore.ErrMonitoringFutureConflict) ||
		errors.Is(err, assetstore.ErrMonitoringBoundaryConflict) ||
		errors.Is(err, assetstore.ErrMonitoringStateConflict) ||
		errors.Is(err, assetstore.ErrNodeGenerationExhausted) {
		return "conflict"
	}
	return "unavailable"
}

func emptyNodeBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, nodeManagementBodyLimit)
	value, err := io.ReadAll(r.Body)
	return err == nil && len(value) == 0
}

func decodeNodeEmptyObject(w http.ResponseWriter, r *http.Request, s *Server) bool {
	fields, ok := decodeNodeObject(w, r, s)
	return ok && len(fields) == 0
}

func decodeNodeObject(w http.ResponseWriter, r *http.Request, s *Server) (map[string]json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, nodeManagementBodyLimit)
	decoder := json.NewDecoder(r.Body)
	first, err := decoder.Token()
	if err != nil {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return nil, false
	}
	delimiter, valid := first.(json.Delim)
	if !valid || delimiter != '{' {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return nil, false
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		key, keyOK := keyToken.(string)
		if keyErr != nil || !keyOK {
			writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
			return nil, false
		}
		if _, duplicate := fields[key]; duplicate {
			writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
			return nil, false
		}
		var raw json.RawMessage
		if err = decoder.Decode(&raw); err != nil {
			writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
			return nil, false
		}
		fields[key] = raw
	}
	if _, err = decoder.Token(); err != nil {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return nil, false
	}
	var extra json.RawMessage
	if decoder.Decode(&extra) != io.EOF {
		writeNodeOperationError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
		return nil, false
	}
	return fields, true
}

func boundedNodeProbeReason(reason drivers.Reason) string {
	value := string(reason)
	switch value {
	case "none", "http_status", "response_invalid", "response_too_large", "timeout", "cancelled", "network_unavailable", "dns_rejected", "tls_rejected", "redirect_rejected", "target_rejected":
		return value
	default:
		return "network_unavailable"
	}
}

func writeNodeProbeError(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	switch {
	case errors.Is(err, assetstore.ErrNodeNotFound), errors.Is(err, assetstore.ErrAssetNotFound):
		writeNodeOperationError(w, r, s, http.StatusNotFound, ErrorCodeAssetNotFound)
	case errors.Is(err, assetstore.ErrNodeRetired):
		writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCodeAssetRetired)
	case errors.Is(err, drivers.ErrCapabilityUnsupported), errors.Is(err, drivers.ErrNodeTypeUnsupported), errors.Is(err, drivers.ErrDriverContractMismatch):
		writeNodeOperationError(w, r, s, http.StatusConflict, ErrorCode("capability_unsupported"))
	default:
		writeNodeOperationError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
	}
}

func writeNodeOperationError(w http.ResponseWriter, r *http.Request, s *Server, status int, code ErrorCode) {
	requestID := r.Header.Get("X-Request-ID")
	if s != nil {
		requestID = s.requestID(r)
	}
	writeJSON(w, status, ErrorResponse{Code: code, Message: "The Node operation could not be completed.", RequestId: requestID})
}
