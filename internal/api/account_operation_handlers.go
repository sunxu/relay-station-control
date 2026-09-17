package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	accountadmin "github.com/sunxu/relay-station-control/internal/accountadmin"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

const accountMultipartLimit int64 = 1073152

type accountMultipartFailure string

const (
	accountMultipartInvalid  accountMultipartFailure = "invalid_request"
	accountMultipartTooLarge accountMultipartFailure = "upload_too_large"
)

type accountCommandRequest struct {
	CommandID      uuid.UUID
	NodeInstanceID uuid.UUID
	AccountKey     string
}

func (s *Server) DisableAccountOperation(w http.ResponseWriter, r *http.Request, p DisableAccountOperationParams) {
	s.executeAccountJSON(w, r, p.XCSRFToken, assetstore.AccountDisable, []string{"command_id", "node_instance_id", "account_key"}, false)
}

func (s *Server) EnableAccountOperation(w http.ResponseWriter, r *http.Request, p EnableAccountOperationParams) {
	s.executeAccountJSON(w, r, p.XCSRFToken, assetstore.AccountEnable, []string{"command_id", "node_instance_id", "account_key"}, false)
}

func (s *Server) RemoveAccountOperation(w http.ResponseWriter, r *http.Request, p RemoveAccountOperationParams) {
	s.executeAccountJSON(w, r, p.XCSRFToken, assetstore.AccountRemove, []string{"command_id", "node_instance_id", "account_key", "confirmation"}, true)
}

func (s *Server) executeAccountJSON(w http.ResponseWriter, r *http.Request, csrf CsrfToken, kind assetstore.AccountOperationKind, allowed []string, remove bool) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, csrf)
	if !ok {
		return
	}
	if s.accountOperations == nil || s.accountOperationReader == nil {
		s.writeAccountError(w, r, "service_unavailable", http.StatusServiceUnavailable)
		return
	}
	fields, ok := decodeAccountObject(w, r, 8192, allowed...)
	if !ok {
		return
	}
	command, ok := parseAccountCommand(fields, kind, remove)
	if !ok {
		s.writeAccountError(w, r, "invalid_request", http.StatusBadRequest)
		return
	}
	command.ActorAdminID = session.AdminID
	command.RequestID = s.requestID(r)
	intent, err := s.accountOperations.CanonicalIntentForCommand(command)
	if err != nil {
		s.writeAccountError(w, r, accountErrorCode(err), accountErrorStatus(err))
		return
	}
	command.CanonicalIntent = intent
	operation, err := s.accountOperations.Execute(r.Context(), command)
	if err == nil && s.writeStoredAccountReceipt(r, w, command.CommandID, session.AdminID) {
		return
	}
	s.writeAccountOperationResult(w, r, operation, err)
}

func (s *Server) UploadNewAccountOperation(w http.ResponseWriter, r *http.Request, p UploadNewAccountOperationParams) {
	s.executeAccountUpload(w, r, p.XCSRFToken, assetstore.AccountUploadNew)
}

func (s *Server) ReplaceExistingAccountOperation(w http.ResponseWriter, r *http.Request, p ReplaceExistingAccountOperationParams) {
	s.executeAccountUpload(w, r, p.XCSRFToken, assetstore.AccountReplaceExisting)
}

func (s *Server) executeAccountUpload(w http.ResponseWriter, r *http.Request, csrf CsrfToken, kind assetstore.AccountOperationKind) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, csrf)
	if !ok {
		return
	}
	if s.accountOperations == nil || s.accountOperationReader == nil {
		s.writeAccountError(w, r, "service_unavailable", http.StatusServiceUnavailable)
		return
	}
	request, credential, failure := parseAccountMultipart(w, r)
	if failure != "" {
		status := http.StatusBadRequest
		if failure == accountMultipartTooLarge {
			status = http.StatusRequestEntityTooLarge
		}
		s.writeAccountError(w, r, string(failure), status)
		return
	}
	command, ok := parseAccountCommand(request, kind, false)
	if !ok {
		s.writeAccountError(w, r, "invalid_request", http.StatusBadRequest)
		return
	}
	command.ActorAdminID = session.AdminID
	command.RequestID = s.requestID(r)
	command.Credential = credential
	intent, err := s.accountOperations.CanonicalIntentForCommand(command)
	if err != nil {
		s.writeAccountError(w, r, accountErrorCode(err), accountErrorStatus(err))
		return
	}
	command.CanonicalIntent = intent
	operation, err := s.accountOperations.Execute(r.Context(), command)
	if err == nil && s.writeStoredAccountReceipt(r, w, command.CommandID, session.AdminID) {
		return
	}
	s.writeAccountOperationResult(w, r, operation, err)
}

func (s *Server) GetAccountOperation(w http.ResponseWriter, r *http.Request, commandID openapi_types.UUID) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	if s.accountOperationReader == nil {
		s.writeAccountError(w, r, "service_unavailable", http.StatusServiceUnavailable)
		return
	}
	op, err := s.accountOperationReader.Operation(r.Context(), uuid.UUID(commandID))
	if err != nil {
		s.writeAccountError(w, r, "operation_not_found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, AccountOperationResponse{Operation: accountOperationProjection(op)})
}

func (s *Server) LifecycleOverrideAccountOperation(w http.ResponseWriter, r *http.Request, target openapi_types.UUID, p LifecycleOverrideAccountOperationParams) {
	s.executeAccountOverride(w, r, p.XCSRFToken, uuid.UUID(target), true)
}

func (s *Server) SameAccountOverrideAccountOperation(w http.ResponseWriter, r *http.Request, target openapi_types.UUID, p SameAccountOverrideAccountOperationParams) {
	s.executeAccountOverride(w, r, p.XCSRFToken, uuid.UUID(target), false)
}

func (s *Server) executeAccountOverride(w http.ResponseWriter, r *http.Request, csrf CsrfToken, target uuid.UUID, lifecycle bool) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, csrf)
	if !ok {
		return
	}
	fields, ok := decodeAccountObject(w, r, 8192, "command_id", "reason", "confirmation", "detail")
	if !ok {
		return
	}
	commandID, valid := parseUUIDField(fields, "command_id")
	reason, reasonOK := parseStringField(fields, "reason")
	confirmation, confirmationOK := parseStringField(fields, "confirmation")
	detail, detailOK := parseOptionalStringField(fields, "detail")
	if !valid || !reasonOK || !confirmationOK || !detailOK || target == uuid.Nil {
		s.writeAccountError(w, r, "invalid_request", http.StatusBadRequest)
		return
	}
	command := accountadmin.OverrideCommand{CommandID: commandID, ActorAdminID: session.AdminID, TargetOperation: target, Reason: reason, Confirmation: confirmation, RequestID: s.requestID(r)}
	if detail != nil {
		command.Detail, command.DetailPresent = *detail, true
	}
	var op assetstore.AccountAdminOperation
	var err error
	if lifecycle {
		op, err = s.accountOperations.LifecycleOverride(r.Context(), command)
	} else {
		op, err = s.accountOperations.SameAccountOverride(r.Context(), command)
	}
	if err == nil && s.writeStoredAccountReceipt(r, w, commandID, session.AdminID) {
		return
	}
	s.writeAccountOperationResult(w, r, op, err)
}

func parseAccountMultipart(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, []byte, accountMultipartFailure) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data;") {
		return nil, nil, accountMultipartInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, accountMultipartLimit)
	if err := r.ParseMultipartForm(accountMultipartLimit); err != nil || r.MultipartForm == nil {
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				return nil, nil, accountMultipartTooLarge
			}
		}
		return nil, nil, accountMultipartInvalid
	}
	if len(r.MultipartForm.Value)+len(r.MultipartForm.File) != 2 || len(r.MultipartForm.File["credential"]) != 1 || len(r.MultipartForm.Value["request"])+len(r.MultipartForm.File["request"]) != 1 {
		return nil, nil, accountMultipartInvalid
	}
	var requestRaw []byte
	if values := r.MultipartForm.Value["request"]; len(values) == 1 {
		requestRaw = []byte(values[0])
	} else {
		file, err := r.MultipartForm.File["request"][0].Open()
		if err != nil {
			return nil, nil, accountMultipartInvalid
		}
		requestRaw, err = io.ReadAll(io.LimitReader(file, 8193))
		file.Close()
		if err != nil {
			return nil, nil, accountMultipartInvalid
		}
	}
	if len(requestRaw) > 8192 {
		return nil, nil, accountMultipartInvalid
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(requestRaw, &request) != nil || request == nil {
		return nil, nil, accountMultipartInvalid
	}
	for field := range request {
		if field != "command_id" && field != "node_instance_id" && field != "account_key" {
			return nil, nil, accountMultipartInvalid
		}
	}
	file, err := r.MultipartForm.File["credential"][0].Open()
	if err != nil {
		return nil, nil, accountMultipartInvalid
	}
	defer file.Close()
	credential, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(credential) > 1<<20 {
		if len(credential) > 1<<20 {
			return nil, nil, accountMultipartTooLarge
		}
		return nil, nil, accountMultipartInvalid
	}
	return request, credential, ""
}

func decodeAccountObject(w http.ResponseWriter, r *http.Request, limit int64, allowed ...string) (map[string]json.RawMessage, bool) {
	var body map[string]json.RawMessage
	if !decodeJSONWithLimit(w, r, &body, limit) || body == nil {
		return nil, false
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allow[name] = struct{}{}
	}
	for name := range body {
		if _, ok := allow[name]; !ok {
			writeJSON(w, http.StatusBadRequest, AccountOperationErrorResponse{Error: ErrorResponse{Code: ErrorCode("invalid_request"), Message: accountErrorMessage("invalid_request"), RequestId: w.Header().Get("X-Request-ID")}})
			return nil, false
		}
	}
	return body, true
}

func parseAccountCommand(fields map[string]json.RawMessage, kind assetstore.AccountOperationKind, remove bool) (accountadmin.Command, bool) {
	commandID, ok := parseUUIDField(fields, "command_id")
	nodeID, nodeOK := parseUUIDField(fields, "node_instance_id")
	accountKey, keyOK := parseStringField(fields, "account_key")
	if !ok || !nodeOK || !keyOK || (remove && !hasExactString(fields, "confirmation", "REMOVE")) {
		return accountadmin.Command{}, false
	}
	return accountadmin.Command{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: accountKey, Kind: kind}, true
}

func parseUUIDField(fields map[string]json.RawMessage, name string) (uuid.UUID, bool) {
	var value string
	raw, ok := fields[name]
	if !ok || json.Unmarshal(raw, &value) != nil || value != strings.ToLower(value) {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(value)
	return id, err == nil && id != uuid.Nil && id.String() == value
}

func parseStringField(fields map[string]json.RawMessage, name string) (string, bool) {
	var value string
	raw, ok := fields[name]
	if !ok || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func parseOptionalStringField(fields map[string]json.RawMessage, name string) (*string, bool) {
	raw, ok := fields[name]
	if !ok {
		return nil, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	return &value, true
}

func hasExactString(fields map[string]json.RawMessage, name, expected string) bool {
	value, ok := parseStringField(fields, name)
	return ok && value == expected
}

func (s *Server) writeStoredAccountReceipt(r *http.Request, w http.ResponseWriter, commandID, actor uuid.UUID) bool {
	receipt, err := s.accountOperationReader.Receipt(r.Context(), commandID)
	if err != nil || receipt.ActorAdminID != actor {
		return false
	}
	w.Header().Set("Content-Type", receipt.ContentType)
	w.WriteHeader(receipt.HTTPStatus)
	_, _ = w.Write(receipt.ResponseBody)
	return true
}

func (s *Server) writeAccountOperationResult(w http.ResponseWriter, r *http.Request, op assetstore.AccountAdminOperation, err error) {
	if err != nil {
		code := accountErrorCode(err)
		status := accountErrorStatus(err)
		if op.CommandID != uuid.Nil {
			writeJSON(w, status, AccountOperationErrorResponse{Error: ErrorResponse{Code: ErrorCode(code), Message: accountErrorMessage(code), RequestId: s.requestID(r)}, Operation: ptr(accountOperationProjection(op))})
		} else {
			s.writeAccountError(w, r, code, status)
		}
		return
	}
	if op.ExecutionState == assetstore.AccountFailed {
		code := "service_unavailable"
		if op.RemoteResultCode != nil {
			code = *op.RemoteResultCode
		}
		writeJSON(w, accountErrorStatusCode(code), AccountOperationErrorResponse{Error: ErrorResponse{Code: ErrorCode(code), Message: accountErrorMessage(code), RequestId: s.requestID(r)}, Operation: ptr(accountOperationProjection(op))})
		return
	}
	status := http.StatusAccepted
	if op.ExecutionState == assetstore.AccountRemoteApplied || op.ExecutionState == assetstore.AccountRemoteNoop {
		status = http.StatusOK
	}
	writeJSON(w, status, AccountOperationResponse{Operation: accountOperationProjection(op)})
}

func (s *Server) writeAccountError(w http.ResponseWriter, r *http.Request, code string, status int) {
	writeJSON(w, status, AccountOperationErrorResponse{Error: ErrorResponse{Code: ErrorCode(code), Message: accountErrorMessage(code), RequestId: s.requestID(r)}})
}

func accountOperationProjection(op assetstore.AccountAdminOperation) AccountOperationProjection {
	result := (*AccountOperationProjectionResult)(nil)
	switch op.ExecutionState {
	case assetstore.AccountRemoteApplied:
		v := AccountOperationProjectionResultApplied
		result = &v
	case assetstore.AccountRemoteNoop:
		v := AccountOperationProjectionResultNoop
		result = &v
	case assetstore.AccountFailed:
		v := AccountOperationProjectionResultFailed
		result = &v
	}
	return AccountOperationProjection{CommandId: op.CommandID, NodeInstanceId: op.NodeInstanceID, AccountKey: op.AccountKey, OperationKind: AccountOperationProjectionOperationKind(op.OperationKind), ExecutionState: AccountOperationProjectionExecutionState(op.ExecutionState), Result: result, ErrorCode: op.RemoteResultCode, LifecycleOverridden: op.LifecycleOverrideAt != nil, LifecycleOverrideReason: op.LifecycleOverrideReason, SameAccountOverridden: op.SameAccountOverrideAt != nil, SameAccountOverrideReason: op.SameAccountOverrideReason, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt}
}

func ptr[T any](v T) *T { return &v }

func accountErrorCode(err error) string {
	switch {
	case errors.Is(err, accountadmin.ErrInvalidCommand):
		return "invalid_request"
	case errors.Is(err, accountadmin.ErrUnsupportedProvider):
		return "unsupported_provider"
	case errors.Is(err, accountadmin.ErrNodeNotFound):
		return "node_not_found"
	case errors.Is(err, assetstore.ErrAccountOperationNotFound):
		return "operation_not_found"
	case errors.Is(err, assetstore.ErrCommandConflict):
		return "command_conflict"
	case errors.Is(err, assetstore.ErrAccountOperationNotOverridable):
		return "account_operation_not_overridable"
	case errors.Is(err, assetstore.ErrLifecycleOverrideAlreadySet):
		return "lifecycle_override_already_set"
	case errors.Is(err, assetstore.ErrSameAccountOverrideAlreadySet):
		return "same_account_override_already_set"
	case errors.Is(err, assetstore.ErrAccountOperationBlocked):
		return "account_operation_in_progress"
	default:
		return "service_unavailable"
	}
}

func accountErrorStatus(err error) int { return accountErrorStatusCode(accountErrorCode(err)) }

func accountErrorStatusCode(code string) int {
	switch code {
	case "invalid_request", "upload_invalid", "identity_mismatch":
		return http.StatusBadRequest
	case "upload_too_large":
		return http.StatusRequestEntityTooLarge
	case "node_not_found", "account_target_not_found", "operation_not_found":
		return http.StatusNotFound
	case "unsupported_provider", "node_retired", "node_monitoring_ineligible", "account_target_ambiguous", "account_target_exists", "account_filename_conflict", "account_operation_in_progress", "account_operation_not_overridable", "lifecycle_override_already_set", "same_account_override_already_set", "command_conflict":
		return http.StatusConflict
	case "unsupported_node_version", "node_management_unavailable", "service_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func accountErrorMessage(code string) string {
	if code == "remote_outcome_unknown" {
		return "The native outcome is unknown."
	}
	return "The account operation could not be completed."
}
