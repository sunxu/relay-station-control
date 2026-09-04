package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

// pgTime converts a non-nullable time.Time into a valid pgtype.Timestamptz.
func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

// ErrInvalidCrossNodeDuplicateOwnershipLifecycleInput is returned when the
// caller supplies an environment_id/account_key that does not pass basic
// validation before any database work begins.
var ErrInvalidCrossNodeDuplicateOwnershipLifecycleInput = errors.New(
	"store: invalid cross-node duplicate ownership lifecycle input")

// CrossNodeDuplicateOwnershipEvaluation is the outcome of one lifecycle
// evaluation pass (design.md §4 / §1B.8): detect, refresh, degrade, affected
// Node add/remove, resolve, and reopen are all the same transaction, that
// transaction just yields one of these three outcomes.
type CrossNodeDuplicateOwnershipEvaluation struct {
	OccurrenceID  uuid.UUID
	Status        string // "ACTIVE" or "RESOLVED"
	EvidenceState string // "complete" or "degraded"
	// AffectedNodes is the resulting affected Node set (ascending
	// instance_id order) after this pass. For a RESOLVED occurrence this is
	// the frozen historical membership at the moment of resolve.
	AffectedNodes []uuid.UUID
	Added         []uuid.UUID
	Removed       []uuid.UUID
	// EvaluationID identifies this lifecycle pass's evaluation. It always
	// reflects the evaluation actually performed in Go (evaluateEvidenceTx),
	// but a zero-evidence degraded pass (every retained Node unclassifiable
	// this pass) persists no evidence rows for it, so occurrence.latest_evaluation_id
	// in the database is left pointing at the previous pass that did have
	// evidence, not at this EvaluationID.
	EvaluationID uuid.UUID
	EvaluationAt time.Time
	// Created is true when this pass created a brand-new occurrence row
	// (detect/reopen); false for refresh/degrade/add/remove/resolve against
	// an already-existing occurrence.
	Created bool
}

// crossNodeDuplicateEvidenceRow is the per-Node classification returned by
// control_evaluate_cross_node_duplicate_evidence_v1
// (migrations/00015_cross_node_duplicate_ownership_evidence_evaluation_query_access.sql),
// decoded from its to_jsonb-wrapped row.
type crossNodeDuplicateEvidenceRow struct {
	InstanceID        uuid.UUID  `json:"instance_id"`
	ObservationKind   string     `json:"observation_kind"`
	SourceProvider    string     `json:"source_provider"`
	SourceScheduledAt time.Time  `json:"source_scheduled_at"`
	SourceCompletedAt time.Time  `json:"source_completed_at"`
	SourcePollRunID   *uuid.UUID `json:"source_poll_run_id"`
}

// CrossNodeDuplicateOwnershipLifecycleRepository implements the Phase 3
// detect/refresh/degrade/affected-node add-remove/resolve/reopen lifecycle
// transaction (design.md §4, §1B.8). It writes only to the three tables
// created by migrations/00013 (cross_node_duplicate_occurrences,
// cross_node_duplicate_occurrence_nodes, cross_node_duplicate_occurrence_evidence)
// under relay_control_runtime's existing least-privilege grants, and reads
// eligible-owner / evidence-evaluation source truth only through the
// SECURITY DEFINER functions from migrations/00014 and 00015. It never reads
// Gateway Directory, Binding, or scheduler/runtime state, and never performs
// API/UI/alert/auto-remediation work.
type CrossNodeDuplicateOwnershipLifecycleRepository struct {
	pool *pgxpool.Pool
}

// NewCrossNodeDuplicateOwnershipLifecycleRepository constructs a repository
// bound to the given pool (production callers must pass the runtime pool).
func NewCrossNodeDuplicateOwnershipLifecycleRepository(pool *pgxpool.Pool) (*CrossNodeDuplicateOwnershipLifecycleRepository, error) {
	if pool == nil {
		return nil, errors.New("store: cross-node duplicate ownership lifecycle database is unavailable")
	}
	return &CrossNodeDuplicateOwnershipLifecycleRepository{pool: pool}, nil
}

// Evaluate runs one full lifecycle evaluation pass for the given
// environment_id/account_key semantic key, in a single database
// transaction, and returns the resulting occurrence state. It returns
// (nil, nil) when there is currently no ACTIVE occurrence for this key and
// fewer than two eligible owners exist (nothing to detect, nothing to
// resolve).
//
// Ordering (Phase 3 review, lock-before-re-evaluate): row-locking an
// existing ACTIVE occurrence always happens first and unconditionally; only
// once a row is locked (or confirmed absent) does discovery run, and only
// once discovery has run does this pass fetch its one evaluationAt and call
// the authoritative evidence-evaluation function. Nothing discovered or
// evaluated before a row lock is ever reused after acquiring that lock -- a
// losing INSERT race re-does discovery and re-evaluation from scratch after
// re-selecting the existing row FOR UPDATE (see create()).
func (repository *CrossNodeDuplicateOwnershipLifecycleRepository) Evaluate(
	ctx context.Context, environmentID string, accountKey string,
) (*CrossNodeDuplicateOwnershipEvaluation, error) {
	if repository == nil {
		return nil, ErrInvalidCrossNodeDuplicateOwnershipLifecycleInput
	}
	if environmentID == "" || !validAccountInventoryAccountKey(accountKey) {
		return nil, ErrInvalidCrossNodeDuplicateOwnershipLifecycleInput
	}

	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := generated.New(tx)

	existing, existsErr := queries.SelectActiveCrossNodeDuplicateOccurrenceForUpdate(ctx,
		generated.SelectActiveCrossNodeDuplicateOccurrenceForUpdateParams{
			EnvironmentID: environmentID, AccountKey: accountKey,
		})
	found := true
	if errors.Is(existsErr, pgx.ErrNoRows) {
		found = false
	} else if existsErr != nil {
		return nil, existsErr
	}

	var result *CrossNodeDuplicateOwnershipEvaluation
	if found {
		result, err = repository.reconcile(ctx, queries, existing)
	} else {
		result, err = repository.create(ctx, queries, environmentID, accountKey)
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		// Nothing to do / nothing survived authoritative reconfirmation:
		// nothing was written, so it is safe to roll back.
		return nil, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// listEligibleOwnersTx calls control_list_eligible_cross_node_owners_v1
// (migrations/00014) against the transaction-scoped connection. This is
// used only to discover which Node instance_ids to feed into the
// authoritative per-Node evaluate() call below; its own internal
// database_now need not match evaluationAt exactly (see
// cross_node_duplicate_ownership_lifecycle.go doc comment / Phase 3 review
// notes: discovery vs. authoritative classification).
func listEligibleOwnersTx(ctx context.Context, queries *generated.Queries, accountKey string) ([]uuid.UUID, error) {
	rows, err := queries.ListEligibleOwnersByAccountKey(ctx, accountKey)
	if err != nil {
		return nil, err
	}
	owners := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if !row.Valid {
			return nil, errors.New("store: cross-node duplicate ownership eligible-owner query returned an invalid instance_id")
		}
		owners = append(owners, uuidFromPG(row))
	}
	return owners, nil
}

// evaluateEvidenceTx calls control_evaluate_cross_node_duplicate_evidence_v1
// (migrations/00015) for exactly the given instance_ids at the single
// evaluationAt instant, and returns a map keyed by instance_id. A Node
// omitted from the returned map could not be evaluated this pass (no
// account_inventory_provider_states row at all for this account_key's
// provider) and must be treated as unverifiable/degraded by the caller.
func evaluateEvidenceTx(
	ctx context.Context, queries *generated.Queries, accountKey string, instanceIDs []uuid.UUID, evaluationAt time.Time,
) (map[uuid.UUID]crossNodeDuplicateEvidenceRow, error) {
	result := make(map[uuid.UUID]crossNodeDuplicateEvidenceRow, len(instanceIDs))
	if len(instanceIDs) == 0 {
		return result, nil
	}
	pgIDs := make([]pgtype.UUID, len(instanceIDs))
	for i, id := range instanceIDs {
		pgIDs[i] = nullableUUID(id)
	}
	encodedRows, err := queries.EvaluateCrossNodeDuplicateEvidence(ctx, generated.EvaluateCrossNodeDuplicateEvidenceParams{
		AccountKey:  accountKey,
		InstanceIds: pgIDs,
		AtTime:      pgTime(evaluationAt),
	})
	if err != nil {
		return nil, err
	}
	for _, encoded := range encodedRows {
		var row crossNodeDuplicateEvidenceRow
		if err := decodeStrictJSON(encoded, &row); err != nil {
			return nil, err
		}
		switch row.ObservationKind {
		case "owner_confirmed", "absence_confirmed", "degraded":
		default:
			return nil, fmt.Errorf("store: cross-node duplicate ownership evidence evaluation returned unknown observation_kind %q", row.ObservationKind)
		}
		result[row.InstanceID] = row
	}
	return result, nil
}

// filterOwnerConfirmedCandidates returns, in candidate order, only the
// candidates that evaluate() classified as owner_confirmed at the single
// evaluationAt this pass used. Extracted as a pure function (Phase 3 review
// items 1 and 5) so the "discovery found >= 2 candidates but the
// authoritative reconfirm at evaluationAt confirms fewer than 2" branch can
// be exercised deterministically with synthetic evalResults, instead of
// needing a real clock-timing race against the 15-minute freshness
// boundary.
func filterOwnerConfirmedCandidates(
	candidates []uuid.UUID, evalResults map[uuid.UUID]crossNodeDuplicateEvidenceRow,
) []uuid.UUID {
	var confirmed []uuid.UUID
	for _, node := range candidates {
		if row, ok := evalResults[node]; ok && row.ObservationKind == "owner_confirmed" {
			confirmed = append(confirmed, node)
		}
	}
	return confirmed
}

// crossNodeDuplicateMembershipDecision is the outcome of classifying one
// reconcile() pass's allNodes union against currentSet using evalResults.
type crossNodeDuplicateMembershipDecision struct {
	ToAdd               []uuid.UUID
	ToRemove            []uuid.UUID
	AnyDegradedRetained bool
}

// classifyCrossNodeDuplicateMembership is the pure decision logic reconcile()
// uses to turn one evaluate() pass into add/remove/degrade decisions (Phase
// 3 review item 4): a Node newly eligible per discovery that evaluate()
// does NOT classify owner_confirmed (absence_confirmed, degraded, or simply
// absent from evalResults because it has no provider_state row at all) must
// never join the affected set. Extracted as a pure function so this branch
// can be exercised deterministically with synthetic evalResults, since
// discovery and evaluate() share one identical predicate and can only
// genuinely diverge across the microsecond window between the two calls
// within a single transaction (not something a test should try to race).
func classifyCrossNodeDuplicateMembership(
	allNodes []uuid.UUID, currentSet map[uuid.UUID]bool, evalResults map[uuid.UUID]crossNodeDuplicateEvidenceRow,
) crossNodeDuplicateMembershipDecision {
	var decision crossNodeDuplicateMembershipDecision
	for _, node := range allNodes {
		row, ok := evalResults[node]
		inCurrent := currentSet[node]
		switch {
		case !ok:
			// No account_inventory_provider_states row at all for this
			// Node this pass: cannot cite any evidence, cannot add, cannot
			// remove. A currently-retained Node in this state makes the
			// occurrence degraded/unverifiable this pass.
			if inCurrent {
				decision.AnyDegradedRetained = true
			}
		case row.ObservationKind == "owner_confirmed":
			if !inCurrent {
				decision.ToAdd = append(decision.ToAdd, node)
			}
			// else: retained and reconfirmed, no membership change.
		case row.ObservationKind == "absence_confirmed":
			if inCurrent {
				decision.ToRemove = append(decision.ToRemove, node)
			}
			// else: a non-member Node proven absent needs no action; a
			// newly-discovered Node classified absence_confirmed/degraded
			// must never join toAdd.
		case row.ObservationKind == "degraded":
			if inCurrent {
				decision.AnyDegradedRetained = true
			}
		}
	}
	return decision
}

// create implements the detect/reopen path (design.md §4 detect; reopen is
// simply detect against a semantic key whose only prior occurrence is
// RESOLVED, which is why it needs no special-casing here: the partial
// unique index only blocks a second concurrent ACTIVE row).
//
// Ordering (Phase 3 review item 1): discovery runs first; if it already
// can't support a duplicate, this returns immediately with nothing written.
// Only once discovery clears the >=2 bar does this pass fetch its one
// evaluationAt and authoritatively reconfirm each discovered candidate;
// only candidates evaluate() classifies owner_confirmed at that instant
// count toward the occurrence. If fewer than two survive that
// reconfirmation, this returns (nil, nil) having written nothing at all --
// no occurrence, no evidence, no membership row (never a phantom
// detect-then-immediately-resolved occurrence).
func (repository *CrossNodeDuplicateOwnershipLifecycleRepository) create(
	ctx context.Context, queries *generated.Queries, environmentID, accountKey string,
) (*CrossNodeDuplicateOwnershipEvaluation, error) {
	eligible, err := listEligibleOwnersTx(ctx, queries, accountKey)
	if err != nil {
		return nil, err
	}
	if len(eligible) < 2 {
		return nil, nil
	}

	evaluationAtPG, err := queries.SelectClockTimestamp(ctx)
	if err != nil {
		return nil, err
	}
	evaluationAt := evaluationAtPG.Time

	evalResults, err := evaluateEvidenceTx(ctx, queries, accountKey, eligible, evaluationAt)
	if err != nil {
		return nil, err
	}
	confirmed := filterOwnerConfirmedCandidates(eligible, evalResults)
	sortUUIDs(confirmed)
	if len(confirmed) < 2 {
		// Authoritative reconfirmation at evaluationAt did not hold up
		// discovery's candidate set. Nothing is written for this pass.
		return nil, nil
	}

	occurrenceIDPG, err := queries.InsertCrossNodeDuplicateOccurrence(ctx, generated.InsertCrossNodeDuplicateOccurrenceParams{
		EnvironmentID: environmentID, AccountKey: accountKey, EvaluationAt: pgTime(evaluationAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Race: another transaction created the ACTIVE occurrence between
		// our SELECT-for-update miss and this INSERT. Re-select it for
		// update and discard every value computed before the lock
		// (eligible/evaluationAt/evalResults/confirmed): reconcile()
		// re-does discovery and authoritative evaluation from scratch
		// against the now-locked row (Phase 3 review item 2 -- a
		// pre-lock evaluationAt must never be reused for a post-lock
		// lifecycle decision).
		existing, selectErr := queries.SelectActiveCrossNodeDuplicateOccurrenceForUpdate(ctx,
			generated.SelectActiveCrossNodeDuplicateOccurrenceForUpdateParams{
				EnvironmentID: environmentID, AccountKey: accountKey,
			})
		if selectErr != nil {
			return nil, fmt.Errorf("store: cross-node duplicate ownership detect lost insert race and re-select failed: %w", selectErr)
		}
		return repository.reconcile(ctx, queries, existing)
	}
	if err != nil {
		return nil, err
	}
	occurrenceID := uuidFromPG(occurrenceIDPG)

	evaluationID := uuid.New()
	for _, node := range confirmed {
		row := evalResults[node]
		if err := insertEvidenceRow(ctx, queries, occurrenceID, node, row, evaluationID, evaluationAt); err != nil {
			return nil, err
		}
		if err := queries.InsertCrossNodeDuplicateOccurrenceNode(ctx, generated.InsertCrossNodeDuplicateOccurrenceNodeParams{
			OccurrenceID: nullableUUID(occurrenceID), InstanceID: nullableUUID(node),
			FirstConfirmedAt: pgTime(evaluationAt),
		}); err != nil {
			return nil, err
		}
	}

	if err := queries.RefreshCrossNodeDuplicateOccurrenceProjection(ctx, generated.RefreshCrossNodeDuplicateOccurrenceProjectionParams{
		EvaluationAt: pgTime(evaluationAt), EvidenceState: "complete", HasEvidence: true,
		EvaluationID: nullableUUID(evaluationID), OccurrenceID: nullableUUID(occurrenceID),
	}); err != nil {
		return nil, err
	}
	return &CrossNodeDuplicateOwnershipEvaluation{
		OccurrenceID: occurrenceID, Status: "ACTIVE", EvidenceState: "complete",
		AffectedNodes: confirmed, Added: confirmed, EvaluationID: evaluationID, EvaluationAt: evaluationAt, Created: true,
	}, nil
}

// reconcile implements refresh/degrade/affected-node add/remove/resolve
// (design.md §4) against an already row-locked existing ACTIVE occurrence.
//
// Ordering (Phase 3 review item 2): discovery (listEligibleOwnersTx) and the
// one evaluationAt this pass uses are both fetched only after existing has
// already been row-locked by the caller -- never before.
func (repository *CrossNodeDuplicateOwnershipLifecycleRepository) reconcile(
	ctx context.Context, queries *generated.Queries, existing generated.CrossNodeDuplicateOccurrence,
) (*CrossNodeDuplicateOwnershipEvaluation, error) {
	occurrenceID := uuidFromPG(existing.OccurrenceID)

	currentRows, err := queries.ListCrossNodeDuplicateOccurrenceNodes(ctx, nullableUUID(occurrenceID))
	if err != nil {
		return nil, err
	}
	currentSet := make(map[uuid.UUID]bool, len(currentRows))
	for _, row := range currentRows {
		currentSet[uuidFromPG(row.InstanceID)] = true
	}

	eligible, err := listEligibleOwnersTx(ctx, queries, existing.AccountKey)
	if err != nil {
		return nil, err
	}

	allNodesSet := make(map[uuid.UUID]bool, len(currentSet)+len(eligible))
	for node := range currentSet {
		allNodesSet[node] = true
	}
	for _, node := range eligible {
		allNodesSet[node] = true
	}
	allNodes := make([]uuid.UUID, 0, len(allNodesSet))
	for node := range allNodesSet {
		allNodes = append(allNodes, node)
	}
	sortUUIDs(allNodes)

	evaluationAtPG, err := queries.SelectClockTimestamp(ctx)
	if err != nil {
		return nil, err
	}
	evaluationAt := evaluationAtPG.Time

	evaluationID := uuid.New()
	evalResults, err := evaluateEvidenceTx(ctx, queries, existing.AccountKey, allNodes, evaluationAt)
	if err != nil {
		return nil, err
	}

	decision := classifyCrossNodeDuplicateMembership(allNodes, currentSet, evalResults)
	toAdd, toRemove, anyDegradedRetained := decision.ToAdd, decision.ToRemove, decision.AnyDegradedRetained

	evidenceCount := 0
	for _, node := range allNodes {
		row, ok := evalResults[node]
		// Evidence is appended for every Node this pass actually evaluated
		// (design.md §4 refresh: "append 本次全部 per-node evidence"),
		// regardless of whether it changes membership.
		if ok {
			if err := insertEvidenceRow(ctx, queries, occurrenceID, node, row, evaluationID, evaluationAt); err != nil {
				return nil, err
			}
			evidenceCount++
		}
	}

	// Absence evidence is written above, before the membership DELETE below
	// (design.md Phase 3 review item F: "先写 absence_confirmed evidence，
	// 再 DELETE occurrence_node").
	for _, node := range toRemove {
		if err := queries.DeleteCrossNodeDuplicateOccurrenceNode(ctx, generated.DeleteCrossNodeDuplicateOccurrenceNodeParams{
			OccurrenceID: nullableUUID(occurrenceID), InstanceID: nullableUUID(node),
		}); err != nil {
			return nil, err
		}
	}
	for _, node := range toAdd {
		if err := queries.InsertCrossNodeDuplicateOccurrenceNode(ctx, generated.InsertCrossNodeDuplicateOccurrenceNodeParams{
			OccurrenceID: nullableUUID(occurrenceID), InstanceID: nullableUUID(node),
			FirstConfirmedAt: pgTime(evaluationAt),
		}); err != nil {
			return nil, err
		}
	}

	retainedSet := make(map[uuid.UUID]bool, len(currentSet)+len(toAdd))
	for node := range currentSet {
		retainedSet[node] = true
	}
	for _, node := range toRemove {
		delete(retainedSet, node)
	}
	for _, node := range toAdd {
		retainedSet[node] = true
	}
	retained := make([]uuid.UUID, 0, len(retainedSet))
	for node := range retainedSet {
		retained = append(retained, node)
	}
	sortUUIDs(retained)

	evidenceState := "complete"
	if anyDegradedRetained {
		evidenceState = "degraded"
	}

	// Resolve requires a coherent evaluation covering every currently
	// retained affected Node (no degraded/unverifiable Node left, i.e.
	// evidence_state == complete), owner_confirmed count <= 1 (exactly
	// len(retained) once every retained Node is owner_confirmed), and at
	// least one evidence row actually backing this evaluation_id (design.md
	// §4 resolve; Phase 3 review item 3 defensive guard -- in practice
	// evidenceCount == 0 can only coincide with a currently-empty affected
	// set, never with a genuine resolve).
	if evidenceState == "complete" && len(retained) <= 1 && evidenceCount > 0 {
		if err := queries.ResolveCrossNodeDuplicateOccurrence(ctx, generated.ResolveCrossNodeDuplicateOccurrenceParams{
			EvaluationAt: pgTime(evaluationAt), EvaluationID: nullableUUID(evaluationID),
			OccurrenceID: nullableUUID(occurrenceID),
		}); err != nil {
			return nil, err
		}
		return &CrossNodeDuplicateOwnershipEvaluation{
			OccurrenceID: occurrenceID, Status: "RESOLVED", EvidenceState: "complete",
			AffectedNodes: retained, Added: toAdd, Removed: toRemove,
			EvaluationID: evaluationID, EvaluationAt: evaluationAt,
		}, nil
	}

	// Zero-evidence degraded pass (Phase 3 review item 3): no evidence row
	// was written this pass at all (every retained Node was unclassifiable
	// this pass), so latest_evaluation_id / last_fully_verified_at must be
	// left pointing at whatever evaluation last actually had evidence
	// backing it, instead of advancing to an evaluation_id with zero
	// evidence rows (which the 00013 latest_evaluation_id consistency
	// trigger would reject with 23514).
	hasEvidence := evidenceCount > 0
	if err := queries.RefreshCrossNodeDuplicateOccurrenceProjection(ctx, generated.RefreshCrossNodeDuplicateOccurrenceProjectionParams{
		EvaluationAt: pgTime(evaluationAt), EvidenceState: evidenceState, HasEvidence: hasEvidence,
		EvaluationID: nullableUUID(evaluationID), OccurrenceID: nullableUUID(occurrenceID),
	}); err != nil {
		return nil, err
	}
	return &CrossNodeDuplicateOwnershipEvaluation{
		OccurrenceID: occurrenceID, Status: "ACTIVE", EvidenceState: evidenceState,
		AffectedNodes: retained, Added: toAdd, Removed: toRemove,
		EvaluationID: evaluationID, EvaluationAt: evaluationAt,
	}, nil
}

func insertEvidenceRow(
	ctx context.Context, queries *generated.Queries,
	occurrenceID, node uuid.UUID, row crossNodeDuplicateEvidenceRow, evaluationID uuid.UUID, evaluationAt time.Time,
) error {
	var sourcePollRunID pgtype.UUID
	if row.SourcePollRunID != nil {
		sourcePollRunID = nullableUUID(*row.SourcePollRunID)
	}
	return queries.InsertCrossNodeDuplicateOccurrenceEvidence(ctx, generated.InsertCrossNodeDuplicateOccurrenceEvidenceParams{
		OccurrenceID: nullableUUID(occurrenceID), InstanceID: nullableUUID(node),
		ObservationKind: row.ObservationKind, SourcePollRunID: sourcePollRunID,
		SourceProvider: row.SourceProvider, SourceScheduledAt: pgTime(row.SourceScheduledAt),
		SourceCompletedAt: pgTime(row.SourceCompletedAt),
		EvaluationID:      nullableUUID(evaluationID), EvaluationAt: pgTime(evaluationAt),
	})
}

func sortUUIDs(ids []uuid.UUID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
}
