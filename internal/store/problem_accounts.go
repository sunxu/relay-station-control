package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidProblemAccountQuery = errors.New("store: invalid problem account query")

type ProblemAccountFilters struct {
	Provider string
	NodeID   uuid.UUID
	Severity string
	Reason   string
	Email    string
}

type ProblemAccountCursor struct {
	Severity string
	Since    time.Time
	Email    string
	NodeID   uuid.UUID
}

type ProblemAccountQuery struct {
	Filters ProblemAccountFilters
	After   *ProblemAccountCursor
	Limit   int
}

type ProblemAccountIssue struct {
	OccurrenceID uuid.UUID `json:"occurrence_id"`
	Type         string    `json:"type"`
	Reason       string    `json:"reason"`
	Severity     string    `json:"severity"`
	Since        time.Time `json:"since"`
}

type ProblemAccountItem struct {
	InstanceID         uuid.UUID
	NodeName           string
	AccountKey         string
	Email              string
	Provider           string
	Issues             []ProblemAccountIssue
	Availability       AccountAvailability
	TokenState         string
	LastRefreshAt      *time.Time
	ExpectedValidUntil *time.Time
	NextRetryAt        *time.Time
	LastSuccessAt      *time.Time
	LastFailureAt      *time.Time
	HighestSeverity    string
	OldestActiveSince  time.Time
}

type ProblemAccountPage struct {
	Items   []ProblemAccountItem
	HasMore bool
}

type ProblemAccountReader interface {
	ListProblemAccounts(context.Context, ProblemAccountQuery) (ProblemAccountPage, error)
}

type ProblemAccountRepository struct{ pool *pgxpool.Pool }

func NewProblemAccountRepository(pool *pgxpool.Pool) (*ProblemAccountRepository, error) {
	if pool == nil {
		return nil, errors.New("store: problem account database unavailable")
	}
	return &ProblemAccountRepository{pool: pool}, nil
}

var problemAccountProviderPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func ValidateProblemAccountQuery(q ProblemAccountQuery) error {
	f := q.Filters
	if q.Limit < 1 || q.Limit > 100 || (f.Provider != "" && !problemAccountProviderPattern.MatchString(f.Provider)) ||
		(f.Severity != "" && f.Severity != "Critical" && f.Severity != "Warning") ||
		(f.Reason != "" && f.Reason != "token_invalid" && f.Reason != "account_blocked" && f.Reason != "forbidden" && f.Reason != "cross_node_duplicate_ownership") ||
		(f.Email != "" && !validProblemAccountEmail(f.Email)) {
		return ErrInvalidProblemAccountQuery
	}
	if c := q.After; c != nil && (c.NodeID == uuid.Nil || c.Since.IsZero() || !validProblemAccountEmail(c.Email) || (c.Severity != "Critical" && c.Severity != "Warning")) {
		return ErrInvalidProblemAccountQuery
	}
	return nil
}

func validProblemAccountEmail(value string) bool {
	if value == "" || len(value) > 320 || !utf8.ValidString(value) || value != strings.ToLower(strings.TrimSpace(value)) {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

// The query owns membership, aggregation, filtering, ordering and pagination.
// This adapter only maps the bounded DB projection; diagnostics never create or
// resolve a Problem issue.
func (r *ProblemAccountRepository) ListProblemAccounts(ctx context.Context, q ProblemAccountQuery) (ProblemAccountPage, error) {
	if err := ValidateProblemAccountQuery(q); err != nil {
		return ProblemAccountPage{}, err
	}
	if r == nil || r.pool == nil {
		return ProblemAccountPage{}, errors.New("store: problem account database unavailable")
	}
	var node, afterNode any
	var afterSince *time.Time
	afterSeverity, afterEmail := "", ""
	if q.Filters.NodeID != uuid.Nil {
		node = q.Filters.NodeID
	}
	if q.After != nil {
		afterNode, afterSince, afterSeverity, afterEmail = q.After.NodeID, &q.After.Since, q.After.Severity, q.After.Email
	}
	rows, err := r.pool.Query(ctx, `SELECT instance_id,node_name,account_key,email,provider,issues,availability,token_state,last_refresh_at,expected_valid_until,next_retry_at,last_success_at,last_failure_at,highest_severity,oldest_active_since
        FROM public.control_query_problem_accounts_v1($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
        ORDER BY CASE highest_severity WHEN 'Critical' THEN 2 ELSE 1 END DESC,oldest_active_since,email COLLATE "C",instance_id`,
		q.Filters.Provider, node, q.Filters.Severity, q.Filters.Reason, q.Filters.Email, afterSeverity, afterSince, afterEmail, afterNode, q.Limit)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22023" {
			return ProblemAccountPage{}, ErrInvalidProblemAccountQuery
		}
		return ProblemAccountPage{}, err
	}
	defer rows.Close()
	page := ProblemAccountPage{Items: make([]ProblemAccountItem, 0, q.Limit+1)}
	for rows.Next() {
		var item ProblemAccountItem
		var issues, availability []byte
		if err := rows.Scan(&item.InstanceID, &item.NodeName, &item.AccountKey, &item.Email, &item.Provider, &issues, &availability, &item.TokenState, &item.LastRefreshAt, &item.ExpectedValidUntil, &item.NextRetryAt, &item.LastSuccessAt, &item.LastFailureAt, &item.HighestSeverity, &item.OldestActiveSince); err != nil {
			return ProblemAccountPage{}, err
		}
		if err := json.Unmarshal(issues, &item.Issues); err != nil {
			return ProblemAccountPage{}, err
		}
		if err := json.Unmarshal(availability, &item.Availability); err != nil {
			return ProblemAccountPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ProblemAccountPage{}, err
	}
	page.HasMore = len(page.Items) > q.Limit
	if page.HasMore {
		page.Items = page.Items[:q.Limit]
	}
	return page, nil
}
