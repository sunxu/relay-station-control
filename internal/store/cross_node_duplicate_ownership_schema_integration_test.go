package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// crossNodeDuplicateSchemaFixture provisions the minimal environment/node
// rows needed to exercise the Phase 1B occurrence/affected-node/evidence
// schema in isolation (migration 00013).
type crossNodeDuplicateSchemaFixture struct {
	environmentID string
	nodeA         uuid.UUID
	nodeB         uuid.UUID
	nodeC         uuid.UUID
}

func newCrossNodeDuplicateSchemaFixture(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
) crossNodeDuplicateSchemaFixture {
	t.Helper()
	fixture := crossNodeDuplicateSchemaFixture{
		environmentID: "cross-node-duplicate-test",
		nodeA:         uuid.New(),
		nodeB:         uuid.New(),
		nodeC:         uuid.New(),
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO environments(
		environment_id, name, environment_type
	) VALUES ($1, 'Cross-node Duplicate Test', 'dev')`,
		fixture.environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type, driver_contract_version, display_name
	) VALUES ('cross-node-duplicate','v1','Cross Node Duplicate Node')
	ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []uuid.UUID{fixture.nodeA, fixture.nodeB, fixture.nodeC} {
		if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
			instance_id, display_name, node_type, driver_contract_version,
			management_endpoint, reader_secret_ref
		) VALUES ($1,'Cross Node Duplicate Node','cross-node-duplicate','v1',$2,NULL)`,
			nodeID, "https://node-"+nodeID.String()+".test"); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

// insertOccurrence inserts a minimal legal ACTIVE occurrence row (no evidence
// yet, latest_evaluation_id NULL) and returns its occurrence_id.
func (fixture crossNodeDuplicateSchemaFixture) insertOccurrence(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, accountKey string, seenAt time.Time,
) uuid.UUID {
	t.Helper()
	var occurrenceID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrences(
		environment_id, account_key, conflict_type, status, severity,
		first_seen_at, last_seen_at, evidence_state
	) VALUES ($1,$2,'cross_node_duplicate_ownership','ACTIVE','Critical',$3,$3,'complete')
	RETURNING occurrence_id`,
		fixture.environmentID, accountKey, seenAt,
	).Scan(&occurrenceID); err != nil {
		t.Fatal(err)
	}
	return occurrenceID
}

// insertEvidence inserts one evidence observation row and returns its
// observation_id.
func (fixture crossNodeDuplicateSchemaFixture) insertEvidence(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
	occurrenceID uuid.UUID,
	nodeID uuid.UUID,
	kind string,
	evaluationID uuid.UUID,
	evaluationAt time.Time,
) uuid.UUID {
	t.Helper()
	var observationID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
		occurrence_id, instance_id, observation_kind, source_provider,
		source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
	) VALUES ($1,$2,$3,'openai',$4,$4,$5,$4)
	RETURNING observation_id`,
		occurrenceID, nodeID, kind, evaluationAt, evaluationID,
	).Scan(&observationID); err != nil {
		t.Fatal(err)
	}
	return observationID
}

func TestCrossNodeDuplicateOwnershipSchemaFoundation(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newCrossNodeDuplicateSchemaFixture(t, ctx, database)
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("plaintext account_key round-trips and evidence has no account_key column", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:person@example.com", now)
		var readBack string
		if err := database.owner.QueryRow(ctx,
			`SELECT account_key FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`,
			occurrenceID,
		).Scan(&readBack); err != nil {
			t.Fatal(err)
		}
		if readBack != "openai:person@example.com" {
			t.Fatalf("account_key = %q, want plaintext canonical value", readBack)
		}
		var columnExists bool
		if err := database.owner.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'cross_node_duplicate_occurrence_evidence'
			  AND column_name = 'account_key'
		)`).Scan(&columnExists); err != nil {
			t.Fatal(err)
		}
		if columnExists {
			t.Fatal("evidence table must not have an account_key column")
		}
	})

	t.Run("account_key shape rejects missing colon and bad provider charset", func(t *testing.T) {
		for name, key := range map[string]string{
			"no colon":        "openaiperson@example.com",
			"empty provider":  ":person@example.com",
			"upper provider":  "OpenAI:person@example.com",
			"empty email":     "openai:",
			"email not lower": "openai:Person@example.com",
		} {
			t.Run(name, func(t *testing.T) {
				_, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrences(
					environment_id, account_key, conflict_type, status, severity,
					first_seen_at, last_seen_at, evidence_state
				) VALUES ($1,$2,'cross_node_duplicate_ownership','ACTIVE','Critical',$3,$3,'complete')`,
					fixture.environmentID, key, now)
				requireGatewayDirectorySQLState(t, err, "23514")
			})
		}
	})

	t.Run("same semantic key cannot have two ACTIVE occurrences", func(t *testing.T) {
		fixture.insertOccurrence(t, ctx, database, "openai:dup1@example.com", now)
		_, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,'openai:dup1@example.com','cross_node_duplicate_ownership','ACTIVE','Critical',$2,$2,'complete')`,
			fixture.environmentID, now)
		requireGatewayDirectorySQLState(t, err, "23505")
	})

	t.Run("RESOLVED then reopen with a new occurrence is allowed", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup2@example.com", now)
		evaluationID := uuid.New()
		fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA, "absence_confirmed", evaluationID, now)
		if _, err := database.owner.Exec(ctx, `UPDATE cross_node_duplicate_occurrences
			SET status='RESOLVED', resolved_at=$2, latest_evaluation_id=$3
			WHERE occurrence_id=$1`,
			occurrenceID, now, evaluationID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,'openai:dup2@example.com','cross_node_duplicate_ownership','ACTIVE','Critical',$2,$2,'complete')`,
			fixture.environmentID, now); err != nil {
			t.Fatalf("expected reopen after RESOLVED to succeed: %v", err)
		}
	})

	t.Run("INSERT ACTIVE succeeds, INSERT RESOLVED is rejected", func(t *testing.T) {
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,'openai:dup2b@example.com','cross_node_duplicate_ownership','ACTIVE','Critical',$2,$2,'complete')`,
			fixture.environmentID, now); err != nil {
			t.Fatalf("expected INSERT ACTIVE to succeed: %v", err)
		}
		_, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, resolved_at, evidence_state
		) VALUES ($1,'openai:dup2c@example.com','cross_node_duplicate_ownership','RESOLVED','Critical',$2,$2,$2,'complete')`,
			fixture.environmentID, now)
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("occurrence identity fields are immutable", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup3@example.com", now)
		_, err := database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET environment_id='other-env' WHERE occurrence_id=$1`,
			occurrenceID)
		requireGatewayDirectorySQLState(t, err, "23514")
		_, err = database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET account_key='openai:other@example.com' WHERE occurrence_id=$1`,
			occurrenceID)
		requireGatewayDirectorySQLState(t, err, "23514")
		_, err = database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET first_seen_at=$2 WHERE occurrence_id=$1`,
			occurrenceID, now.Add(time.Hour))
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("ACTIVE projection fields update, then ACTIVE -> RESOLVED, then RESOLVED is frozen", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup4@example.com", now)
		if _, err := database.owner.Exec(ctx, `UPDATE cross_node_duplicate_occurrences
			SET last_seen_at=$2, evidence_state='degraded', last_fully_verified_at=$2
			WHERE occurrence_id=$1`,
			occurrenceID, now.Add(time.Minute)); err != nil {
			t.Fatalf("expected legal ACTIVE projection update to succeed: %v", err)
		}
		if _, err := database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET status='RESOLVED' WHERE occurrence_id=$1`,
			occurrenceID); err == nil {
			t.Fatal("expected status->RESOLVED without resolved_at in same UPDATE to fail")
		}
		if _, err := database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET status='RESOLVED', resolved_at=$2 WHERE occurrence_id=$1`,
			occurrenceID, now.Add(2*time.Minute)); err != nil {
			t.Fatalf("expected ACTIVE -> RESOLVED with resolved_at set to succeed: %v", err)
		}
		_, err := database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET last_seen_at=$2 WHERE occurrence_id=$1`,
			occurrenceID, now.Add(3*time.Minute))
		requireGatewayDirectorySQLState(t, err, "23514")
		_, err = database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET status='ACTIVE', resolved_at=NULL WHERE occurrence_id=$1`,
			occurrenceID)
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("occurrence DELETE and TRUNCATE are rejected", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup5@example.com", now)
		_, err := database.owner.Exec(ctx,
			`DELETE FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, occurrenceID)
		requireGatewayDirectorySQLState(t, err, "42501")
		// TRUNCATE on cross_node_duplicate_occurrences is intercepted by
		// PostgreSQL's own FK-referenced-table check (0A000) before our own
		// TRUNCATE trigger ever fires, because occurrence_nodes/evidence FK
		// to it; either way the table cannot be truncated.
		_, err = database.owner.Exec(ctx, `TRUNCATE cross_node_duplicate_occurrences`)
		requireGatewayDirectorySQLState(t, err, "0A000")
	})

	t.Run("child INSERT/DELETE only allowed while parent ACTIVE", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup6@example.com", now)
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(
			occurrence_id, instance_id, first_confirmed_at
		) VALUES ($1,$2,$3)`, occurrenceID, fixture.nodeA, now); err != nil {
			t.Fatalf("expected INSERT while parent ACTIVE to succeed: %v", err)
		}
		if _, err := database.owner.Exec(ctx, `DELETE FROM cross_node_duplicate_occurrence_nodes
			WHERE occurrence_id=$1 AND instance_id=$2`, occurrenceID, fixture.nodeA); err != nil {
			t.Fatalf("expected DELETE while parent ACTIVE to succeed: %v", err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(
			occurrence_id, instance_id, first_confirmed_at
		) VALUES ($1,$2,$3)`, occurrenceID, fixture.nodeB, now); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET status='RESOLVED', resolved_at=$2 WHERE occurrence_id=$1`,
			occurrenceID, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		_, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(
			occurrence_id, instance_id, first_confirmed_at
		) VALUES ($1,$2,$3)`, occurrenceID, fixture.nodeC, now)
		requireGatewayDirectorySQLState(t, err, "23514")
		_, err = database.owner.Exec(ctx, `DELETE FROM cross_node_duplicate_occurrence_nodes
			WHERE occurrence_id=$1 AND instance_id=$2`, occurrenceID, fixture.nodeB)
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("child UPDATE and TRUNCATE are rejected", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup7@example.com", now)
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(
			occurrence_id, instance_id, first_confirmed_at
		) VALUES ($1,$2,$3)`, occurrenceID, fixture.nodeA, now); err != nil {
			t.Fatal(err)
		}
		_, err := database.owner.Exec(ctx, `UPDATE cross_node_duplicate_occurrence_nodes
			SET first_confirmed_at=$3 WHERE occurrence_id=$1 AND instance_id=$2`,
			occurrenceID, fixture.nodeA, now.Add(time.Minute))
		requireGatewayDirectorySQLState(t, err, "42501")
		_, err = database.owner.Exec(ctx, `TRUNCATE cross_node_duplicate_occurrence_nodes`)
		requireGatewayDirectorySQLState(t, err, "42501")
	})

	t.Run("evidence INSERT only allowed while parent occurrence is ACTIVE", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup7b@example.com", now)
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ($1,$2,'owner_confirmed','openai',$3,$3,$4,$3)`,
			occurrenceID, fixture.nodeA, now, uuid.New()); err != nil {
			t.Fatalf("expected evidence INSERT to succeed while parent occurrence is ACTIVE: %v", err)
		}

		// Normal resolve order: evidence first (still ACTIVE), then the
		// occurrence transitions ACTIVE -> RESOLVED.
		resolveEvaluationID := uuid.New()
		fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA,
			"absence_confirmed", resolveEvaluationID, now.Add(time.Minute))
		if _, err := database.owner.Exec(ctx, `UPDATE cross_node_duplicate_occurrences
			SET status='RESOLVED', resolved_at=$2, latest_evaluation_id=$3
			WHERE occurrence_id=$1`,
			occurrenceID, now.Add(time.Minute), resolveEvaluationID); err != nil {
			t.Fatal(err)
		}

		// Once RESOLVED, appending more evidence to the same occurrence is
		// rejected: evidence can only ever be appended to an ACTIVE parent.
		_, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ($1,$2,'owner_confirmed','openai',$3,$3,$4,$3)`,
			occurrenceID, fixture.nodeA, now.Add(2*time.Minute), uuid.New())
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("one observation per occurrence+evaluation+Node", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup7c@example.com", now)
		evaluationID := uuid.New()
		fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA,
			"owner_confirmed", evaluationID, now)

		// Same occurrence + evaluation + Node: second observation rejected.
		_, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ($1,$2,'owner_confirmed','openai',$3,$3,$4,$3)`,
			occurrenceID, fixture.nodeA, now, evaluationID)
		requireGatewayDirectorySQLState(t, err, "23505")

		// Same occurrence + evaluation, different Node: both succeed.
		fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeB,
			"owner_confirmed", evaluationID, now)

		// Different evaluation, same Node: allowed.
		fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA,
			"owner_confirmed", uuid.New(), now.Add(time.Minute))
	})

	t.Run("evidence UPDATE, DELETE, TRUNCATE are rejected", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup8@example.com", now)
		observationID := fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA,
			"owner_confirmed", uuid.New(), now)
		_, err := database.owner.Exec(ctx, `UPDATE cross_node_duplicate_occurrence_evidence
			SET source_provider='anthropic' WHERE observation_id=$1`, observationID)
		requireGatewayDirectorySQLState(t, err, "42501")
		_, err = database.owner.Exec(ctx,
			`DELETE FROM cross_node_duplicate_occurrence_evidence WHERE observation_id=$1`, observationID)
		requireGatewayDirectorySQLState(t, err, "42501")
		_, err = database.owner.Exec(ctx, `TRUNCATE cross_node_duplicate_occurrence_evidence`)
		requireGatewayDirectorySQLState(t, err, "42501")
	})

	t.Run("source_poll_run_id has no FK: Account Inventory retention cannot touch evidence", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup9@example.com", now)
		danglingPollRunID := uuid.New() // never inserted into account_inventory_poll_runs
		if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_poll_run_id, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ($1,$2,'owner_confirmed',$3,'openai',$4,$4,$5,$4)`,
			occurrenceID, fixture.nodeA, danglingPollRunID, now, uuid.New()); err != nil {
			t.Fatalf("expected dangling/never-existed source_poll_run_id to be accepted (no FK): %v", err)
		}
	})

	t.Run("latest_evaluation_id must reference an existing evaluation for this occurrence", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup10@example.com", now)
		otherOccurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup10b@example.com", now)
		nonexistentEvaluationID := uuid.New()
		_, err := database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET latest_evaluation_id=$2 WHERE occurrence_id=$1`,
			occurrenceID, nonexistentEvaluationID)
		requireGatewayDirectorySQLState(t, err, "23514")

		otherEvaluationID := uuid.New()
		fixture.insertEvidence(t, ctx, database, otherOccurrenceID, fixture.nodeA,
			"owner_confirmed", otherEvaluationID, now)
		_, err = database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET latest_evaluation_id=$2 WHERE occurrence_id=$1`,
			occurrenceID, otherEvaluationID)
		requireGatewayDirectorySQLState(t, err, "23514")

		evaluationID := uuid.New()
		fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA,
			"owner_confirmed", evaluationID, now)
		if _, err := database.owner.Exec(ctx,
			`UPDATE cross_node_duplicate_occurrences SET latest_evaluation_id=$2 WHERE occurrence_id=$1`,
			occurrenceID, evaluationID); err != nil {
			t.Fatalf("expected evidence-first-then-latest_evaluation_id update to succeed: %v", err)
		}
	})

	t.Run("evidence FK to relay_node_assets and cross_node_duplicate_occurrences", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup11@example.com", now)
		_, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ($1,'00000000-0000-0000-0000-000000000001','owner_confirmed','openai',$2,$2,$3,$2)`,
			occurrenceID, now, uuid.New())
		requireGatewayDirectorySQLState(t, err, "23503")
		_, err = database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ('00000000-0000-0000-0000-000000000001',$1,'owner_confirmed','openai',$2,$2,$3,$2)`,
			fixture.nodeA, now, uuid.New())
		// A bogus occurrence_id is caught by the BEFORE INSERT parent-ACTIVE
		// guard before the FK is ever evaluated (no matching parent row means
		// parent_status IS NULL, which is "not ACTIVE").
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("asset/environment deletion is restricted while duplicate history exists", func(t *testing.T) {
		occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:dup12@example.com", now)
		fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA, "owner_confirmed", uuid.New(), now)
		// ON DELETE RESTRICT raises restrict_violation (23001), not
		// foreign_key_violation (23503); see
		// relay_node_gateway_account_binding_schema_integration_test.go.
		_, err := database.owner.Exec(ctx, `DELETE FROM relay_node_assets WHERE instance_id=$1`, fixture.nodeA)
		requireGatewayDirectorySQLState(t, err, "23001")
		// environments already has its own unconditional delete-block guard
		// (identity singleton, 23514) that fires before our FK is ever
		// evaluated; either way environment deletion cannot silently
		// cascade-lose duplicate-ownership history.
		_, err = database.owner.Exec(ctx, `DELETE FROM environments WHERE environment_id=$1`, fixture.environmentID)
		requireGatewayDirectorySQLState(t, err, "23514")
	})
}
