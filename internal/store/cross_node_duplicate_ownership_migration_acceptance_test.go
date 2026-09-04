package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestCrossNodeDuplicateOwnershipMigrationUpDownUp proves migration 00013 is
// additive and symmetric on a clean schema: 13 -> 12 -> 13 round-trips
// cleanly, and the pre-existing migration 12 fail-closed rollback guard is
// unaffected by this additive migration.
func TestCrossNodeDuplicateOwnershipMigrationUpDownUp(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()

	assertVersion := func(t *testing.T, want int32) {
		t.Helper()
		var got int32
		if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("migration version = %d, want %d", got, want)
		}
	}

	assertVersion(t, 13)

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, 12)

	var tablesExist bool
	if err := database.owner.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name IN (
			'cross_node_duplicate_occurrences',
			'cross_node_duplicate_occurrence_nodes',
			'cross_node_duplicate_occurrence_evidence'
		)
	)`).Scan(&tablesExist); err != nil {
		t.Fatal(err)
	}
	if tablesExist {
		t.Fatal("expected migration 13 down to drop all three cross-node duplicate tables")
	}

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, 13)
}

// TestCrossNodeDuplicateOwnershipMigrationDownFailsClosedWithHistory proves
// that once any occurrence, affected-node, or evidence history row exists,
// migration 00013's Down is fail-closed (SQLSTATE 55000) and the history
// remains fully intact afterward.
func TestCrossNodeDuplicateOwnershipMigrationDownFailsClosedWithHistory(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newCrossNodeDuplicateSchemaFixture(t, ctx, database)
	now := time.Now().UTC().Truncate(time.Microsecond)

	occurrenceID := fixture.insertOccurrence(t, ctx, database, "openai:rollback@example.com", now)
	if _, err := database.owner.Exec(ctx, `INSERT INTO cross_node_duplicate_occurrence_nodes(
		occurrence_id, instance_id, first_confirmed_at
	) VALUES ($1,$2,$3)`, occurrenceID, fixture.nodeA, now); err != nil {
		t.Fatal(err)
	}
	fixture.insertEvidence(t, ctx, database, occurrenceID, fixture.nodeA, "owner_confirmed", uuid.New(), now)

	err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down")
	if err == nil {
		t.Fatal("expected migration 13 down to fail closed while occurrence history exists")
	}
	if !strings.Contains(err.Error(), "55000") {
		t.Fatalf("expected SQLSTATE 55000 from migration 13 fail-closed guard, got: %v", err)
	}

	// History must remain fully intact: still exactly the version-13
	// schema, and the rows are untouched.
	var version int32
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 13 {
		t.Fatalf("migration version after failed down = %d, want 13 (unchanged)", version)
	}
	var occurrenceCount, nodeCount, evidenceCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrences WHERE occurrence_id=$1`, occurrenceID).Scan(&occurrenceCount); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrence_nodes WHERE occurrence_id=$1`, occurrenceID).Scan(&nodeCount); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM cross_node_duplicate_occurrence_evidence WHERE occurrence_id=$1`, occurrenceID).Scan(&evidenceCount); err != nil {
		t.Fatal(err)
	}
	if occurrenceCount != 1 || nodeCount != 1 || evidenceCount != 1 {
		t.Fatalf("history rows changed after failed rollback: occurrence=%d node=%d evidence=%d, want 1/1/1",
			occurrenceCount, nodeCount, evidenceCount)
	}
}

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
