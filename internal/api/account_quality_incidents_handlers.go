package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	store "github.com/sunxu/relay-station-control/internal/store"
)

type nodeAccountQualityIncidentCursor struct {
	InstanceID   uuid.UUID `json:"instance_id"`
	Status       string    `json:"status"`
	Provider     string    `json:"provider"`
	FailureClass string    `json:"failure_class"`
	LastSeen     time.Time `json:"last_seen"`
	AccountKey   string    `json:"account_key"`
	RowClass     string    `json:"row_class"`
}

func (s *Server) ListNodeAccountQualityIncidents(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params ListNodeAccountQualityIncidentsParams) {
	s.prepare(w, r, true)
	if !s.authorizeTopologyRead(w, r) {
		return
	}
	nodeID := uuid.UUID(instanceID)
	status := "active"
	if params.Status != nil {
		status = string(*params.Status)
	}
	provider := ""
	if params.Provider != nil {
		provider = *params.Provider
	}
	failureClass := ""
	if params.FailureClass != nil {
		failureClass = string(*params.FailureClass)
	}
	limit := 25
	if params.Limit != nil {
		limit = *params.Limit
	}
	if nodeID == uuid.Nil || status != "active" || (provider != "" && !accountQualityProviderPattern.MatchString(provider)) || (failureClass != "" && !validIncidentFailureClass(failureClass)) || limit < 1 || limit > 100 {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	var afterSeen *time.Time
	afterAccount, afterClass := "", ""
	if params.Cursor != nil {
		if len(*params.Cursor) == 0 || len(*params.Cursor) > 8192 {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(*params.Cursor)
		var cursor nodeAccountQualityIncidentCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.InstanceID != nodeID || cursor.Status != status || cursor.Provider != provider || cursor.FailureClass != failureClass || cursor.LastSeen.IsZero() || !validHistoryAccountKey(cursor.AccountKey) || cursor.RowClass == "" || !validIncidentFailureClass(cursor.RowClass) || cursor.RowClass != cursor.FailureClass && failureClass != "" {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		afterSeen = &cursor.LastSeen
		afterAccount, afterClass = cursor.AccountKey, cursor.RowClass
	}
	if s.accountQualityIncidents == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.accountQualityIncidents.ListAccountQualityIncidents(ctx, store.AccountQualityIncidentQuery{InstanceID: nodeID, Provider: provider, FailureClass: failureClass, AfterLastSeen: afterSeen, AfterAccountKey: afterAccount, AfterFailureClass: afterClass, Limit: limit})
	if err != nil {
		s.topologyReadError(w, r, err)
		return
	}
	items := make([]NodeAccountQualityIncidentItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, NodeAccountQualityIncidentItem{NodeId: item.NodeID, AccountKey: item.AccountKey, Provider: item.Provider, FailureClass: NodeAccountQualityIncidentItemFailureClass(item.FailureClass), Status: NodeAccountQualityIncidentItemStatus(item.Status), FirstSeen: item.FirstSeen, LastSeen: item.LastSeen, HitCount: item.HitCount, LastSuccessAt: item.LastSuccessAt})
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(nodeAccountQualityIncidentCursor{InstanceID: nodeID, Status: status, Provider: provider, FailureClass: failureClass, LastSeen: last.LastSeen, AccountKey: last.AccountKey, RowClass: last.FailureClass})
		if err != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		value := base64.RawURLEncoding.EncodeToString(raw)
		if len(value) > 8192 {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		next = &value
	}
	writeJSON(w, http.StatusOK, NodeAccountQualityIncidentResponse{InstanceId: nodeID, Items: items, NextCursor: next})
}

func validIncidentFailureClass(value string) bool {
	switch value {
	case "auth", "quota", "rate_limit", "upstream":
		return true
	default:
		return false
	}
}
