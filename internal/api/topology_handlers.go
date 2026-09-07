package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

// authorizeTopologyRead checks the existing administrator role without requiring
// CSRF on GET. These reads never execute a binding or inventory mutation.
func (s *Server) authorizeTopologyRead(w http.ResponseWriter, r *http.Request) bool {
	session, ok := s.requireSession(w, r, false, "")
	if !ok {
		return false
	}
	if session.Role != "super_admin" {
		s.writeError(w, r, authn.ErrForbidden)
		return false
	}
	return true
}

type nodeDuplicateHistoryCursor struct {
	InstanceID   uuid.UUID `json:"instance_id"`
	Status       string    `json:"status"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	OccurrenceID uuid.UUID `json:"occurrence_id"`
}

func (s *Server) ListNodeDuplicateHistory(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params ListNodeDuplicateHistoryParams) {
	s.prepare(w, r, true)
	if !s.authorizeTopologyRead(w, r) {
		return
	}
	nodeID := uuid.UUID(instanceID)
	limit := 25
	if params.Limit != nil {
		limit = *params.Limit
	}
	status := ""
	if params.Status != nil {
		status = string(*params.Status)
	}
	if nodeID == uuid.Nil || limit < 1 || limit > 200 || (status != "" && status != "ACTIVE" && status != "RESOLVED") {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	var cursor *assetstore.CrossNodeDuplicateOccurrenceCursor
	if params.Cursor != nil {
		var decoded nodeDuplicateHistoryCursor
		if len(*params.Cursor) == 0 || len(*params.Cursor) > 512 {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(*params.Cursor)
		if err != nil || json.Unmarshal(raw, &decoded) != nil || decoded.InstanceID != nodeID || decoded.Status != status || decoded.LastSeenAt.IsZero() || decoded.OccurrenceID == uuid.Nil {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		cursor = &assetstore.CrossNodeDuplicateOccurrenceCursor{LastSeenAt: decoded.LastSeenAt, OccurrenceID: decoded.OccurrenceID}
	}
	if s.nodeDuplicateHistory == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.nodeDuplicateHistory.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(ctx, nodeID, status, cursor, limit)
	if err != nil {
		s.topologyReadError(w, r, err)
		return
	}
	if page.ObservedAt == nil || page.ObservedAt.IsZero() {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	items := make([]CrossNodeDuplicateOccurrenceSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, crossNodeDuplicateOccurrenceSummaryResponse(item))
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(nodeDuplicateHistoryCursor{InstanceID: nodeID, Status: status, LastSeenAt: last.LastSeenAt, OccurrenceID: last.OccurrenceID})
		if err != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		encoded := base64.RawURLEncoding.EncodeToString(raw)
		next = &encoded
	}
	writeJSON(w, http.StatusOK, NodeDuplicateHistoryResponse{InstanceId: nodeID, ObservedAt: *page.ObservedAt, Involvement: Historical, Items: items, NextCursor: next})
}

func (s *Server) GetNodeInventoryProviderStates(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId) {
	s.prepare(w, r, true)
	if !s.authorizeTopologyRead(w, r) {
		return
	}
	nodeID := uuid.UUID(instanceID)
	if nodeID == uuid.Nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	if s.providerStates == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	view, err := s.providerStates.GetProviderStates(ctx, nodeID)
	if err != nil {
		s.topologyReadError(w, r, err)
		return
	}
	providers := make([]NodeInventoryProviderState, 0, len(view.Providers))
	for _, p := range view.Providers {
		row := NodeInventoryProviderState{Provider: p.Provider, MonitoringStatus: NodeInventoryProviderStateMonitoringStatus(p.MonitoringStatus), CurrentScheduledAt: p.CurrentScheduledAt, LastCompleteAt: p.LastCompleteAt, SnapshotFreshness: NodeInventoryProviderStateSnapshotFreshness(p.SnapshotFreshness), HealthScheduledAt: p.HealthScheduledAt, HealthDegraded: p.HealthDegraded}
		if p.State != nil {
			v := *p.State
			row.State = &v
		}
		if p.HealthReason != nil {
			v := *p.HealthReason
			row.HealthReason = &v
		}
		providers = append(providers, row)
	}
	writeJSON(w, http.StatusOK, NodeInventoryProviderStatesResponse{InstanceId: nodeID, ObservedAt: view.ObservedAt, Providers: providers})
}

func (s *Server) topologyReadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, assetstore.ErrAssetNotFound), errors.Is(err, assetstore.ErrAccountInventoryProviderStateNotFound):
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorCodeNotFound, Message: "The Relay Node was not found.", RequestId: s.requestID(r)})
	case errors.Is(err, assetstore.ErrInvalidAccountInventoryProviderStateQuery), errors.Is(err, assetstore.ErrInvalidAssetQuery), errors.Is(err, assetstore.ErrCrossNodeDuplicateOccurrenceQuery):
		s.writeError(w, r, authn.ErrInvalid)
	default:
		s.writeError(w, r, authn.ErrUnavailable)
	}
}
