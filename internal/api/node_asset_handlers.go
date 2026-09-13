package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func (s *Server) RegisterNodeAsset(w http.ResponseWriter, r *http.Request, p RegisterNodeAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, p.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "new_instance_id", "display_name", "management_endpoint", "node_type", "driver_contract_version", "capabilities", "reader_secret_ref")
	if !ok {
		return
	}
	c := assetstore.NodeCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), Secret: secretPatch(body, "reader_secret_ref")}
	if !requiredUUID(body, "command_id", &c.CommandID) || !requiredUUID(body, "new_instance_id", &c.NewInstanceID) || !requiredString(body, "display_name", &c.DisplayName) || !requiredString(body, "management_endpoint", &c.ManagementEndpoint) || !rawString(body, "node_type", &c.NodeType) || !rawString(body, "driver_contract_version", &c.DriverContractVersion) || !rawStrings(body, "capabilities", &c.Capabilities) {
		nodeError(w, r, s, assetstore.ErrInvalidNode)
		return
	}
	s.runNodeCommand(w, r, c, s.nodeAssets.Register)
}
func (s *Server) EditNodeAsset(w http.ResponseWriter, r *http.Request, id NodeInstanceId, p EditNodeAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, p.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "expected_revision", "display_name", "management_endpoint", "reader_secret_ref")
	if !ok {
		return
	}
	c := assetstore.NodeCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), InstanceID: uuid.UUID(id), DisplayName: optionalString(body, "display_name"), ManagementEndpoint: optionalString(body, "management_endpoint"), Secret: secretPatch(body, "reader_secret_ref")}
	if !requiredUUID(body, "command_id", &c.CommandID) || !requiredRevision(body, "expected_revision", &c.ExpectedRevision) {
		nodeError(w, r, s, assetstore.ErrInvalidNode)
		return
	}
	s.runNodeCommand(w, r, c, s.nodeAssets.Edit)
}
func (s *Server) RetireNodeAsset(w http.ResponseWriter, r *http.Request, id NodeInstanceId, p RetireNodeAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, p.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "expected_revision")
	if !ok {
		return
	}
	c := assetstore.NodeCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), InstanceID: uuid.UUID(id), Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if !requiredUUID(body, "command_id", &c.CommandID) || !requiredRevision(body, "expected_revision", &c.ExpectedRevision) {
		nodeError(w, r, s, assetstore.ErrInvalidNode)
		return
	}
	s.runNodeCommand(w, r, c, s.nodeAssets.Retire)
}
func (s *Server) ReplaceNodeAsset(w http.ResponseWriter, r *http.Request, id NodeInstanceId, p ReplaceNodeAssetParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, p.XCSRFToken)
	if !ok {
		return
	}
	body, ok := decodeGatewayObject(w, r, "command_id", "expected_revision", "new_instance_id", "display_name", "management_endpoint", "node_type", "driver_contract_version", "capabilities", "reader_secret_ref")
	if !ok {
		return
	}
	c := assetstore.NodeCommand{ActorAdminID: session.AdminID, RequestID: s.requestID(r), InstanceID: uuid.UUID(id), Secret: secretPatch(body, "reader_secret_ref")}
	if !requiredUUID(body, "command_id", &c.CommandID) || !requiredRevision(body, "expected_revision", &c.ExpectedRevision) || !requiredUUID(body, "new_instance_id", &c.NewInstanceID) || !requiredString(body, "display_name", &c.DisplayName) || !requiredString(body, "management_endpoint", &c.ManagementEndpoint) || !rawString(body, "node_type", &c.NodeType) || !rawString(body, "driver_contract_version", &c.DriverContractVersion) || !rawStrings(body, "capabilities", &c.Capabilities) {
		nodeError(w, r, s, assetstore.ErrInvalidNode)
		return
	}
	s.runNodeCommand(w, r, c, s.nodeAssets.Replace)
}
func rawString(m map[string]json.RawMessage, k string, out *string) bool {
	v, ok := m[k]
	return ok && json.Unmarshal(v, out) == nil && *out != ""
}
func rawStrings(m map[string]json.RawMessage, k string, out *[]string) bool {
	v, ok := m[k]
	return ok && json.Unmarshal(v, out) == nil && len(*out) > 0
}
func (s *Server) runNodeCommand(w http.ResponseWriter, r *http.Request, c assetstore.NodeCommand, fn func(context.Context, assetstore.NodeCommand) (assetstore.NodeCommandResult, error)) {
	if s.nodeAssets == nil {
		s.writeError(w, r, errors.New("node lifecycle unavailable"))
		return
	}
	result, err := fn(r.Context(), c)
	if err != nil {
		if s.assetMetrics != nil {
			s.assetMetrics.RecordNodeMutation(nodeMetricAction(r.URL.Path), nodeMutationMetricResult(err))
		}
		nodeError(w, r, s, err)
		return
	}
	if s.assetMetrics != nil {
		metricResult := "success"
		if result.Replayed {
			metricResult = "replay"
		}
		s.assetMetrics.RecordNodeMutation(nodeMetricAction(r.URL.Path), metricResult)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.HTTPStatus)
	_, _ = w.Write(append(result.Body, '\n'))
}

func nodeMutationMetricResult(err error) string {
	if errors.Is(err, assetstore.ErrInvalidNode) || errors.Is(err, assetstore.ErrInvalidNodeEndpoint) || errors.Is(err, assetstore.ErrInvalidNodeSecret) {
		return "invalid"
	}
	if errors.Is(err, assetstore.ErrCommandConflict) || errors.Is(err, assetstore.ErrNodeRetired) || errors.Is(err, assetstore.ErrNodeIdentityExists) || errors.Is(err, assetstore.ErrStaleAssetRevision) || errors.Is(err, assetstore.ErrAssetRevisionExhausted) {
		return "conflict"
	}
	return "unavailable"
}

func nodeMetricAction(path string) string {
	if strings.HasSuffix(path, "/retire") {
		return "retire"
	}
	if strings.HasSuffix(path, "/replace") {
		return "replace"
	}
	if strings.Contains(path, "/api/assets/nodes/") {
		return "edit"
	}
	return "register"
}
func nodeError(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	switch {
	case errors.Is(err, assetstore.ErrInvalidNode):
		writeGatewayAPIError(w, r, s, http.StatusBadRequest, ErrorCodeValidationFailed)
	case errors.Is(err, assetstore.ErrInvalidNodeEndpoint):
		writeGatewayAPIError(w, r, s, http.StatusBadRequest, ErrorCodeInvalidEndpoint)
	case errors.Is(err, assetstore.ErrInvalidNodeSecret):
		writeGatewayAPIError(w, r, s, http.StatusBadRequest, ErrorCodeSecretConfigurationInvalid)
	case errors.Is(err, assetstore.ErrNodeNotFound):
		writeGatewayAPIError(w, r, s, http.StatusNotFound, ErrorCodeAssetNotFound)
	case errors.Is(err, assetstore.ErrNodeRetired):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeAssetRetired)
	case errors.Is(err, assetstore.ErrNodeIdentityExists):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeDuplicateIdentity)
	case errors.Is(err, assetstore.ErrStaleAssetRevision):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeStaleRevision)
	case errors.Is(err, assetstore.ErrAssetRevisionExhausted):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeRevisionExhausted)
	case errors.Is(err, assetstore.ErrCommandConflict):
		writeGatewayAPIError(w, r, s, http.StatusConflict, ErrorCodeCommandConflict)
	default:
		writeGatewayAPIError(w, r, s, http.StatusServiceUnavailable, ErrorCodeServiceUnavailable)
	}
}
