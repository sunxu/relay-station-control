package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

// crossNodeDuplicateOwnershipKey is one (environment_id, account_key)
// reconciliation unit -- the same semantic key cross_node_duplicate_occurrences
// dedupes ACTIVE rows on (design.md §1B.2/§5.1).
type crossNodeDuplicateOwnershipKey struct {
	environmentID string
	accountKey    string
}

// CrossNodeDuplicateOwnershipReconciler recomputes duplicate-ownership
// occurrence state against current Account Inventory truth after a process
// restart or missed detection cycles (Phase 4, design.md §5.1). It contains
// no lifecycle logic of its own: it only computes the reconciliation key set
// and delegates every key to the existing Phase 3
// CrossNodeDuplicateOwnershipLifecycleRepository.Evaluate transaction, so
// detect/refresh/degrade/add/remove/resolve/reopen behavior is identical
// whether triggered by a live detection pass or a restart reconciliation
// pass.
type CrossNodeDuplicateOwnershipReconciler struct {
	queries   *generated.Queries
	reader    CrossNodeDuplicateOwnershipReader
	lifecycle *CrossNodeDuplicateOwnershipLifecycleRepository
}

// NewCrossNodeDuplicateOwnershipReconciler constructs a reconciler bound to
// the given pool (production callers must pass the runtime pool), reusing
// an existing CrossNodeDuplicateOwnershipReader (Phase 2) and
// CrossNodeDuplicateOwnershipLifecycleRepository (Phase 3).
func NewCrossNodeDuplicateOwnershipReconciler(
	pool *pgxpool.Pool, reader CrossNodeDuplicateOwnershipReader, lifecycle *CrossNodeDuplicateOwnershipLifecycleRepository,
) (*CrossNodeDuplicateOwnershipReconciler, error) {
	if pool == nil || reader == nil || lifecycle == nil {
		return nil, errors.New("store: cross-node duplicate ownership reconciler dependencies are unavailable")
	}
	return &CrossNodeDuplicateOwnershipReconciler{
		queries: generated.New(pool), reader: reader, lifecycle: lifecycle,
	}, nil
}

// Reconcile recomputes occurrence state for the union of:
//
//  1. every account_key currently reported by ListCrossNodeDuplicateCandidates,
//     attributed to environmentID -- Account Inventory / relay_node_assets
//     are not environment-scoped, so a brand-new occurrence's environment_id
//     can only come from the caller's own environment context; and
//  2. every (environment_id, account_key) that currently has an ACTIVE
//     occurrence row (ListActiveCrossNodeDuplicateOccurrenceKeys), using
//     each row's own recorded environment_id -- this is what lets a
//     reconciliation pass resolve/degrade an occurrence whose account_key
//     has dropped out of the current candidate set since the last pass.
//
// Every key in the union is delegated to the existing Phase 3
// CrossNodeDuplicateOwnershipLifecycleRepository.Evaluate transaction --
// this function never creates, modifies, or reads occurrence/evidence rows
// itself. Keys are deduplicated and processed in deterministic
// (environment_id, account_key) order. All keys are attempted even if one
// fails; per-key errors are joined into the returned error, and (nil, nil)
// Evaluate outcomes (nothing to detect, nothing to resolve) are omitted
// from the returned evaluations.
func (reconciler *CrossNodeDuplicateOwnershipReconciler) Reconcile(
	ctx context.Context, environmentID string,
) ([]*CrossNodeDuplicateOwnershipEvaluation, error) {
	if reconciler == nil {
		return nil, errors.New("store: cross-node duplicate ownership reconciler is unavailable")
	}
	if environmentID == "" {
		return nil, ErrInvalidCrossNodeDuplicateOwnershipLifecycleInput
	}

	keys := make(map[crossNodeDuplicateOwnershipKey]struct{})

	candidates, err := reconciler.reader.ListCrossNodeDuplicateCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: reconcile cross-node duplicate ownership: list candidates: %w", err)
	}
	for _, candidate := range candidates {
		keys[crossNodeDuplicateOwnershipKey{environmentID: environmentID, accountKey: candidate.AccountKey}] = struct{}{}
	}

	activeKeys, err := reconciler.queries.ListActiveCrossNodeDuplicateOccurrenceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: reconcile cross-node duplicate ownership: list active occurrence keys: %w", err)
	}
	for _, row := range activeKeys {
		keys[crossNodeDuplicateOwnershipKey{environmentID: row.EnvironmentID, accountKey: row.AccountKey}] = struct{}{}
	}

	ordered := make([]crossNodeDuplicateOwnershipKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].environmentID != ordered[j].environmentID {
			return ordered[i].environmentID < ordered[j].environmentID
		}
		return ordered[i].accountKey < ordered[j].accountKey
	})

	var evaluations []*CrossNodeDuplicateOwnershipEvaluation
	var errs []error
	for _, key := range ordered {
		evaluation, err := reconciler.lifecycle.Evaluate(ctx, key.environmentID, key.accountKey)
		if err != nil {
			errs = append(errs, fmt.Errorf("reconcile %s/%s: %w", key.environmentID, key.accountKey, err))
			continue
		}
		if evaluation != nil {
			evaluations = append(evaluations, evaluation)
		}
	}
	if len(errs) > 0 {
		return evaluations, errors.Join(errs...)
	}
	return evaluations, nil
}
