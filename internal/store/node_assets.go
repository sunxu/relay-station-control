package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNodeNotFound        = errors.New("store: node not found")
	ErrNodeRetired         = errors.New("store: node retired")
	ErrNodeIdentityExists  = errors.New("store: node identity exists")
	ErrInvalidNode         = errors.New("store: invalid node")
	ErrInvalidNodeEndpoint = errors.New("store: invalid node endpoint")
	ErrInvalidNodeSecret   = errors.New("store: invalid node secret configuration")
)

type NodeReplacement struct {
	OldInstanceID uuid.UUID `json:"old_instance_id"`
	NewInstanceID uuid.UUID `json:"new_instance_id"`
	ReplacedAt    time.Time `json:"replaced_at"`
	ReplacedBy    uuid.UUID `json:"replaced_by"`
	CommandID     uuid.UUID `json:"command_id"`
}
type NodeAssetDetail struct {
	Asset       NodeAsset
	Predecessor *NodeReplacement
	Successor   *NodeReplacement
}

type NodeCommand struct {
	CommandID             uuid.UUID
	ActorAdminID          uuid.UUID
	RequestID             string
	InstanceID            uuid.UUID
	ExpectedRevision      int64
	NewInstanceID         uuid.UUID
	DisplayName           StringPatch
	ManagementEndpoint    StringPatch
	Secret                SecretPatch
	NodeType              string
	DriverContractVersion string
	Capabilities          []string
}

type NodeCommandResult = GatewayCommandResult

type NodeLifecycleRepository struct {
	pool *pgxpool.Pool
	key  []byte
}
type nodeIntentBuilder func(replay bool, receiptKeyVersion *int16) ([]byte, *int16, error)

func NewNodeLifecycleRepository(pool *pgxpool.Pool, key []byte) (*NodeLifecycleRepository, error) {
	if pool == nil {
		return nil, errors.New("store: node lifecycle database unavailable")
	}
	if len(key) != 0 && len(key) != 32 {
		return nil, ErrReceiptKeyUnavailable
	}
	return &NodeLifecycleRepository{pool: pool, key: append([]byte(nil), key...)}, nil
}

func (r *NodeLifecycleRepository) Detail(ctx context.Context, id uuid.UUID) (NodeAssetDetail, error) {
	var d NodeAssetDetail
	n, err := locklessNode(ctx, r.pool, id)
	if err != nil {
		return d, err
	}
	d.Asset = n
	d.Predecessor, err = r.lineage(ctx, `SELECT old_instance_id,new_instance_id,replaced_at,replaced_by,command_id FROM relay_node_asset_replacements WHERE new_instance_id=$1`, id)
	if err != nil {
		return d, err
	}
	d.Successor, err = r.lineage(ctx, `SELECT old_instance_id,new_instance_id,replaced_at,replaced_by,command_id FROM relay_node_asset_replacements WHERE old_instance_id=$1`, id)
	return d, err
}
func (r *NodeLifecycleRepository) lineage(ctx context.Context, q string, id uuid.UUID) (*NodeReplacement, error) {
	var v NodeReplacement
	err := r.pool.QueryRow(ctx, q, id).Scan(&v.OldInstanceID, &v.NewInstanceID, &v.ReplacedAt, &v.ReplacedBy, &v.CommandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

type nodeQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func locklessNode(ctx context.Context, q nodeQuerier, id uuid.UUID) (NodeAsset, error) {
	var readAt time.Time
	if err := q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&readAt); err != nil {
		return NodeAsset{}, err
	}
	return loadNodeMutationProjection(ctx, q, id, readAt)
}

func loadNodeMutationProjection(ctx context.Context, q nodeQuerier, id uuid.UUID, readAt time.Time) (NodeAsset, error) {
	var n NodeAsset
	err := q.QueryRow(ctx, `SELECT instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM relay_node_assets WHERE instance_id=$1`, id).Scan(nodeLifecycleScan(&n)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return n, ErrNodeNotFound
	}
	if err != nil {
		return n, err
	}
	rows, e := rRows(ctx, q, id)
	if e != nil {
		return n, e
	}
	n.Capabilities = rows
	if n.LifecycleStatus == "active" {
		var effectiveFrom time.Time
		var effectiveTo *time.Time
		err = q.QueryRow(ctx, `SELECT effective_from,effective_to FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1 AND cancelled_at IS NULL AND $2::timestamptz <@ active_range ORDER BY effective_from,monitoring_activation_id LIMIT 1`, id, readAt).Scan(&effectiveFrom, &effectiveTo)
		if err == nil {
			n.Monitoring = NodeMonitoring{Current: true, Active: true, EffectiveFrom: &effectiveFrom, EffectiveTo: effectiveTo}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return n, err
		}
	}
	return n, nil
}
func rRows(ctx context.Context, q nodeQuerier, id uuid.UUID) ([]string, error) {
	rows, e := q.Query(ctx, `SELECT capability FROM node_capabilities WHERE instance_id=$1 ORDER BY capability`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *NodeLifecycleRepository) Register(ctx context.Context, c NodeCommand) (NodeCommandResult, error) {
	builder := func(replay bool, kv *int16) ([]byte, *int16, error) {
		if c.CommandID == uuid.Nil || c.ActorAdminID == uuid.Nil || c.NewInstanceID == uuid.Nil || !validNodeDisplay(c.DisplayName) || !c.ManagementEndpoint.Present || !validNodeIdentifier(c.NodeType) || !validNodeIdentifier(c.DriverContractVersion) {
			return nil, nil, ErrInvalidNode
		}
		endpoint, err := normalizeNodeEndpoint(c.ManagementEndpoint.Value)
		if err != nil {
			return nil, nil, err
		}
		c.ManagementEndpoint.Value = endpoint
		var capabilitiesErr error
		c.Capabilities, capabilitiesErr = canonicalCapabilities(c.Capabilities)
		if capabilitiesErr != nil {
			return nil, nil, ErrInvalidNode
		}
		return r.intent("node.register", c, replay, kv)
	}
	return r.transact(ctx, c, "node.register", builder, func(tx pgx.Tx) (int, any, error) {
		if err := validateDriverCapabilities(ctx, tx, c.NodeType, c.DriverContractVersion, c.Capabilities); err != nil {
			return 0, nil, err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM relay_node_assets WHERE instance_id=$1)`, c.NewInstanceID).Scan(&exists); err != nil {
			return 0, nil, err
		}
		if exists {
			return 0, nil, ErrNodeIdentityExists
		}
		var created uuid.UUID
		err := tx.QueryRow(ctx, `SELECT control_create_relay_node_asset($1,$2,$3,$4,$5,$6,$7)`, c.NewInstanceID, c.DisplayName.Value, c.NodeType, c.DriverContractVersion, c.ManagementEndpoint.Value, secretValue(c.Secret), c.Capabilities).Scan(&created)
		if err != nil {
			if uniqueViolation(err) {
				return 0, nil, ErrNodeIdentityExists
			}
			return 0, nil, err
		}
		if err = bumpNodeGeneration(ctx, tx); err != nil {
			return 0, nil, err
		}
		var readAt time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&readAt); err != nil {
			return 0, nil, err
		}
		n, err := loadNodeMutationProjection(ctx, tx, created, readAt)
		if err != nil {
			return 0, nil, err
		}
		body := nodeMutationBody("registered", n, 0, 0, 0)
		return 201, body, r.insertAudit(ctx, tx, c, "node.register", map[string]any{"command_id": c.CommandID, "instance_id": n.InstanceID, "new_revision": n.Revision})
	})
}

func (r *NodeLifecycleRepository) Edit(ctx context.Context, c NodeCommand) (NodeCommandResult, error) {
	builder := func(replay bool, kv *int16) ([]byte, *int16, error) {
		if c.CommandID == uuid.Nil || c.ActorAdminID == uuid.Nil || c.InstanceID == uuid.Nil || c.ExpectedRevision < 1 || (!c.DisplayName.Present && !c.ManagementEndpoint.Present && c.Secret.Operation == SecretAbsent) {
			return nil, nil, ErrInvalidNode
		}
		if c.DisplayName.Present && !validNodeDisplay(c.DisplayName) {
			return nil, nil, ErrInvalidNode
		}
		if c.ManagementEndpoint.Present {
			v, e := normalizeNodeEndpoint(c.ManagementEndpoint.Value)
			if e != nil {
				return nil, nil, e
			}
			c.ManagementEndpoint.Value = v
		}
		return r.intent("node.edit", c, replay, kv)
	}
	return r.transact(ctx, c, "node.edit", builder, func(tx pgx.Tx) (int, any, error) {
		n, err := lockNode(ctx, tx, c.InstanceID)
		if err != nil {
			return 0, nil, err
		}
		if n.LifecycleStatus != "active" {
			return 0, nil, ErrNodeRetired
		}
		if n.Revision != c.ExpectedRevision {
			return 0, nil, ErrStaleAssetRevision
		}
		if n.Revision == maxAssetRevision {
			return 0, nil, ErrAssetRevisionExhausted
		}
		display, endpoint := n.DisplayName, n.ManagementEndpoint
		if c.DisplayName.Present {
			display = c.DisplayName.Value
		}
		if c.ManagementEndpoint.Present {
			endpoint = c.ManagementEndpoint.Value
		}
		query := `UPDATE relay_node_assets SET display_name=$2,management_endpoint=$3,revision=revision+1,updated_at=clock_timestamp() WHERE instance_id=$1`
		args := []any{c.InstanceID, display, endpoint}
		if c.Secret.Operation == SecretClear {
			query = strings.Replace(query, "revision=revision+1", "reader_secret_ref=NULL,revision=revision+1", 1)
		} else if c.Secret.Operation == SecretSet {
			query = strings.Replace(query, "revision=revision+1", "reader_secret_ref=$4,revision=revision+1", 1)
			args = append(args, c.Secret.Value)
		}
		if _, err = tx.Exec(ctx, query, args...); err != nil {
			return 0, nil, err
		}
		if err = bumpNodeGeneration(ctx, tx); err != nil {
			return 0, nil, err
		}
		var readAt time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&readAt); err != nil {
			return 0, nil, err
		}
		out, err := loadNodeMutationProjection(ctx, tx, c.InstanceID, readAt)
		if err != nil {
			return 0, nil, err
		}
		body := nodeMutationBody("updated", out, 0, 0, 0)
		return 200, body, r.insertAudit(ctx, tx, c, "node.edit", map[string]any{"command_id": c.CommandID, "instance_id": out.InstanceID, "old_revision": n.Revision, "new_revision": out.Revision})
	})
}

func (r *NodeLifecycleRepository) Retire(ctx context.Context, c NodeCommand) (NodeCommandResult, error) {
	return r.retireOrReplace(ctx, c, false)
}
func (r *NodeLifecycleRepository) Replace(ctx context.Context, c NodeCommand) (NodeCommandResult, error) {
	return r.retireOrReplace(ctx, c, true)
}

func (r *NodeLifecycleRepository) retireOrReplace(ctx context.Context, c NodeCommand, replace bool) (NodeCommandResult, error) {
	kind := "node.retire"
	if replace {
		kind = "node.replace"
	}
	builder := func(replay bool, kv *int16) ([]byte, *int16, error) {
		if c.CommandID == uuid.Nil || c.ActorAdminID == uuid.Nil || c.InstanceID == uuid.Nil || c.ExpectedRevision < 1 {
			return nil, nil, ErrInvalidNode
		}
		if replace {
			if c.NewInstanceID == uuid.Nil || c.NewInstanceID == c.InstanceID || !validNodeDisplay(c.DisplayName) || !c.ManagementEndpoint.Present || !validNodeIdentifier(c.NodeType) || !validNodeIdentifier(c.DriverContractVersion) {
				return nil, nil, ErrInvalidNode
			}
			v, e := normalizeNodeEndpoint(c.ManagementEndpoint.Value)
			if e != nil {
				return nil, nil, e
			}
			c.ManagementEndpoint.Value = v
			var capabilitiesErr error
			c.Capabilities, capabilitiesErr = canonicalCapabilities(c.Capabilities)
			if capabilitiesErr != nil {
				return nil, nil, ErrInvalidNode
			}
		}
		return r.intent(kind, c, replay, kv)
	}
	return r.transact(ctx, c, kind, builder, func(tx pgx.Tx) (int, any, error) {
		old, err := lockNode(ctx, tx, c.InstanceID)
		if err != nil {
			return 0, nil, err
		}
		if old.LifecycleStatus != "active" {
			return 0, nil, ErrNodeRetired
		}
		if old.Revision != c.ExpectedRevision {
			return 0, nil, ErrStaleAssetRevision
		}
		if old.Revision == maxAssetRevision {
			return 0, nil, ErrAssetRevisionExhausted
		}
		if replace {
			if err = validateDriverCapabilities(ctx, tx, c.NodeType, c.DriverContractVersion, c.Capabilities); err != nil {
				return 0, nil, err
			}
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM relay_node_assets WHERE instance_id=$1)`, c.NewInstanceID).Scan(&exists); err != nil {
				return 0, nil, err
			}
			if exists {
				return 0, nil, ErrNodeIdentityExists
			}
		}
		rows, err := tx.Query(ctx, `SELECT monitoring_activation_id FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1 AND cancelled_at IS NULL AND (effective_to IS NULL OR effective_to>clock_timestamp()) ORDER BY effective_from,monitoring_activation_id FOR UPDATE`, c.InstanceID)
		if err != nil {
			return 0, nil, err
		}
		for rows.Next() {
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return 0, nil, err
		}
		rows.Close()
		bindingRows, err := tx.Query(ctx, `SELECT binding_id FROM relay_node_gateway_account_bindings WHERE relay_node_id=$1 AND ended_at IS NULL ORDER BY binding_id FOR UPDATE`, c.InstanceID)
		if err != nil {
			return 0, nil, err
		}
		for bindingRows.Next() {
		}
		if err = bindingRows.Err(); err != nil {
			bindingRows.Close()
			return 0, nil, err
		}
		bindingRows.Close()
		var boundary time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&boundary); err != nil {
			return 0, nil, err
		}
		reason := "node_retired"
		retireReason := "administrator_retire"
		if replace {
			reason = "node_replaced"
			retireReason = "replacement"
		}
		closedMon, err := tx.Exec(ctx, `UPDATE relay_node_inventory_monitoring_activations SET effective_to=$2,end_reason=$3,end_actor=$4,end_recorded_at=$2 WHERE instance_id=$1 AND cancelled_at IS NULL AND effective_from<$2 AND (effective_to IS NULL OR effective_to>$2)`, c.InstanceID, boundary, reason, c.ActorAdminID.String())
		if err != nil {
			return 0, nil, err
		}
		cancelled, err := tx.Exec(ctx, `UPDATE relay_node_inventory_monitoring_activations SET cancelled_at=$2,cancelled_by=$3,cancel_reason=$4 WHERE instance_id=$1 AND cancelled_at IS NULL AND effective_from>=$2`, c.InstanceID, boundary, c.ActorAdminID, reason)
		if err != nil {
			return 0, nil, err
		}
		closedBinding, err := tx.Exec(ctx, `UPDATE relay_node_gateway_account_bindings SET ended_at=$2,ended_by=$3,end_reason=$4 WHERE relay_node_id=$1 AND ended_at IS NULL`, c.InstanceID, boundary, c.ActorAdminID, reason)
		if err != nil {
			return 0, nil, err
		}
		_, err = tx.Exec(ctx, `SELECT control_abandon_node_inventory_poll_runs($1,$2,$3)`, c.InstanceID, boundary, reason)
		if err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE relay_node_assets SET lifecycle_status='retired',revision=revision+1,retired_at=$2,retired_by=$3,retire_reason=$4,updated_at=$2 WHERE instance_id=$1`, c.InstanceID, boundary, c.ActorAdminID, retireReason); err != nil {
			return 0, nil, err
		}
		if err = bumpNodeGeneration(ctx, tx); err != nil {
			return 0, nil, err
		}
		retired, err := loadNodeMutationProjection(ctx, tx, c.InstanceID, boundary)
		if err != nil {
			return 0, nil, err
		}
		bc, mc, fc := closedBinding.RowsAffected(), closedMon.RowsAffected(), cancelled.RowsAffected()
		if !replace {
			body := nodeMutationBody("retired", retired, bc, mc, fc)
			return 200, body, r.insertAudit(ctx, tx, c, kind, map[string]any{"command_id": c.CommandID, "instance_id": retired.InstanceID, "reason_code": "administrator_retire", "old_revision": old.Revision, "new_revision": retired.Revision, "closed_binding_count": bc, "closed_monitoring_count": mc, "cancelled_future_monitoring_count": fc})
		}
		var created uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT control_create_relay_node_asset($1,$2,$3,$4,$5,$6,$7)`, c.NewInstanceID, c.DisplayName.Value, c.NodeType, c.DriverContractVersion, c.ManagementEndpoint.Value, secretValue(c.Secret), c.Capabilities).Scan(&created); err != nil {
			return 0, nil, err
		}
		fresh, err := loadNodeMutationProjection(ctx, tx, created, boundary)
		if err != nil {
			return 0, nil, err
		}
		lineage := NodeReplacement{c.InstanceID, c.NewInstanceID, boundary.UTC(), c.ActorAdminID, c.CommandID}
		if _, err = tx.Exec(ctx, `INSERT INTO relay_node_asset_replacements(old_instance_id,new_instance_id,replaced_at,replaced_by,command_id) VALUES($1,$2,$3,$4,$5)`, lineage.OldInstanceID, lineage.NewInstanceID, lineage.ReplacedAt, lineage.ReplacedBy, lineage.CommandID); err != nil {
			return 0, nil, err
		}
		body := map[string]any{"result": "replaced", "old_asset": retired, "new_asset": fresh, "closed_binding_count": bc, "closed_monitoring_count": mc, "cancelled_future_monitoring_count": fc, "lineage": lineage}
		return 200, body, r.insertAudit(ctx, tx, c, kind, map[string]any{"command_id": c.CommandID, "old_instance_id": retired.InstanceID, "new_instance_id": fresh.InstanceID, "reason_code": "replacement", "old_revision": old.Revision, "new_revision": fresh.Revision, "closed_binding_count": bc, "closed_monitoring_count": mc, "cancelled_future_monitoring_count": fc})
	})
}

func (r *NodeLifecycleRepository) transact(ctx context.Context, c NodeCommand, kind string, b nodeIntentBuilder, apply func(pgx.Tx) (int, any, error)) (NodeCommandResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return NodeCommandResult{}, err
	}
	defer tx.Rollback(ctx)
	sum := sha256.Sum256(c.CommandID[:])
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(sum[:8]))); err != nil {
		return NodeCommandResult{}, err
	}
	var actor uuid.UUID
	var sk string
	var enc int16
	var hash, result []byte
	var status int16
	var kv *int16
	err = tx.QueryRow(ctx, `SELECT actor_admin_id,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,sanitized_result,response_status FROM asset_admin_command_receipts WHERE command_id=$1`, c.CommandID).Scan(&actor, &sk, &enc, &hash, &kv, &result, &status)
	if err == nil {
		if actor != c.ActorAdminID || sk != kind {
			return NodeCommandResult{}, ErrCommandConflict
		}
		if enc != 1 {
			return NodeCommandResult{}, ErrReceiptEncodingUnknown
		}
		if kv != nil && (*kv != 1 || len(r.key) != 32) {
			return NodeCommandResult{}, ErrReceiptKeyUnavailable
		}
		intent, _, e := b(true, kv)
		if e != nil {
			return NodeCommandResult{}, e
		}
		sum := sha256.Sum256(intent)
		if !hmac.Equal(hash, sum[:]) {
			return NodeCommandResult{}, ErrCommandConflict
		}
		return NodeCommandResult{HTTPStatus: int(status), Body: append(json.RawMessage(nil), result...), Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NodeCommandResult{}, err
	}
	intent, keyVersion, err := b(false, nil)
	if err != nil {
		return NodeCommandResult{}, err
	}
	sum = sha256.Sum256(intent)
	statusCode, body, err := apply(tx)
	if err != nil {
		return NodeCommandResult{}, translateNodeDBError(err)
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return NodeCommandResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id,secret_fingerprint_key_version) VALUES($1,$2,1,$3,$4,$5,$6,$7)`, c.CommandID, kind, sum[:], bodyBytes, statusCode, c.ActorAdminID, keyVersion)
	if err != nil {
		return NodeCommandResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NodeCommandResult{}, err
	}
	return NodeCommandResult{HTTPStatus: statusCode, Body: bodyBytes}, nil
}

func (r *NodeLifecycleRepository) intent(kind string, c NodeCommand, replay bool, receiptKV *int16) ([]byte, *int16, error) {
	triplet := []any{"absent", nil, nil}
	var kv *int16
	switch c.Secret.Operation {
	case SecretAbsent:
	case SecretClear:
		triplet = []any{"clear", nil, nil}
	case SecretSet:
		if !ValidAssetSecretReference(c.Secret.Value) {
			return nil, nil, ErrInvalidNodeSecret
		}
		if replay && receiptKV == nil {
			return nil, nil, ErrCommandConflict
		}
		if len(r.key) != 32 {
			return nil, nil, ErrReceiptKeyUnavailable
		}
		v := int16(1)
		kv = &v
		mac := hmac.New(sha256.New, r.key)
		mac.Write([]byte(assetIntentDomain))
		mac.Write([]byte(kind))
		mac.Write([]byte{0})
		mac.Write([]byte(c.Secret.Value))
		triplet = []any{"set", 1, hex.EncodeToString(mac.Sum(nil))}
	default:
		return nil, nil, ErrInvalidNodeSecret
	}
	if replay && receiptKV != nil && *receiptKV != 1 {
		return nil, nil, ErrReceiptKeyUnavailable
	}
	caps := make([]any, len(c.Capabilities))
	for i, v := range c.Capabilities {
		caps[i] = v
	}
	var value any
	switch kind {
	case "node.register":
		value = []any{1, kind, c.NewInstanceID.String(), c.DisplayName.Value, c.ManagementEndpoint.Value, c.NodeType, c.DriverContractVersion, caps, triplet}
	case "node.edit":
		value = []any{1, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), patchPair(c.DisplayName), patchPair(c.ManagementEndpoint), triplet}
	case "node.retire":
		value = []any{1, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), "administrator_retire"}
	case "node.replace":
		value = []any{1, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), c.NewInstanceID.String(), c.DisplayName.Value, c.ManagementEndpoint.Value, c.NodeType, c.DriverContractVersion, caps, triplet, "replacement"}
	default:
		return nil, nil, ErrInvalidNode
	}
	out, err := encodeCanonicalIntent(value)
	return out, kv, err
}

func lockNode(ctx context.Context, tx pgx.Tx, id uuid.UUID) (NodeAsset, error) {
	var n NodeAsset
	err := tx.QueryRow(ctx, `SELECT instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM relay_node_assets WHERE instance_id=$1 FOR UPDATE`, id).Scan(nodeLifecycleScan(&n)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return n, ErrNodeNotFound
	}
	if err == nil {
		rows, e := tx.Query(ctx, `SELECT capability FROM node_capabilities WHERE instance_id=$1 ORDER BY capability`, id)
		if e != nil {
			return n, e
		}
		for rows.Next() {
			var c string
			if e = rows.Scan(&c); e != nil {
				rows.Close()
				return n, e
			}
			n.Capabilities = append(n.Capabilities, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return n, e
		}
	}
	return n, err
}
func nodeLifecycleScan(n *NodeAsset) []any {
	return []any{&n.InstanceID, &n.DisplayName, &n.NodeType, &n.DriverContractVersion, &n.ManagementEndpoint, &n.SecretConfigured, &n.LifecycleStatus, &n.Revision, &n.CreatedAt, &n.UpdatedAt, &n.RetiredAt, &n.RetiredBy, &n.RetireReason}
}
func canonicalCapabilities(v []string) ([]string, error) {
	if len(v) == 0 {
		return nil, ErrInvalidNode
	}
	out := append([]string(nil), v...)
	sort.Strings(out)
	for i, s := range out {
		if !validNodeIdentifier(s) || (i > 0 && s == out[i-1]) {
			return nil, ErrInvalidNode
		}
	}
	return out, nil
}
func validNodeIdentifier(v string) bool {
	return len(v) >= 1 && len(v) <= 64 && strings.TrimSpace(v) == v
}
func validNodeDisplay(p StringPatch) bool {
	return p.Present && len(p.Value) >= 1 && len(p.Value) <= 100 && strings.TrimSpace(p.Value) == p.Value
}
func normalizeNodeEndpoint(v string) (string, error) {
	if strings.TrimSpace(v) != v || len(v) < 8 || len(v) > 2048 || strings.ContainsAny(v, "\r\n\t ?#@") {
		return "", ErrInvalidNodeEndpoint
	}
	parsed, err := url.Parse(v)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrInvalidNodeEndpoint
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.Contains(host, "%") {
		return "", ErrInvalidNodeEndpoint
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else if !validEndpointHostname(host) {
		return "", ErrInvalidNodeEndpoint
	}
	port := parsed.Port()
	if port != "" {
		portNumber, portErr := strconv.Atoi(port)
		if portErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", ErrInvalidNodeEndpoint
		}
		if (strings.EqualFold(parsed.Scheme, "http") && portNumber == 80) ||
			(strings.EqualFold(parsed.Scheme, "https") && portNumber == 443) {
			port = ""
		}
	}
	escapedPath := parsed.EscapedPath()
	var normalizedPath strings.Builder
	for index := 0; index < len(escapedPath); index++ {
		if escapedPath[index] == '\\' {
			return "", ErrInvalidNodeEndpoint
		}
		if escapedPath[index] != '%' {
			normalizedPath.WriteByte(escapedPath[index])
			continue
		}
		if index+2 >= len(escapedPath) {
			return "", ErrInvalidNodeEndpoint
		}
		octet := strings.ToUpper(escapedPath[index+1 : index+3])
		decoded, decodeErr := strconv.ParseUint(octet, 16, 8)
		if decodeErr != nil || decoded <= 0x1f || decoded == 0x7f || decoded == '/' || decoded == '\\' {
			return "", ErrInvalidNodeEndpoint
		}
		normalizedPath.WriteByte('%')
		normalizedPath.WriteString(octet)
		index += 2
	}
	rawPath := normalizedPath.String()
	for _, segment := range strings.Split(strings.ReplaceAll(rawPath, "%2E", "."), "/") {
		if segment == "." || segment == ".." {
			return "", ErrInvalidNodeEndpoint
		}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	}
	parsed.RawPath = rawPath
	return parsed.String(), nil
}

func validEndpointHostname(host string) bool {
	if strings.Contains(host, "..") || !asciiAlphaNumeric(host[0]) || !asciiAlphaNumeric(host[len(host)-1]) {
		return false
	}
	for _, character := range host {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
func validateDriverCapabilities(ctx context.Context, tx pgx.Tx, nt, dv string, caps []string) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM driver_capabilities WHERE node_type=$1 AND driver_contract_version=$2 AND capability=ANY($3::text[])`, nt, dv, caps).Scan(&count); err != nil {
		return err
	}
	if count != len(caps) {
		return ErrInvalidNode
	}
	return nil
}
func bumpNodeGeneration(ctx context.Context, tx pgx.Tx) error {
	var generation int64
	if err := tx.QueryRow(ctx, `SELECT control_advance_asset_registry_node_generation()`).Scan(&generation); err != nil {
		return err
	}
	return nil
}
func nodeMutationBody(result string, n NodeAsset, b, m, f int64) map[string]any {
	return map[string]any{"result": result, "asset": n, "closed_binding_count": b, "closed_monitoring_count": m, "cancelled_future_monitoring_count": f}
}
func (r *NodeLifecycleRepository) insertAudit(ctx context.Context, tx pgx.Tx, c NodeCommand, action string, details map[string]any) error {
	b, e := json.Marshal(details)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details) VALUES($1,'asset_node',$2,'success',$3,$4,$5)`, uuid.New(), action, c.ActorAdminID, c.RequestID, b)
	return e
}
func translateNodeDBError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if p.Code == "23505" {
			return ErrNodeIdentityExists
		}
		if p.Code == "23514" {
			return ErrInvalidNode
		}
	}
	return err
}
