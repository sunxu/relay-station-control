package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/drivers/gatewaymanagement"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

const gatewayMutationBodyLimit = 16 << 10

func (s *Server) ListGatewayAssets(w http.ResponseWriter, r *http.Request, params ListGatewayAssetsParams) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	if s.gatewayAssets == nil || s.gatewayCursor == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	lifecycle := "active"
	if params.Lifecycle != nil {
		lifecycle = string(*params.Lifecycle)
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = string(*params.Cursor)
		if cursor == "" {
			gatewayError(w, r, s, assetstore.ErrInvalidGatewayCursor)
			return
		}
	}
	environment, err := s.assets.Environment(r.Context())
	if err != nil {
		gatewayError(w, r, s, err)
		return
	}
	decoded, err := s.gatewayCursor.Decode(cursor, lifecycle, environment.ID)
	if err != nil {
		gatewayError(w, r, s, err)
		return
	}
	page, err := s.gatewayAssets.List(r.Context(), lifecycle, decoded.After, limit, decoded.Generation)
	if err != nil {
		gatewayError(w, r, s, err)
		return
	}
	items := make([]GatewayAsset, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, *gatewayResponse(&page.Items[i]))
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		value, encodeErr := s.gatewayCursor.Encode(assetstore.GatewayCursor{After: page.Items[len(page.Items)-1].InstanceID, Environment: environment.ID, Lifecycle: lifecycle, Generation: page.Generation})
		if encodeErr != nil {
			gatewayError(w, r, s, encodeErr)
			return
		}
		next = &value
	}
	writeJSON(w, http.StatusOK, GatewayAssetListResponse{Items: items, NextCursor: next, GatewayCounts: GatewayCounts{Active: int(page.Counts.Active), Retired: int(page.Counts.Retired), Total: int(page.Counts.Total)}})
}

func (s *Server) GetGatewayAssetById(w http.ResponseWriter, r *http.Request, instanceID GatewayInstanceId) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	manager, ok := s.gatewayManager(w, r)
	if !ok {
		return
	}
	detail, err := manager.Detail(r.Context(), uuid.UUID(instanceID))
	if err != nil {
		gatewayError(w, r, s, err)
		return
	}
	result := GatewayAssetDetailResponse{Asset: *gatewayResponse(&detail.Asset)}
	if detail.Predecessor != nil {
		result.Predecessor = gatewayLineage(detail.Predecessor)
	}
	if detail.Successor != nil {
		result.Successor = gatewayLineage(detail.Successor)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) RegisterGatewayAsset(w http.ResponseWriter, r *http.Request, params RegisterGatewayAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, params.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "new_instance_id", "display_name", "management_endpoint", "reader_secret_ref")
	if !ok {
		return
	}
	command := assetstore.GatewayCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), Secret: secretPatch(body, "reader_secret_ref")}
	if !requiredUUID(body, "command_id", &command.CommandID) || !requiredUUID(body, "new_instance_id", &command.NewInstanceID) || !requiredString(body, "display_name", &command.DisplayName) || !requiredString(body, "management_endpoint", &command.ManagementEndpoint) {
		gatewayError(w, r, s, assetstore.ErrInvalidGateway)
		return
	}
	s.runGatewayCommand(w, r, "register", command, s.gatewayAssets.Register)
}

func (s *Server) EditGatewayAsset(w http.ResponseWriter, r *http.Request, instanceID GatewayInstanceId, params EditGatewayAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, params.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "expected_revision", "display_name", "management_endpoint", "reader_secret_ref")
	if !ok {
		return
	}
	command := assetstore.GatewayCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), InstanceID: uuid.UUID(instanceID), DisplayName: optionalString(body, "display_name"), ManagementEndpoint: optionalString(body, "management_endpoint"), Secret: secretPatch(body, "reader_secret_ref")}
	if !requiredUUID(body, "command_id", &command.CommandID) || !requiredRevision(body, "expected_revision", &command.ExpectedRevision) {
		gatewayError(w, r, s, assetstore.ErrInvalidGateway)
		return
	}
	s.runGatewayCommand(w, r, "edit", command, s.gatewayAssets.Edit)
}

func (s *Server) RetireGatewayAsset(w http.ResponseWriter, r *http.Request, instanceID GatewayInstanceId, params RetireGatewayAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, params.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "expected_revision")
	if !ok {
		return
	}
	command := assetstore.GatewayCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), InstanceID: uuid.UUID(instanceID), Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if !requiredUUID(body, "command_id", &command.CommandID) || !requiredRevision(body, "expected_revision", &command.ExpectedRevision) {
		gatewayError(w, r, s, assetstore.ErrInvalidGateway)
		return
	}
	s.runGatewayCommand(w, r, "retire", command, s.gatewayAssets.Retire)
}

func (s *Server) ReplaceGatewayAsset(w http.ResponseWriter, r *http.Request, instanceID GatewayInstanceId, params ReplaceGatewayAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, params.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "expected_revision", "new_instance_id", "display_name", "management_endpoint", "reader_secret_ref")
	if !ok {
		return
	}
	command := assetstore.GatewayCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), InstanceID: uuid.UUID(instanceID), Secret: secretPatch(body, "reader_secret_ref")}
	if !requiredUUID(body, "command_id", &command.CommandID) || !requiredUUID(body, "new_instance_id", &command.NewInstanceID) || !requiredRevision(body, "expected_revision", &command.ExpectedRevision) || !requiredString(body, "display_name", &command.DisplayName) || !requiredString(body, "management_endpoint", &command.ManagementEndpoint) {
		gatewayError(w, r, s, assetstore.ErrInvalidGateway)
		return
	}
	s.runGatewayCommand(w, r, "replace", command, s.gatewayAssets.Replace)
}

func (s *Server) GetGatewayHealth(w http.ResponseWriter, r *http.Request, instanceID GatewayInstanceId) {
	s.runGatewayProbe(w, r, uuid.UUID(instanceID), "gateway.health", false, "")
}

func (s *Server) TestGatewayConnection(w http.ResponseWriter, r *http.Request, instanceID GatewayInstanceId, params TestGatewayConnectionParams) {
	s.runGatewayProbe(w, r, uuid.UUID(instanceID), "gateway.connection_test", true, params.XCSRFToken)
}

func (s *Server) runGatewayProbe(w http.ResponseWriter, r *http.Request, instanceID uuid.UUID, action string, unsafe bool, csrf string) {
	s.prepare(w, r, true)
	var session authn.Session
	var ok bool
	if unsafe {
		session, ok = s.authorizeSuperAdmin(w, r, csrf)
		var empty map[string]json.RawMessage
		if ok && (!decodeJSONWithLimit(w, r, &empty, 128) || len(empty) != 0) {
			return
		}
	} else {
		session, ok = s.requireSession(w, r, false, "")
		if ok && session.Role != "super_admin" {
			s.writeError(w, r, authn.ErrForbidden)
			return
		}
	}
	if !ok {
		return
	}
	manager, ok := s.gatewayManager(w, r)
	if !ok {
		return
	}
	endpoint, err := manager.ProbeTarget(r.Context(), instanceID)
	if err != nil {
		gatewayError(w, r, s, err)
		return
	}
	client, err := gatewaymanagement.NewClient(endpoint)
	if err != nil {
		gatewayError(w, r, s, assetstore.ErrInvalidGateway)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gatewaymanagement.DefaultProbeTimeout)
	observation, probeErr := client.Probe(ctx)
	cancel()
	result := "failed"
	if observation.Healthy {
		result = "healthy"
	} else if observation.Reason == gatewaymanagement.ProbeReasonTimeout {
		result = "timeout"
	}
	if err = manager.RecordProbe(r.Context(), session.AdminID, instanceID, s.requestID(r), action, result); err != nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	if probeErr != nil {
		var typed *gatewaymanagement.ProbeError
		if errors.As(probeErr, &typed) && typed.Reason == gatewaymanagement.ProbeReasonTimeout {
			if s.assetMetrics != nil {
				s.assetMetrics.RecordGatewayProbe(strings.TrimPrefix(action, "gateway."), "timeout")
			}
			writeGatewayAPIError(w, r, s, http.StatusGatewayTimeout, ErrorCodeProbeTimeout)
		} else {
			if s.assetMetrics != nil {
				s.assetMetrics.RecordGatewayProbe(strings.TrimPrefix(action, "gateway."), "failed")
			}
			writeGatewayAPIError(w, r, s, http.StatusBadGateway, ErrorCodeProbeFailed)
		}
		return
	}
	if s.assetMetrics != nil {
		s.assetMetrics.RecordGatewayProbe(strings.TrimPrefix(action, "gateway."), "healthy")
	}
	writeJSON(w, http.StatusOK, GatewayProbeResult{InstanceId: instanceID, Result: Healthy, ObservedAt: time.Now().UTC()})
}

func (s *Server) gatewayManager(w http.ResponseWriter, r *http.Request) (assetstore.GatewayLifecycleManager, bool) {
	if s.gatewayAssets == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return nil, false
	}
	return s.gatewayAssets, true
}

func (s *Server) runGatewayCommand(w http.ResponseWriter, r *http.Request, action string, command assetstore.GatewayCommand, operation func(context.Context, assetstore.GatewayCommand) (assetstore.GatewayCommandResult, error)) {
	if s.gatewayAssets == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	result, err := operation(r.Context(), command)
	if err != nil {
		if s.assetMetrics != nil {
			s.assetMetrics.RecordGatewayMutation(action, gatewayMutationMetricResult(err))
		}
		gatewayError(w, r, s, err)
		return
	}
	if s.assetMetrics != nil {
		if result.Replayed {
			s.assetMetrics.RecordGatewayMutation(action, "replay")
		} else {
			s.assetMetrics.RecordGatewayMutation(action, "success")
		}
	}
	w.WriteHeader(result.HTTPStatus)
	_, _ = w.Write(append(result.Body, '\n'))
}

func gatewayMutationMetricResult(err error) string {
	if errors.Is(err, assetstore.ErrInvalidGateway) || errors.Is(err, assetstore.ErrInvalidGatewayEndpoint) || errors.Is(err, assetstore.ErrInvalidGatewaySecret) {
		return "invalid"
	}
	if errors.Is(err, assetstore.ErrReceiptKeyUnavailable) || errors.Is(err, assetstore.ErrReceiptEncodingUnknown) {
		return "unavailable"
	}
	if errors.Is(err, assetstore.ErrCommandConflict) || errors.Is(err, assetstore.ErrGatewayRetired) || errors.Is(err, assetstore.ErrCurrentGatewayExists) || errors.Is(err, assetstore.ErrGatewayIdentityExists) || errors.Is(err, assetstore.ErrStaleAssetRevision) || errors.Is(err, assetstore.ErrAssetRevisionExhausted) {
		return "conflict"
	}
	return "unavailable"
}

func decodeGatewayObject(w http.ResponseWriter, r *http.Request, allowed ...string) (map[string]json.RawMessage, bool) {
	var body map[string]json.RawMessage
	if !decodeJSONWithLimit(w, r, &body, gatewayMutationBodyLimit) {
		return nil, false
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key := range body {
		if _, exists := allow[key]; !exists {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorCodeValidationFailed, Message: "The request is invalid.", RequestId: w.Header().Get("X-Request-ID")})
			return nil, false
		}
	}
	return body, true
}

func requiredUUID(body map[string]json.RawMessage, key string, target *uuid.UUID) bool {
	raw, ok := body[key]
	if !ok || json.Unmarshal(raw, target) != nil || *target == uuid.Nil {
		return false
	}
	return true
}
func requiredRevision(body map[string]json.RawMessage, key string, target *int64) bool {
	var value string
	raw, ok := body[key]
	if !ok || json.Unmarshal(raw, &value) != nil {
		return false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return false
	}
	*target = parsed
	return true
}
func requiredString(body map[string]json.RawMessage, key string, target *assetstore.StringPatch) bool {
	raw, ok := body[key]
	var value string
	if !ok || json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" || len(value) > 1024 {
		return false
	}
	*target = assetstore.StringPatch{Present: true, Value: value}
	return true
}
func optionalString(body map[string]json.RawMessage, key string) assetstore.StringPatch {
	raw, ok := body[key]
	if !ok {
		return assetstore.StringPatch{}
	}
	var value string
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return assetstore.StringPatch{Present: true}
	}
	return assetstore.StringPatch{Present: true, Value: value}
}
func secretPatch(body map[string]json.RawMessage, key string) assetstore.SecretPatch {
	raw, ok := body[key]
	if !ok {
		return assetstore.SecretPatch{Operation: assetstore.SecretAbsent}
	}
	if string(raw) == "null" {
		return assetstore.SecretPatch{Operation: assetstore.SecretClear}
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" || len(value) > 1024 {
		return assetstore.SecretPatch{Operation: "invalid"}
	}
	return assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: value}
}
func gatewayLineage(value *assetstore.GatewayReplacement) *GatewayReplacementLineage {
	if value == nil {
		return nil
	}
	return &GatewayReplacementLineage{OldInstanceId: value.OldInstanceID, NewInstanceId: value.NewInstanceID, ReplacedAt: value.ReplacedAt, ReplacedBy: value.ReplacedBy, CommandId: value.CommandID}
}

func gatewayError(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	switch {
	case errors.Is(err, assetstore.ErrGatewayNotFound):
		writeGatewayAPIError(w, r, s, http.StatusNotFound, ErrorCodeAssetNotFound)
	case errors.Is(err, assetstore.ErrGatewayRetired):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeAssetRetired)
	case errors.Is(err, assetstore.ErrCurrentGatewayExists):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeCurrentGatewayExists)
	case errors.Is(err, assetstore.ErrGatewayIdentityExists):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeDuplicateIdentity)
	case errors.Is(err, assetstore.ErrStaleAssetRevision):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeStaleRevision)
	case errors.Is(err, assetstore.ErrAssetRevisionExhausted):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeRevisionExhausted)
	case errors.Is(err, assetstore.ErrCommandConflict):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeCommandConflict)
	case errors.Is(err, assetstore.ErrInvalidGatewayEndpoint):
		writeGatewayAPIError(w, r, s, http.StatusBadRequest, ErrorCodeInvalidEndpoint)
	case errors.Is(err, assetstore.ErrInvalidGatewaySecret):
		writeGatewayAPIError(w, r, s, http.StatusBadRequest, ErrorCodeSecretConfigurationInvalid)
	case errors.Is(err, assetstore.ErrInvalidGateway), errors.Is(err, assetstore.ErrInvalidGatewayCursor):
		writeGatewayAPIError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
	case errors.Is(err, assetstore.ErrGatewayCursorStale):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeCursorStale)
	case errors.Is(err, assetstore.ErrReceiptKeyUnavailable), errors.Is(err, assetstore.ErrReceiptEncodingUnknown):
		writeGatewayAPIError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
	default:
		writeGatewayAPIError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
	}
}
func writeGatewayAPIError(w http.ResponseWriter, r *http.Request, s *Server, status int, code ErrorCode) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: "The Gateway operation could not be completed.", RequestId: s.requestID(r)})
}
