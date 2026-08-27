package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	accountstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	defaultAccountInventoryPageLimit = 50
	maxAccountInventoryPageLimit     = 100
	maxAccountInventoryCursorBytes   = 1536
	maxAccountInventoryRequestBytes  = 16 << 10
)

func (s *Server) QueryAccountInventory(w http.ResponseWriter, r *http.Request, params QueryAccountInventoryParams) {
	startedAt := time.Now()
	metricWriter := &accountInventoryMetricResponseWriter{ResponseWriter: w}
	w = metricWriter
	metricResultCount := 0
	defer func() {
		if s.accountInventoryMetrics != nil {
			result, errorCode := accountInventoryMetricStatus(metricWriter.status)
			_ = s.accountInventoryMetrics.record(result, errorCode, metricResultCount, time.Since(startedAt))
		}
	}()
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, params.XCSRFToken)
	if !ok {
		return
	}
	var body AccountInventoryQueryRequest
	if !decodeJSONWithLimit(w, r, &body, maxAccountInventoryRequestBytes) {
		return
	}
	if !validAccountInventoryRequest(body) {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	if s.accountInventory == nil || s.accountInventoryCursor == nil {
		s.accountInventoryError(w, r, accountstore.ErrAccountInventoryInconsistent)
		return
	}

	query := accountInventoryStoreQuery(body)
	filters := accountstore.AccountInventoryCursorFilters{
		Provider: query.Filters.Provider, Lifecycle: string(query.Filters.Lifecycle),
		BasicStatus: string(query.Filters.BasicStatus), Email: query.Filters.Email,
	}
	if body.Cursor != nil {
		after, err := s.accountInventoryCursor.Decode(
			*body.Cursor, session.AdminID, query.InstanceID, filters,
		)
		if err != nil {
			s.accountInventoryError(w, r, accountstore.ErrInvalidAccountInventoryCursor)
			return
		}
		query.AfterAccountKey = after
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	page, err := s.accountInventory.QueryPageAndAudit(r.Context(), query, accountstore.AccountInventoryViewAudit{
		ActorAdminID: session.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID,
	})
	if err != nil {
		s.accountInventoryError(w, r, err)
		return
	}

	var nextCursor *string
	if page.HasMore {
		if page.ContinuationAccountKey == "" {
			s.accountInventoryError(w, r, accountstore.ErrAccountInventoryInconsistent)
			return
		}
		encoded, err := s.accountInventoryCursor.Encode(
			session.AdminID, query.InstanceID, filters, page.ContinuationAccountKey,
		)
		if err != nil {
			s.accountInventoryError(w, r, accountstore.ErrAccountInventoryInconsistent)
			return
		}
		nextCursor = &encoded
	}
	items := make([]AccountInventoryItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, accountInventoryResponseItem(item))
	}
	metricResultCount = len(items)
	writeJSON(w, http.StatusOK, AccountInventoryQueryResponse{Items: items, NextCursor: nextCursor})
}

func accountInventoryStoreQuery(request AccountInventoryQueryRequest) accountstore.AccountInventoryQuery {
	limit := defaultAccountInventoryPageLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	query := accountstore.AccountInventoryQuery{InstanceID: uuid.UUID(request.InstanceId), Limit: limit}
	if request.Provider != nil {
		query.Filters.Provider = string(*request.Provider)
	}
	if request.Lifecycle != nil {
		query.Filters.Lifecycle = accountstore.AccountInventoryLifecycle(*request.Lifecycle)
	}
	if request.BasicStatus != nil {
		query.Filters.BasicStatus = accountstore.AccountInventoryBasicStatus(*request.BasicStatus)
	}
	if request.Email != nil {
		query.Filters.Email = string(*request.Email)
	}
	return query
}

func accountInventoryResponseItem(item accountstore.AccountInventoryItem) AccountInventoryItem {
	return AccountInventoryItem{
		InstanceId: item.InstanceID, Provider: ProviderName(item.Provider), Email: NormalizedAccountEmail(item.Email),
		BasicStatus: AccountInventoryBasicStatus(item.BasicStatus), Lifecycle: AccountInventoryLifecycle(item.Lifecycle),
		ConsecutiveMissingCount: item.ConsecutiveMissingCount,
		FirstSeenAt:             item.FirstSeenAt, LastSeenAt: item.LastSeenAt,
		MissingSince: item.MissingSince, OutOfScopeSince: item.OutOfScopeSince,
		LastRefreshAt: item.LastRefreshAt, NextRetryAt: item.NextRetryAt,
		SourceUpdatedAt: item.SourceUpdatedAt, ProviderLastCompleteAt: item.ProviderLastCompleteAt,
		ProviderDegraded:  item.ProviderDegraded,
		SnapshotFreshness: AccountInventorySnapshotFreshness(item.SnapshotFreshness),
	}
}

func (s *Server) accountInventoryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accountstore.ErrInvalidAccountInventoryQuery),
		errors.Is(err, accountstore.ErrInvalidAccountInventoryCursor):
		s.writeError(w, r, authn.ErrInvalid)
	case errors.Is(err, accountstore.ErrAccountInventoryInstanceNotFound):
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Code: ErrorCodeNotFound, Message: "The Relay Node was not found.", RequestId: s.requestID(r),
		})
	case errors.Is(err, accountstore.ErrAccountInventoryCapabilityUnsupported):
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Code: ErrorCodeConflict, Message: "The Relay Node does not support account inventory.", RequestId: s.requestID(r),
		})
	default:
		slog.Warn("account inventory query unavailable", "component", "account_inventory", "operation", "query", "reason", "database_unavailable")
		s.writeError(w, r, authn.ErrUnavailable)
	}
}

func validAccountInventoryRequest(request AccountInventoryQueryRequest) bool {
	if uuid.UUID(request.InstanceId) == uuid.Nil {
		return false
	}
	if request.Provider != nil && !validAssetIdentifier(string(*request.Provider), 1) {
		return false
	}
	if request.Lifecycle != nil && !request.Lifecycle.Valid() {
		return false
	}
	if request.BasicStatus != nil && !request.BasicStatus.Valid() {
		return false
	}
	if request.Email != nil && !validNormalizedAccountEmail(string(*request.Email)) {
		return false
	}
	if request.Cursor != nil && (len(*request.Cursor) < 1 || len(*request.Cursor) > maxAccountInventoryCursorBytes) {
		return false
	}
	if request.Limit != nil && (*request.Limit < 1 || *request.Limit > maxAccountInventoryPageLimit) {
		return false
	}
	return true
}

func validNormalizedAccountEmail(value string) bool {
	if value == "" || !utf8.ValidString(value) || len(value) > 320 || utf8.RuneCountInString(value) > 320 ||
		value != strings.TrimSpace(value) || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
