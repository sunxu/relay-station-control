package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

var (
	ErrMonitoringDisableFenceConflict = errors.New("store: monitoring disable fence conflict")
	ErrMonitoringTargetRetired        = errors.New("store: monitoring target retired")
	ErrMonitoringFutureConflict       = errors.New("store: monitoring future conflict")
	ErrMonitoringBoundaryConflict     = errors.New("store: monitoring boundary conflict")
	ErrMonitoringStateConflict        = errors.New("store: monitoring state conflict")
	ErrNodeGenerationExhausted        = errors.New("store: node generation exhausted")
)

const (
	nodeMonitoringEnableKind  = "node.monitoring_enable"
	nodeMonitoringDisableKind = "node.monitoring_disable"
)

type NodeMonitoringCommand struct {
	CommandID    uuid.UUID
	ActorAdminID uuid.UUID
	RequestID    string
	InstanceID   uuid.UUID
}

type NodeMonitoringRepository struct{ pool *pgxpool.Pool }

type nodeMonitoringDBResult struct {
	ActivationID   *uuid.UUID `json:"activation_id"`
	Boundary       time.Time  `json:"boundary"`
	ClosedCount    int64      `json:"closed_count"`
	CancelledCount int64      `json:"cancelled_count"`
	CurrentChanged bool       `json:"current_changed"`
}

func NewNodeMonitoringRepository(pool *pgxpool.Pool) (*NodeMonitoringRepository, error) {
	if pool == nil {
		return nil, errors.New("store: node monitoring database unavailable")
	}
	return &NodeMonitoringRepository{pool: pool}, nil
}

// AuthorizeNodeProbe snapshots the fixed, secret-free probe target in a short
// read-only transaction. Probe authorization never reads or returns the Node's
// Reader Secret reference; the probe Driver does not need credentials.
func (r *NodeMonitoringRepository) AuthorizeNodeProbe(ctx context.Context, instanceID uuid.UUID) (drivers.NodeTarget, error) {
	if instanceID == uuid.Nil {
		return drivers.NodeTarget{}, ErrInvalidNode
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return drivers.NodeTarget{}, err
	}
	defer tx.Rollback(ctx)
	var lifecycle, nodeType, contract, endpoint string
	var capabilities []string
	err = tx.QueryRow(ctx, `
		SELECT instance_id,lifecycle_status,node_type,driver_contract_version,
		       management_endpoint,capabilities
		FROM public.control_authorize_node_probe_v1($1)`, instanceID).Scan(
		&instanceID, &lifecycle, &nodeType, &contract, &endpoint, &capabilities)
	if errors.Is(err, pgx.ErrNoRows) {
		return drivers.NodeTarget{}, ErrNodeNotFound
	}
	if err != nil {
		return drivers.NodeTarget{}, err
	}
	if lifecycle != "active" {
		return drivers.NodeTarget{}, ErrNodeRetired
	}
	if nodeType != string(drivers.NodeTypeCLIProxyAPI) ||
		contract != string(drivers.DriverContractCLIProxyAPIAuthFilesV1) {
		return drivers.NodeTarget{}, drivers.ErrDriverContractMismatch
	}
	declared := make([]drivers.Capability, 0, len(capabilities))
	healthCap := false
	for _, capability := range capabilities {
		value := drivers.Capability(capability)
		declared = append(declared, value)
		if value == drivers.CapabilityManagementHealthRead {
			healthCap = true
		}
	}
	if !healthCap {
		return drivers.NodeTarget{}, drivers.ErrCapabilityUnsupported
	}
	if err = tx.Commit(ctx); err != nil {
		return drivers.NodeTarget{}, err
	}
	return drivers.NodeTarget{
		InstanceID: instanceID, NodeType: drivers.NodeType(nodeType),
		DriverContractVersion: drivers.DriverContractVersion(contract),
		ManagementEndpoint:    endpoint, Capabilities: declared,
	}, nil
}

func (r *NodeMonitoringRepository) RecordNodeProbe(
	ctx context.Context,
	actor, instanceID uuid.UUID,
	requestID, action string,
	details map[string]any,
) error {
	if actor == uuid.Nil || instanceID == uuid.Nil || requestID == "" ||
		(action != "node.health" && action != "node.connection_test") || len(details) != 4 ||
		details["instance_id"] != instanceID.String() {
		return ErrInvalidNode
	}
	result, resultOK := details["result"].(string)
	reason, reasonOK := details["reason"].(string)
	latency, latencyOK := details["latency_ms"].(int64)
	if !resultOK || !reasonOK || !latencyOK ||
		(result != "success" && result != "failure") ||
		latency < 0 || latency > 30000 || !validNodeProbeReason(reason) {
		return ErrInvalidNode
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	auditResult := "failure"
	if result == "success" {
		auditResult = "success"
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO public.audit_logs(
			audit_id,category,action,result,actor_admin_id,request_id,details)
		VALUES($1,'asset_node',$2,$3,$4,$5,$6)`,
		uuid.New(), action, auditResult, actor, requestID, encoded)
	return err
}

func validNodeProbeReason(reason string) bool {
	switch reason {
	case "none", "http_status", "response_invalid", "response_too_large", "timeout",
		"cancelled", "network_unavailable", "dns_rejected", "tls_rejected",
		"redirect_rejected", "target_rejected":
		return true
	default:
		return false
	}
}

func (r *NodeMonitoringRepository) Enable(ctx context.Context, c NodeMonitoringCommand) (NodeCommandResult, error) {
	return r.mutate(ctx, c, true)
}

func (r *NodeMonitoringRepository) Disable(ctx context.Context, c NodeMonitoringCommand) (NodeCommandResult, error) {
	return r.mutate(ctx, c, false)
}

func (r *NodeMonitoringRepository) mutate(ctx context.Context, c NodeMonitoringCommand, enable bool) (NodeCommandResult, error) {
	kind, reason := nodeMonitoringDisableKind, "administrator_disable"
	if enable {
		kind, reason = nodeMonitoringEnableKind, "administrator_enable"
	}
	if c.CommandID == uuid.Nil || c.ActorAdminID == uuid.Nil || c.InstanceID == uuid.Nil {
		return NodeCommandResult{}, ErrInvalidNode
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return NodeCommandResult{}, err
	}
	defer tx.Rollback(ctx)
	keySum := sha256.Sum256(c.CommandID[:])
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(keySum[:8]))); err != nil {
		return NodeCommandResult{}, err
	}

	var actor uuid.UUID
	var storedKind string
	var encodingVersion int16
	var storedHash, storedResult []byte
	var storedStatus int16
	var storedKeyVersion *int16
	err = tx.QueryRow(ctx, `SELECT actor_admin_id,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,sanitized_result,response_status FROM public.asset_admin_command_receipts WHERE command_id=$1`, c.CommandID).Scan(&actor, &storedKind, &encodingVersion, &storedHash, &storedKeyVersion, &storedResult, &storedStatus)
	if err == nil {
		if actor != c.ActorAdminID || storedKind != kind {
			return NodeCommandResult{}, ErrCommandConflict
		}
		if encodingVersion != 1 {
			return NodeCommandResult{}, ErrReceiptEncodingUnknown
		}
		if storedKeyVersion != nil {
			return NodeCommandResult{}, ErrReceiptKeyUnavailable
		}
		intent, intentErr := nodeMonitoringIntent(kind, c.InstanceID, reason)
		if intentErr != nil {
			return NodeCommandResult{}, intentErr
		}
		intentHash := sha256.Sum256(intent)
		if !equalBytes(storedHash, intentHash[:]) {
			return NodeCommandResult{}, ErrCommandConflict
		}
		return NodeCommandResult{HTTPStatus: int(storedStatus), Body: append(json.RawMessage(nil), storedResult...), Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NodeCommandResult{}, err
	}
	intent, intentErr := nodeMonitoringIntent(kind, c.InstanceID, reason)
	if intentErr != nil {
		return NodeCommandResult{}, intentErr
	}
	intentHash := sha256.Sum256(intent)
	node, err := lockNode(ctx, tx, c.InstanceID)
	if err != nil {
		return NodeCommandResult{}, translateNodeMonitoringDBError(err)
	}
	if node.LifecycleStatus != "active" {
		return NodeCommandResult{}, ErrMonitoringTargetRetired
	}
	var fence uuid.UUID
	err = tx.QueryRow(ctx, `SELECT command_id FROM public.asset_admin_command_receipts WHERE command_kind='node.monitoring_disable' AND sanitized_result->>'instance_id'=$1 ORDER BY committed_at DESC LIMIT 1`, c.InstanceID.String()).Scan(&fence)
	if errors.Is(err, pgx.ErrNoRows) {
		fence = uuid.Nil
	} else if err != nil {
		return NodeCommandResult{}, err
	}
	var rawDBResult []byte
	if err = tx.QueryRow(ctx, `SELECT public.control_set_node_inventory_monitoring($1,$2,NULL,$3,$4,$5)`, c.InstanceID, enable, reason, c.ActorAdminID.String(), monitoringNullableUUID(fence)).Scan(&rawDBResult); err != nil {
		return NodeCommandResult{}, translateNodeMonitoringDBError(err)
	}
	var dbResult nodeMonitoringDBResult
	if err = json.Unmarshal(rawDBResult, &dbResult); err != nil || dbResult.Boundary.IsZero() ||
		dbResult.ClosedCount < 0 || dbResult.CancelledCount < 0 {
		return NodeCommandResult{}, errors.New("store: monitoring result is invalid")
	}
	projection, err := loadNodeMutationProjection(ctx, tx, c.InstanceID, dbResult.Boundary)
	if err != nil {
		return NodeCommandResult{}, err
	}
	result := "enabled"
	if enable && !dbResult.CurrentChanged {
		result = "already_enabled"
	}
	closedCount, cancelledCount := dbResult.ClosedCount, dbResult.CancelledCount
	transition := enable && dbResult.CurrentChanged
	if !enable {
		transition = closedCount != 0 || cancelledCount != 0
		if !transition {
			result, transition = "already_disabled", false
		} else {
			result = "disabled"
		}
	}
	monitoringActive := projection.Monitoring.Current
	var activationID *uuid.UUID
	var effectiveFrom, effectiveTo *time.Time
	if enable && monitoringActive {
		activationID = dbResult.ActivationID
		effectiveFrom = projection.Monitoring.EffectiveFrom
		effectiveTo = projection.Monitoring.EffectiveTo
	}
	body, err := json.Marshal(map[string]any{
		"result":                            result,
		"instance_id":                       c.InstanceID,
		"lifecycle_status":                  projection.LifecycleStatus,
		"revision":                          strconv.FormatInt(projection.Revision, 10),
		"boundary":                          dbResult.Boundary.UTC(),
		"monitoring_active":                 monitoringActive,
		"monitoring_activation_id":          activationID,
		"effective_from":                    effectiveFrom,
		"effective_to":                      effectiveTo,
		"closed_monitoring_count":           closedCount,
		"cancelled_future_monitoring_count": cancelledCount,
	})
	if err != nil {
		return NodeCommandResult{}, err
	}
	if transition {
		details, marshalErr := json.Marshal(map[string]any{"command_id": c.CommandID, "instance_id": c.InstanceID, "closed_monitoring_count": closedCount, "cancelled_future_monitoring_count": cancelledCount})
		if marshalErr != nil {
			return NodeCommandResult{}, marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details) VALUES($1,'asset_node',$2,'success',$3,$4,$5)`, uuid.New(), kind, c.ActorAdminID, c.RequestID, details); err != nil {
			return NodeCommandResult{}, err
		}
	}
	if enable {
		_, err = tx.Exec(ctx, `INSERT INTO public.asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id,secret_fingerprint_key_version) VALUES($1,$2,1,$3,$4,200,$5,NULL)`, c.CommandID, kind, intentHash[:], body, c.ActorAdminID)
	} else {
		committedAt, timestampErr := nextDisableFenceTimestamp(ctx, tx, fence)
		if timestampErr != nil {
			return NodeCommandResult{}, timestampErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO public.asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id,committed_at,secret_fingerprint_key_version) VALUES($1,$2,1,$3,$4,200,$5,$6,NULL)`, c.CommandID, kind, intentHash[:], body, c.ActorAdminID, committedAt)
	}
	if err != nil {
		return NodeCommandResult{}, err
	}
	// JSONB is the immutable response source of truth. Read its persisted
	// representation before commit so the first response is byte-for-byte the
	// same representation returned by every replay.
	if err = tx.QueryRow(ctx, `SELECT sanitized_result FROM public.asset_admin_command_receipts WHERE command_id=$1`, c.CommandID).Scan(&body); err != nil {
		return NodeCommandResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NodeCommandResult{}, err
	}
	return NodeCommandResult{HTTPStatus: 200, Body: body}, nil
}

func nodeMonitoringIntent(kind string, id uuid.UUID, reason string) ([]byte, error) {
	if id == uuid.Nil || (kind != nodeMonitoringEnableKind && kind != nodeMonitoringDisableKind) {
		return nil, ErrInvalidNode
	}
	return encodeCanonicalIntent([]any{1, kind, id.String(), reason})
}

func nextDisableFenceTimestamp(ctx context.Context, tx pgx.Tx, previous uuid.UUID) (time.Time, error) {
	var previousAt *time.Time
	if previous != uuid.Nil {
		if err := tx.QueryRow(ctx, `SELECT committed_at FROM public.asset_admin_command_receipts WHERE command_id=$1`, previous).Scan(&previousAt); err != nil {
			return time.Time{}, err
		}
	}
	var next time.Time
	if err := tx.QueryRow(ctx, `SELECT CASE WHEN $1::timestamptz IS NULL THEN clock_timestamp() ELSE GREATEST(clock_timestamp(), $1::timestamptz + interval '1 microsecond') END`, previousAt).Scan(&next); err != nil {
		return time.Time{}, err
	}
	if previousAt != nil && !next.After(*previousAt) {
		return time.Time{}, errors.New("store: disable fence timestamp is not strictly increasing")
	}
	return next, nil
}

func monitoringNullableUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for i := range left {
		different |= left[i] ^ right[i]
	}
	return different == 0
}

func translateNodeMonitoringDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "55000" {
			return ErrMonitoringDisableFenceConflict
		}
		if pgErr.Code == "23505" && pgErr.Message == "future monitoring activation conflicts with immediate enable" {
			return ErrMonitoringFutureConflict
		}
		if pgErr.Code == "23P01" {
			return ErrMonitoringBoundaryConflict
		}
		if pgErr.Code == "23514" && pgErr.Message == "monitoring target is retired" {
			return ErrMonitoringTargetRetired
		}
		if pgErr.Code == "22003" {
			return ErrNodeGenerationExhausted
		}
	}
	return err
}
