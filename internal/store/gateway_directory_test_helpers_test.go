package store_test

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

func gatewayDirectoryCurrentSlot(t *testing.T, ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) time.Time {
	t.Helper()
	var slot time.Time
	if err := query.QueryRow(ctx, `SELECT to_timestamp(
		floor(extract(epoch FROM clock_timestamp())/180)*180
	)`).Scan(&slot); err != nil {
		t.Fatal(err)
	}
	return slot.UTC()
}

func insertGatewayInstance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gatewayID uuid.UUID, endpoint, secretRef string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT instance_id FROM gateway_instances WHERE singleton_id = 1 FOR UPDATE`).Scan(&currentID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if err == nil && currentID != gatewayID {
		const fixtureAdminID = "00000000-0000-4000-8000-00000000a901"
		if _, err := tx.Exec(ctx, `INSERT INTO control_admin_users(
			admin_id, login_name, display_name, status, activated_at
		) VALUES ($1, 'gateway-directory-fixture-admin', 'Gateway Directory Fixture Admin', 'enabled', clock_timestamp())
		ON CONFLICT (admin_id) DO NOTHING`, fixtureAdminID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE gateway_instances
			SET singleton_id = NULL,
				lifecycle_status = 'retired',
				retired_at = clock_timestamp(),
				retired_by = $1,
				retire_reason = 'replacement',
				revision = revision + 1,
				updated_at = clock_timestamp()
			WHERE instance_id = $2`, fixtureAdminID, currentID); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := tx.Exec(ctx, `INSERT INTO gateway_instances(
		singleton_id, instance_id, display_name, management_endpoint, reader_secret_ref, directory_credential_sealed,
		created_at, updated_at
	) VALUES (
		1, $1, 'Gateway Directory Test', $2, $3, $4,
		clock_timestamp(), clock_timestamp()
	)
	ON CONFLICT (instance_id) DO UPDATE SET
		singleton_id = EXCLUDED.singleton_id,
		instance_id = EXCLUDED.instance_id,
		display_name = EXCLUDED.display_name,
		management_endpoint = EXCLUDED.management_endpoint,
		reader_secret_ref = EXCLUDED.reader_secret_ref,
		directory_credential_sealed = EXCLUDED.directory_credential_sealed,
		updated_at = EXCLUDED.updated_at`,
		gatewayID, endpoint, nil, make([]byte, 29)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func writeGatewayDirectorySecretResolver(t *testing.T, reference, token string) drivers.SecretResolver {
	t.Helper()
	_ = reference
	return staticGatewayDirectorySecretResolver{token: token}
}

type staticGatewayDirectorySecretResolver struct{ token string }

func (resolver staticGatewayDirectorySecretResolver) Resolve(context.Context, drivers.SecretReference) (*drivers.Secret, error) {
	return drivers.NewSecretFromBytes([]byte(resolver.token)), nil
}

func trustServerCertificate(t *testing.T, server *httptest.Server) {
	t.Helper()
	if server == nil || server.TLS == nil || len(server.TLS.Certificates) == 0 || len(server.TLS.Certificates[0].Certificate) == 0 {
		t.Fatal("missing test server certificate")
	}
	path := filepath.Join(t.TempDir(), "directory-server.pem")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	if err := pem.Encode(file, &pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}); err != nil {
		_ = file.Close()
		t.Fatalf("encode cert file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close cert file: %v", err)
	}
	t.Setenv("SSL_CERT_FILE", path)
	t.Setenv("SSL_CERT_DIR", "")
}
