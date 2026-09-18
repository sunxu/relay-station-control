package store_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestStage0Migration51NodeRegisterLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES ('cliproxyapi','v1','management_health_read') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name           string
		blob           []byte
		wantConfigured bool
	}{
		{name: "unconfigured"},
		{name: "configured", blob: make([]byte, 29), wantConfigured: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			var created uuid.UUID
			err := database.runtime.QueryRow(ctx, `SELECT public.control_register_relay_node_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::text,$5::text,$6::bytea,$7::text[])`, id, "Stage0 Node", "cliproxyapi", "v1", "http://stage0-node.example", tc.blob, []string{"management_health_read"}).Scan(&created)
			if err != nil {
				t.Fatal(err)
			}
			var lifecycle string
			var revision int
			var legacy *string
			var sealed []byte
			var configured bool
			if err := database.owner.QueryRow(ctx, `SELECT lifecycle_status,revision,reader_secret_ref,management_credential_sealed,reader_secret_configured FROM relay_node_assets WHERE instance_id=$1`, created).Scan(&lifecycle, &revision, &legacy, &sealed, &configured); err != nil {
				t.Fatal(err)
			}
			if lifecycle != "active" || revision != 1 || legacy != nil || configured != tc.wantConfigured {
				t.Fatalf("state lifecycle=%s revision=%d legacy_present=%v configured=%v", lifecycle, revision, legacy != nil, configured)
			}
			if tc.wantConfigured && string(sealed) != string(tc.blob) {
				t.Fatal("sealed value does not match supplied blob")
			}
			if !tc.wantConfigured && sealed != nil {
				t.Fatal("unconfigured node has sealed credential")
			}
		})
	}
}

func TestStage0Migration51NodeEditLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES ('cliproxyapi','v1','management_health_read') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	register := func(blob []byte) uuid.UUID {
		id := uuid.New()
		var created uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT public.control_register_relay_node_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::text,$5::text,$6::bytea,$7::text[])`, id, "Edit Node", "cliproxyapi", "v1", "http://edit-node.example", blob, []string{"management_health_read"}).Scan(&created); err != nil {
			t.Fatal(err)
		}
		return created
	}
	blobA := bytes.Repeat([]byte{0xA1}, 29)
	blobB := bytes.Repeat([]byte{0xB2}, 29)
	t.Run("keep", func(t *testing.T) {
		id := register(blobA)
		if _, err := database.runtime.Exec(ctx, `SELECT public.control_edit_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, id, 1, "Edit Node 2", "http://edit-node-2.example", "keep", nil); err != nil {
			t.Fatal(err)
		}
		assertNodeEditState(t, ctx, database, id, 2, "Edit Node 2", "http://edit-node-2.example", blobA, true)
	})
	t.Run("set", func(t *testing.T) {
		id := register(nil)
		if _, err := database.runtime.Exec(ctx, `SELECT public.control_edit_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, id, 1, "Edit Node Set", "http://edit-node-set.example", "set", blobB); err != nil {
			t.Fatal(err)
		}
		assertNodeEditState(t, ctx, database, id, 2, "Edit Node Set", "http://edit-node-set.example", blobB, true)
	})
	t.Run("clear", func(t *testing.T) {
		id := register(blobA)
		if _, err := database.runtime.Exec(ctx, `SELECT public.control_edit_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, id, 1, "Edit Node Clear", "http://edit-node-clear.example", "clear", nil); err != nil {
			t.Fatal(err)
		}
		assertNodeEditState(t, ctx, database, id, 2, "Edit Node Clear", "http://edit-node-clear.example", nil, false)
	})
	t.Run("invalid combinations and fences", func(t *testing.T) {
		id := register(blobA)
		for _, tc := range []struct {
			action string
			blob   []byte
		}{{"other", nil}, {"keep", blobB}, {"clear", blobB}, {"set", nil}, {"set", []byte{1}}} {
			if _, err := database.runtime.Exec(ctx, `SELECT public.control_edit_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, id, 1, "Invalid", "http://invalid.example", tc.action, tc.blob); err == nil {
				t.Fatalf("action=%s unexpectedly succeeded", tc.action)
			}
		}
		if _, err := database.runtime.Exec(ctx, `SELECT public.control_edit_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, id, 99, "Stale", "http://stale.example", "keep", nil); err == nil {
			t.Fatal("stale revision unexpectedly succeeded")
		} else {
			requirePostgresCode(t, err, "P0005")
		}
		assertNodeEditState(t, ctx, database, id, 1, "Edit Node", "http://edit-node.example", blobA, true)
		retired := register(blobA)
		admin := uuid.New()
		if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'stage0-edit-admin','Stage 0 Edit Admin','enabled',clock_timestamp())`, admin); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `UPDATE relay_node_assets SET lifecycle_status='retired',revision=revision+1,retired_at=clock_timestamp(),retired_by=$2,retire_reason='administrator_retire',updated_at=clock_timestamp() WHERE instance_id=$1`, retired, admin); err != nil {
			t.Fatal(err)
		}
		if _, err := database.runtime.Exec(ctx, `SELECT public.control_edit_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, retired, 1, "Retired", "http://retired.example", "clear", nil); err == nil {
			t.Fatal("retired edit unexpectedly succeeded")
		} else {
			requirePostgresCode(t, err, "P0003")
		}
	})
}

func assertNodeEditState(t *testing.T, ctx context.Context, database *isolatedJobDatabase, id uuid.UUID, revision int, display, endpoint string, expected []byte, configured bool) {
	t.Helper()
	var gotRevision int
	var gotDisplay, gotEndpoint string
	var got []byte
	var gotConfigured bool
	if err := database.owner.QueryRow(ctx, `SELECT revision,display_name,management_endpoint,management_credential_sealed,reader_secret_configured FROM relay_node_assets WHERE instance_id=$1`, id).Scan(&gotRevision, &gotDisplay, &gotEndpoint, &got, &gotConfigured); err != nil {
		t.Fatal(err)
	}
	if gotRevision != revision || gotDisplay != display || gotEndpoint != endpoint || gotConfigured != configured || !bytes.Equal(got, expected) {
		t.Fatalf("unexpected node edit state revision=%d display=%q endpoint=%q configured=%v", gotRevision, gotDisplay, gotEndpoint, gotConfigured)
	}
}

func TestStage0Migration51NodeRetireLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES ('cliproxyapi','v1','management_health_read') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'stage0-retire-admin','Stage 0 Retire Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	register := func(blob []byte) uuid.UUID {
		id := uuid.New()
		var created uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT public.control_register_relay_node_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::text,$5::text,$6::bytea,$7::text[])`, id, "Retire Node", "cliproxyapi", "v1", "http://retire-node.example", blob, []string{"management_health_read"}).Scan(&created); err != nil {
			t.Fatal(err)
		}
		return created
	}
	callRetire := func(id uuid.UUID, revision int64, boundary time.Time, retiredBy uuid.UUID, reason string) error {
		_, err := database.runtime.Exec(ctx, `SELECT public.control_retire_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::text,$6::timestamptz)`, id, revision, boundary, retiredBy, reason, boundary)
		return err
	}
	blob := bytes.Repeat([]byte{0xC3}, 29)

	t.Run("configured node retire", func(t *testing.T) {
		id := register(blob)
		boundary := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Microsecond)
		if err := callRetire(id, 1, boundary, admin, "administrator_retire"); err != nil {
			t.Fatal(err)
		}
		var lifecycle string
		var revision int
		var sealed []byte
		var configured bool
		var retiredAt, updatedAt time.Time
		var retiredBy uuid.UUID
		var reason string
		var legacy *string
		if err := database.owner.QueryRow(ctx, `SELECT lifecycle_status,revision,management_credential_sealed,reader_secret_configured,retired_at,retired_by,retire_reason,updated_at,reader_secret_ref FROM relay_node_assets WHERE instance_id=$1`, id).Scan(&lifecycle, &revision, &sealed, &configured, &retiredAt, &retiredBy, &reason, &updatedAt, &legacy); err != nil {
			t.Fatal(err)
		}
		if lifecycle != "retired" || revision != 2 || sealed != nil || configured || !retiredAt.Equal(boundary) || retiredBy != admin || reason != "administrator_retire" || !updatedAt.Equal(boundary) || legacy != nil {
			t.Fatalf("unexpected retired state lifecycle=%s revision=%d configured=%v retired_by=%s reason=%s", lifecycle, revision, configured, retiredBy, reason)
		}
	})

	t.Run("stale revision preserves credential", func(t *testing.T) {
		id := register(blob)
		boundary := time.Now().UTC().Add(3 * time.Minute).Truncate(time.Microsecond)
		if err := callRetire(id, 99, boundary, admin, "administrator_retire"); err == nil {
			t.Fatal("stale retire unexpectedly succeeded")
		} else {
			requirePostgresCode(t, err, "P0005")
		}
		var lifecycle string
		var revision int
		var sealed []byte
		var configured bool
		if err := database.owner.QueryRow(ctx, `SELECT lifecycle_status,revision,management_credential_sealed,reader_secret_configured FROM relay_node_assets WHERE instance_id=$1`, id).Scan(&lifecycle, &revision, &sealed, &configured); err != nil {
			t.Fatal(err)
		}
		if lifecycle != "active" || revision != 1 || !bytes.Equal(sealed, blob) || !configured {
			t.Fatalf("stale retire mutated state lifecycle=%s revision=%d configured=%v", lifecycle, revision, configured)
		}
	})

	t.Run("already retired preserves credential", func(t *testing.T) {
		id := register(blob)
		boundary := time.Now().UTC().Add(4 * time.Minute).Truncate(time.Microsecond)
		if _, err := database.owner.Exec(ctx, `UPDATE relay_node_assets SET lifecycle_status='retired',revision=revision+1,retired_at=$2,retired_by=$3,retire_reason='administrator_retire',updated_at=$2 WHERE instance_id=$1`, id, boundary, admin); err != nil {
			t.Fatal(err)
		}
		if err := callRetire(id, 1, boundary.Add(time.Minute), admin, "administrator_retire"); err == nil {
			t.Fatal("already-retired retire unexpectedly succeeded")
		}
		var revision int
		var sealed []byte
		if err := database.owner.QueryRow(ctx, `SELECT revision,management_credential_sealed FROM relay_node_assets WHERE instance_id=$1`, id).Scan(&revision, &sealed); err != nil {
			t.Fatal(err)
		}
		if revision != 2 || !bytes.Equal(sealed, blob) {
			t.Fatalf("already-retired retire mutated state revision=%d", revision)
		}
	})

	t.Run("revision overflow preserves credential", func(t *testing.T) {
		id := register(blob)
		if _, err := database.owner.Exec(ctx, `ALTER TABLE relay_node_assets DISABLE TRIGGER relay_node_assets_lifecycle_guard`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `UPDATE relay_node_assets SET revision=9223372036854775807 WHERE instance_id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `ALTER TABLE relay_node_assets ENABLE TRIGGER relay_node_assets_lifecycle_guard`); err != nil {
			t.Fatal(err)
		}
		if err := callRetire(id, 9223372036854775807, time.Date(2026, 9, 18, 12, 3, 0, 0, time.UTC), admin, "administrator_retire"); err == nil {
			t.Fatal("overflow retire unexpectedly succeeded")
		} else {
			requirePostgresCode(t, err, "P0004")
		}
		var lifecycle string
		var revision int64
		var sealed []byte
		if err := database.owner.QueryRow(ctx, `SELECT lifecycle_status,revision,management_credential_sealed FROM relay_node_assets WHERE instance_id=$1`, id).Scan(&lifecycle, &revision, &sealed); err != nil {
			t.Fatal(err)
		}
		if lifecycle != "active" || revision != 9223372036854775807 || !bytes.Equal(sealed, blob) {
			t.Fatalf("overflow retire mutated state lifecycle=%s revision=%d", lifecycle, revision)
		}
	})
}

func TestStage0Migration51NodeReplaceLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES ('cliproxyapi','v1','management_health_read') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'stage0-replace-admin','Stage 0 Replace Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	register := func(blob []byte) uuid.UUID {
		id := uuid.New()
		var created uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT public.control_register_relay_node_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::text,$5::text,$6::bytea,$7::text[])`, id, "Replace Node", "cliproxyapi", "v1", "http://replace-node.example", blob, []string{"management_health_read"}).Scan(&created); err != nil {
			t.Fatal(err)
		}
		return created
	}
	callReplace := func(oldID uuid.UUID, revision int64, boundary time.Time, newID uuid.UUID, display, endpoint string, blob []byte) error {
		_, err := database.runtime.Exec(ctx, `SELECT public.control_replace_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::uuid,$6::text,$7::text,$8::text,$9::text,$10::bytea,$11::text[])`, oldID, revision, boundary, admin, newID, display, "cliproxyapi", "v1", endpoint, blob, []string{"management_health_read"})
		return err
	}
	blobA := bytes.Repeat([]byte{0xA4}, 29)
	blobB := bytes.Repeat([]byte{0xB4}, 29)

	t.Run("unconfigured replacement", func(t *testing.T) {
		oldID := register(blobA)
		newID := uuid.New()
		var createdAt time.Time
		if err := database.owner.QueryRow(ctx, `SELECT created_at FROM relay_node_assets WHERE instance_id=$1`, oldID).Scan(&createdAt); err != nil {
			t.Fatal(err)
		}
		boundary := createdAt.Add(time.Millisecond)
		var returned uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT public.control_replace_relay_node_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::uuid,$6::text,$7::text,$8::text,$9::text,$10::bytea,$11::text[])`, oldID, 1, boundary, admin, newID, "Replacement Node", "cliproxyapi", "v1", "http://replacement-node.example", nil, []string{"management_health_read"}).Scan(&returned); err != nil {
			t.Fatal(err)
		}
		if returned != newID {
			t.Fatalf("replacement id=%s want=%s", returned, newID)
		}
		assertNodeReplaceState(t, ctx, database, oldID, "retired", 2, nil, false, boundary, admin, "replacement")
		assertNodeReplaceState(t, ctx, database, newID, "active", 1, nil, false, time.Time{}, uuid.Nil, "")
	})

	t.Run("configured replacement", func(t *testing.T) {
		oldID := register(blobA)
		newID := uuid.New()
		var createdAt time.Time
		if err := database.owner.QueryRow(ctx, `SELECT created_at FROM relay_node_assets WHERE instance_id=$1`, oldID).Scan(&createdAt); err != nil {
			t.Fatal(err)
		}
		boundary := createdAt.Add(time.Millisecond)
		if err := callReplace(oldID, 1, boundary, newID, "Configured Replacement", "http://configured-replacement.example", blobB); err != nil {
			t.Fatal(err)
		}
		assertNodeReplaceState(t, ctx, database, oldID, "retired", 2, nil, false, boundary, admin, "replacement")
		assertNodeReplaceState(t, ctx, database, newID, "active", 1, blobB, true, time.Time{}, uuid.Nil, "")
		if oldID == newID {
			t.Fatal("replacement reused predecessor identity")
		}
	})

	t.Run("invalid replacements preserve predecessor", func(t *testing.T) {
		cases := []struct {
			name        string
			oldRevision int64
			newID       uuid.UUID
			blob        []byte
		}{
			{name: "stale revision", oldRevision: 99, newID: uuid.New(), blob: nil},
			{name: "same identity", oldRevision: 1, newID: uuid.Nil, blob: nil},
			{name: "existing replacement identity", oldRevision: 1, newID: uuid.Nil, blob: nil},
			{name: "overflow", oldRevision: 9223372036854775807, newID: uuid.New(), blob: nil},
			{name: "short sealed blob", oldRevision: 1, newID: uuid.New(), blob: []byte{1}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				oldID := register(blobA)
				if tc.name == "same identity" {
					tc.newID = oldID
				}
				if tc.name == "overflow" {
					if _, err := database.owner.Exec(ctx, `ALTER TABLE relay_node_assets DISABLE TRIGGER relay_node_assets_lifecycle_guard`); err != nil {
						t.Fatal(err)
					}
					if _, err := database.owner.Exec(ctx, `UPDATE relay_node_assets SET revision=9223372036854775807 WHERE instance_id=$1`, oldID); err != nil {
						t.Fatal(err)
					}
					if _, err := database.owner.Exec(ctx, `ALTER TABLE relay_node_assets ENABLE TRIGGER relay_node_assets_lifecycle_guard`); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "stale revision" {
					tc.oldRevision = 99
				}
				if tc.name == "existing replacement identity" {
					tc.newID = register(nil)
				}
				if err := callReplace(oldID, tc.oldRevision, time.Date(2026, 9, 18, 13, 2, 0, 0, time.UTC), tc.newID, "Invalid Replacement", "http://invalid-replacement.example", tc.blob); err == nil {
					t.Fatal("invalid replacement unexpectedly succeeded")
				}
				var lifecycle string
				var revision int64
				var sealed []byte
				if err := database.owner.QueryRow(ctx, `SELECT lifecycle_status,revision,management_credential_sealed FROM relay_node_assets WHERE instance_id=$1`, oldID).Scan(&lifecycle, &revision, &sealed); err != nil {
					t.Fatal(err)
				}
				wantRevision := int64(1)
				if tc.name == "overflow" {
					wantRevision = 9223372036854775807
				}
				if lifecycle != "active" || revision != wantRevision || !bytes.Equal(sealed, blobA) {
					t.Fatalf("invalid replacement mutated predecessor lifecycle=%s revision=%d", lifecycle, revision)
				}
			})
		}
	})
}

func assertNodeReplaceState(t *testing.T, ctx context.Context, database *isolatedJobDatabase, id uuid.UUID, lifecycle string, revision int, expected []byte, configured bool, retiredAt time.Time, retiredBy uuid.UUID, reason string) {
	t.Helper()
	var gotLifecycle string
	var gotRevision int
	var got []byte
	var gotConfigured bool
	var gotRetiredAt *time.Time
	var gotRetiredBy *uuid.UUID
	var gotReason *string
	var legacy *string
	if err := database.owner.QueryRow(ctx, `SELECT lifecycle_status,revision,management_credential_sealed,reader_secret_configured,retired_at,retired_by,retire_reason,reader_secret_ref FROM relay_node_assets WHERE instance_id=$1`, id).Scan(&gotLifecycle, &gotRevision, &got, &gotConfigured, &gotRetiredAt, &gotRetiredBy, &gotReason, &legacy); err != nil {
		t.Fatal(err)
	}
	if gotLifecycle != lifecycle || gotRevision != revision || gotConfigured != configured || !bytes.Equal(got, expected) || legacy != nil {
		t.Fatalf("unexpected replacement state lifecycle=%s revision=%d configured=%v", gotLifecycle, gotRevision, gotConfigured)
	}
	if lifecycle == "retired" {
		if gotRetiredAt == nil || !gotRetiredAt.Equal(retiredAt) || gotRetiredBy == nil || *gotRetiredBy != retiredBy || gotReason == nil || *gotReason != reason {
			t.Fatalf("unexpected retired metadata")
		}
	}
}

func TestStage0Migration51GatewayRegisterLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	register := func(id uuid.UUID, display, endpoint string, blob []byte) (uuid.UUID, error) {
		var created uuid.UUID
		err := database.runtime.QueryRow(ctx, `SELECT public.control_register_gateway_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::bytea)`, id, display, endpoint, blob).Scan(&created)
		return created, err
	}
	blob := bytes.Repeat([]byte{0xA5}, 29)
	firstID := uuid.New()
	created, err := register(firstID, "Stage 0 Gateway", "http://stage0-gateway.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if created != firstID {
		t.Fatalf("gateway id=%s want=%s", created, firstID)
	}
	var singleton int16
	var gotID uuid.UUID
	var display, endpoint, lifecycle string
	var revision int
	var legacy *string
	var sealed []byte
	var configured bool
	if err := database.owner.QueryRow(ctx, `SELECT singleton_id,instance_id,display_name,management_endpoint,lifecycle_status,revision,reader_secret_ref,directory_credential_sealed,reader_secret_configured FROM gateway_instances WHERE singleton_id=1`).Scan(&singleton, &gotID, &display, &endpoint, &lifecycle, &revision, &legacy, &sealed, &configured); err != nil {
		t.Fatal(err)
	}
	if singleton != 1 || gotID != firstID || display != "Stage 0 Gateway" || endpoint != "http://stage0-gateway.example" || lifecycle != "active" || revision != 1 || legacy != nil || sealed != nil || configured {
		t.Fatalf("unexpected unconfigured gateway singleton=%d id=%s lifecycle=%s revision=%d configured=%v", singleton, gotID, lifecycle, revision, configured)
	}

	admin := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'stage0-gateway-register-admin','Stage 0 Gateway Register Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	var createdAt time.Time
	if err := database.owner.QueryRow(ctx, `SELECT created_at FROM gateway_instances WHERE instance_id=$1`, firstID).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	retiredAt := createdAt.Add(time.Millisecond)
	if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET singleton_id=NULL,lifecycle_status='retired',retired_at=$2,retired_by=$3,retire_reason='replacement',revision=revision+1,updated_at=$2 WHERE instance_id=$1`, firstID, retiredAt, admin); err != nil {
		t.Fatal(err)
	}

	configuredID := uuid.New()
	created, err = register(configuredID, "Configured Gateway", "http://configured-gateway.example", blob)
	if err != nil {
		t.Fatal(err)
	}
	if created != configuredID {
		t.Fatalf("configured gateway id=%s want=%s", created, configuredID)
	}
	if err := database.owner.QueryRow(ctx, `SELECT singleton_id,instance_id,display_name,management_endpoint,lifecycle_status,revision,reader_secret_ref,directory_credential_sealed,reader_secret_configured FROM gateway_instances WHERE singleton_id=1`).Scan(&singleton, &gotID, &display, &endpoint, &lifecycle, &revision, &legacy, &sealed, &configured); err != nil {
		t.Fatal(err)
	}
	if singleton != 1 || gotID != configuredID || display != "Configured Gateway" || endpoint != "http://configured-gateway.example" || lifecycle != "active" || revision != 1 || legacy != nil || !bytes.Equal(sealed, blob) || !configured {
		t.Fatalf("unexpected configured gateway singleton=%d id=%s lifecycle=%s revision=%d configured=%v", singleton, gotID, lifecycle, revision, configured)
	}

	beforeCount := 0
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	if _, err := register(uuid.New(), "Second Gateway", "http://second-gateway.example", nil); err == nil {
		t.Fatal("current gateway singleton unexpectedly accepted a second gateway")
	}
	if _, err := register(configuredID, "Duplicate Gateway", "http://duplicate-gateway.example", blob); err == nil {
		t.Fatal("duplicate gateway identity unexpectedly accepted")
	}
	if _, err := register(uuid.New(), "Short Gateway", "http://short-gateway.example", []byte{1}); err == nil {
		t.Fatal("short sealed gateway credential unexpectedly accepted")
	}
	var afterCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&afterCount); err != nil {
		t.Fatal(err)
	}
	if afterCount != beforeCount {
		t.Fatalf("failed gateway registrations changed row count from %d to %d", beforeCount, afterCount)
	}
}

func TestStage0Migration51GatewayEditLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	register := func(blob []byte) uuid.UUID {
		id := uuid.New()
		var created uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT public.control_register_gateway_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::bytea)`, id, "Edit Gateway", "http://edit-gateway.example", blob).Scan(&created); err != nil {
			t.Fatal(err)
		}
		return created
	}
	retire := func(id uuid.UUID, admin uuid.UUID) {
		boundary := time.Date(2026, 9, 18, 15, 0, 0, 0, time.UTC)
		if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET singleton_id=NULL,lifecycle_status='retired',retired_at=$2,retired_by=$3,retire_reason='administrator_retire',revision=revision+1,updated_at=$2 WHERE instance_id=$1`, id, boundary, admin); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(id uuid.UUID, revision int64, display, endpoint, action string, blob []byte) error {
		_, err := database.runtime.Exec(ctx, `SELECT public.control_edit_gateway_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, id, revision, display, endpoint, action, blob)
		return err
	}
	blobA := bytes.Repeat([]byte{0xA6}, 29)
	blobB := bytes.Repeat([]byte{0xB6}, 29)
	admin := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'stage0-gateway-edit-admin','Stage 0 Gateway Edit Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}

	t.Run("keep", func(t *testing.T) {
		id := register(blobA)
		if err := edit(id, 1, "Edit Gateway 2", "http://edit-gateway-2.example", "keep", nil); err != nil {
			t.Fatal(err)
		}
		assertGatewayEditState(t, ctx, database, id, 2, "Edit Gateway 2", "http://edit-gateway-2.example", blobA, true)
		retire(id, admin)
	})
	t.Run("set", func(t *testing.T) {
		id := register(nil)
		if err := edit(id, 1, "Edit Gateway Set", "http://edit-gateway-set.example", "set", blobB); err != nil {
			t.Fatal(err)
		}
		assertGatewayEditState(t, ctx, database, id, 2, "Edit Gateway Set", "http://edit-gateway-set.example", blobB, true)
		retire(id, admin)
	})
	t.Run("clear and invalid combinations", func(t *testing.T) {
		id := register(blobA)
		if err := edit(id, 1, "Edit Gateway Clear", "http://edit-gateway-clear.example", "clear", nil); err != nil {
			t.Fatal(err)
		}
		assertGatewayEditState(t, ctx, database, id, 2, "Edit Gateway Clear", "http://edit-gateway-clear.example", nil, false)
		retire(id, admin)
	})
	t.Run("negative fences", func(t *testing.T) {
		id := register(blobA)
		for _, tc := range []struct {
			action string
			blob   []byte
		}{
			{action: "other"},
			{action: "keep", blob: blobB},
			{action: "clear", blob: blobB},
			{action: "set"},
			{action: "set", blob: []byte{1}},
		} {
			if err := edit(id, 1, "Invalid", "http://invalid-gateway.example", tc.action, tc.blob); err == nil {
				t.Fatalf("action=%s unexpectedly succeeded", tc.action)
			}
		}
		if err := edit(id, 99, "Stale", "http://stale-gateway.example", "keep", nil); err == nil {
			t.Fatal("stale gateway edit unexpectedly succeeded")
		} else {
			requirePostgresCode(t, err, "P0005")
		}
		retire(id, admin)
		if err := edit(id, 1, "Retired", "http://retired-gateway.example", "clear", nil); err == nil {
			t.Fatal("retired gateway edit unexpectedly succeeded")
		} else {
			requirePostgresCode(t, err, "P0003")
		}
		overflowID := register(blobA)
		if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET revision=9223372036854775807 WHERE instance_id=$1`, overflowID); err != nil {
			t.Fatal(err)
		}
		if err := edit(overflowID, 9223372036854775807, "Overflow", "http://overflow-gateway.example", "keep", nil); err == nil {
			t.Fatal("overflow gateway edit unexpectedly succeeded")
		} else {
			requirePostgresCode(t, err, "P0004")
		}
		assertGatewayEditState(t, ctx, database, overflowID, 9223372036854775807, "Edit Gateway", "http://edit-gateway.example", blobA, true)
	})
}

func assertGatewayEditState(t *testing.T, ctx context.Context, database *isolatedJobDatabase, id uuid.UUID, revision int, display, endpoint string, expected []byte, configured bool) {
	t.Helper()
	var gotRevision int
	var gotDisplay, gotEndpoint, lifecycle string
	var got []byte
	var gotConfigured bool
	var legacy *string
	var singleton *int16
	if err := database.owner.QueryRow(ctx, `SELECT singleton_id,revision,display_name,management_endpoint,lifecycle_status,directory_credential_sealed,reader_secret_configured,reader_secret_ref FROM gateway_instances WHERE instance_id=$1`, id).Scan(&singleton, &gotRevision, &gotDisplay, &gotEndpoint, &lifecycle, &got, &gotConfigured, &legacy); err != nil {
		t.Fatal(err)
	}
	if singleton == nil || *singleton != 1 || lifecycle != "active" || gotRevision != revision || gotDisplay != display || gotEndpoint != endpoint || gotConfigured != configured || !bytes.Equal(got, expected) || legacy != nil {
		t.Fatalf("unexpected gateway edit state revision=%d display=%q endpoint=%q lifecycle=%s configured=%v", gotRevision, gotDisplay, gotEndpoint, lifecycle, gotConfigured)
	}
}

func TestStage0Migration51GatewayRetireLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'stage0-gateway-retire-admin','Stage 0 Gateway Retire Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	register := func(blob []byte) uuid.UUID {
		id := uuid.New()
		var created uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT public.control_register_gateway_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::bytea)`, id, "Retire Gateway", "http://retire-gateway.example", blob).Scan(&created); err != nil {
			t.Fatal(err)
		}
		return created
	}
	retire := func(id uuid.UUID, revision int64, boundary time.Time) error {
		_, err := database.runtime.Exec(ctx, `SELECT public.control_retire_gateway_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::text)`, id, revision, boundary, admin, "administrator_retire")
		return err
	}
	blob := bytes.Repeat([]byte{0xA7}, 29)
	firstID := register(blob)
	if err := retire(firstID, 99, time.Date(2026, 9, 18, 16, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("stale gateway retire unexpectedly succeeded")
	}
	assertGatewayRetireState(t, ctx, database, firstID, 1, "active", 1, blob, true, true)

	boundary := time.Date(2026, 9, 18, 16, 1, 0, 123000000, time.UTC)
	if err := retire(firstID, 1, boundary); err != nil {
		t.Fatal(err)
	}
	var singleton *int16
	var lifecycle string
	var revision int
	var sealed []byte
	var configured bool
	var retiredAt time.Time
	var retiredBy uuid.UUID
	var reason string
	var updatedAt time.Time
	var legacy *string
	if err := database.owner.QueryRow(ctx, `SELECT singleton_id,lifecycle_status,revision,directory_credential_sealed,reader_secret_configured,retired_at,retired_by,retire_reason,updated_at,reader_secret_ref FROM gateway_instances WHERE instance_id=$1`, firstID).Scan(&singleton, &lifecycle, &revision, &sealed, &configured, &retiredAt, &retiredBy, &reason, &updatedAt, &legacy); err != nil {
		t.Fatal(err)
	}
	if singleton != nil || lifecycle != "retired" || revision != 2 || sealed != nil || configured || !retiredAt.Equal(boundary) || retiredBy != admin || reason != "administrator_retire" || !updatedAt.Equal(boundary) || legacy != nil {
		t.Fatalf("unexpected retired gateway state lifecycle=%s revision=%d configured=%v", lifecycle, revision, configured)
	}
	if err := retire(firstID, 2, boundary.Add(time.Minute)); err == nil {
		t.Fatal("already-retired/non-current gateway retire unexpectedly succeeded")
	} else {
		requirePostgresCode(t, err, "P0003")
	}
	assertGatewayRetireState(t, ctx, database, firstID, 1, "retired", 2, nil, false, false)
	if err := retire(uuid.New(), 1, boundary); err == nil {
		t.Fatal("missing gateway retire unexpectedly succeeded")
	} else {
		requirePostgresCode(t, err, "P0002")
	}

	overflowID := register(blob)
	if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET revision=9223372036854775807 WHERE instance_id=$1`, overflowID); err != nil {
		t.Fatal(err)
	}
	if err := retire(overflowID, 9223372036854775807, boundary.Add(2*time.Minute)); err == nil {
		t.Fatal("overflow gateway retire unexpectedly succeeded")
	} else {
		requirePostgresCode(t, err, "P0004")
	}
	assertGatewayRetireState(t, ctx, database, overflowID, 1, "active", 9223372036854775807, blob, true, true)
}

func assertGatewayRetireState(t *testing.T, ctx context.Context, database *isolatedJobDatabase, id uuid.UUID, expectedSingleton int, expectedLifecycle string, expectedRevision int, expected []byte, configured, current bool) {
	t.Helper()
	var singleton *int16
	var lifecycle string
	var revision int
	var sealed []byte
	var gotConfigured bool
	if err := database.owner.QueryRow(ctx, `SELECT singleton_id,lifecycle_status,revision,directory_credential_sealed,reader_secret_configured FROM gateway_instances WHERE instance_id=$1`, id).Scan(&singleton, &lifecycle, &revision, &sealed, &gotConfigured); err != nil {
		t.Fatal(err)
	}
	if lifecycle != expectedLifecycle || revision != expectedRevision || gotConfigured != configured || !bytes.Equal(sealed, expected) || (current && (singleton == nil || int(*singleton) != expectedSingleton)) || (!current && singleton != nil) {
		t.Fatalf("unexpected gateway retire state lifecycle=%s revision=%d configured=%v current=%v", lifecycle, revision, gotConfigured, singleton != nil)
	}
}

func TestStage0Migration51GatewayReplaceLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'stage0-gateway-replace-admin','Stage 0 Gateway Replace Admin','enabled',clock_timestamp())`, admin); err != nil {
		t.Fatal(err)
	}
	register := func(blob []byte) uuid.UUID {
		id := uuid.New()
		var created uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT public.control_register_gateway_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::bytea)`, id, "Replace Gateway", "http://replace-gateway.example", blob).Scan(&created); err != nil {
			t.Fatal(err)
		}
		return created
	}
	retire := func(id uuid.UUID) {
		boundary := time.Date(2026, 9, 18, 17, 0, 0, 0, time.UTC)
		if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET singleton_id=NULL,lifecycle_status='retired',retired_at=$2,retired_by=$3,retire_reason='administrator_retire',revision=revision+1,updated_at=$2 WHERE instance_id=$1`, id, boundary, admin); err != nil {
			t.Fatal(err)
		}
	}
	replace := func(oldID uuid.UUID, revision int64, boundary time.Time, newID uuid.UUID, display, endpoint string, blob []byte) error {
		_, err := database.runtime.Exec(ctx, `SELECT public.control_replace_gateway_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::uuid,$6::text,$7::text,$8::bytea)`, oldID, revision, boundary, admin, newID, display, endpoint, blob)
		return err
	}
	blobA := bytes.Repeat([]byte{0xA8}, 29)
	blobB := bytes.Repeat([]byte{0xB8}, 29)

	oldUnconfigured := register(blobA)
	newUnconfigured := uuid.New()
	boundary := time.Date(2026, 9, 18, 17, 1, 0, 123000000, time.UTC)
	var returned uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT public.control_replace_gateway_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::uuid,$6::text,$7::text,$8::bytea)`, oldUnconfigured, 1, boundary, admin, newUnconfigured, "Replacement Gateway", "http://replacement-gateway.example", nil).Scan(&returned); err != nil {
		t.Fatal(err)
	}
	if returned != newUnconfigured {
		t.Fatalf("replacement id=%s want=%s", returned, newUnconfigured)
	}
	assertGatewayReplaceState(t, ctx, database, oldUnconfigured, 0, "retired", 2, nil, false, false)
	assertGatewayReplaceState(t, ctx, database, newUnconfigured, 1, "active", 1, nil, false, true)
	retire(newUnconfigured)

	oldConfigured := register(blobA)
	newConfigured := uuid.New()
	boundary = time.Date(2026, 9, 18, 17, 2, 0, 0, time.UTC)
	if err := replace(oldConfigured, 1, boundary, newConfigured, "Configured Replacement Gateway", "http://configured-replacement-gateway.example", blobB); err != nil {
		t.Fatal(err)
	}
	assertGatewayReplaceState(t, ctx, database, oldConfigured, 0, "retired", 2, nil, false, false)
	assertGatewayReplaceState(t, ctx, database, newConfigured, 1, "active", 1, blobB, true, true)
	if bytes.Equal(blobA, blobB) {
		t.Fatal("replacement test blobs must be distinct")
	}

	invalid := []struct {
		name     string
		oldID    uuid.UUID
		revision int64
		newID    uuid.UUID
		blob     []byte
	}{
		{name: "stale revision", oldID: newConfigured, revision: 99, newID: uuid.New()},
		{name: "retired predecessor", oldID: oldConfigured, revision: 1, newID: uuid.New()},
		{name: "non-current predecessor", oldID: oldUnconfigured, revision: 1, newID: uuid.New()},
		{name: "same identity", oldID: newConfigured, revision: 1, newID: newConfigured},
		{name: "existing replacement identity", oldID: newConfigured, revision: 1, newID: oldConfigured},
		{name: "invalid sealed blob", oldID: newConfigured, revision: 1, newID: uuid.New(), blob: []byte{1}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := replace(tc.oldID, tc.revision, boundary.Add(time.Minute), tc.newID, "Invalid Replacement", "http://invalid-replacement-gateway.example", tc.blob); err == nil {
				t.Fatal("invalid gateway replacement unexpectedly succeeded")
			}
		})
	}
	if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET revision=9223372036854775807 WHERE instance_id=$1`, newConfigured); err != nil {
		t.Fatal(err)
	}
	if err := replace(newConfigured, 9223372036854775807, boundary.Add(2*time.Minute), uuid.New(), "Overflow Replacement", "http://overflow-replacement-gateway.example", nil); err == nil {
		t.Fatal("overflow gateway replacement unexpectedly succeeded")
	}
	assertGatewayReplaceState(t, ctx, database, newConfigured, 1, "active", 9223372036854775807, blobB, true, true)
}

func assertGatewayReplaceState(t *testing.T, ctx context.Context, database *isolatedJobDatabase, id uuid.UUID, expectedSingleton int16, lifecycle string, revision int, expected []byte, configured, current bool) {
	t.Helper()
	var singleton *int16
	var gotLifecycle string
	var gotRevision int
	var sealed []byte
	var gotConfigured bool
	var legacy *string
	if err := database.owner.QueryRow(ctx, `SELECT singleton_id,lifecycle_status,revision,directory_credential_sealed,reader_secret_configured,reader_secret_ref FROM gateway_instances WHERE instance_id=$1`, id).Scan(&singleton, &gotLifecycle, &gotRevision, &sealed, &gotConfigured, &legacy); err != nil {
		t.Fatal(err)
	}
	if gotLifecycle != lifecycle || gotRevision != revision || gotConfigured != configured || !bytes.Equal(sealed, expected) || legacy != nil || (current && (singleton == nil || *singleton != expectedSingleton)) || (!current && singleton != nil) {
		t.Fatalf("unexpected gateway replacement state lifecycle=%s revision=%d configured=%v current=%v", gotLifecycle, gotRevision, gotConfigured, singleton != nil)
	}
}

func TestStage0Migration51MutationSurfaceACL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	standalone := []string{
		"public.control_set_node_management_credential_sealed_v1(uuid,bytea)",
		"public.control_clear_node_management_credential_sealed_v1(uuid)",
		"public.control_set_gateway_directory_credential_sealed_v1(uuid,bytea)",
		"public.control_clear_gateway_directory_credential_sealed_v1(uuid)",
	}
	for _, signature := range standalone {
		t.Run("standalone "+signature, func(t *testing.T) {
			var present, runtimeExecute, publicExecute, registrarExecute bool
			if err := database.owner.QueryRow(ctx, `SELECT
				to_regprocedure($1) IS NOT NULL,
				CASE WHEN to_regprocedure($1) IS NULL THEN false ELSE has_function_privilege('relay_control_runtime', to_regprocedure($1), 'EXECUTE') END,
				CASE WHEN to_regprocedure($1) IS NULL THEN false ELSE has_function_privilege('public', to_regprocedure($1), 'EXECUTE') END,
				CASE WHEN to_regprocedure($1) IS NULL THEN false ELSE has_function_privilege('relay_control_asset_registrar', to_regprocedure($1), 'EXECUTE') END`, signature).Scan(&present, &runtimeExecute, &publicExecute, &registrarExecute); err != nil {
				t.Fatal(err)
			}
			if present && (runtimeExecute || publicExecute || registrarExecute) {
				t.Fatalf("standalone surface remains present=%v runtime=%v public=%v registrar=%v", present, runtimeExecute, publicExecute, registrarExecute)
			}
		})
	}
	fixed := []string{
		"public.control_register_relay_node_asset_stage0_v1(uuid,text,text,text,text,bytea,text[])",
		"public.control_edit_relay_node_asset_stage0_v1(uuid,bigint,text,text,text,bytea)",
		"public.control_retire_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text,timestamptz)",
		"public.control_replace_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,text,text,bytea,text[])",
		"public.control_register_gateway_asset_stage0_v1(uuid,text,text,bytea)",
		"public.control_edit_gateway_asset_stage0_v1(uuid,bigint,text,text,text,bytea)",
		"public.control_retire_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text)",
		"public.control_replace_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,bytea)",
	}
	for _, signature := range fixed {
		t.Run("fixed "+signature, func(t *testing.T) {
			var present, runtimeExecute, publicExecute, registrarExecute bool
			if err := database.owner.QueryRow(ctx, `SELECT
				to_regprocedure($1) IS NOT NULL,
				CASE WHEN to_regprocedure($1) IS NULL THEN false ELSE has_function_privilege('relay_control_runtime', to_regprocedure($1), 'EXECUTE') END,
				CASE WHEN to_regprocedure($1) IS NULL THEN false ELSE has_function_privilege('public', to_regprocedure($1), 'EXECUTE') END,
				CASE WHEN to_regprocedure($1) IS NULL THEN false ELSE has_function_privilege('relay_control_asset_registrar', to_regprocedure($1), 'EXECUTE') END`, signature).Scan(&present, &runtimeExecute, &publicExecute, &registrarExecute); err != nil {
				t.Fatal(err)
			}
			if !present || !runtimeExecute || publicExecute || registrarExecute {
				t.Fatalf("fixed surface present=%v runtime=%v public=%v registrar=%v", present, runtimeExecute, publicExecute, registrarExecute)
			}
		})
	}
}

// TestStage0Migration51ProtectedStateAndCommitment exercises the Gate 2
// database boundary without using the current generated application queries.
func TestStage0Migration51ProtectedStateAndCommitment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}

	var version, floor int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT max(version_id) FROM goose_db_version WHERE is_applied),
		(SELECT phase6_evidence_floor FROM control_runtime_compatibility WHERE singleton_id=1)`).Scan(&version, &floor); err != nil {
		t.Fatal(err)
	}
	if version != 51 || floor != 4 {
		t.Fatalf("migration/floor=%d/%d", version, floor)
	}

	var columns int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND ((table_name='relay_node_assets' AND column_name='management_credential_sealed') OR (table_name='gateway_instances' AND column_name='directory_credential_sealed'))`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 2 {
		t.Fatalf("protected columns=%d", columns)
	}

	var canReadNode, canWriteNode, canReadGateway, canWriteGateway bool
	if err := database.owner.QueryRow(ctx, `SELECT
		has_column_privilege('relay_control_runtime', 'relay_node_assets', 'management_credential_sealed', 'SELECT'),
		has_column_privilege('relay_control_runtime', 'relay_node_assets', 'management_credential_sealed', 'UPDATE'),
		has_column_privilege('relay_control_runtime', 'gateway_instances', 'directory_credential_sealed', 'SELECT'),
		has_column_privilege('relay_control_runtime', 'gateway_instances', 'directory_credential_sealed', 'UPDATE')`).Scan(
		&canReadNode, &canWriteNode, &canReadGateway, &canWriteGateway); err != nil {
		t.Fatal(err)
	}
	if canReadNode || canWriteNode || canReadGateway || canWriteGateway {
		t.Fatalf("protected direct privileges node read/write=%v/%v gateway read/write=%v/%v", canReadNode, canWriteNode, canReadGateway, canWriteGateway)
	}

	var canReadProjection, canWriteProjection bool
	if err := database.owner.QueryRow(ctx, `SELECT
		has_column_privilege('relay_control_runtime', 'relay_node_assets', 'reader_secret_configured', 'SELECT'),
		has_column_privilege('relay_control_runtime', 'relay_node_assets', 'reader_secret_configured', 'UPDATE')`).Scan(&canReadProjection, &canWriteProjection); err != nil {
		t.Fatal(err)
	}
	if !canReadProjection || canWriteProjection {
		t.Fatalf("projection privileges read/write=%v/%v", canReadProjection, canWriteProjection)
	}

	key := make([]byte, 32)
	key[0] = 1
	status := ""
	if err := database.owner.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, key).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "initialized" {
		t.Fatalf("initialization status=%s", status)
	}
	if err := database.owner.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, key).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "available" {
		t.Fatalf("repeat status=%s", status)
	}
	wrong := make([]byte, 32)
	wrong[0] = 2
	if err := database.owner.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, wrong).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "unavailable" {
		t.Fatalf("mismatch status=%s", status)
	}

	var commitmentRows int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM control_asset_credential_key_identity`).Scan(&commitmentRows); err != nil {
		t.Fatal(err)
	}
	if commitmentRows != 1 {
		t.Fatalf("commitment rows=%d", commitmentRows)
	}
	for name, statement := range map[string]string{
		"update":   `UPDATE control_asset_credential_key_identity SET k2_identity_commitment = decode(repeat('00', 32), 'hex')`,
		"delete":   `DELETE FROM control_asset_credential_key_identity`,
		"truncate": `TRUNCATE control_asset_credential_key_identity`,
	} {
		t.Run("commitment_"+name, func(t *testing.T) {
			_, err := database.owner.Exec(ctx, statement)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestStage0Migration51RejectsShortSealedStateAndMarksConfigured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}

	nodeID := "00000000-0000-0000-0000-000000000051"
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers
		(node_type, driver_contract_version, display_name)
		VALUES ('stage0-driver', 'v1', 'Stage 0 test driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets
		(instance_id, display_name, node_type, driver_contract_version, management_endpoint, reader_secret_ref)
		VALUES ($1::uuid, 'stage0 node', 'stage0-driver', 'v1', 'http://stage0-node.invalid', NULL)`, nodeID); err != nil {
		t.Fatal(err)
	}
	valid := make([]byte, 29)
	if _, err := database.owner.Exec(ctx, `UPDATE relay_node_assets
		SET management_credential_sealed=$1, revision=revision+1, updated_at=clock_timestamp()
		WHERE instance_id=$2::uuid`, valid, nodeID); err != nil {
		t.Fatal(err)
	}
	var configured bool
	if err := database.owner.QueryRow(ctx, `SELECT reader_secret_configured FROM relay_node_assets WHERE instance_id=$1::uuid`, nodeID).Scan(&configured); err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("sealed state did not set secret_configured")
	}
	_, err := database.owner.Exec(ctx, `UPDATE relay_node_assets
		SET management_credential_sealed=$1, revision=revision+1, updated_at=clock_timestamp()
		WHERE instance_id=$2::uuid`, make([]byte, 28), nodeID)
	requirePostgresCode(t, err, "23514")
}

func TestStage0Migration51GatewayProjectionAndProtectedACL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	instanceID := "00000000-0000-0000-0000-000000000053"
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_instances
		(singleton_id, instance_id, display_name, management_endpoint, reader_secret_ref)
		VALUES (1, $1::uuid, 'stage0 gateway', 'http://stage0-gateway.invalid', NULL)`, instanceID); err != nil {
		t.Fatal(err)
	}
	var configured bool
	if err := database.owner.QueryRow(ctx, `SELECT reader_secret_configured FROM gateway_instances WHERE singleton_id=1`).Scan(&configured); err != nil {
		t.Fatal(err)
	}
	if configured {
		t.Fatal("NULL gateway sealed state was reported as configured")
	}
	if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET directory_credential_sealed=$1 WHERE singleton_id=1`, make([]byte, 29)); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT reader_secret_configured FROM gateway_instances WHERE singleton_id=1`).Scan(&configured); err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("non-NULL gateway sealed state was not reported as configured")
	}

	for _, statement := range []string{
		`SELECT management_credential_sealed FROM relay_node_assets`,
		`SELECT directory_credential_sealed FROM gateway_instances`,
		`UPDATE relay_node_assets SET management_credential_sealed=NULL`,
		`UPDATE gateway_instances SET directory_credential_sealed=NULL`,
	} {
		_, err := database.runtime.Exec(ctx, statement)
		requirePostgresCode(t, err, "42501")
	}
	var nodeSealed []byte
	if err := database.runtime.QueryRow(ctx, `SELECT public.control_read_node_management_credential_sealed_v1($1::uuid)`, instanceID).Scan(&nodeSealed); err != nil {
		t.Fatalf("narrow node read failed: %v", err)
	}
}

func TestStage0Migration51AbsentCommitmentWithSealedStateFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	nodeID := "00000000-0000-0000-0000-000000000052"
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers
		(node_type, driver_contract_version, display_name)
		VALUES ('stage0-driver', 'v1', 'Stage 0 test driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets
		(instance_id, display_name, node_type, driver_contract_version, management_endpoint, reader_secret_ref, management_credential_sealed)
		VALUES ($1::uuid, 'sealed node', 'stage0-driver', 'v1', 'http://sealed-node.invalid', NULL, $2)`, nodeID, make([]byte, 29)); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	key[0] = 7
	var status string
	if err := database.owner.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, key).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "invalid_database_state" {
		t.Fatalf("status=%s", status)
	}
	var commitments int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM control_asset_credential_key_identity`).Scan(&commitments); err != nil {
		t.Fatal(err)
	}
	if commitments != 0 {
		t.Fatalf("invalid state wrote %d commitments", commitments)
	}
}

func TestStage0Migration51RejectsLegacyGatewayReferenceBeforeDDL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_instances
		(singleton_id, instance_id, display_name, management_endpoint, reader_secret_ref)
		VALUES (1, gen_random_uuid(), 'legacy gateway', 'http://legacy-gateway.invalid', 'docker-secret://stage0/legacy')`); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err == nil {
		t.Fatal("migration accepted a non-null legacy reference")
	}
	var version int
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 50 {
		t.Fatalf("failed migration changed version to %d", version)
	}
	var protectedColumns int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND column_name IN ('management_credential_sealed','directory_credential_sealed')`).Scan(&protectedColumns); err != nil {
		t.Fatal(err)
	}
	if protectedColumns != 0 {
		t.Fatalf("failed migration left protected columns=%d", protectedColumns)
	}
}

func TestStage0Migration51RejectsLegacyNodeReferenceBeforeDDL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "50")
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers
		(node_type, driver_contract_version, display_name)
		VALUES ('legacy-stage0-driver', 'v1', 'Legacy Stage 0 test driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets
		(instance_id, display_name, node_type, driver_contract_version, management_endpoint, reader_secret_ref)
		VALUES (gen_random_uuid(), 'legacy node', 'legacy-stage0-driver', 'v1', 'http://legacy-node.invalid', 'docker-secret://stage0/legacy')`); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err == nil {
		t.Fatal("migration accepted a non-null legacy reference")
	}
	var version int
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 50 {
		t.Fatalf("failed migration changed version to %d", version)
	}
}

func TestStage0Migration51CommitmentInitializationRace(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		otherKey   byte
		wantSecond string
	}{
		{name: "same key", otherKey: 1, wantSecond: "available"},
		{name: "different key", otherKey: 2, wantSecond: "unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			database := newIsolatedJobDatabase(t, "up-to", "50")
			if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
				t.Fatal(err)
			}
			keyA := make([]byte, 32)
			keyB := make([]byte, 32)
			keyA[0], keyB[0] = 1, testCase.otherKey
			start := make(chan struct{})
			results := make(chan string, 2)
			var group sync.WaitGroup
			for _, key := range [][]byte{keyA, keyB} {
				group.Add(1)
				go func(key []byte) {
					defer group.Done()
					<-start
					var status string
					if err := database.owner.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, key).Scan(&status); err != nil {
						results <- "error"
						return
					}
					results <- status
				}(key)
			}
			close(start)
			group.Wait()
			close(results)
			counts := map[string]int{}
			for status := range results {
				counts[status]++
			}
			if counts["initialized"] != 1 || counts[testCase.wantSecond] != 1 || counts["error"] != 0 {
				t.Fatalf("initialization outcomes=%v", counts)
			}
			var rows int
			if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM control_asset_credential_key_identity`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if rows != 1 {
				t.Fatalf("commitment rows=%d", rows)
			}
		})
	}
}
