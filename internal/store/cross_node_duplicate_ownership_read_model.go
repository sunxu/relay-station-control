package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

// ErrCrossNodeDuplicateOccurrenceQuery marks invalid list/detail query input
// (bad limit, bad cursor shape, invalid status/account_key/instance_id
// filter). ErrCrossNodeDuplicateOccurrenceNotFound marks a detail lookup for
// an occurrence_id that does not exist.
var (
	ErrCrossNodeDuplicateOccurrenceQuery    = errors.New("store: invalid cross-node duplicate occurrence query")
	ErrCrossNodeDuplicateOccurrenceNotFound = errors.New("store: cross-node duplicate occurrence not found")
)

const (
	defaultCrossNodeDuplicateOccurrencePageSize = 50
	maximumCrossNodeDuplicateOccurrencePageSize = 200
	defaultCrossNodeDuplicateEvidencePageSize   = 50
	maximumCrossNodeDuplicateEvidencePageSize   = 200
)

// CrossNodeDuplicateOccurrenceFilters narrows ListOccurrences: each non-zero
// field is ANDed together, mirroring the Phase 5 review request (status /
// account_key / instance_id).
type CrossNodeDuplicateOccurrenceFilters struct {
	Status     string
	AccountKey string
	InstanceID uuid.UUID
}

// CrossNodeDuplicateOccurrenceCursor is an opaque, unsigned keyset cursor
// (last_seen_at, occurrence_id). Unlike JobCursorCodec/AccountInventoryCursorCodec,
// this is intentionally NOT HMAC-signed: every field returned by this read
// model is already visible to any authenticated Control session regardless
// of cursor value (filters are always re-applied server-side in the SQL
// WHERE clause), so an attacker-supplied cursor can only shift the paging
// window within data the caller can already read in full -- it cannot cross
// a privilege or filter boundary. Building a second HMAC cursor codec here
// would duplicate the existing JobCursorCodec/AccountInventoryCursorCodec
// machinery for no additional security benefit.
type CrossNodeDuplicateOccurrenceCursor struct {
	LastSeenAt   time.Time
	OccurrenceID uuid.UUID
}

// CrossNodeDuplicateOccurrenceSummary is the minimal list/detail projection
// requested by Phase 5: occurrence identity, lifecycle projection fields,
// and the current affected-node set. account_key is returned as plaintext
// by design (frozen non-goal: no masked/HMAC/fingerprint handling).
type CrossNodeDuplicateOccurrenceSummary struct {
	OccurrenceID        uuid.UUID
	EnvironmentID       string
	AccountKey          string
	ConflictType        string
	Status              string
	Severity            string
	FirstSeenAt         time.Time
	LastSeenAt          time.Time
	ResolvedAt          *time.Time
	EvidenceState       string
	LastFullyVerifiedAt *time.Time
	LatestEvaluationID  *uuid.UUID
	AffectedNodes       []uuid.UUID
}

// CrossNodeDuplicateOccurrencePage is one bounded page of ListOccurrences.
type CrossNodeDuplicateOccurrencePage struct {
	Items      []CrossNodeDuplicateOccurrenceSummary
	HasMore    bool
	ObservedAt *time.Time
}

// CrossNodeDuplicateOccurrenceEvidenceItem is one retention-safe evidence
// observation, returned only from the separate evidence-detail query (never
// inlined into a list response, per Phase 5 review item 5D).
type CrossNodeDuplicateOccurrenceEvidenceItem struct {
	ObservationID     uuid.UUID
	InstanceID        uuid.UUID
	ObservationKind   string
	SourceProvider    string
	SourceScheduledAt time.Time
	SourceCompletedAt time.Time
	SourcePollRunID   *uuid.UUID
	EvaluationID      uuid.UUID
	EvaluationAt      time.Time
	RecordedAt        time.Time
}

// CrossNodeDuplicateOccurrenceEvidencePage is one bounded page of
// ListOccurrenceEvidence.
type CrossNodeDuplicateOccurrenceEvidencePage struct {
	Items   []CrossNodeDuplicateOccurrenceEvidenceItem
	HasMore bool
}

// CrossNodeDuplicateOwnershipOccurrenceReader is the Phase 5 read-only API
// surface: list/filter occurrences, read one occurrence detail, and read
// one occurrence's evidence history. It never mutates lifecycle state --
// that remains exclusively owned by CrossNodeDuplicateOwnershipLifecycleRepository.
type CrossNodeDuplicateOwnershipOccurrenceReader interface {
	ListOccurrences(ctx context.Context, filters CrossNodeDuplicateOccurrenceFilters, cursor *CrossNodeDuplicateOccurrenceCursor, limit int) (CrossNodeDuplicateOccurrencePage, error)
	GetOccurrence(ctx context.Context, occurrenceID uuid.UUID) (CrossNodeDuplicateOccurrenceSummary, error)
	ListOccurrenceEvidence(ctx context.Context, occurrenceID uuid.UUID, cursor *time.Time, cursorObservationID uuid.UUID, limit int) (CrossNodeDuplicateOccurrenceEvidencePage, error)
}

// CrossNodeDuplicateOwnershipHistoryReader is kept separate from the current
// occurrence reader so existing current-membership fakes do not gain a new
// obligation. Historical involvement is proven by append-only evidence.
type CrossNodeDuplicateOwnershipHistoryReader interface {
	ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(ctx context.Context, nodeID uuid.UUID, status string, cursor *CrossNodeDuplicateOccurrenceCursor, limit int) (CrossNodeDuplicateOccurrencePage, error)
}

// CrossNodeDuplicateOwnershipOccurrenceRepository implements
// CrossNodeDuplicateOwnershipOccurrenceReader against the Phase 1B
// persistence tables. It only SELECTs -- relay_control_runtime already
// holds SELECT on all three tables (migrations/00013 grants section), so no
// new migration/SECURITY DEFINER function is needed for this read model
// (unlike Phase 2's Account Inventory query-access boundary).
type CrossNodeDuplicateOwnershipOccurrenceRepository struct {
	queries *generated.Queries
}

var _ CrossNodeDuplicateOwnershipOccurrenceReader = (*CrossNodeDuplicateOwnershipOccurrenceRepository)(nil)
var _ CrossNodeDuplicateOwnershipHistoryReader = (*CrossNodeDuplicateOwnershipOccurrenceRepository)(nil)

func NewCrossNodeDuplicateOwnershipOccurrenceRepository(pool *pgxpool.Pool) (*CrossNodeDuplicateOwnershipOccurrenceRepository, error) {
	if pool == nil {
		return nil, errors.New("store: cross-node duplicate occurrence database is unavailable")
	}
	return &CrossNodeDuplicateOwnershipOccurrenceRepository{queries: generated.New(pool)}, nil
}

// ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode returns each
// occurrence once when its immutable evidence contains the target Node. The
// status filter is optional (empty means both ACTIVE and RESOLVED), and the
// cursor is bound by the caller to this target/status query.
func (repository *CrossNodeDuplicateOwnershipOccurrenceRepository) ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(
	ctx context.Context, nodeID uuid.UUID, status string, cursor *CrossNodeDuplicateOccurrenceCursor, limit int,
) (CrossNodeDuplicateOccurrencePage, error) {
	if repository == nil || repository.queries == nil || nodeID == uuid.Nil ||
		limit < 1 || limit > maximumCrossNodeDuplicateOccurrencePageSize ||
		(status != "" && status != "ACTIVE" && status != "RESOLVED") ||
		(cursor != nil && (cursor.LastSeenAt.IsZero() || cursor.OccurrenceID == uuid.Nil)) {
		return CrossNodeDuplicateOccurrencePage{}, ErrCrossNodeDuplicateOccurrenceQuery
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := repository.queries.GetNodeAsset(queryCtx, nullableUUID(nodeID)); errors.Is(err, pgx.ErrNoRows) {
		return CrossNodeDuplicateOccurrencePage{}, ErrAssetNotFound
	} else if err != nil {
		return CrossNodeDuplicateOccurrencePage{}, err
	}

	var afterTime *time.Time
	var afterID *uuid.UUID
	if cursor != nil {
		afterTime = &cursor.LastSeenAt
		afterID = &cursor.OccurrenceID
	}
	observedAt, err := repository.queries.GetRelayBindingDBTime(queryCtx)
	if err != nil {
		return CrossNodeDuplicateOccurrencePage{}, err
	}
	params := generated.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNodeParams{
		TargetNodeID:      nullableUUID(nodeID),
		Status:            nullableText(status),
		AfterLastSeenAt:   nullableTimeValue(afterTime),
		AfterOccurrenceID: nullableUUID(uuid.Nil),
		PageSize:          int32(limit + 1),
	}
	if afterID != nil {
		params.AfterOccurrenceID = nullableUUID(*afterID)
	}
	rows, err := repository.queries.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(queryCtx, params)
	if err != nil {
		return CrossNodeDuplicateOccurrencePage{}, err
	}
	items := make([]CrossNodeDuplicateOccurrenceSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, crossNodeDuplicateOccurrenceSummaryFromHistoryRow(row))
	}
	observedAtValue := observedAt.Time.UTC()
	page := CrossNodeDuplicateOccurrencePage{Items: items, ObservedAt: &observedAtValue}
	if len(page.Items) > limit {
		page.HasMore = true
		page.Items = page.Items[:limit]
	}
	return page, nil
}

func (repository *CrossNodeDuplicateOwnershipOccurrenceRepository) ListOccurrences(
	ctx context.Context, filters CrossNodeDuplicateOccurrenceFilters, cursor *CrossNodeDuplicateOccurrenceCursor, limit int,
) (CrossNodeDuplicateOccurrencePage, error) {
	if limit < 1 || limit > maximumCrossNodeDuplicateOccurrencePageSize ||
		(filters.Status != "" && filters.Status != "ACTIVE" && filters.Status != "RESOLVED") ||
		(cursor != nil && (cursor.LastSeenAt.IsZero() || cursor.OccurrenceID == uuid.Nil)) {
		return CrossNodeDuplicateOccurrencePage{}, ErrCrossNodeDuplicateOccurrenceQuery
	}
	params := generated.ListCrossNodeDuplicateOccurrencesParams{
		Status: nullableText(filters.Status), AccountKey: nullableText(filters.AccountKey),
		InstanceID: nullableUUID(filters.InstanceID), PageSize: int32(limit + 1),
	}
	if cursor != nil {
		params.AfterLastSeenAt = nullableTimeValue(&cursor.LastSeenAt)
		params.AfterOccurrenceID = nullableUUID(cursor.OccurrenceID)
	}
	rows, err := repository.queries.ListCrossNodeDuplicateOccurrences(ctx, params)
	if err != nil {
		return CrossNodeDuplicateOccurrencePage{}, err
	}
	var page CrossNodeDuplicateOccurrencePage
	page.HasMore = len(rows) > limit
	if page.HasMore {
		rows = rows[:limit]
	}
	page.Items = make([]CrossNodeDuplicateOccurrenceSummary, 0, len(rows))
	for _, row := range rows {
		page.Items = append(page.Items, crossNodeDuplicateOccurrenceSummaryFromListRow(row))
	}
	return page, nil
}

func (repository *CrossNodeDuplicateOwnershipOccurrenceRepository) GetOccurrence(
	ctx context.Context, occurrenceID uuid.UUID,
) (CrossNodeDuplicateOccurrenceSummary, error) {
	if occurrenceID == uuid.Nil {
		return CrossNodeDuplicateOccurrenceSummary{}, ErrCrossNodeDuplicateOccurrenceQuery
	}
	row, err := repository.queries.GetCrossNodeDuplicateOccurrence(ctx, nullableUUID(occurrenceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CrossNodeDuplicateOccurrenceSummary{}, ErrCrossNodeDuplicateOccurrenceNotFound
	}
	if err != nil {
		return CrossNodeDuplicateOccurrenceSummary{}, err
	}
	return crossNodeDuplicateOccurrenceSummaryFromDetailRow(row), nil
}

func (repository *CrossNodeDuplicateOwnershipOccurrenceRepository) ListOccurrenceEvidence(
	ctx context.Context, occurrenceID uuid.UUID, cursor *time.Time, cursorObservationID uuid.UUID, limit int,
) (CrossNodeDuplicateOccurrenceEvidencePage, error) {
	if occurrenceID == uuid.Nil || limit < 1 || limit > maximumCrossNodeDuplicateEvidencePageSize ||
		(cursor != nil && (cursor.IsZero() || cursorObservationID == uuid.Nil)) {
		return CrossNodeDuplicateOccurrenceEvidencePage{}, ErrCrossNodeDuplicateOccurrenceQuery
	}
	params := generated.ListCrossNodeDuplicateOccurrenceEvidenceParams{
		OccurrenceID: nullableUUID(occurrenceID), PageSize: int32(limit + 1),
	}
	if cursor != nil {
		params.AfterRecordedAt = nullableTimeValue(cursor)
		params.AfterObservationID = nullableUUID(cursorObservationID)
	}
	rows, err := repository.queries.ListCrossNodeDuplicateOccurrenceEvidence(ctx, params)
	if err != nil {
		return CrossNodeDuplicateOccurrenceEvidencePage{}, err
	}
	var page CrossNodeDuplicateOccurrenceEvidencePage
	page.HasMore = len(rows) > limit
	if page.HasMore {
		rows = rows[:limit]
	}
	page.Items = make([]CrossNodeDuplicateOccurrenceEvidenceItem, 0, len(rows))
	for _, row := range rows {
		page.Items = append(page.Items, CrossNodeDuplicateOccurrenceEvidenceItem{
			ObservationID: uuidFromPG(row.ObservationID), InstanceID: uuidFromPG(row.InstanceID),
			ObservationKind: row.ObservationKind, SourceProvider: row.SourceProvider,
			SourceScheduledAt: row.SourceScheduledAt.Time.UTC(), SourceCompletedAt: row.SourceCompletedAt.Time.UTC(),
			SourcePollRunID: nullableUUIDPointer(row.SourcePollRunID),
			EvaluationID:    uuidFromPG(row.EvaluationID), EvaluationAt: row.EvaluationAt.Time.UTC(),
			RecordedAt: row.RecordedAt.Time.UTC(),
		})
	}
	return page, nil
}

func crossNodeDuplicateOccurrenceSummaryFromListRow(row generated.ListCrossNodeDuplicateOccurrencesRow) CrossNodeDuplicateOccurrenceSummary {
	return CrossNodeDuplicateOccurrenceSummary{
		OccurrenceID: uuidFromPG(row.OccurrenceID), EnvironmentID: row.EnvironmentID, AccountKey: row.AccountKey,
		ConflictType: row.ConflictType, Status: row.Status, Severity: row.Severity,
		FirstSeenAt: row.FirstSeenAt.Time.UTC(), LastSeenAt: row.LastSeenAt.Time.UTC(),
		ResolvedAt: nullableTime(row.ResolvedAt), EvidenceState: row.EvidenceState,
		LastFullyVerifiedAt: nullableTime(row.LastFullyVerifiedAt), LatestEvaluationID: nullableUUIDPointer(row.LatestEvaluationID),
		AffectedNodes: uuidSliceFromPG(row.AffectedNodeIds),
	}
}

func crossNodeDuplicateOccurrenceSummaryFromHistoryRow(row generated.ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNodeRow) CrossNodeDuplicateOccurrenceSummary {
	return CrossNodeDuplicateOccurrenceSummary{
		OccurrenceID: uuidFromPG(row.OccurrenceID), EnvironmentID: row.EnvironmentID, AccountKey: row.AccountKey,
		ConflictType: row.ConflictType, Status: row.Status, Severity: row.Severity,
		FirstSeenAt: row.FirstSeenAt.Time.UTC(), LastSeenAt: row.LastSeenAt.Time.UTC(),
		ResolvedAt: nullableTime(row.ResolvedAt), EvidenceState: row.EvidenceState,
		LastFullyVerifiedAt: nullableTime(row.LastFullyVerifiedAt), LatestEvaluationID: nullableUUIDPointer(row.LatestEvaluationID),
		AffectedNodes: uuidSliceFromPG(row.AffectedNodeIds),
	}
}

func crossNodeDuplicateOccurrenceSummaryFromDetailRow(row generated.GetCrossNodeDuplicateOccurrenceRow) CrossNodeDuplicateOccurrenceSummary {
	return CrossNodeDuplicateOccurrenceSummary{
		OccurrenceID: uuidFromPG(row.OccurrenceID), EnvironmentID: row.EnvironmentID, AccountKey: row.AccountKey,
		ConflictType: row.ConflictType, Status: row.Status, Severity: row.Severity,
		FirstSeenAt: row.FirstSeenAt.Time.UTC(), LastSeenAt: row.LastSeenAt.Time.UTC(),
		ResolvedAt: nullableTime(row.ResolvedAt), EvidenceState: row.EvidenceState,
		LastFullyVerifiedAt: nullableTime(row.LastFullyVerifiedAt), LatestEvaluationID: nullableUUIDPointer(row.LatestEvaluationID),
		AffectedNodes: uuidSliceFromPG(row.AffectedNodeIds),
	}
}

func uuidSliceFromPG(values []pgtype.UUID) []uuid.UUID {
	if len(values) == 0 {
		return nil
	}
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		result = append(result, uuidFromPG(value))
	}
	return result
}
