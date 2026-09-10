package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func (s *Server) SetProblemAccountReader(reader store.ProblemAccountReader) error {
	if reader == nil || s.service == nil {
		return errors.New("api: problem account reader unavailable")
	}
	codec, err := store.NewProblemAccountCursorCodec(s.service.Config().Keyring)
	if err != nil {
		return err
	}
	s.problemAccounts, s.problemAccountCursor = reader, codec
	return nil
}

func (s *Server) QueryProblemAccounts(w http.ResponseWriter, r *http.Request, params QueryProblemAccountsParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, string(params.XCSRFToken))
	if !ok {
		return
	}
	if session.Role != "super_admin" {
		s.auditSecurityRejection(r, &session, authn.AuditAuthorization, authn.ErrorCodeForbidden)
		s.writeError(w, r, authn.ErrForbidden)
		return
	}
	var body ProblemAccountQueryRequest
	if !decodeJSONWithLimit(w, r, &body, 16<<10) {
		return
	}
	q := store.ProblemAccountQuery{Limit: 25}
	if body.Provider != nil {
		if *body.Provider == "" {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		q.Filters.Provider = *body.Provider
	}
	if body.Node != nil {
		q.Filters.NodeID = uuid.UUID(*body.Node)
		if q.Filters.NodeID == uuid.Nil {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
	}
	if body.Severity != nil {
		if *body.Severity == "" {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		q.Filters.Severity = string(*body.Severity)
	}
	if body.Reason != nil {
		if *body.Reason == "" {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		q.Filters.Reason = string(*body.Reason)
	}
	if body.Email != nil {
		q.Filters.Email = strings.ToLower(strings.TrimSpace(*body.Email))
		if !validNormalizedAccountEmail(q.Filters.Email) {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
	}
	if body.Limit != nil {
		q.Limit = *body.Limit
	}
	if store.ValidateProblemAccountQuery(q) != nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	if s.problemAccounts == nil || s.problemAccountCursor == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	if body.Cursor != nil {
		after, err := s.problemAccountCursor.Decode(*body.Cursor, session.AdminID, q.Filters)
		if err != nil {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		q.After = &after
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.problemAccounts.ListProblemAccounts(ctx, q)
	if err != nil {
		if errors.Is(err, store.ErrInvalidProblemAccountQuery) {
			s.writeError(w, r, authn.ErrInvalid)
		} else {
			s.writeError(w, r, authn.ErrUnavailable)
		}
		return
	}
	items := make([]ProblemAccountItem, 0, len(page.Items))
	for _, item := range page.Items {
		issues := make([]ProblemAccountIssue, 0, len(item.Issues))
		for _, issue := range item.Issues {
			issues = append(issues, ProblemAccountIssue{OccurrenceId: issue.OccurrenceID, Type: ProblemAccountIssueType(issue.Type), Reason: ProblemAccountIssueReason(issue.Reason), Severity: ProblemAccountIssueSeverity(issue.Severity), Since: issue.Since})
		}
		items = append(items, ProblemAccountItem{InstanceId: item.InstanceID, NodeName: item.NodeName, AccountKey: item.AccountKey, Email: item.Email, Provider: ProblemAccountItemProvider(item.Provider), Issues: issues,
			Availability: &AccountAvailability{State: AccountAvailabilityState(item.Availability.State), Reason: AccountAvailabilityReason(item.Availability.Reason), Since: item.Availability.Since},
			TokenState:   ProblemAccountItemTokenState(item.TokenState), LastRefreshAt: item.LastRefreshAt, ExpectedValidUntil: item.ExpectedValidUntil, NextRetryAt: item.NextRetryAt, LastSuccessAt: item.LastSuccessAt, LastFailureAt: item.LastFailureAt, HighestSeverity: ProblemAccountItemHighestSeverity(item.HighestSeverity), OldestActiveSince: item.OldestActiveSince})
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		value, err := s.problemAccountCursor.Encode(session.AdminID, q.Filters, store.ProblemAccountCursor{Severity: last.HighestSeverity, Since: last.OldestActiveSince, Email: last.Email, NodeID: last.InstanceID})
		if err != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		next = &value
	}
	writeJSON(w, http.StatusOK, ProblemAccountResponse{Items: items, NextCursor: next})
}
