package store_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
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
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_instances(
		singleton_id, instance_id, display_name, management_endpoint, reader_secret_ref,
		created_at, updated_at
	) VALUES (
		1, $1, 'Gateway Directory Test', $2, $3,
		clock_timestamp(), clock_timestamp()
	)
	ON CONFLICT (singleton_id) DO UPDATE SET
		instance_id = EXCLUDED.instance_id,
		display_name = EXCLUDED.display_name,
		management_endpoint = EXCLUDED.management_endpoint,
		reader_secret_ref = EXCLUDED.reader_secret_ref,
		updated_at = EXCLUDED.updated_at`,
		gatewayID, endpoint, secretRef); err != nil {
		t.Fatal(err)
	}
}

func writeGatewayDirectorySecretResolver(t *testing.T, reference, token string) drivers.SecretResolver {
	resolver, _ := writeGatewayDirectorySecretResolverWithPath(t, reference, token)
	return resolver
}

func writeGatewayDirectorySecretResolverWithPath(t *testing.T, reference, token string) (drivers.SecretResolver, string) {
	t.Helper()
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "reader-token")
	if err := os.WriteFile(secretPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	mapping := filepath.Join(directory, "mapping.json")
	encoded, err := json.Marshal(map[string]any{
		"provider": "file",
		"references": []map[string]string{{
			"reference": reference,
			"path":      secretPath,
		}},
	})
	if err != nil {
		t.Fatalf("marshal secret mapping: %v", err)
	}
	if err := os.WriteFile(mapping, encoded, 0o600); err != nil {
		t.Fatalf("write secret mapping: %v", err)
	}
	resolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{MappingFile: mapping})
	if err != nil {
		t.Fatalf("construct secret resolver: %v", err)
	}
	return resolver, secretPath
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
