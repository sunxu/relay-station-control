package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	store "github.com/sunxu/relay-station-control/internal/store"
)

type nodeAccountRequestHistoryCursor struct {
	InstanceID uuid.UUID `json:"instance_id"`
	AccountKey string    `json:"account_key"`
	OccurredAt time.Time `json:"occurred_at"`
	EventHash  string    `json:"event_hash"`
}

func (s *Server) ListNodeAccountRequestHistory(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params ListNodeAccountRequestHistoryParams) {
	s.prepare(w, r, true)
	if !s.authorizeTopologyRead(w, r) {
		return
	}
	nodeID := uuid.UUID(instanceID)
	accountKey := params.AccountKey
	limit := 25
	if params.Limit != nil {
		limit = *params.Limit
	}
	if nodeID == uuid.Nil || !validHistoryAccountKey(accountKey) || limit < 1 || limit > 100 {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	var afterAt *time.Time
	afterHash := ""
	if params.Cursor != nil {
		if len(*params.Cursor) == 0 || len(*params.Cursor) > 8192 {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(*params.Cursor)
		var cursor nodeAccountRequestHistoryCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.InstanceID != nodeID || cursor.AccountKey != accountKey || cursor.OccurredAt.IsZero() || cursor.EventHash == "" || len(cursor.EventHash) > 256 || !utf8.ValidString(cursor.EventHash) || strings.ContainsRune(cursor.EventHash, 0) {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		afterAt = &cursor.OccurredAt
		afterHash = cursor.EventHash
	}
	if s.accountRequestHistory == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.accountRequestHistory.ListAccountRequestHistory(ctx, store.AccountRequestHistoryQuery{InstanceID: nodeID, AccountKey: accountKey, AfterOccurredAt: afterAt, AfterEventHash: afterHash, Limit: limit})
	if err != nil {
		s.accountRequestHistoryError(w, r, err)
		return
	}
	items := make([]NodeAccountRequestHistoryItem, 0, len(page.Items))
	for _, item := range page.Items {
		var class *NodeAccountRequestHistoryItemFailureClass
		if item.FailureClass != nil {
			value := NodeAccountRequestHistoryItemFailureClass(*item.FailureClass)
			class = &value
		}
		items = append(items, NodeAccountRequestHistoryItem{OccurredAt: item.OccurredAt, Model: item.Model, Success: item.Success, FailureClass: class, DurationMs: item.DurationMS, RequestId: item.RequestID})
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(nodeAccountRequestHistoryCursor{InstanceID: nodeID, AccountKey: accountKey, OccurredAt: last.OccurredAt, EventHash: last.EventHash})
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
	writeJSON(w, http.StatusOK, NodeAccountRequestHistoryResponse{InstanceId: nodeID, AccountKey: accountKey, Items: items, NextCursor: next})
}

func (s *Server) accountRequestHistoryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidAccountInventoryQuery):
		s.writeError(w, r, authn.ErrInvalid)
	case errors.Is(err, store.ErrAccountInventoryInstanceNotFound):
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorCodeNotFound, Message: "The Relay Node or account was not found.", RequestId: s.requestID(r)})
	default:
		s.writeError(w, r, authn.ErrUnavailable)
	}
}

func validHistoryAccountKey(value string) bool {
	return len(value) >= 3 && len(value) <= 385 && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && strings.Contains(value, ":") && value == strings.TrimSpace(value)
}
