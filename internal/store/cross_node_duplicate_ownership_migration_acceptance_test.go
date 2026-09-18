package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestCrossNodeDuplicateOwnershipRuntimePrivilegeMatrix proves the exact
// least-privilege GRANT matrix frozen in migration 00013: relay_control_runtime
// can perform only the verbs the frozen occurrence/affected-node/evidence
// lifecycle needs, and nothing more.
func TestCrossNodeDuplicateOwnershipRuntimePrivilegeMatrix(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newCrossNodeDuplicateSchemaFixture(t, ctx, database)
	now := time.Now().UTC().Truncate(time.Microsecond)

	requireDenied := func(t *testing.T, statement string, args ...any) {
		t.Helper()
		_, err := database.runtime.Exec(ctx, statement, args...)
		if err == nil {
			t.Fatalf("expected %q to be denied for relay_control_runtime", statement)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("expected SQLSTATE 42501 permission denied for %q, got: %v", statement, err)
		}
	}

	t.Run("occurrence: SELECT/INSERT and column-scoped UPDATE only", func(t *testing.T) {
		var occurrenceID uuid.UUID
		if err := database.runtime.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,'openai:priv1@example.com','cross_node_duplicate_ownership','ACTIVE','Critical',$2,$2,'complete')
		RETURNING occurrence_id`,
			fixture.environmentID, now,
		).Scan(&occurrenceID); err != nil {
			t.Fatalf("expected relay_control_runtime INSERT on occurrences to succeed: %v", err)
		}
		if _, err := database.runtime.Exec(ctx, `SELECT * FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, occurrenceID); err != nil {
			t.Fatalf("expected relay_control_runtime SELECT on occurrences to succeed: %v", err)
		}
		if _, err := database.runtime.Exec(ctx, `UPDATE cross_node_duplicate_occurrences
			SET last_seen_at=$2, evidence_state='degraded', last_fully_verified_at=$2
			WHERE occurrence_id=$1`, occurrenceID, now.Add(time.Minute)); err != nil {
			t.Fatalf("expected relay_control_runtime UPDATE of mutable projection columns to succeed: %v", err)
		}
		requireDenied(t, `UPDATE cross_node_duplicate_occurrences SET environment_id=environment_id WHERE occurrence_id=$1`, occurrenceID)
		requireDenied(t, `UPDATE cross_node_duplicate_occurrences SET account_key=account_key WHERE occurrence_id=$1`, occurrenceID)
		requireDenied(t, `UPDATE cross_node_duplicate_occurrences SET conflict_type=conflict_type WHERE occurrence_id=$1`, occurrenceID)
		requireDenied(t, `UPDATE cross_node_duplicate_occurrences SET severity=severity WHERE occurrence_id=$1`, occurrenceID)
		requireDenied(t, `UPDATE cross_node_duplicate_occurrences SET first_seen_at=first_seen_at WHERE occurrence_id=$1`, occurrenceID)
		requireDenied(t, `DELETE FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, occurrenceID)
	})

	t.Run("occurrence_nodes: SELECT/INSERT/DELETE only, no UPDATE", func(t *testing.T) {
		var occurrenceID uuid.UUID
		if err := database.owner.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,'openai:priv2@example.com','cross_node_duplicate_ownership','ACTIVE','Critical',$2,$2,'complete')
		RETURNING occurrence_id`,
			fixture.environmentID, now,
		).Scan(&occurrenceID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.runtime.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(
			occurrence_id, instance_id, first_confirmed_at
		) VALUES ($1,$2,$3)`, occurrenceID, fixture.nodeA, now); err != nil {
			t.Fatalf("expected relay_control_runtime INSERT on occurrence_nodes to succeed: %v", err)
		}
		if _, err := database.runtime.Exec(ctx, `SELECT * FROM cross_node_duplicate_occurrence_nodes WHERE occurrence_id=$1`, occurrenceID); err != nil {
			t.Fatalf("expected relay_control_runtime SELECT on occurrence_nodes to succeed: %v", err)
		}
		requireDenied(t, `UPDATE cross_node_duplicate_occurrence_nodes SET first_confirmed_at=first_confirmed_at WHERE occurrence_id=$1`, occurrenceID)
		if _, err := database.runtime.Exec(ctx, `DELETE FROM cross_node_duplicate_occurrence_nodes
			WHERE occurrence_id=$1 AND instance_id=$2`, occurrenceID, fixture.nodeA); err != nil {
			t.Fatalf("expected relay_control_runtime DELETE on occurrence_nodes to succeed: %v", err)
		}
	})

	t.Run("evidence: SELECT/INSERT only, no UPDATE/DELETE", func(t *testing.T) {
		var occurrenceID uuid.UUID
		if err := database.owner.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,'openai:priv3@example.com','cross_node_duplicate_ownership','ACTIVE','Critical',$2,$2,'complete')
		RETURNING occurrence_id`,
			fixture.environmentID, now,
		).Scan(&occurrenceID); err != nil {
			t.Fatal(err)
		}
		var observationID uuid.UUID
		if err := database.runtime.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrence_evidence(
			occurrence_id, instance_id, observation_kind, source_provider,
			source_scheduled_at, source_completed_at, evaluation_id, evaluation_at
		) VALUES ($1,$2,'owner_confirmed','openai',$3,$3,$4,$3)
		RETURNING observation_id`,
			occurrenceID, fixture.nodeA, now, uuid.New(),
		).Scan(&observationID); err != nil {
			t.Fatalf("expected relay_control_runtime INSERT on evidence to succeed: %v", err)
		}
		if _, err := database.runtime.Exec(ctx, `SELECT * FROM cross_node_duplicate_occurrence_evidence WHERE observation_id=$1`, observationID); err != nil {
			t.Fatalf("expected relay_control_runtime SELECT on evidence to succeed: %v", err)
		}
		requireDenied(t, `UPDATE cross_node_duplicate_occurrence_evidence SET source_provider='anthropic' WHERE observation_id=$1`, observationID)
		requireDenied(t, `DELETE FROM cross_node_duplicate_occurrence_evidence WHERE observation_id=$1`, observationID)
	})

	t.Run("latest_evaluation_id row lock is usable by relay_control_runtime", func(t *testing.T) {
		// The occurrence_nodes/occurrence enforcement functions execute a
		// SELECT ... FOR UPDATE on cross_node_duplicate_occurrences as the
		// invoking role; this proves relay_control_runtime's column-scoped
		// UPDATE grant on occurrences satisfies PostgreSQL's FOR UPDATE ACL
		// check (see migration 00012's precedent comment for why a
		// generated/no real column would not).
		var occurrenceID uuid.UUID
		if err := database.owner.QueryRow(ctx, `INSERT INTO cross_node_duplicate_occurrences(
			environment_id, account_key, conflict_type, status, severity,
			first_seen_at, last_seen_at, evidence_state
		) VALUES ($1,'openai:priv4@example.com','cross_node_duplicate_ownership','ACTIVE','Critical',$2,$2,'complete')
		RETURNING occurrence_id`,
			fixture.environmentID, now,
		).Scan(&occurrenceID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.runtime.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(
			occurrence_id, instance_id, first_confirmed_at
		) VALUES ($1,$2,$3)`, occurrenceID, fixture.nodeA, now); err != nil {
			t.Fatalf("expected relay_control_runtime child INSERT (which locks parent occurrence FOR UPDATE) to succeed: %v", err)
		}
	})
}

// TestCrossNodeDuplicateOwnershipEvidenceEvaluationRuntimePrivileges proves
// the exact least-privilege EXECUTE matrix frozen in migration 00015:
// relay_control_runtime (and only relay_control_runtime) may EXECUTE the
// evidence-evaluation SECURITY DEFINER function, and it still has zero
// direct SELECT access on account_inventory/account_inventory_provider_states.
func TestCrossNodeDuplicateOwnershipEvidenceEvaluationRuntimePrivileges(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)

	var databaseError *pgconn.PgError
	if _, err := database.runtime.Exec(ctx, `SELECT 1 FROM account_inventory LIMIT 1`); !errors.As(err, &databaseError) || databaseError.Code != "42501" {
		t.Fatalf("runtime direct SELECT account_inventory SQLSTATE = %v", err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT 1 FROM account_inventory_provider_states LIMIT 1`); !errors.As(err, &databaseError) || databaseError.Code != "42501" {
		t.Fatalf("runtime direct SELECT account_inventory_provider_states SQLSTATE = %v", err)
	}

	if _, err := database.runtime.Exec(ctx, `SELECT * FROM control_evaluate_cross_node_duplicate_evidence_v1('openai:priv-eval@example.com', ARRAY[]::uuid[], clock_timestamp())`); err != nil {
		t.Fatalf("expected relay_control_runtime EXECUTE on control_evaluate_cross_node_duplicate_evidence_v1 to succeed: %v", err)
	}

	type permission struct {
		name      string
		arguments int
		role      string
		allowed   bool
	}
	wanted := []permission{
		{"control_evaluate_cross_node_duplicate_evidence_v1", 3, "relay_control_runtime", true},
		{"control_evaluate_cross_node_duplicate_evidence_v1", 3, "public", false},
		{"control_evaluate_cross_node_duplicate_evidence_v1", 3, "relay_control_asset_registrar", false},
	}
	for _, expected := range wanted {
		var allowed bool
		if err := database.owner.QueryRow(ctx, `SELECT has_function_privilege($3,p.oid,'EXECUTE')
			FROM pg_proc AS p JOIN pg_namespace AS n ON n.oid=p.pronamespace
			WHERE n.nspname='public' AND p.proname=$1 AND p.pronargs=$2`,
			expected.name, expected.arguments, expected.role).Scan(&allowed); err != nil {
			t.Fatal(err)
		}
		if allowed != expected.allowed {
			t.Fatalf("%s/%s execute = %t, want %t", expected.name, expected.role, allowed, expected.allowed)
		}
	}
}
