package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

type relayBindingSchemaFixture struct {
	gatewayID    uuid.UUID
	snapshotID   uuid.UUID
	adminID      uuid.UUID
	otherAdminID uuid.UUID
	accountIDs   []int64
}

func newRelayBindingSchemaFixture(t *testing.T, ctx context.Context, database *isolatedJobDatabase) relayBindingSchemaFixture {
	t.Helper()
	fixture := relayBindingSchemaFixture{
		gatewayID:    uuid.New(),
		snapshotID:   uuid.New(),
		adminID:      uuid.New(),
		otherAdminID: uuid.New(),
		accountIDs:   []int64{101, 102, 103, 104, 105, 106, 107, 108, 109, 110},
	}
	insertGatewayInstance(t, ctx, database.owner, fixture.gatewayID, "http://binding-gateway.test", "file://binding-reader")
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type, driver_contract_version, display_name
	) VALUES ('binding-node','v1','Binding Node')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(
		admin_id, login_name, display_name
	) VALUES
		($1,'binding_admin','Binding Admin'),
		($2,'binding_admin_two','Binding Admin Two')`,
		fixture.adminID, fixture.otherAdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
		snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count
	) VALUES ($1,$2,$3,1,$4)`,
		fixture.snapshotID, fixture.gatewayID, make([]byte, 32), len(fixture.accountIDs)); err != nil {
		t.Fatal(err)
	}
	for _, accountID := range fixture.accountIDs {
		if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1,$2,$3,'linux','apikey',NULL,'active')`,
			fixture.snapshotID, accountID, "Account"); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture relayBindingSchemaFixture) insertNode(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
) uuid.UUID {
	t.Helper()
	nodeID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id, display_name, node_type, driver_contract_version,
		management_endpoint, reader_secret_ref
	) VALUES ($1,'Binding Node','binding-node','v1',$2,NULL)`,
		nodeID, "https://node-"+nodeID.String()+".test"); err != nil {
		t.Fatal(err)
	}
	return nodeID
}

func (fixture relayBindingSchemaFixture) insertBinding(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
	nodeID uuid.UUID,
	accountID int64,
	bindReason string,
) uuid.UUID {
	t.Helper()
	var bindingID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO relay_node_gateway_account_bindings(
		relay_node_id, gateway_instance_id, gateway_account_id,
		evidence_snapshot_id, bound_by, bind_reason
	) VALUES ($1,$2,$3,$4,$5,$6)
	RETURNING binding_id`,
		nodeID, fixture.gatewayID, accountID, fixture.snapshotID, fixture.adminID, bindReason,
	).Scan(&bindingID); err != nil {
		t.Fatal(err)
	}
	return bindingID
}

func TestRelayNodeGatewayAccountBindingSchema(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, database)

	t.Run("current binding unique by node", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[0], "administrator_bind")
		_, err := database.owner.Exec(ctx, `INSERT INTO relay_node_gateway_account_bindings(
			relay_node_id, gateway_instance_id, gateway_account_id,
			evidence_snapshot_id, bound_by, bind_reason
		) VALUES ($1,$2,$3,$4,$5,'administrator_bind')`,
			nodeID, fixture.gatewayID, fixture.accountIDs[1], fixture.snapshotID, fixture.adminID)
		requireGatewayDirectorySQLState(t, err, "23505")
	})

	t.Run("current binding unique by account", func(t *testing.T) {
		firstNodeID := fixture.insertNode(t, ctx, database)
		secondNodeID := fixture.insertNode(t, ctx, database)
		fixture.insertBinding(t, ctx, database, firstNodeID, fixture.accountIDs[2], "administrator_bind")
		_, err := database.owner.Exec(ctx, `INSERT INTO relay_node_gateway_account_bindings(
			relay_node_id, gateway_instance_id, gateway_account_id,
			evidence_snapshot_id, bound_by, bind_reason
		) VALUES ($1,$2,$3,$4,$5,'administrator_bind')`,
			secondNodeID, fixture.gatewayID, fixture.accountIDs[2], fixture.snapshotID, fixture.adminID)
		requireGatewayDirectorySQLState(t, err, "23505")
	})

	t.Run("foreign keys and reasons", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		unusedAccountID := fixture.accountIDs[9]
		for name, statement := range map[string]string{
			"missing node": `INSERT INTO relay_node_gateway_account_bindings(
				relay_node_id,gateway_instance_id,gateway_account_id,evidence_snapshot_id,bound_by,bind_reason
			) VALUES ('00000000-0000-0000-0000-000000000001','` + fixture.gatewayID.String() + `',` + fmt.Sprint(unusedAccountID) + `,'` + fixture.snapshotID.String() + `','` + fixture.adminID.String() + `','administrator_bind')`,
			"missing gateway": `INSERT INTO relay_node_gateway_account_bindings(
				relay_node_id,gateway_instance_id,gateway_account_id,evidence_snapshot_id,bound_by,bind_reason
			) VALUES ('` + nodeID.String() + `','00000000-0000-0000-0000-000000000001',` + fmt.Sprint(unusedAccountID) + `,'` + fixture.snapshotID.String() + `','` + fixture.adminID.String() + `','administrator_bind')`,
			"missing admin": `INSERT INTO relay_node_gateway_account_bindings(
				relay_node_id,gateway_instance_id,gateway_account_id,evidence_snapshot_id,bound_by,bind_reason
			) VALUES ('` + nodeID.String() + `','` + fixture.gatewayID.String() + `',` + fmt.Sprint(unusedAccountID) + `,'` + fixture.snapshotID.String() + `','00000000-0000-0000-0000-000000000001','administrator_bind')`,
		} {
			t.Run(name, func(t *testing.T) {
				_, err := database.owner.Exec(ctx, statement)
				requireGatewayDirectorySQLState(t, err, "23503")
			})
		}
		_, err := database.owner.Exec(ctx, `INSERT INTO relay_node_gateway_account_bindings(
			relay_node_id,gateway_instance_id,gateway_account_id,evidence_snapshot_id,bound_by,bind_reason
		) VALUES ($1,$2,$3,$4,$5,'invalid_reason')`,
			nodeID, fixture.gatewayID, fixture.accountIDs[3], fixture.snapshotID, fixture.adminID)
		requireGatewayDirectorySQLState(t, err, "23514")
		_, err = database.owner.Exec(ctx, `INSERT INTO relay_node_gateway_account_bindings(
			relay_node_id,gateway_instance_id,gateway_account_id,evidence_snapshot_id,bound_by,bind_reason
		) VALUES ($1,$2,0,$3,$4,'administrator_bind')`,
			nodeID, fixture.gatewayID, fixture.snapshotID, fixture.adminID)
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("evidence account and gateway must match snapshot", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		otherSnapshotID := uuid.New()
		if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id,gateway_instance_id,fingerprint,schema_version,account_count
		) VALUES ($1,$2,$3,1,1)`,
			otherSnapshotID, fixture.gatewayID, append([]byte{1}, make([]byte, 31)...)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id,account_id,name,platform,type,url,status
		) VALUES ($1,999,'Other','linux','apikey',NULL,'active')`, otherSnapshotID); err != nil {
			t.Fatal(err)
		}
		_, err := database.owner.Exec(ctx, `INSERT INTO relay_node_gateway_account_bindings(
			relay_node_id,gateway_instance_id,gateway_account_id,evidence_snapshot_id,bound_by,bind_reason
		) VALUES ($1,$2,$3,$4,$5,'administrator_bind')`,
			nodeID, fixture.gatewayID, fixture.accountIDs[4], otherSnapshotID, fixture.adminID)
		requireGatewayDirectorySQLState(t, err, "23503")
	})

	t.Run("identity and bound metadata are immutable", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		bindingID := fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[5], "administrator_bind")
		_, err := database.owner.Exec(ctx, `UPDATE relay_node_gateway_account_bindings
			SET gateway_account_id=$2 WHERE binding_id=$1`, bindingID, fixture.accountIDs[6])
		requireGatewayDirectorySQLState(t, err, "23514")
		_, err = database.owner.Exec(ctx, `UPDATE relay_node_gateway_account_bindings
			SET bound_by=$2 WHERE binding_id=$1`, bindingID, fixture.otherAdminID)
		requireGatewayDirectorySQLState(t, err, "23514")
	})

	t.Run("close is complete and one time", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		bindingID := fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[6], "administrator_bind")
		_, err := database.owner.Exec(ctx, `UPDATE relay_node_gateway_account_bindings
			SET ended_at=clock_timestamp() WHERE binding_id=$1`, bindingID)
		requireGatewayDirectorySQLState(t, err, "23514")
		_, err = database.owner.Exec(ctx, `UPDATE relay_node_gateway_account_bindings
			SET ended_at=clock_timestamp(),ended_by='00000000-0000-0000-0000-000000000001',
			    end_reason='administrator_unbind'
			WHERE binding_id=$1`, bindingID)
		requireGatewayDirectorySQLState(t, err, "23503")
		_, err = database.owner.Exec(ctx, `UPDATE relay_node_gateway_account_bindings
			SET ended_at=clock_timestamp(),ended_by=$2,end_reason='invalid_reason'
			WHERE binding_id=$1`, bindingID, fixture.otherAdminID)
		requireGatewayDirectorySQLState(t, err, "23514")

		if _, err := database.owner.Exec(ctx, `UPDATE relay_node_gateway_account_bindings
			SET ended_at=clock_timestamp(),ended_by=$2,end_reason='administrator_unbind'
			WHERE binding_id=$1`, bindingID, fixture.otherAdminID); err != nil {
			t.Fatal(err)
		}
		_, err = database.owner.Exec(ctx, `UPDATE relay_node_gateway_account_bindings
			SET end_reason='administrator_rebind' WHERE binding_id=$1`, bindingID)
		requireGatewayDirectorySQLState(t, err, "23514")
		fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[6], "administrator_rebind")
	})

	t.Run("delete and truncate are rejected", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		bindingID := fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[7], "administrator_bind")
		_, err := database.owner.Exec(ctx, `DELETE FROM relay_node_gateway_account_bindings WHERE binding_id=$1`, bindingID)
		requireGatewayDirectorySQLState(t, err, "42501")
		_, err = database.owner.Exec(ctx, `TRUNCATE relay_node_gateway_account_bindings`)
		requireGatewayDirectorySQLState(t, err, "42501")
	})

	t.Run("node gateway and admin deletes are restricted", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[8], "administrator_bind")
		// ON DELETE RESTRICT raises restrict_violation (23001), not
		// foreign_key_violation (23503).
		_, err := database.owner.Exec(ctx, `DELETE FROM relay_node_assets WHERE instance_id=$1`, nodeID)
		requireGatewayDirectorySQLState(t, err, "23001")
		_, err = database.owner.Exec(ctx, `DELETE FROM gateway_instances WHERE instance_id=$1`, fixture.gatewayID)
		requireGatewayDirectorySQLState(t, err, "23001")
		_, err = database.owner.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id=$1`, fixture.adminID)
		requireGatewayDirectorySQLState(t, err, "23001")
	})
}

func TestRelayBindingAuditShape(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, database)
	nodeID := fixture.insertNode(t, ctx, database)

	details := func(oldAccountID, newAccountID any, reason string) []byte {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"relay_node_id":          nodeID,
			"gateway_instance_id":    fixture.gatewayID,
			"old_gateway_account_id": oldAccountID,
			"new_gateway_account_id": newAccountID,
			"evidence_snapshot_id":   fixture.snapshotID,
			"reason_code":            reason,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	insertAudit := func(action string, encoded []byte) error {
		_, err := database.owner.Exec(ctx, `INSERT INTO audit_logs(
			category,action,result,actor_admin_id,request_id,details
		) VALUES ('relay_binding',$1,'success',$2,$3,$4::jsonb)`,
			action, fixture.adminID, "binding-"+uuid.NewString(), encoded)
		return err
	}

	for name, testCase := range map[string]struct {
		action  string
		details []byte
	}{
		"bind": {
			action:  "relay_binding.bind",
			details: details(nil, fixture.accountIDs[0], "administrator_bind"),
		},
		"unbind": {
			action:  "relay_binding.unbind",
			details: details(fixture.accountIDs[0], nil, "administrator_unbind"),
		},
		"rebind": {
			action:  "relay_binding.rebind",
			details: details(fixture.accountIDs[0], fixture.accountIDs[1], "administrator_rebind"),
		},
		"bigint max": {
			action:  "relay_binding.bind",
			details: details(nil, int64(9223372036854775807), "administrator_bind"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := insertAudit(testCase.action, testCase.details); err != nil {
				t.Fatal(err)
			}
		})
	}

	extra := map[string]any{}
	if err := json.Unmarshal(details(nil, fixture.accountIDs[0], "administrator_bind"), &extra); err != nil {
		t.Fatal(err)
	}
	extra["raw_url"] = "https://secret.invalid/?token=canary"
	extraEncoded, err := json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	missingKey := map[string]any{}
	if err := json.Unmarshal(details(nil, fixture.accountIDs[0], "administrator_bind"), &missingKey); err != nil {
		t.Fatal(err)
	}
	delete(missingKey, "evidence_snapshot_id")
	missingKeyEncoded, err := json.Marshal(missingKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, testCase := range map[string]struct {
		category string
		action   string
		details  []byte
	}{
		"extra key": {
			category: "relay_binding",
			action:   "relay_binding.bind",
			details:  extraEncoded,
		},
		"missing key": {
			category: "relay_binding",
			action:   "relay_binding.bind",
			details:  missingKeyEncoded,
		},
		"wrong null shape": {
			category: "relay_binding",
			action:   "relay_binding.bind",
			details:  details(fixture.accountIDs[0], fixture.accountIDs[1], "administrator_bind"),
		},
		"wrong reason": {
			category: "relay_binding",
			action:   "relay_binding.unbind",
			details:  details(fixture.accountIDs[0], nil, "administrator_rebind"),
		},
		"invalid action": {
			category: "relay_binding",
			action:   "relay_binding.disable",
			details:  details(fixture.accountIDs[0], nil, "administrator_unbind"),
		},
		"binding action under existing category": {
			category: "authorization",
			action:   "relay_binding.bind",
			details:  details(nil, fixture.accountIDs[0], "administrator_bind"),
		},
		"account above bigint max": {
			category: "relay_binding",
			action:   "relay_binding.bind",
			details:  details(nil, json.Number("9223372036854775808"), "administrator_bind"),
		},
		"zero account": {
			category: "relay_binding",
			action:   "relay_binding.bind",
			details:  details(nil, 0, "administrator_bind"),
		},
		"fractional account": {
			category: "relay_binding",
			action:   "relay_binding.bind",
			details:  details(nil, 1.5, "administrator_bind"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := database.owner.Exec(ctx, `INSERT INTO audit_logs(
				category,action,result,actor_admin_id,request_id,details
			) VALUES ($1,$2,'success',$3,$4,$5::jsonb)`,
				testCase.category, testCase.action, fixture.adminID,
				"binding-reject-"+uuid.NewString(), testCase.details)
			requireGatewayDirectorySQLState(t, err, "23514")
		})
	}

	if _, err := database.owner.Exec(ctx, `INSERT INTO audit_logs(
		category,action,result,actor_admin_id,request_id,details
	) VALUES ('authorization','authorization.check','success',$1,$2,'{}'::jsonb)`,
		fixture.adminID, "existing-"+uuid.NewString()); err != nil {
		t.Fatalf("existing audit category changed: %v", err)
	}
}
