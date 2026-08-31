package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/auth"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrInvalidAccountInventoryQuery          = errors.New("store: invalid account inventory query")
	ErrAccountInventoryInstanceNotFound      = errors.New("store: account inventory instance was not found")
	ErrAccountInventoryCapabilityUnsupported = errors.New("store: account inventory capability is unsupported")
	ErrAccountInventoryInconsistent          = errors.New("store: account inventory current state is inconsistent")
)

type AccountInventoryBasicStatus string

const (
	AccountInventoryBasicStatusReportedActive AccountInventoryBasicStatus = "reported_active"
	AccountInventoryBasicStatusDisabled       AccountInventoryBasicStatus = "disabled"
	AccountInventoryBasicStatusUnavailable    AccountInventoryBasicStatus = "unavailable"
	AccountInventoryBasicStatusError          AccountInventoryBasicStatus = "error"
	AccountInventoryBasicStatusUnknown        AccountInventoryBasicStatus = "unknown"
)

type AccountInventorySnapshotFreshness string

const (
	AccountInventorySnapshotFreshnessFresh      AccountInventorySnapshotFreshness = "fresh"
	AccountInventorySnapshotFreshnessStale      AccountInventorySnapshotFreshness = "stale"
	AccountInventorySnapshotFreshnessOutOfScope AccountInventorySnapshotFreshness = "out_of_scope"
)

type AccountInventoryQueryFilters struct {
	Provider    string
	Lifecycle   AccountInventoryLifecycle
	BasicStatus AccountInventoryBasicStatus
	Email       string
}

type AccountInventoryQuery struct {
	InstanceID      uuid.UUID
	Filters         AccountInventoryQueryFilters
	AfterAccountKey string
	Limit           int
}

type AccountInventoryViewAudit struct {
	ActorAdminID      uuid.UUID
	SourceFingerprint []byte
	RequestID         string
}

type AccountInventoryItem struct {
	InstanceID              uuid.UUID
	Provider                string
	Email                   string
	BasicStatus             AccountInventoryBasicStatus
	Lifecycle               AccountInventoryLifecycle
	ConsecutiveMissingCount int
	FirstSeenAt             time.Time
	LastSeenAt              time.Time
	MissingSince            *time.Time
	OutOfScopeSince         *time.Time
	LastRefreshAt           *time.Time
	NextRetryAt             *time.Time
	SourceUpdatedAt         *time.Time
	ProviderLastCompleteAt  time.Time
	ProviderDegraded        bool
	SnapshotFreshness       AccountInventorySnapshotFreshness
}

type AccountInventoryPage struct {
	Items                  []AccountInventoryItem
	HasMore                bool
	ContinuationAccountKey string
}

type AccountInventoryReader interface {
	QueryPageAndAudit(context.Context, AccountInventoryQuery, AccountInventoryViewAudit) (AccountInventoryPage, error)
	CheckCompatibility(context.Context) error
}

type AccountInventoryRepository struct {
	pool       *pgxpool.Pool
	queries    *generated.Queries
	compatible atomic.Bool
}

var _ AccountInventoryReader = (*AccountInventoryRepository)(nil)

func NewAccountInventoryRepository(pool *pgxpool.Pool) (*AccountInventoryRepository, error) {
	if pool == nil {
		return nil, errors.New("store: account inventory database is unavailable")
	}
	return &AccountInventoryRepository{pool: pool, queries: generated.New(pool)}, nil
}

func (repository *AccountInventoryRepository) CheckCompatibility(ctx context.Context) error {
	compatible, err := repository.queries.CheckAccountInventoryReadonlyQueryCompatibility(ctx)
	if err != nil || !compatible.Valid || !compatible.Bool {
		repository.compatible.Store(false)
		return ErrAccountInventoryInconsistent
	}
	repository.compatible.Store(true)
	return nil
}

func (repository *AccountInventoryRepository) QueryPageAndAudit(
	ctx context.Context, query AccountInventoryQuery, audit AccountInventoryViewAudit,
) (AccountInventoryPage, error) {
	if repository == nil || !repository.compatible.Load() {
		return AccountInventoryPage{}, ErrAccountInventoryInconsistent
	}
	if !validAccountInventoryQuery(query) || !validAccountInventoryViewAudit(audit) {
		return AccountInventoryPage{}, ErrInvalidAccountInventoryQuery
	}

	var committed AccountInventoryPage
	err := pgx.BeginTxFunc(ctx, repository.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		encoded, err := queries.QueryCurrentAccountInventoryV1(ctx,
			generated.QueryCurrentAccountInventoryV1Params{
				InstanceID: nullableUUID(query.InstanceID), Provider: query.Filters.Provider,
				Lifecycle: string(query.Filters.Lifecycle), BasicStatus: string(query.Filters.BasicStatus),
				NormalizedEmail: query.Filters.Email, AfterAccountKey: query.AfterAccountKey,
				PageLimit: int32(query.Limit + 1),
			})
		if err != nil {
			return accountInventoryDatabaseError(err)
		}
		page, err := accountInventoryPageFromRows(query, encoded)
		if err != nil {
			return err
		}
		details, err := auth.SanitizeAuditDetails(auth.AuditAccountInventoryView, map[string]any{
			"instance_id":              query.InstanceID.String(),
			"provider_filter_used":     query.Filters.Provider != "",
			"lifecycle_filter_used":    query.Filters.Lifecycle != "",
			"basic_status_filter_used": query.Filters.BasicStatus != "",
			"email_filter_used":        query.Filters.Email != "",
			"cursor_used":              query.AfterAccountKey != "",
			"result_count":             len(page.Items),
		})
		if err != nil {
			return ErrAccountInventoryInconsistent
		}
		category, err := auth.AuditCategoryFor(auth.AuditAccountInventoryView)
		if err != nil {
			return ErrAccountInventoryInconsistent
		}
		encodedDetails, err := json.Marshal(details)
		if err != nil {
			return ErrAccountInventoryInconsistent
		}
		_, err = queries.InsertAuditLog(ctx, generated.InsertAuditLogParams{
			AuditID: nullableUUID(uuid.New()), Category: string(category),
			Action: string(auth.AuditAccountInventoryView), Result: string(auth.AuditResultSuccess),
			ActorAdminID: nullableUUID(audit.ActorAdminID), SourceFingerprint: audit.SourceFingerprint,
			RequestID: audit.RequestID, Details: encodedDetails,
		})
		if err != nil {
			return err
		}
		committed = page
		return nil
	})
	if err != nil {
		return AccountInventoryPage{}, err
	}
	return committed, nil
}

type accountInventoryRow struct {
	InstanceID              uuid.UUID                         `json:"instance_id"`
	Provider                string                            `json:"provider"`
	AccountKey              string                            `json:"account_key"`
	NormalizedEmail         string                            `json:"normalized_email"`
	BasicStatus             AccountInventoryBasicStatus       `json:"basic_status"`
	Lifecycle               AccountInventoryLifecycle         `json:"lifecycle"`
	ConsecutiveMissingCount int                               `json:"consecutive_missing_count"`
	FirstSeenAt             time.Time                         `json:"first_seen_at"`
	LastSeenAt              time.Time                         `json:"last_seen_at"`
	MissingSince            *time.Time                        `json:"missing_since"`
	OutOfScopeSince         *time.Time                        `json:"out_of_scope_since"`
	LastRefreshAt           *time.Time                        `json:"last_refresh_at"`
	NextRetryAt             *time.Time                        `json:"next_retry_at"`
	SourceUpdatedAt         *time.Time                        `json:"source_updated_at"`
	ProviderLastCompleteAt  time.Time                         `json:"provider_last_complete_at"`
	ProviderDegraded        *bool                             `json:"provider_degraded"`
	SnapshotFreshness       AccountInventorySnapshotFreshness `json:"snapshot_freshness"`
}

func accountInventoryPageFromRows(query AccountInventoryQuery, rows [][]byte) (AccountInventoryPage, error) {
	page := AccountInventoryPage{Items: make([]AccountInventoryItem, 0, min(len(rows), query.Limit))}
	page.HasMore = len(rows) > query.Limit
	if page.HasMore {
		rows = rows[:query.Limit]
	}
	for _, encoded := range rows {
		var row accountInventoryRow
		if err := decodeStrictJSON(encoded, &row); err != nil || !validAccountInventoryRow(query.InstanceID, row) {
			return AccountInventoryPage{}, ErrAccountInventoryInconsistent
		}
		page.Items = append(page.Items, AccountInventoryItem{
			InstanceID: row.InstanceID, Provider: row.Provider, Email: row.NormalizedEmail,
			BasicStatus: row.BasicStatus, Lifecycle: row.Lifecycle,
			ConsecutiveMissingCount: row.ConsecutiveMissingCount,
			FirstSeenAt:             row.FirstSeenAt.UTC(), LastSeenAt: row.LastSeenAt.UTC(),
			MissingSince: utcTimePointer(row.MissingSince), OutOfScopeSince: utcTimePointer(row.OutOfScopeSince),
			LastRefreshAt: utcTimePointer(row.LastRefreshAt), NextRetryAt: utcTimePointer(row.NextRetryAt),
			SourceUpdatedAt:        utcTimePointer(row.SourceUpdatedAt),
			ProviderLastCompleteAt: row.ProviderLastCompleteAt.UTC(),
			ProviderDegraded:       *row.ProviderDegraded, SnapshotFreshness: row.SnapshotFreshness,
		})
		if page.HasMore && len(page.Items) == len(rows) {
			page.ContinuationAccountKey = row.AccountKey
		}
	}
	return page, nil
}

func validAccountInventoryQuery(query AccountInventoryQuery) bool {
	filters := query.Filters
	return query.InstanceID != uuid.Nil && query.Limit >= 1 && query.Limit <= 100 &&
		(filters.Provider == "" || validProviderName(filters.Provider)) &&
		(filters.Lifecycle == "" || validAccountInventoryLifecycle(filters.Lifecycle)) &&
		(filters.BasicStatus == "" || validAccountInventoryBasicStatus(filters.BasicStatus)) &&
		(filters.Email == "" || validAccountInventoryEmail(filters.Email)) &&
		(query.AfterAccountKey == "" || validAccountInventoryAccountKey(query.AfterAccountKey))
}

func validAccountInventoryViewAudit(audit AccountInventoryViewAudit) bool {
	return audit.ActorAdminID != uuid.Nil && len(audit.SourceFingerprint) == 32 &&
		audit.RequestID == strings.TrimSpace(audit.RequestID) &&
		utf8.RuneCountInString(audit.RequestID) >= 1 && utf8.RuneCountInString(audit.RequestID) <= 128
}

func validAccountInventoryRow(instanceID uuid.UUID, row accountInventoryRow) bool {
	if row.InstanceID != instanceID || !validProviderName(row.Provider) ||
		!validAccountInventoryEmail(row.NormalizedEmail) ||
		row.AccountKey != row.Provider+":"+row.NormalizedEmail ||
		!validAccountInventoryBasicStatus(row.BasicStatus) ||
		!validAccountInventoryLifecycle(row.Lifecycle) ||
		row.FirstSeenAt.IsZero() || row.LastSeenAt.IsZero() || row.ProviderLastCompleteAt.IsZero() ||
		row.ProviderDegraded == nil ||
		row.FirstSeenAt.After(row.LastSeenAt) ||
		(row.MissingSince != nil && !row.MissingSince.After(row.LastSeenAt)) ||
		(row.OutOfScopeSince != nil && row.OutOfScopeSince.Before(row.LastSeenAt)) {
		return false
	}
	switch row.Lifecycle {
	case AccountInventoryPresent:
		return row.ConsecutiveMissingCount == 0 && row.MissingSince == nil && row.OutOfScopeSince == nil &&
			validActiveAccountInventoryFreshness(row.SnapshotFreshness)
	case AccountInventorySuspectedMissing:
		return row.ConsecutiveMissingCount == 1 && row.MissingSince == nil && row.OutOfScopeSince == nil &&
			validActiveAccountInventoryFreshness(row.SnapshotFreshness)
	case AccountInventoryMissing:
		return row.ConsecutiveMissingCount == 2 && row.MissingSince != nil && row.OutOfScopeSince == nil &&
			validActiveAccountInventoryFreshness(row.SnapshotFreshness)
	case AccountInventoryOutOfScope:
		return row.ConsecutiveMissingCount == 0 && row.MissingSince == nil && row.OutOfScopeSince != nil &&
			row.SnapshotFreshness == AccountInventorySnapshotFreshnessOutOfScope
	default:
		return false
	}
}

func validAccountInventoryBasicStatus(value AccountInventoryBasicStatus) bool {
	switch value {
	case AccountInventoryBasicStatusReportedActive, AccountInventoryBasicStatusDisabled,
		AccountInventoryBasicStatusUnavailable, AccountInventoryBasicStatusError,
		AccountInventoryBasicStatusUnknown:
		return true
	default:
		return false
	}
}

func validActiveAccountInventoryFreshness(value AccountInventorySnapshotFreshness) bool {
	return value == AccountInventorySnapshotFreshnessFresh || value == AccountInventorySnapshotFreshnessStale
}

func validAccountInventoryEmail(value string) bool {
	return validNormalizedIdentity(value, 320) && value == strings.ToLower(strings.TrimSpace(value))
}

func validAccountInventoryAccountKey(value string) bool {
	separator := strings.IndexByte(value, ':')
	return len(value) >= 3 && len(value) <= 385 && separator >= 1 &&
		validProviderName(value[:separator]) && validAccountInventoryEmail(value[separator+1:])
}

func accountInventoryDatabaseError(err error) error {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return err
	}
	switch databaseError.Code {
	case "22023":
		return ErrInvalidAccountInventoryQuery
	case "P0404":
		return ErrAccountInventoryInstanceNotFound
	case "P0409":
		return ErrAccountInventoryCapabilityUnsupported
	case "P0503":
		return ErrAccountInventoryInconsistent
	default:
		return err
	}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func (AccountInventoryQueryFilters) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryQueryFilters]"))
}

func (AccountInventoryQuery) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryQuery]"))
}

func (AccountInventoryViewAudit) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryViewAudit]"))
}

func (AccountInventoryItem) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryItem]"))
}

func (AccountInventoryPage) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryPage]"))
}
