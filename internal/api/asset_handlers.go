package api

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

var canonicalAssetIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func (s *Server) GetEnvironment(w http.ResponseWriter, r *http.Request) {
	s.prepare(w, r, true)
	if !s.authorizeAssetRead(w, r, AssetReadEnvironment) {
		return
	}
	environment, err := s.assets.Environment(r.Context())
	if err != nil {
		s.assetReadError(w, r, AssetReadEnvironment, err)
		return
	}
	s.recordAssetRead(AssetReadEnvironment, AssetReadResultSuccess)
	s.refreshAssetCounts(r)
	writeJSON(w, http.StatusOK, EnvironmentAsset{
		EnvironmentId: environment.ID, Name: environment.Name,
		EnvironmentType: EnvironmentAssetEnvironmentType(environment.Type),
	})
}

func (s *Server) GetGatewayAsset(w http.ResponseWriter, r *http.Request) {
	s.prepare(w, r, true)
	if !s.authorizeAssetRead(w, r, AssetReadGateway) {
		return
	}
	var gateway *assetstore.GatewayAsset
	var err error
	if s.gatewayAssets != nil {
		gateway, err = s.gatewayAssets.Current(r.Context())
	} else {
		gateway, err = s.assets.Gateway(r.Context())
	}
	if err != nil {
		s.assetReadError(w, r, AssetReadGateway, err)
		return
	}
	if gateway == nil {
		s.recordAssetRead(AssetReadGateway, AssetReadResultEmpty)
		s.refreshAssetCounts(r)
		writeJSON(w, http.StatusOK, GatewayAssetResponse{Status: GatewayAssetResponseStatusNotRegistered})
		return
	}
	s.recordAssetRead(AssetReadGateway, AssetReadResultSuccess)
	s.refreshAssetCounts(r)
	writeJSON(w, http.StatusOK, GatewayAssetResponse{Status: GatewayAssetResponseStatusRegistered, Gateway: gatewayResponse(gateway)})
}

func (s *Server) ListNodeAssets(w http.ResponseWriter, r *http.Request, params ListNodeAssetsParams) {
	s.prepare(w, r, true)
	if !s.authorizeAssetRead(w, r, AssetReadNodes) {
		return
	}
	filters := assetstore.NodeListFilters{MonitoringActive: params.MonitoringActive}
	if params.NodeType != nil {
		filters.NodeType = string(*params.NodeType)
		if !validAssetIdentifier(filters.NodeType, 2) {
			s.assetReadError(w, r, AssetReadNodes, assetstore.ErrInvalidAssetQuery)
			return
		}
	}
	if params.Capability != nil {
		filters.Capability = string(*params.Capability)
		if !params.Capability.Valid() {
			s.assetReadError(w, r, AssetReadNodes, assetstore.ErrInvalidAssetQuery)
			return
		}
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = string(*params.Cursor)
		if cursor == "" {
			s.assetReadError(w, r, AssetReadNodes, assetstore.ErrInvalidNodeCursor)
			return
		}
	}
	after, err := s.nodeCursor.Decode(cursor, filters)
	if err != nil {
		s.assetReadError(w, r, AssetReadNodes, assetstore.ErrInvalidNodeCursor)
		return
	}
	limit := 50
	if params.Limit != nil {
		limit = int(*params.Limit)
	}
	page, err := s.assets.ListNodes(r.Context(), filters, after, limit)
	if err != nil {
		s.assetReadError(w, r, AssetReadNodes, err)
		return
	}
	items := make([]NodeAsset, 0, len(page.Items))
	for _, node := range page.Items {
		items = append(items, nodeResponse(node))
	}
	var nextCursor *string
	if page.HasMore && len(page.Items) > 0 {
		value, encodeErr := s.nodeCursor.Encode(page.Items[len(page.Items)-1].InstanceID, filters)
		if encodeErr != nil {
			s.assetReadError(w, r, AssetReadNodes, encodeErr)
			return
		}
		nextCursor = &value
	}
	result := AssetReadResultSuccess
	if len(items) == 0 {
		result = AssetReadResultEmpty
	}
	s.recordAssetRead(AssetReadNodes, result)
	s.refreshAssetCounts(r)
	writeJSON(w, http.StatusOK, NodeAssetListResponse{Items: items, NextCursor: nextCursor})
}

func (s *Server) GetNodeAsset(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId) {
	s.prepare(w, r, true)
	if !s.authorizeAssetRead(w, r, AssetReadNodeDetail) {
		return
	}
	node, err := s.assets.Node(r.Context(), uuid.UUID(instanceID))
	if err != nil {
		s.assetReadError(w, r, AssetReadNodeDetail, err)
		return
	}
	s.recordAssetRead(AssetReadNodeDetail, AssetReadResultSuccess)
	s.refreshAssetCounts(r)
	writeJSON(w, http.StatusOK, nodeResponse(node))
}

func (s *Server) ListNodeDrivers(w http.ResponseWriter, r *http.Request) {
	s.prepare(w, r, true)
	if !s.authorizeAssetRead(w, r, AssetReadDrivers) {
		return
	}
	drivers, err := s.assets.Drivers(r.Context())
	if err != nil {
		s.assetReadError(w, r, AssetReadDrivers, err)
		return
	}
	items := make([]NodeDriver, 0, len(drivers))
	for _, driver := range drivers {
		items = append(items, NodeDriver{
			NodeType: driver.NodeType, DriverContractVersion: driver.DriverContractVersion,
			DisplayName: driver.DisplayName, LifecycleStatus: NodeDriverLifecycleStatus(driver.LifecycleStatus),
			Capabilities: capabilityResponses(driver.Capabilities), CreatedAt: driver.CreatedAt,
		})
	}
	result := AssetReadResultSuccess
	if len(items) == 0 {
		result = AssetReadResultEmpty
	}
	s.recordAssetRead(AssetReadDrivers, result)
	s.refreshAssetCounts(r)
	writeJSON(w, http.StatusOK, NodeDriverListResponse{Items: items})
}

func (s *Server) GetCurrentProviderInventoryPolicy(w http.ResponseWriter, r *http.Request, params GetCurrentProviderInventoryPolicyParams) {
	s.prepare(w, r, true)
	if !s.authorizeAssetRead(w, r, AssetReadCurrentProviderPolicy) {
		return
	}
	nodeType, driverVersion := string(params.NodeType), string(params.DriverContractVersion)
	if !validAssetIdentifier(nodeType, 2) || !validAssetIdentifier(driverVersion, 1) {
		s.assetReadError(w, r, AssetReadCurrentProviderPolicy, assetstore.ErrInvalidAssetQuery)
		return
	}
	policy, err := s.assets.CurrentProviderPolicy(r.Context(), nodeType, driverVersion)
	if err != nil {
		s.assetReadError(w, r, AssetReadCurrentProviderPolicy, err)
		return
	}
	response := CurrentProviderInventoryPolicyResponse{
		Status: NotConfigured, NodeType: nodeType, DriverContractVersion: driverVersion,
	}
	result := AssetReadResultEmpty
	if policy != nil {
		response.Status = Configured
		response.Policy = &ProviderInventoryPolicy{
			VersionId: policy.VersionID, ActiveProviders: providerResponses(policy.ActiveProviders),
			OutOfScopeProviders: providerResponses(policy.OutOfScopeProviders),
			EffectiveFrom:       policy.EffectiveFrom, EffectiveTo: policy.EffectiveTo, CreatedAt: policy.CreatedAt,
		}
		result = AssetReadResultSuccess
	}
	s.recordAssetRead(AssetReadCurrentProviderPolicy, result)
	s.refreshAssetCounts(r)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) authorizeAssetRead(w http.ResponseWriter, r *http.Request, operation AssetReadOperation) bool {
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		s.recordAssetRead(operation, AssetReadResultUnauthorized)
		return false
	}
	if s.assets == nil || s.nodeCursor == nil {
		s.recordAssetRead(operation, AssetReadResultUnavailable)
		s.writeError(w, r, authn.ErrUnavailable)
		return false
	}
	return true
}

func (s *Server) assetReadError(w http.ResponseWriter, r *http.Request, operation AssetReadOperation, err error) {
	switch {
	case errors.Is(err, assetstore.ErrInvalidAssetQuery), errors.Is(err, assetstore.ErrInvalidNodeCursor):
		s.recordAssetRead(operation, AssetReadResultInvalid)
		s.writeError(w, r, authn.ErrInvalid)
	case errors.Is(err, assetstore.ErrAssetNotFound):
		s.recordAssetRead(operation, AssetReadResultNotFound)
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorCodeNotFound, Message: "The asset was not found.", RequestId: s.requestID(r)})
	default:
		s.recordAssetRead(operation, AssetReadResultUnavailable)
		slog.Warn("asset registry read failed", "component", "assets", "operation", string(operation), "reason", "database_unavailable")
		s.writeError(w, r, authn.ErrUnavailable)
	}
}

func (s *Server) recordAssetRead(operation AssetReadOperation, result AssetReadResult) {
	if s.assetMetrics != nil {
		_ = s.assetMetrics.RecordRead(operation, result)
	}
}

func (s *Server) refreshAssetCounts(r *http.Request) {
	if s.assetMetrics == nil || s.assets == nil {
		return
	}
	counts, err := s.assets.Counts(r.Context())
	if err != nil {
		return
	}
	_ = s.assetMetrics.SetCount(AssetKindEnvironment, counts.Environments)
	_ = s.assetMetrics.SetCount(AssetKindGateway, counts.Gateways)
	_ = s.assetMetrics.SetCount(AssetKindNode, counts.Nodes)
	_ = s.assetMetrics.SetCount(AssetKindDriver, counts.Drivers)
	_ = s.assetMetrics.SetCount(AssetKindProviderPolicy, counts.ProviderPolicies)
}

func gatewayResponse(gateway *assetstore.GatewayAsset) *GatewayAsset {
	result := &GatewayAsset{
		InstanceId: gateway.InstanceID, DisplayName: gateway.DisplayName,
		ManagementEndpoint: gateway.ManagementEndpoint, SecretConfigured: gateway.SecretConfigured,
		LifecycleStatus: GatewayAssetLifecycleStatus(gateway.LifecycleStatus), Revision: strconv.FormatInt(gateway.Revision, 10),
		RetiredAt: gateway.RetiredAt, RetiredBy: gateway.RetiredBy,
		CreatedAt: gateway.CreatedAt, UpdatedAt: gateway.UpdatedAt,
	}
	if gateway.RetireReason != nil {
		value := GatewayAssetRetireReason(*gateway.RetireReason)
		result.RetireReason = &value
	}
	return result
}

func nodeResponse(node assetstore.NodeAsset) NodeAsset {
	return NodeAsset{
		InstanceId: node.InstanceID, DisplayName: node.DisplayName, NodeType: node.NodeType,
		DriverContractVersion: node.DriverContractVersion, ManagementEndpoint: node.ManagementEndpoint,
		SecretConfigured: node.SecretConfigured, Capabilities: capabilityResponses(node.Capabilities),
		Monitoring: NodeMonitoringStatus{Active: node.Monitoring.Active, EffectiveFrom: node.Monitoring.EffectiveFrom, EffectiveTo: node.Monitoring.EffectiveTo},
		CreatedAt:  node.CreatedAt, UpdatedAt: node.UpdatedAt,
	}
}

func capabilityResponses(values []string) []NodeCapability {
	result := make([]NodeCapability, 0, len(values))
	for _, value := range values {
		result = append(result, NodeCapability(value))
	}
	return result
}

func providerResponses(values []string) []ProviderName {
	result := make([]ProviderName, 0, len(values))
	for _, value := range values {
		result = append(result, ProviderName(value))
	}
	return result
}

func validAssetIdentifier(value string, minimum int) bool {
	return len(value) >= minimum && len(value) <= 64 && canonicalAssetIdentifier.MatchString(value)
}
