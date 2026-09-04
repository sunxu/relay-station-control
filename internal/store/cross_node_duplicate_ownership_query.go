package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

// ErrInvalidCrossNodeDuplicateOwnershipQuery is returned when the caller
// supplies an account_key that does not match the canonical Account
// Inventory shape (provider ':' normalized_email) frozen in design.md
// Phase 1A.
var ErrInvalidCrossNodeDuplicateOwnershipQuery = errors.New(
	"store: invalid cross-node duplicate ownership query")

// CrossNodeDuplicateOwnershipCandidate is one account_key with two or more
// currently eligible owner Nodes (design.md Phase 1A eligible-owner
// predicate). OwnerInstanceIDs is deterministically ordered ascending by
// instance_id and reflects a single evaluation (one database_now).
type CrossNodeDuplicateOwnershipCandidate struct {
	AccountKey       string
	OwnerInstanceIDs []uuid.UUID
}

// CrossNodeDuplicateOwnershipReader exposes the Phase 2 read-only ownership
// queries frozen in design.md Phase 1A. It does not create, modify, or read
// any occurrence/evidence row, does not read Gateway Directory, Binding, or
// scheduler/runtime state, and performs no writes.
type CrossNodeDuplicateOwnershipReader interface {
	// ListEligibleOwnersByAccountKey returns the current set of eligible
	// owner Node instance_ids for the given canonical account_key, ordered
	// deterministically ascending by instance_id. Empty (nil, no error) if
	// the account_key is unknown or has no eligible owner.
	ListEligibleOwnersByAccountKey(ctx context.Context, accountKey string) ([]uuid.UUID, error)
	// ListCrossNodeDuplicateCandidates returns one row per account_key that
	// currently has >= 2 eligible owner Nodes, ordered deterministically
	// ascending by account_key. It never returns Node pairs; each
	// account_key is aggregated exactly once per call (one evaluation).
	ListCrossNodeDuplicateCandidates(ctx context.Context) ([]CrossNodeDuplicateOwnershipCandidate, error)
}

// CrossNodeDuplicateOwnershipRepository implements CrossNodeDuplicateOwnershipReader
// by calling two SECURITY DEFINER readonly functions,
// control_list_eligible_cross_node_owners_v1 and
// control_list_cross_node_duplicate_candidates_v1
// (migrations/00014_cross_node_duplicate_ownership_query_access.sql), which
// implement the Phase 1A frozen eligible-owner predicate. relay_control_runtime
// has EXECUTE on both functions but no direct SELECT on account_inventory or
// account_inventory_provider_states (REVOKE ALL, migrations/00007 L841-843);
// this repository is production-safe against the runtime connection pool.
type CrossNodeDuplicateOwnershipRepository struct {
	queries *generated.Queries
}

var _ CrossNodeDuplicateOwnershipReader = (*CrossNodeDuplicateOwnershipRepository)(nil)

// NewCrossNodeDuplicateOwnershipRepository constructs a repository bound to
// the given pool.
func NewCrossNodeDuplicateOwnershipRepository(pool *pgxpool.Pool) (*CrossNodeDuplicateOwnershipRepository, error) {
	if pool == nil {
		return nil, errors.New("store: cross-node duplicate ownership database is unavailable")
	}
	return &CrossNodeDuplicateOwnershipRepository{queries: generated.New(pool)}, nil
}

func (repository *CrossNodeDuplicateOwnershipRepository) ListEligibleOwnersByAccountKey(
	ctx context.Context, accountKey string,
) ([]uuid.UUID, error) {
	if repository == nil {
		return nil, ErrInvalidCrossNodeDuplicateOwnershipQuery
	}
	if !validAccountInventoryAccountKey(accountKey) {
		return nil, ErrInvalidCrossNodeDuplicateOwnershipQuery
	}
	rows, err := repository.queries.ListEligibleOwnersByAccountKey(ctx, accountKey)
	if err != nil {
		return nil, err
	}
	owners := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if !row.Valid {
			return nil, errors.New("store: cross-node duplicate ownership query returned an invalid instance_id")
		}
		owners = append(owners, uuidFromPG(row))
	}
	return owners, nil
}

func (repository *CrossNodeDuplicateOwnershipRepository) ListCrossNodeDuplicateCandidates(
	ctx context.Context,
) ([]CrossNodeDuplicateOwnershipCandidate, error) {
	if repository == nil {
		return nil, ErrInvalidCrossNodeDuplicateOwnershipQuery
	}
	encodedRows, err := repository.queries.ListCrossNodeDuplicateCandidates(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]CrossNodeDuplicateOwnershipCandidate, 0, len(encodedRows))
	for _, encoded := range encodedRows {
		var row struct {
			AccountKey       string      `json:"account_key"`
			OwnerInstanceIds []uuid.UUID `json:"owner_instance_ids"`
		}
		if err := decodeStrictJSON(encoded, &row); err != nil {
			return nil, err
		}
		if !validAccountInventoryAccountKey(row.AccountKey) || len(row.OwnerInstanceIds) < 2 {
			return nil, errors.New("store: cross-node duplicate ownership query returned an invalid candidate")
		}
		candidates = append(candidates, CrossNodeDuplicateOwnershipCandidate{
			AccountKey: row.AccountKey, OwnerInstanceIDs: row.OwnerInstanceIds,
		})
	}
	return candidates, nil
}
