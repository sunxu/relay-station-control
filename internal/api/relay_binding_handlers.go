package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func (s *Server) getNodeRelayBindingRepository(w http.ResponseWriter, r *http.Request) (*assetstore.RelayBindingRepository, bool) {
	if s.relayBindings == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return nil, false
	}
	return s.relayBindings, true
}

func (s *Server) authorizeSuperAdmin(w http.ResponseWriter, r *http.Request, csrf string) (authn.Session, bool) {
	session, ok := s.requireSession(w, r, true, csrf)
	if !ok {
		return authn.Session{}, false
	}
	if session.Role != "super_admin" {
		s.writeError(w, r, authn.ErrForbidden)
		return authn.Session{}, false
	}
	return session, true
}

func (s *Server) GetNodeRelayBinding(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	repo, ok := s.getNodeRelayBindingRepository(w, r)
	if !ok {
		return
	}
	nodeID := uuid.UUID(instanceID)
	if nodeID == uuid.Nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}

	view, err := repo.GetNodeCentricBindingView(r.Context(), nodeID)
	if err != nil {
		if errors.Is(err, assetstore.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{
				Code:      ErrorCodeNotFound,
				Message:   "The Relay Node was not found.",
				RequestId: s.requestID(r),
			})
			return
		}
		if errors.Is(err, assetstore.ErrInvalidAssetQuery) {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}

	resp := NodeRelayBindingResponse{
		RelayNodeId:        view.RelayNodeID,
		Resolution:         RelayBindingResolution(view.Resolution),
		DirectoryFreshness: RelayBindingFreshness(view.DirectoryFreshness),
		ContextSource:      RelayBindingContextSource(view.ContextSource),
		ObservedAt:         view.ObservedAt,
	}
	if view.CurrentBinding != nil {
		b := bindingDetailResponse(*view.CurrentBinding)
		resp.CurrentBinding = &b
	}
	if view.GatewayInstanceID != nil {
		resp.GatewayInstanceId = view.GatewayInstanceID
	}
	if view.GatewayAccountID != nil {
		id := strconv.FormatInt(*view.GatewayAccountID, 10)
		resp.GatewayAccountId = &id
	}
	if view.LastSuccessObservationAt != nil {
		resp.LastSuccessObservationAt = view.LastSuccessObservationAt
	}
	if view.AccountContext != nil {
		ctx := accountContextResponse(*view.AccountContext)
		resp.AccountContext = &ctx
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) GetGatewayAccountRelayBindings(w http.ResponseWriter, r *http.Request, instanceID GatewayInstanceId) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	repo, ok := s.getNodeRelayBindingRepository(w, r)
	if !ok {
		return
	}
	gatewayID := uuid.UUID(instanceID)
	if gatewayID == uuid.Nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}

	view, err := repo.GetGatewayAccountCentricBindingView(r.Context(), gatewayID)
	if err != nil {
		if errors.Is(err, assetstore.ErrAssetNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{
				Code:      ErrorCodeNotFound,
				Message:   "The Gateway was not found.",
				RequestId: s.requestID(r),
			})
			return
		}
		if errors.Is(err, assetstore.ErrInvalidAssetQuery) {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}

	accounts := make([]GatewayAccountCentricBindingItem, 0, len(view.Accounts))
	for _, item := range view.Accounts {
		acctItem := GatewayAccountCentricBindingItem{
			GatewayAccountId: strconv.FormatInt(item.GatewayAccountID, 10),
			AccountContext:   accountContextResponse(item.AccountContext),
			Resolution:       RelayBindingResolution(item.Resolution),
			ContextSource:    RelayBindingContextSource(item.ContextSource),
		}
		if item.BoundRelayNodeID != nil {
			acctItem.BoundRelayNodeId = item.BoundRelayNodeID
		}
		if item.CurrentBinding != nil {
			b := bindingDetailResponse(*item.CurrentBinding)
			acctItem.CurrentBinding = &b
		}
		accounts = append(accounts, acctItem)
	}

	resp := GatewayAccountRelayBindingsResponse{
		GatewayInstanceId:  view.GatewayInstanceID,
		DirectoryFreshness: RelayBindingFreshness(view.DirectoryFreshness),
		ObservedAt:         view.ObservedAt,
		Accounts:           accounts,
	}
	if view.CurrentSnapshotID != nil {
		resp.CurrentSnapshotId = view.CurrentSnapshotID
	}
	if view.LastSuccessObservationAt != nil {
		resp.LastSuccessObservationAt = view.LastSuccessObservationAt
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) ListUnresolvedRelayBindings(w http.ResponseWriter, r *http.Request) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	repo, ok := s.getNodeRelayBindingRepository(w, r)
	if !ok {
		return
	}

	gateway, err := s.assets.Gateway(r.Context())
	if err != nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	if gateway == nil {
		writeJSON(w, http.StatusOK, UnresolvedRelayBindingsResponse{Items: []UnresolvedRelayBindingItem{}})
		return
	}

	items, err := repo.ListUnresolvedGatewayAccountBindings(r.Context(), gateway.InstanceID)
	if err != nil {
		if errors.Is(err, assetstore.ErrAssetNotFound) {
			writeJSON(w, http.StatusOK, UnresolvedRelayBindingsResponse{Items: []UnresolvedRelayBindingItem{}})
			return
		}
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}

	resultItems := make([]UnresolvedRelayBindingItem, 0, len(items))
	for _, item := range items {
		resItem := UnresolvedRelayBindingItem{
			CurrentBinding:           bindingDetailResponse(item.CurrentBinding),
			Resolution:               RelayBindingResolution(item.Resolution),
			DirectoryFreshness:       RelayBindingFreshness(item.DirectoryFreshness),
			LastSuccessObservationAt: item.LastSuccessObservationAt,
			ContextSource:            RelayBindingContextSource(item.ContextSource),
			ObservedAt:               item.ObservedAt,
		}
		if item.LastKnownAccountContext != nil {
			ctx := accountContextResponse(*item.LastKnownAccountContext)
			resItem.LastKnownAccountContext = &ctx
		}
		resultItems = append(resultItems, resItem)
	}

	writeJSON(w, http.StatusOK, UnresolvedRelayBindingsResponse{Items: resultItems})
}

func (s *Server) BindRelayNode(w http.ResponseWriter, r *http.Request, params BindRelayNodeParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, params.XCSRFToken)
	if !ok {
		return
	}
	repo, ok := s.getNodeRelayBindingRepository(w, r)
	if !ok {
		return
	}

	var body BindRelayNodeRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	nodeID := uuid.UUID(body.RelayNodeId)
	gatewayID := uuid.UUID(body.GatewayInstanceId)
	accountID, valid := parseGatewayAccountID(body.GatewayAccountId)
	if nodeID == uuid.Nil || gatewayID == uuid.Nil || !valid {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}

	result, err := repo.Bind(r.Context(), assetstore.BindParams{
		RelayNodeID:       nodeID,
		GatewayInstanceID: gatewayID,
		GatewayAccountID:  accountID,
		AdminID:           session.AdminID,
		RequestID:         s.requestID(r),
	})
	if err != nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}

	s.handleMutationResult(w, r, result)
}

func (s *Server) RebindRelayNode(w http.ResponseWriter, r *http.Request, params RebindRelayNodeParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, params.XCSRFToken)
	if !ok {
		return
	}
	repo, ok := s.getNodeRelayBindingRepository(w, r)
	if !ok {
		return
	}

	var body RebindRelayNodeRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	nodeID := uuid.UUID(body.RelayNodeId)
	newGatewayID := uuid.UUID(body.NewGatewayInstanceId)
	newAccountID, valid := parseGatewayAccountID(body.NewGatewayAccountId)
	if nodeID == uuid.Nil || newGatewayID == uuid.Nil || !valid {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}

	result, err := repo.Rebind(r.Context(), assetstore.RebindParams{
		RelayNodeID:          nodeID,
		NewGatewayInstanceID: newGatewayID,
		NewGatewayAccountID:  newAccountID,
		AdminID:              session.AdminID,
		RequestID:            s.requestID(r),
	})
	if err != nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}

	s.handleMutationResult(w, r, result)
}

func (s *Server) UnbindRelayNode(w http.ResponseWriter, r *http.Request, params UnbindRelayNodeParams) {
	s.prepare(w, r, true)
	session, ok := s.authorizeSuperAdmin(w, r, params.XCSRFToken)
	if !ok {
		return
	}
	repo, ok := s.getNodeRelayBindingRepository(w, r)
	if !ok {
		return
	}

	var body UnbindRelayNodeRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	nodeID := uuid.UUID(body.RelayNodeId)
	if nodeID == uuid.Nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}

	result, err := repo.Unbind(r.Context(), assetstore.UnbindParams{
		RelayNodeID: nodeID,
		AdminID:     session.AdminID,
		RequestID:   s.requestID(r),
	})
	if err != nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}

	s.handleMutationResult(w, r, result)
}

func (s *Server) handleMutationResult(w http.ResponseWriter, r *http.Request, result assetstore.RelayBindingResult) {
	reqID := s.requestID(r)
	switch result.Outcome {
	case assetstore.RelayBindingOutcomeSuccess:
		resp := RelayBindingMutationResponse{
			Outcome:     RelayBindingMutationResponseOutcomeSuccess,
			OperationAt: result.OperationAt,
		}
		if result.Binding != nil {
			b := bindingDetailResponse(*result.Binding)
			resp.Binding = &b
		}
		if result.PreviousBinding != nil {
			b := bindingDetailResponse(*result.PreviousBinding)
			resp.PreviousBinding = &b
		}
		writeJSON(w, http.StatusOK, resp)

	case assetstore.RelayBindingOutcomeAlreadyUnbound:
		if result.OperationAt.IsZero() {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		resp := RelayBindingMutationResponse{
			Outcome:     RelayBindingMutationResponseOutcomeAlreadyUnbound,
			OperationAt: result.OperationAt,
		}
		writeJSON(w, http.StatusOK, resp)

	case assetstore.RelayBindingOutcomeNoCurrentBinding:
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Code:      ErrorCodeNoCurrentBinding,
			Message:   "The Relay Node is not currently bound.",
			RequestId: reqID,
		})

	case assetstore.RelayBindingOutcomeDirectoryUnavailable:
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			Code:      ErrorCodeDirectoryUnavailable,
			Message:   "Gateway Directory snapshot is unavailable.",
			RequestId: reqID,
		})

	case assetstore.RelayBindingOutcomeDirectoryStale:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Code:      ErrorCodeDirectoryStale,
			Message:   "Gateway Directory snapshot is stale.",
			RequestId: reqID,
		})

	case assetstore.RelayBindingOutcomeAccountNotFound:
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Code:      ErrorCodeAccountNotFound,
			Message:   "Target Gateway Account was not found in current Directory snapshot.",
			RequestId: reqID,
		})

	case assetstore.RelayBindingOutcomeNodeNotFound:
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Code:      ErrorCodeNotFound,
			Message:   "The Relay Node was not found.",
			RequestId: reqID,
		})

	case assetstore.RelayBindingOutcomeGatewayNotFound:
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Code:      ErrorCodeNotFound,
			Message:   "The Gateway was not found.",
			RequestId: reqID,
		})

	case assetstore.RelayBindingOutcomeNodeConflict:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Code:      ErrorCodeNodeConflict,
			Message:   "The Relay Node is already bound to a Gateway Account.",
			RequestId: reqID,
		})

	case assetstore.RelayBindingOutcomeAccountConflict:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Code:      ErrorCodeAccountConflict,
			Message:   "The Gateway Account is already bound to another Relay Node.",
			RequestId: reqID,
		})

	default:
		s.writeError(w, r, authn.ErrUnavailable)
	}
}

func bindingDetailResponse(b assetstore.RelayNodeGatewayAccountBinding) RelayNodeGatewayAccountBindingDetail {
	detail := RelayNodeGatewayAccountBindingDetail{
		BindingId:          b.BindingID,
		RelayNodeId:        b.RelayNodeID,
		GatewayInstanceId:  b.GatewayInstanceID,
		GatewayAccountId:   strconv.FormatInt(b.GatewayAccountID, 10),
		EvidenceSnapshotId: b.EvidenceSnapshotID,
		BoundAt:            b.BoundAt,
		BoundBy:            b.BoundBy,
		BindReason:         RelayNodeGatewayAccountBindingDetailBindReason(b.BindReason),
	}
	if b.EndedAt != nil {
		detail.EndedAt = b.EndedAt
	}
	if b.EndedBy != nil {
		detail.EndedBy = b.EndedBy
	}
	if b.EndReason != nil {
		r := RelayNodeGatewayAccountBindingDetailEndReason(*b.EndReason)
		detail.EndReason = &r
	}
	return detail
}

func accountContextResponse(c assetstore.GatewayAccountContext) GatewayAccountContext {
	return GatewayAccountContext{
		AccountId: strconv.FormatInt(c.AccountID, 10),
		Name:      c.Name,
		Platform:  c.Platform,
		Type:      c.Type,
		Url:       c.URL,
		Status:    c.Status,
	}
}

// parseGatewayAccountID accepts only canonical positive decimal int64 strings.
// Lexical validation precedes ParseInt, which rejects overflow without rounding.
func parseGatewayAccountID(value string) (int64, bool) {
	if len(value) == 0 || len(value) > 19 || value[0] < '1' || value[0] > '9' {
		return 0, false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}
