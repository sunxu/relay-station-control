package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	duplicatestore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	defaultCrossNodeDuplicateOccurrencePageLimit = 50
	maxCrossNodeDuplicateOccurrenceCursorBytes   = 512
)

// crossNodeDuplicateOccurrenceListCursor and
// crossNodeDuplicateEvidenceListCursor are plain base64(JSON) opaque
// cursors -- intentionally NOT HMAC-signed like JobCursorCodec /
// AccountInventoryCursorCodec. Every field this read model returns is
// already visible to any authenticated Control session regardless of
// cursor value (list/detail filters are always re-applied server-side in
// the store's SQL WHERE clause), so a tampered cursor can only shift the
// paging window within data the caller can already read in full -- it can
// never cross a privilege or filter boundary. Building a second HMAC
// cursor codec here would duplicate existing machinery for no additional
// security benefit (Phase 5 review: minimal-change principle).
type crossNodeDuplicateOccurrenceListCursor struct {
	LastSeenAt   time.Time `json:"last_seen_at"`
	OccurrenceID uuid.UUID `json:"occurrence_id"`
}

type crossNodeDuplicateEvidenceListCursor struct {
	RecordedAt    time.Time `json:"recorded_at"`
	ObservationID uuid.UUID `json:"observation_id"`
}

func encodeCrossNodeDuplicateOccurrenceListCursor(value crossNodeDuplicateOccurrenceListCursor) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCrossNodeDuplicateOccurrenceListCursor(encoded string) (crossNodeDuplicateOccurrenceListCursor, error) {
	var cursor crossNodeDuplicateOccurrenceListCursor
	if encoded == "" || len(encoded) > maxCrossNodeDuplicateOccurrenceCursorBytes {
		return cursor, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cursor, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery
	}
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.LastSeenAt.IsZero() || cursor.OccurrenceID == uuid.Nil {
		return crossNodeDuplicateOccurrenceListCursor{}, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery
	}
	return cursor, nil
}

func encodeCrossNodeDuplicateEvidenceListCursor(value crossNodeDuplicateEvidenceListCursor) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCrossNodeDuplicateEvidenceListCursor(encoded string) (crossNodeDuplicateEvidenceListCursor, error) {
	var cursor crossNodeDuplicateEvidenceListCursor
	if encoded == "" || len(encoded) > maxCrossNodeDuplicateOccurrenceCursorBytes {
		return cursor, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cursor, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery
	}
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.RecordedAt.IsZero() || cursor.ObservationID == uuid.Nil {
		return crossNodeDuplicateEvidenceListCursor{}, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery
	}
	return cursor, nil
}

func (s *Server) authorizeCrossNodeDuplicateOccurrenceRead(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return false
	}
	if s.crossNodeDuplicateOccurrences == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return false
	}
	return true
}

func (s *Server) ListCrossNodeDuplicateOccurrences(w http.ResponseWriter, r *http.Request, params ListCrossNodeDuplicateOccurrencesParams) {
	s.prepare(w, r, true)
	if !s.authorizeCrossNodeDuplicateOccurrenceRead(w, r) {
		return
	}
	limit := defaultCrossNodeDuplicateOccurrencePageLimit
	if params.Limit != nil {
		limit = *params.Limit
	}
	filters := duplicatestore.CrossNodeDuplicateOccurrenceFilters{}
	if params.Status != nil {
		filters.Status = string(*params.Status)
	}
	if params.AccountKey != nil {
		filters.AccountKey = *params.AccountKey
	}
	if params.InstanceId != nil {
		filters.InstanceID = uuid.UUID(*params.InstanceId)
	}
	var cursor *duplicatestore.CrossNodeDuplicateOccurrenceCursor
	if params.Cursor != nil {
		decoded, err := decodeCrossNodeDuplicateOccurrenceListCursor(*params.Cursor)
		if err != nil {
			s.crossNodeDuplicateOccurrenceReadError(w, r, err)
			return
		}
		cursor = &duplicatestore.CrossNodeDuplicateOccurrenceCursor{LastSeenAt: decoded.LastSeenAt, OccurrenceID: decoded.OccurrenceID}
	}
	page, err := s.crossNodeDuplicateOccurrences.ListOccurrences(r.Context(), filters, cursor, limit)
	if err != nil {
		s.crossNodeDuplicateOccurrenceReadError(w, r, err)
		return
	}
	items := make([]CrossNodeDuplicateOccurrenceSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, crossNodeDuplicateOccurrenceSummaryResponse(item))
	}
	var nextCursor *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		encoded, encodeErr := encodeCrossNodeDuplicateOccurrenceListCursor(crossNodeDuplicateOccurrenceListCursor{
			LastSeenAt: last.LastSeenAt, OccurrenceID: last.OccurrenceID,
		})
		if encodeErr != nil {
			s.crossNodeDuplicateOccurrenceReadError(w, r, encodeErr)
			return
		}
		nextCursor = &encoded
	}
	writeJSON(w, http.StatusOK, CrossNodeDuplicateOccurrenceListResponse{Items: items, NextCursor: nextCursor})
}

func (s *Server) GetCrossNodeDuplicateOccurrence(w http.ResponseWriter, r *http.Request, occurrenceId OccurrenceId) {
	s.prepare(w, r, true)
	if !s.authorizeCrossNodeDuplicateOccurrenceRead(w, r) {
		return
	}
	detail, err := s.crossNodeDuplicateOccurrences.GetOccurrence(r.Context(), uuid.UUID(occurrenceId))
	if err != nil {
		s.crossNodeDuplicateOccurrenceReadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, crossNodeDuplicateOccurrenceSummaryResponse(detail))
}

func (s *Server) ListCrossNodeDuplicateOccurrenceEvidence(w http.ResponseWriter, r *http.Request, occurrenceId OccurrenceId, params ListCrossNodeDuplicateOccurrenceEvidenceParams) {
	s.prepare(w, r, true)
	if !s.authorizeCrossNodeDuplicateOccurrenceRead(w, r) {
		return
	}
	limit := defaultCrossNodeDuplicateOccurrencePageLimit
	if params.Limit != nil {
		limit = *params.Limit
	}
	var cursorTime *time.Time
	var cursorObservationID uuid.UUID
	if params.Cursor != nil {
		decoded, err := decodeCrossNodeDuplicateEvidenceListCursor(*params.Cursor)
		if err != nil {
			s.crossNodeDuplicateOccurrenceReadError(w, r, err)
			return
		}
		cursorTime, cursorObservationID = &decoded.RecordedAt, decoded.ObservationID
	}
	page, err := s.crossNodeDuplicateOccurrences.ListOccurrenceEvidence(r.Context(), uuid.UUID(occurrenceId), cursorTime, cursorObservationID, limit)
	if err != nil {
		s.crossNodeDuplicateOccurrenceReadError(w, r, err)
		return
	}
	items := make([]CrossNodeDuplicateOccurrenceEvidenceItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, crossNodeDuplicateOccurrenceEvidenceItemResponse(item))
	}
	var nextCursor *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		encoded, encodeErr := encodeCrossNodeDuplicateEvidenceListCursor(crossNodeDuplicateEvidenceListCursor{
			RecordedAt: last.RecordedAt, ObservationID: last.ObservationID,
		})
		if encodeErr != nil {
			s.crossNodeDuplicateOccurrenceReadError(w, r, encodeErr)
			return
		}
		nextCursor = &encoded
	}
	writeJSON(w, http.StatusOK, CrossNodeDuplicateOccurrenceEvidenceListResponse{Items: items, NextCursor: nextCursor})
}

func (s *Server) crossNodeDuplicateOccurrenceReadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery):
		s.writeError(w, r, authn.ErrInvalid)
	case errors.Is(err, duplicatestore.ErrCrossNodeDuplicateOccurrenceNotFound):
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorCodeNotFound, Message: "The occurrence was not found.", RequestId: s.requestID(r)})
	default:
		slog.Warn("cross-node duplicate occurrence read failed", "component", "cross_node_duplicate_ownership", "action", "read", "result", "database_unavailable")
		s.writeError(w, r, authn.ErrUnavailable)
	}
}

func crossNodeDuplicateOccurrenceSummaryResponse(value duplicatestore.CrossNodeDuplicateOccurrenceSummary) CrossNodeDuplicateOccurrenceSummary {
	affected := make([]uuid.UUID, 0, len(value.AffectedNodes))
	affected = append(affected, value.AffectedNodes...)
	var latestEvaluationID *uuid.UUID
	if value.LatestEvaluationID != nil {
		converted := *value.LatestEvaluationID
		latestEvaluationID = &converted
	}
	return CrossNodeDuplicateOccurrenceSummary{
		OccurrenceId: value.OccurrenceID, EnvironmentId: value.EnvironmentID, AccountKey: value.AccountKey,
		ConflictType: value.ConflictType, Status: CrossNodeDuplicateOccurrenceStatus(value.Status),
		Severity:    CrossNodeDuplicateOccurrenceSummarySeverity(value.Severity),
		FirstSeenAt: value.FirstSeenAt, LastSeenAt: value.LastSeenAt, ResolvedAt: value.ResolvedAt,
		EvidenceState:       CrossNodeDuplicateOccurrenceEvidenceState(value.EvidenceState),
		LastFullyVerifiedAt: value.LastFullyVerifiedAt, LatestEvaluationId: latestEvaluationID,
		AffectedNodes: affected,
	}
}

func crossNodeDuplicateOccurrenceEvidenceItemResponse(value duplicatestore.CrossNodeDuplicateOccurrenceEvidenceItem) CrossNodeDuplicateOccurrenceEvidenceItem {
	var sourcePollRunID *uuid.UUID
	if value.SourcePollRunID != nil {
		converted := *value.SourcePollRunID
		sourcePollRunID = &converted
	}
	return CrossNodeDuplicateOccurrenceEvidenceItem{
		ObservationId: value.ObservationID, InstanceId: value.InstanceID,
		ObservationKind: CrossNodeDuplicateOccurrenceObservationKind(value.ObservationKind),
		SourceProvider:  value.SourceProvider, SourceScheduledAt: value.SourceScheduledAt, SourceCompletedAt: value.SourceCompletedAt,
		SourcePollRunId: sourcePollRunID, EvaluationId: value.EvaluationID, EvaluationAt: value.EvaluationAt,
		RecordedAt: value.RecordedAt,
	}
}
