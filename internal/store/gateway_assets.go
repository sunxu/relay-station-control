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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers/gatewaymanagement"
)

var (
	ErrGatewayNotFound        = errors.New("store: gateway not found")
	ErrGatewayRetired         = errors.New("store: gateway retired")
	ErrCurrentGatewayExists   = errors.New("store: current gateway exists")
	ErrGatewayIdentityExists  = errors.New("store: gateway identity exists")
	ErrStaleAssetRevision     = errors.New("store: stale asset revision")
	ErrAssetRevisionExhausted = errors.New("store: asset revision exhausted")
	ErrCommandConflict        = errors.New("store: command conflict")
	ErrInvalidGateway         = errors.New("store: invalid gateway")
	ErrInvalidGatewayEndpoint = errors.New("store: invalid gateway endpoint")
	ErrInvalidGatewaySecret   = errors.New("store: invalid gateway secret configuration")
	ErrReceiptKeyUnavailable  = errors.New("store: receipt key unavailable")
	ErrReceiptEncodingUnknown = errors.New("store: receipt encoding unavailable")
)

const (
	gatewayLifecycleActive  = "active"
	gatewayLifecycleRetired = "retired"
	assetIntentDomain       = "relay-station/asset-admin-intent/v1/"
	maxAssetRevision        = int64(^uint64(0) >> 1)
)

type GatewayReplacement struct {
	OldInstanceID uuid.UUID `json:"old_instance_id"`
	NewInstanceID uuid.UUID `json:"new_instance_id"`
	ReplacedAt    time.Time `json:"replaced_at"`
	ReplacedBy    uuid.UUID `json:"replaced_by"`
	CommandID     uuid.UUID `json:"command_id"`
}

type GatewayAssetPage struct {
	Items      []GatewayAsset
	HasMore    bool
	Generation string
	Counts     GatewayCounts
}

type GatewayAssetDetail struct {
	Asset       GatewayAsset
	Predecessor *GatewayReplacement
	Successor   *GatewayReplacement
}

type GatewayCounts struct{ Active, Retired, Total int64 }

type SecretOperation string

const (
	SecretAbsent SecretOperation = "absent"
	SecretClear  SecretOperation = "clear"
	SecretSet    SecretOperation = "set"
)

type SecretPatch struct {
	Operation SecretOperation
	Value     string
}

type StringPatch struct {
	Present bool
	Value   string
}

type GatewayCommand struct {
	CommandID          uuid.UUID
	ActorAdminID       uuid.UUID
	RequestID          string
	InstanceID         uuid.UUID
	ExpectedRevision   int64
	NewInstanceID      uuid.UUID
	DisplayName        StringPatch
	ManagementEndpoint StringPatch
	Secret             SecretPatch
}

type GatewayCommandResult struct {
	HTTPStatus int             `json:"http_status"`
	Body       json.RawMessage `json:"body"`
	Replayed   bool            `json:"-"`
}

type GatewayLifecycleRepository struct {
	pool *pgxpool.Pool
	key  []byte
}

type gatewayIntentBuilder func(replay bool, receiptKeyVersion *int16) ([]byte, *int16, error)

type GatewayLifecycleManager interface {
	Current(context.Context) (*GatewayAsset, error)
	Detail(context.Context, uuid.UUID) (GatewayAssetDetail, error)
	List(context.Context, string, uuid.UUID, int, string) (GatewayAssetPage, error)
	Counts(context.Context) (GatewayCounts, error)
	Register(context.Context, GatewayCommand) (GatewayCommandResult, error)
	Edit(context.Context, GatewayCommand) (GatewayCommandResult, error)
	Retire(context.Context, GatewayCommand) (GatewayCommandResult, error)
	Replace(context.Context, GatewayCommand) (GatewayCommandResult, error)
	ProbeTarget(context.Context, uuid.UUID) (string, error)
	RecordProbe(context.Context, uuid.UUID, uuid.UUID, string, string, string) error
}

func (r *GatewayLifecycleRepository) Current(ctx context.Context) (*GatewayAsset, error) {
	var asset GatewayAsset
	err := r.pool.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE singleton_id=1`).Scan(gatewayScan(&asset)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &asset, err
}

func (r *GatewayLifecycleRepository) Detail(ctx context.Context, id uuid.UUID) (GatewayAssetDetail, error) {
	if id == uuid.Nil {
		return GatewayAssetDetail{}, ErrInvalidGateway
	}
	var result GatewayAssetDetail
	err := r.pool.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE instance_id=$1`, id).Scan(gatewayScan(&result.Asset)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayAssetDetail{}, ErrGatewayNotFound
	}
	if err != nil {
		return GatewayAssetDetail{}, err
	}
	result.Predecessor, err = r.lineage(ctx, `SELECT old_instance_id,new_instance_id,replaced_at,replaced_by,command_id FROM gateway_asset_replacements WHERE new_instance_id=$1`, id)
	if err != nil {
		return GatewayAssetDetail{}, err
	}
	result.Successor, err = r.lineage(ctx, `SELECT old_instance_id,new_instance_id,replaced_at,replaced_by,command_id FROM gateway_asset_replacements WHERE old_instance_id=$1`, id)
	return result, err
}

func (r *GatewayLifecycleRepository) lineage(ctx context.Context, query string, id uuid.UUID) (*GatewayReplacement, error) {
	var value GatewayReplacement
	err := r.pool.QueryRow(ctx, query, id).Scan(&value.OldInstanceID, &value.NewInstanceID, &value.ReplacedAt, &value.ReplacedBy, &value.CommandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &value, err
}

func (r *GatewayLifecycleRepository) Counts(ctx context.Context) (GatewayCounts, error) {
	var value GatewayCounts
	err := r.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE lifecycle_status='active'),count(*) FILTER (WHERE lifecycle_status='retired'),count(*) FROM gateway_instances`).Scan(&value.Active, &value.Retired, &value.Total)
	return value, err
}

func (r *GatewayLifecycleRepository) List(ctx context.Context, lifecycle string, after uuid.UUID, limit int, expectedGeneration string) (GatewayAssetPage, error) {
	if lifecycle == "" {
		lifecycle = "active"
	}
	if lifecycle != "active" && lifecycle != "retired" && lifecycle != "all" || limit < 1 || limit > 100 {
		return GatewayAssetPage{}, ErrInvalidGateway
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return GatewayAssetPage{}, err
	}
	defer tx.Rollback(ctx)
	var generation string
	if err = tx.QueryRow(ctx, `SELECT coalesce(sum(revision),0)::text FROM gateway_instances`).Scan(&generation); err != nil {
		return GatewayAssetPage{}, err
	}
	if expectedGeneration != "" && expectedGeneration != generation {
		return GatewayAssetPage{}, ErrGatewayCursorStale
	}
	rows, err := tx.Query(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE ($1='all' OR lifecycle_status=$1) AND ($2::uuid IS NULL OR instance_id>$2) ORDER BY instance_id ASC LIMIT $3`, lifecycle, nullableUUID(after), limit+1)
	if err != nil {
		return GatewayAssetPage{}, err
	}
	defer rows.Close()
	page := GatewayAssetPage{Generation: generation, Items: make([]GatewayAsset, 0, limit)}
	for rows.Next() {
		var a GatewayAsset
		if err = rows.Scan(gatewayScan(&a)...); err != nil {
			return GatewayAssetPage{}, err
		}
		page.Items = append(page.Items, a)
	}
	if err = rows.Err(); err != nil {
		return GatewayAssetPage{}, err
	}
	if len(page.Items) > limit {
		page.HasMore = true
		page.Items = page.Items[:limit]
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE lifecycle_status='active'),count(*) FILTER (WHERE lifecycle_status='retired'),count(*) FROM gateway_instances`).Scan(&page.Counts.Active, &page.Counts.Retired, &page.Counts.Total); err != nil {
		return GatewayAssetPage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return GatewayAssetPage{}, err
	}
	return page, nil
}

func NewGatewayLifecycleRepository(pool *pgxpool.Pool, key []byte) (*GatewayLifecycleRepository, error) {
	if pool == nil {
		return nil, errors.New("store: gateway lifecycle database unavailable")
	}
	if len(key) != 0 && len(key) != 32 {
		return nil, ErrReceiptKeyUnavailable
	}
	return &GatewayLifecycleRepository{pool: pool, key: append([]byte(nil), key...)}, nil
}

func NormalizeGatewayManagementEndpoint(raw string) (string, error) {
	origin, err := gatewaymanagement.ValidateOrigin(raw)
	if err != nil {
		return "", ErrInvalidGatewayEndpoint
	}
	return origin.URL(""), nil
}

func (r *GatewayLifecycleRepository) Register(ctx context.Context, command GatewayCommand) (GatewayCommandResult, error) {
	if command.CommandID == uuid.Nil || command.ActorAdminID == uuid.Nil {
		return GatewayCommandResult{}, ErrInvalidGateway
	}
	buildIntent := func(replay bool, receiptKeyVersion *int16) ([]byte, *int16, error) {
		if command.NewInstanceID == uuid.Nil || !validGatewayDisplayName(command.DisplayName) || !command.ManagementEndpoint.Present {
			return nil, nil, ErrInvalidGateway
		}
		endpoint, err := NormalizeGatewayManagementEndpoint(command.ManagementEndpoint.Value)
		if err != nil {
			return nil, nil, err
		}
		command.ManagementEndpoint.Value = endpoint
		return r.intent("gateway.register", command, replay, receiptKeyVersion)
	}
	return r.transact(ctx, command, "gateway.register", buildIntent, func(tx pgx.Tx) (int, any, error) {
		var occupied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_instances WHERE singleton_id=1)`).Scan(&occupied); err != nil {
			return 0, nil, err
		}
		if occupied {
			return 0, nil, ErrCurrentGatewayExists
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_instances WHERE instance_id=$1)`, command.NewInstanceID).Scan(&exists); err != nil {
			return 0, nil, err
		}
		if exists {
			return 0, nil, ErrGatewayIdentityExists
		}
		row := GatewayAsset{}
		err := tx.QueryRow(ctx, `INSERT INTO gateway_instances(singleton_id,instance_id,display_name,management_endpoint,reader_secret_ref,lifecycle_status,revision) VALUES(1,$1,$2,$3,$4,'active',1) RETURNING instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason`, command.NewInstanceID, command.DisplayName.Value, command.ManagementEndpoint.Value, secretValue(command.Secret)).Scan(gatewayScan(&row)...)
		if err != nil {
			if uniqueViolation(err) {
				return 0, nil, ErrCurrentGatewayExists
			}
			return 0, nil, err
		}
		body := map[string]any{"result": "registered", "asset": row}
		return 201, body, r.insertGatewayAudit(ctx, tx, command, "gateway.register", map[string]any{"command_id": command.CommandID, "instance_id": row.InstanceID, "new_revision": row.Revision})
	})
}

func (r *GatewayLifecycleRepository) Edit(ctx context.Context, command GatewayCommand) (GatewayCommandResult, error) {
	if command.CommandID == uuid.Nil || command.ActorAdminID == uuid.Nil {
		return GatewayCommandResult{}, ErrInvalidGateway
	}
	buildIntent := func(replay bool, receiptKeyVersion *int16) ([]byte, *int16, error) {
		if command.InstanceID == uuid.Nil || command.ExpectedRevision < 1 || (!command.DisplayName.Present && !command.ManagementEndpoint.Present && command.Secret.Operation == SecretAbsent) {
			return nil, nil, ErrInvalidGateway
		}
		if command.DisplayName.Present && !validGatewayDisplayName(command.DisplayName) {
			return nil, nil, ErrInvalidGateway
		}
		if command.ManagementEndpoint.Present {
			endpoint, err := NormalizeGatewayManagementEndpoint(command.ManagementEndpoint.Value)
			if err != nil {
				return nil, nil, err
			}
			command.ManagementEndpoint.Value = endpoint
		}
		return r.intent("gateway.edit", command, replay, receiptKeyVersion)
	}
	return r.transact(ctx, command, "gateway.edit", buildIntent, func(tx pgx.Tx) (int, any, error) {
		current, err := lockGateway(ctx, tx, command.InstanceID)
		if err != nil {
			return 0, nil, err
		}
		if current.LifecycleStatus != gatewayLifecycleActive {
			return 0, nil, ErrGatewayRetired
		}
		if current.Revision != command.ExpectedRevision {
			return 0, nil, ErrStaleAssetRevision
		}
		if current.Revision == maxAssetRevision {
			return 0, nil, ErrAssetRevisionExhausted
		}
		display := current.DisplayName
		if command.DisplayName.Present {
			display = command.DisplayName.Value
		}
		endpoint := current.ManagementEndpoint
		if command.ManagementEndpoint.Present {
			endpoint = command.ManagementEndpoint.Value
		}
		var row GatewayAsset
		query := `UPDATE gateway_instances SET display_name=$2,management_endpoint=$3,revision=revision+1,updated_at=clock_timestamp() WHERE instance_id=$1 RETURNING instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason`
		arguments := []any{command.InstanceID, display, endpoint}
		if command.Secret.Operation == SecretClear {
			query = `UPDATE gateway_instances SET display_name=$2,management_endpoint=$3,reader_secret_ref=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE instance_id=$1 RETURNING instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason`
		} else if command.Secret.Operation == SecretSet {
			query = `UPDATE gateway_instances SET display_name=$2,management_endpoint=$3,reader_secret_ref=$4,revision=revision+1,updated_at=clock_timestamp() WHERE instance_id=$1 RETURNING instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason`
			arguments = append(arguments, command.Secret.Value)
		}
		err = tx.QueryRow(ctx, query, arguments...).Scan(gatewayScan(&row)...)
		if err != nil {
			return 0, nil, err
		}
		body := map[string]any{"result": "updated", "asset": row}
		return 200, body, r.insertGatewayAudit(ctx, tx, command, "gateway.edit", map[string]any{"command_id": command.CommandID, "instance_id": row.InstanceID, "old_revision": current.Revision, "new_revision": row.Revision})
	})
}

func (r *GatewayLifecycleRepository) Retire(ctx context.Context, command GatewayCommand) (GatewayCommandResult, error) {
	if command.CommandID == uuid.Nil || command.ActorAdminID == uuid.Nil {
		return GatewayCommandResult{}, ErrInvalidGateway
	}
	buildIntent := func(replay bool, receiptKeyVersion *int16) ([]byte, *int16, error) {
		if command.InstanceID == uuid.Nil || command.ExpectedRevision < 1 {
			return nil, nil, ErrInvalidGateway
		}
		return r.intent("gateway.retire", command, replay, receiptKeyVersion)
	}
	return r.transact(ctx, command, "gateway.retire", buildIntent, func(tx pgx.Tx) (int, any, error) {
		current, err := lockGateway(ctx, tx, command.InstanceID)
		if err != nil {
			return 0, nil, err
		}
		if current.LifecycleStatus != gatewayLifecycleActive {
			return 0, nil, ErrGatewayRetired
		}
		if current.Revision != command.ExpectedRevision {
			return 0, nil, ErrStaleAssetRevision
		}
		if current.Revision == maxAssetRevision {
			return 0, nil, ErrAssetRevisionExhausted
		}
		rows, err := tx.Query(ctx, `SELECT binding_id FROM relay_node_gateway_account_bindings WHERE gateway_instance_id=$1 AND ended_at IS NULL ORDER BY relay_node_id ASC FOR UPDATE`, command.InstanceID)
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
		var boundary time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&boundary); err != nil {
			return 0, nil, err
		}
		result, err := tx.Exec(ctx, `UPDATE relay_node_gateway_account_bindings SET ended_at=$2,ended_by=$3,end_reason='gateway_retired' WHERE gateway_instance_id=$1 AND ended_at IS NULL`, command.InstanceID, boundary, command.ActorAdminID)
		if err != nil {
			return 0, nil, err
		}
		var row GatewayAsset
		err = tx.QueryRow(ctx, `UPDATE gateway_instances SET singleton_id=NULL,lifecycle_status='retired',retired_at=$2,retired_by=$3,retire_reason='administrator_retire',revision=revision+1,updated_at=$2 WHERE instance_id=$1 RETURNING instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason`, command.InstanceID, boundary, command.ActorAdminID).Scan(gatewayScan(&row)...)
		if err != nil {
			return 0, nil, err
		}
		count := result.RowsAffected()
		body := map[string]any{"result": "retired", "asset": row, "closed_binding_count": count}
		return 200, body, r.insertGatewayAudit(ctx, tx, command, "gateway.retire", map[string]any{"command_id": command.CommandID, "instance_id": row.InstanceID, "reason_code": "administrator_retire", "old_revision": current.Revision, "new_revision": row.Revision, "closed_binding_count": count})
	})
}

func (r *GatewayLifecycleRepository) Replace(ctx context.Context, command GatewayCommand) (GatewayCommandResult, error) {
	if command.CommandID == uuid.Nil || command.ActorAdminID == uuid.Nil {
		return GatewayCommandResult{}, ErrInvalidGateway
	}
	buildIntent := func(replay bool, receiptKeyVersion *int16) ([]byte, *int16, error) {
		if command.InstanceID == uuid.Nil || command.NewInstanceID == uuid.Nil || command.ExpectedRevision < 1 || !validGatewayDisplayName(command.DisplayName) || !command.ManagementEndpoint.Present {
			return nil, nil, ErrInvalidGateway
		}
		endpoint, err := NormalizeGatewayManagementEndpoint(command.ManagementEndpoint.Value)
		if err != nil {
			return nil, nil, err
		}
		command.ManagementEndpoint.Value = endpoint
		return r.intent("gateway.replace", command, replay, receiptKeyVersion)
	}
	return r.transact(ctx, command, "gateway.replace", buildIntent, func(tx pgx.Tx) (int, any, error) {
		old, err := lockGateway(ctx, tx, command.InstanceID)
		if err != nil {
			return 0, nil, err
		}
		if old.LifecycleStatus != gatewayLifecycleActive {
			return 0, nil, ErrGatewayRetired
		}
		if old.Revision != command.ExpectedRevision {
			return 0, nil, ErrStaleAssetRevision
		}
		if old.Revision == maxAssetRevision {
			return 0, nil, ErrAssetRevisionExhausted
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_instances WHERE instance_id=$1)`, command.NewInstanceID).Scan(&exists); err != nil {
			return 0, nil, err
		}
		if exists {
			return 0, nil, ErrGatewayIdentityExists
		}
		rows, err := tx.Query(ctx, `SELECT binding_id FROM relay_node_gateway_account_bindings WHERE gateway_instance_id=$1 AND ended_at IS NULL ORDER BY relay_node_id ASC FOR UPDATE`, command.InstanceID)
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
		var boundary time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&boundary); err != nil {
			return 0, nil, err
		}
		closed, err := tx.Exec(ctx, `UPDATE relay_node_gateway_account_bindings SET ended_at=$2,ended_by=$3,end_reason='gateway_replaced' WHERE gateway_instance_id=$1 AND ended_at IS NULL`, command.InstanceID, boundary, command.ActorAdminID)
		if err != nil {
			return 0, nil, err
		}
		var oldRow GatewayAsset
		err = tx.QueryRow(ctx, `UPDATE gateway_instances SET singleton_id=NULL,lifecycle_status='retired',retired_at=$2,retired_by=$3,retire_reason='replacement',revision=revision+1,updated_at=$2 WHERE instance_id=$1 RETURNING instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason`, command.InstanceID, boundary, command.ActorAdminID).Scan(gatewayScan(&oldRow)...)
		if err != nil {
			return 0, nil, err
		}
		var newRow GatewayAsset
		err = tx.QueryRow(ctx, `INSERT INTO gateway_instances(singleton_id,instance_id,display_name,management_endpoint,reader_secret_ref,lifecycle_status,revision) VALUES(1,$1,$2,$3,$4,'active',1) RETURNING instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason`, command.NewInstanceID, command.DisplayName.Value, command.ManagementEndpoint.Value, secretValue(command.Secret)).Scan(gatewayScan(&newRow)...)
		if err != nil {
			return 0, nil, err
		}
		lineage := GatewayReplacement{OldInstanceID: command.InstanceID, NewInstanceID: command.NewInstanceID, ReplacedAt: boundary.UTC(), ReplacedBy: command.ActorAdminID, CommandID: command.CommandID}
		_, err = tx.Exec(ctx, `INSERT INTO gateway_asset_replacements(old_instance_id,new_instance_id,replaced_at,replaced_by,command_id) VALUES($1,$2,$3,$4,$5)`, lineage.OldInstanceID, lineage.NewInstanceID, lineage.ReplacedAt, lineage.ReplacedBy, lineage.CommandID)
		if err != nil {
			return 0, nil, err
		}
		count := closed.RowsAffected()
		body := map[string]any{"result": "replaced", "old_asset": oldRow, "new_asset": newRow, "closed_binding_count": count, "lineage": lineage}
		return 200, body, r.insertGatewayAudit(ctx, tx, command, "gateway.replace", map[string]any{"command_id": command.CommandID, "old_instance_id": oldRow.InstanceID, "new_instance_id": newRow.InstanceID, "reason_code": "replacement", "old_revision": old.Revision, "new_revision": newRow.Revision, "closed_binding_count": count})
	})
}

func (r *GatewayLifecycleRepository) transact(ctx context.Context, command GatewayCommand, kind string, buildIntent gatewayIntentBuilder, apply func(pgx.Tx) (int, any, error)) (GatewayCommandResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	defer tx.Rollback(ctx)
	sum := sha256.Sum256(command.CommandID[:])
	lockKey := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return GatewayCommandResult{}, err
	}
	var actor uuid.UUID
	var storedKind string
	var storedEncoding int16
	var storedHash []byte
	var resultBytes []byte
	var storedStatus int16
	var storedKeyVersion *int16
	err = tx.QueryRow(ctx, `SELECT actor_admin_id,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,sanitized_result,response_status FROM asset_admin_command_receipts WHERE command_id=$1`, command.CommandID).Scan(&actor, &storedKind, &storedEncoding, &storedHash, &storedKeyVersion, &resultBytes, &storedStatus)
	if err == nil {
		if actor != command.ActorAdminID {
			return GatewayCommandResult{}, ErrCommandConflict
		}
		if storedKind != kind {
			return GatewayCommandResult{}, ErrCommandConflict
		}
		if storedEncoding != 1 {
			return GatewayCommandResult{}, ErrReceiptEncodingUnknown
		}
		if storedKeyVersion != nil {
			if *storedKeyVersion != 1 || len(r.key) != 32 {
				return GatewayCommandResult{}, ErrReceiptKeyUnavailable
			}
		}
		intent, _, buildErr := buildIntent(true, storedKeyVersion)
		if buildErr != nil {
			return GatewayCommandResult{}, buildErr
		}
		hash := sha256.Sum256(intent)
		if !hmac.Equal(storedHash, hash[:]) {
			return GatewayCommandResult{}, ErrCommandConflict
		}
		saved := GatewayCommandResult{HTTPStatus: int(storedStatus), Body: append(json.RawMessage(nil), resultBytes...)}
		saved.Replayed = true
		return saved, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return GatewayCommandResult{}, err
	}
	intent, keyVersion, err := buildIntent(false, nil)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	hash := sha256.Sum256(intent)
	status, body, err := apply(tx)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	saved := GatewayCommandResult{HTTPStatus: status, Body: bodyBytes}
	_, err = tx.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id,secret_fingerprint_key_version) VALUES($1,$2,1,$3,$4,$5,$6,$7)`, command.CommandID, kind, hash[:], bodyBytes, status, command.ActorAdminID, keyVersion)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return GatewayCommandResult{}, err
	}
	return saved, nil
}

func (r *GatewayLifecycleRepository) intent(kind string, c GatewayCommand, replay bool, receiptKeyVersion *int16) ([]byte, *int16, error) {
	triplet := []any{"absent", nil, nil}
	var keyVersion *int16
	switch c.Secret.Operation {
	case SecretAbsent:
	case SecretClear:
		triplet = []any{"clear", nil, nil}
	case SecretSet:
		if !validGatewaySecretReference(c.Secret.Value) {
			return nil, nil, ErrInvalidGatewaySecret
		}
		if replay && receiptKeyVersion == nil {
			return nil, nil, ErrCommandConflict
		}
		if len(r.key) != 32 {
			return nil, nil, ErrReceiptKeyUnavailable
		}
		v := int16(1)
		keyVersion = &v
		mac := hmac.New(sha256.New, r.key)
		mac.Write([]byte(assetIntentDomain))
		mac.Write([]byte(kind))
		mac.Write([]byte{0})
		mac.Write([]byte(c.Secret.Value))
		triplet = []any{"set", 1, hex.EncodeToString(mac.Sum(nil))}
	default:
		return nil, nil, ErrInvalidGatewaySecret
	}
	if replay && receiptKeyVersion != nil && *receiptKeyVersion != 1 {
		return nil, nil, ErrReceiptKeyUnavailable
	}
	var value any
	switch kind {
	case "gateway.register":
		value = []any{1, kind, c.NewInstanceID.String(), c.DisplayName.Value, c.ManagementEndpoint.Value, triplet}
	case "gateway.edit":
		value = []any{1, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), patchPair(c.DisplayName), patchPair(c.ManagementEndpoint), triplet}
	case "gateway.retire":
		value = []any{1, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), "administrator_retire"}
	case "gateway.replace":
		value = []any{1, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), c.NewInstanceID.String(), c.DisplayName.Value, c.ManagementEndpoint.Value, triplet, "replacement"}
	default:
		return nil, nil, ErrInvalidGateway
	}
	b, err := encodeCanonicalIntent(value)
	return b, keyVersion, err
}

func validGatewaySecretReference(value string) bool {
	return ValidAssetSecretReference(value)
}

func encodeCanonicalIntent(value any) ([]byte, error) {
	var builder strings.Builder
	var appendValue func(any) error
	appendString := func(value string) {
		builder.WriteByte('"')
		for _, char := range value {
			switch char {
			case '"', '\\':
				builder.WriteByte('\\')
				builder.WriteRune(char)
			default:
				if char < 0x20 {
					builder.WriteString(`\u`)
					builder.WriteString(fmt.Sprintf("%04x", char))
				} else {
					builder.WriteRune(char)
				}
			}
		}
		builder.WriteByte('"')
	}
	appendValue = func(value any) error {
		switch typed := value.(type) {
		case nil:
			builder.WriteString("null")
		case string:
			appendString(typed)
		case int:
			builder.WriteString(strconv.Itoa(typed))
		case []any:
			builder.WriteByte('[')
			for index, item := range typed {
				if index > 0 {
					builder.WriteByte(',')
				}
				if err := appendValue(item); err != nil {
					return err
				}
			}
			builder.WriteByte(']')
		default:
			return ErrInvalidGateway
		}
		return nil
	}
	if err := appendValue(value); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

func patchPair(p StringPatch) []any {
	if !p.Present {
		return []any{"absent", nil}
	}
	return []any{"set", p.Value}
}
func validGatewayDisplayName(p StringPatch) bool {
	return p.Present && p.Value != "" && strings.TrimSpace(p.Value) == p.Value && len(p.Value) <= 100
}
func secretValue(p SecretPatch) any {
	if p.Operation == SecretSet {
		return p.Value
	}
	return nil
}
func uniqueViolation(err error) bool {
	var p *pgconn.PgError
	return errors.As(err, &p) && p.Code == "23505"
}

func lockGateway(ctx context.Context, tx pgx.Tx, id uuid.UUID) (GatewayAsset, error) {
	var row GatewayAsset
	err := tx.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE instance_id=$1 FOR UPDATE`, id).Scan(gatewayScan(&row)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayAsset{}, ErrGatewayNotFound
	}
	return row, err
}
func gatewayScan(a *GatewayAsset) []any {
	return []any{&a.InstanceID, &a.DisplayName, &a.ManagementEndpoint, &a.SecretConfigured, &a.LifecycleStatus, &a.Revision, &a.CreatedAt, &a.UpdatedAt, &a.RetiredAt, &a.RetiredBy, &a.RetireReason}
}

func (r *GatewayLifecycleRepository) insertGatewayAudit(ctx context.Context, tx pgx.Tx, c GatewayCommand, action string, details map[string]any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details) VALUES($1,'asset_gateway',$2,'success',$3,$4,$5)`, uuid.New(), action, c.ActorAdminID, c.RequestID, b)
	return err
}

func (r *GatewayLifecycleRepository) RecordProbe(ctx context.Context, actor, instanceID uuid.UUID, requestID, action, result string) error {
	if actor == uuid.Nil || instanceID == uuid.Nil || requestID == "" ||
		(action != "gateway.health" && action != "gateway.connection_test") ||
		(result != "healthy" && result != "timeout" && result != "failed") {
		return ErrInvalidGateway
	}
	details, err := json.Marshal(map[string]any{"instance_id": instanceID, "probe_result": result})
	if err != nil {
		return err
	}
	auditResult := "failure"
	if result == "healthy" {
		auditResult = "success"
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details) VALUES($1,'asset_gateway',$2,$3,$4,$5,$6)`, uuid.New(), action, auditResult, actor, requestID, details)
	return err
}

func (r *GatewayLifecycleRepository) ProbeTarget(ctx context.Context, instanceID uuid.UUID) (string, error) {
	if instanceID == uuid.Nil {
		return "", ErrInvalidGateway
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var endpoint string
	err = tx.QueryRow(ctx, `SELECT management_endpoint FROM gateway_instances WHERE instance_id=$1 AND lifecycle_status='active' AND singleton_id=1 FOR SHARE`, instanceID).Scan(&endpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if lookupErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_instances WHERE instance_id=$1)`, instanceID).Scan(&exists); lookupErr != nil {
			return "", lookupErr
		}
		if exists {
			return "", ErrGatewayRetired
		}
		return "", ErrGatewayNotFound
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return endpoint, nil
}
