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

type accountAvailabilityCursor struct {
	InstanceID   uuid.UUID `json:"instance_id"`
	AccountKey   string    `json:"account_key"`
	Status       string    `json:"status"`
	ConfirmedAt  time.Time `json:"confirmed_at"`
	OccurrenceID uuid.UUID `json:"occurrence_id"`
}

func (s *Server) SetAccountAvailabilityReader(reader store.AccountAvailabilityReader) {
	s.accountAvailability = reader
}

func (s *Server) ListNodeAccountAvailabilityOccurrences(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params ListNodeAccountAvailabilityOccurrencesParams) {
	s.prepare(w, r, true)
	if !s.authorizeTopologyRead(w, r) {
		return
	}
	query := store.AccountAvailabilityOccurrenceQuery{InstanceID: uuid.UUID(instanceID), Status: "ACTIVE", Limit: 25}
	if params.AccountKey != nil {
		query.AccountKey = *params.AccountKey
	}
	if params.Status != nil {
		query.Status = string(*params.Status)
	}
	if params.Limit != nil {
		query.Limit = *params.Limit
	}
	if query.InstanceID == uuid.Nil || (params.AccountKey != nil && !validHistoryAccountKey(query.AccountKey)) || (query.Status != "ACTIVE" && query.Status != "RESOLVED") || query.Limit < 1 || query.Limit > 100 {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	if params.Cursor != nil {
		if len(*params.Cursor) == 0 || len(*params.Cursor) > 8192 {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(*params.Cursor)
		var cursor accountAvailabilityCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.InstanceID != query.InstanceID || cursor.AccountKey != query.AccountKey || cursor.Status != query.Status || cursor.ConfirmedAt.IsZero() || cursor.OccurrenceID == uuid.Nil {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		query.AfterConfirmedAt = &cursor.ConfirmedAt
		query.AfterOccurrenceID = cursor.OccurrenceID
	}
	if s.accountAvailability == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.accountAvailability.ListAccountAvailabilityOccurrences(ctx, query)
	if err != nil {
		s.topologyReadError(w, r, err)
		return
	}
	items := make([]NodeAccountAvailabilityOccurrenceItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, NodeAccountAvailabilityOccurrenceItem{OccurrenceId: item.OccurrenceID, InstanceId: item.InstanceID, AccountKey: item.AccountKey, Reason: NodeAccountAvailabilityOccurrenceItemReason(item.Reason), Severity: NodeAccountAvailabilityOccurrenceItemSeverity(item.Severity), Status: NodeAccountAvailabilityOccurrenceItemStatus(item.Status), FirstSeenAt: item.FirstSeenAt, LastFailureAt: item.LastFailureAt, ConfirmedAt: item.ConfirmedAt, ResolvedAt: item.ResolvedAt})
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(accountAvailabilityCursor{InstanceID: query.InstanceID, AccountKey: query.AccountKey, Status: query.Status, ConfirmedAt: last.ConfirmedAt, OccurrenceID: last.OccurrenceID})
		if err != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		value := base64.RawURLEncoding.EncodeToString(raw)
		next = &value
	}
	writeJSON(w, http.StatusOK, NodeAccountAvailabilityOccurrenceResponse{InstanceId: query.InstanceID, Items: items, NextCursor: next})
}
