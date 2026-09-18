package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
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

	"github.com/sunxu/relay-station-control/internal/assetcredential"
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
	assetIntentV2Domain     = "relay-station/asset-admin-intent/v2/"
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
	SecretAbsent       SecretOperation = "absent"
	SecretKeep         SecretOperation = "keep"
	SecretClear        SecretOperation = "clear"
	SecretSet          SecretOperation = "set"
	SecretExplicitNull SecretOperation = "explicit_null"
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
	pool   *pgxpool.Pool
	key    []byte
	sealer AssetCredentialSealer
}

type gatewayIntentBuilder func(replay bool, receiptKeyVersion *int16, encodingVersion int16) ([]byte, *int16, error)

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

func (r *GatewayLifecycleRepository) sealedCredential(id uuid.UUID, patch SecretPatch) ([]byte, error) {
	if patch.Operation != SecretSet {
		return nil, nil
	}
	if !validateCredentialPlaintext(patch.Value) || !r.sealer.Available() {
		return nil, ErrInvalidGatewaySecret
	}
	sealed, err := r.sealer.Seal(assetcredential.GatewayCredential, id, []byte(patch.Value))
	if err != nil {
		return nil, ErrInvalidGatewaySecret
	}
	return sealed, nil
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
	return NewGatewayLifecycleRepositoryWithSealer(pool, key, unavailableCredentialSealer{})
}

func NewGatewayLifecycleRepositoryWithSealer(pool *pgxpool.Pool, key []byte, sealer AssetCredentialSealer) (*GatewayLifecycleRepository, error) {
	if pool == nil {
		return nil, errors.New("store: gateway lifecycle database unavailable")
	}
	if len(key) != 0 && len(key) != 32 {
		return nil, ErrReceiptKeyUnavailable
	}
	if sealer == nil {
		sealer = unavailableCredentialSealer{}
	}
	return &GatewayLifecycleRepository{pool: pool, key: append([]byte(nil), key...), sealer: sealer}, nil
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
	buildIntent := func(replay bool, receiptKeyVersion *int16, encodingVersion int16) ([]byte, *int16, error) {
		if command.NewInstanceID == uuid.Nil || !validGatewayDisplayName(command.DisplayName) || !command.ManagementEndpoint.Present {
			return nil, nil, ErrInvalidGateway
		}
		endpoint, err := NormalizeGatewayManagementEndpoint(command.ManagementEndpoint.Value)
		if err != nil {
			return nil, nil, err
		}
		command.ManagementEndpoint.Value = endpoint
		return r.intentForEncoding("gateway.register", command, replay, receiptKeyVersion, encodingVersion)
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
		sealed, err := r.sealedCredential(command.NewInstanceID, command.Secret)
		if err != nil {
			return 0, nil, err
		}
		var created uuid.UUID
		err = tx.QueryRow(ctx, `SELECT public.control_register_gateway_asset_stage0_v1($1::uuid,$2::text,$3::text,$4::bytea)`, command.NewInstanceID, command.DisplayName.Value, command.ManagementEndpoint.Value, sealed).Scan(&created)
		if err != nil {
			if uniqueViolation(err) {
				return 0, nil, ErrCurrentGatewayExists
			}
			return 0, nil, err
		}
		var row GatewayAsset
		if err = tx.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE instance_id=$1`, created).Scan(gatewayScan(&row)...); err != nil {
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
	buildIntent := func(replay bool, receiptKeyVersion *int16, encodingVersion int16) ([]byte, *int16, error) {
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
		return r.intentForEncoding("gateway.edit", command, replay, receiptKeyVersion, encodingVersion)
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
		sealed, err := r.sealedCredential(command.InstanceID, command.Secret)
		if err != nil {
			return 0, nil, err
		}
		action := string(command.Secret.Operation)
		if command.Secret.Operation == SecretAbsent {
			action = string(SecretKeep)
		}
		if _, err = tx.Exec(ctx, `SELECT public.control_edit_gateway_asset_stage0_v1($1::uuid,$2::bigint,$3::text,$4::text,$5::text,$6::bytea)`, command.InstanceID, command.ExpectedRevision, display, endpoint, action, sealed); err != nil {
			return 0, nil, err
		}
		var row GatewayAsset
		if err = tx.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE instance_id=$1`, command.InstanceID).Scan(gatewayScan(&row)...); err != nil {
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
	buildIntent := func(replay bool, receiptKeyVersion *int16, encodingVersion int16) ([]byte, *int16, error) {
		if command.InstanceID == uuid.Nil || command.ExpectedRevision < 1 {
			return nil, nil, ErrInvalidGateway
		}
		return r.intentForEncoding("gateway.retire", command, replay, receiptKeyVersion, encodingVersion)
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
		if _, err = tx.Exec(ctx, `SELECT public.control_retire_gateway_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::text)`, command.InstanceID, command.ExpectedRevision, boundary, command.ActorAdminID, "administrator_retire"); err != nil {
			return 0, nil, err
		}
		var row GatewayAsset
		if err = tx.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE instance_id=$1`, command.InstanceID).Scan(gatewayScan(&row)...); err != nil {
			return 0, nil, err
		}
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
	buildIntent := func(replay bool, receiptKeyVersion *int16, encodingVersion int16) ([]byte, *int16, error) {
		if command.InstanceID == uuid.Nil || command.NewInstanceID == uuid.Nil || command.ExpectedRevision < 1 || !validGatewayDisplayName(command.DisplayName) || !command.ManagementEndpoint.Present {
			return nil, nil, ErrInvalidGateway
		}
		endpoint, err := NormalizeGatewayManagementEndpoint(command.ManagementEndpoint.Value)
		if err != nil {
			return nil, nil, err
		}
		command.ManagementEndpoint.Value = endpoint
		return r.intentForEncoding("gateway.replace", command, replay, receiptKeyVersion, encodingVersion)
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
		replacementSealed, sealErr := r.sealedCredential(command.NewInstanceID, command.Secret)
		if sealErr != nil {
			return 0, nil, sealErr
		}
		closed, err := tx.Exec(ctx, `UPDATE relay_node_gateway_account_bindings SET ended_at=$2,ended_by=$3,end_reason='gateway_replaced' WHERE gateway_instance_id=$1 AND ended_at IS NULL`, command.InstanceID, boundary, command.ActorAdminID)
		if err != nil {
			return 0, nil, err
		}
		var created uuid.UUID
		err = tx.QueryRow(ctx, `SELECT public.control_replace_gateway_asset_stage0_v1($1::uuid,$2::bigint,$3::timestamptz,$4::uuid,$5::uuid,$6::text,$7::text,$8::bytea)`, command.InstanceID, command.ExpectedRevision, boundary, command.ActorAdminID, command.NewInstanceID, command.DisplayName.Value, command.ManagementEndpoint.Value, replacementSealed).Scan(&created)
		if err != nil {
			return 0, nil, err
		}
		var oldRow GatewayAsset
		if err = tx.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE instance_id=$1`, command.InstanceID).Scan(gatewayScan(&oldRow)...); err != nil {
			return 0, nil, err
		}
		var newRow GatewayAsset
		if err = tx.QueryRow(ctx, `SELECT instance_id,display_name,management_endpoint,reader_secret_configured,lifecycle_status,revision,created_at,updated_at,retired_at,retired_by,retire_reason FROM gateway_instances WHERE instance_id=$1`, created).Scan(gatewayScan(&newRow)...); err != nil {
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
	if err = lockAdminCommand(ctx, tx, command.CommandID); err != nil {
		return GatewayCommandResult{}, err
	}
	reservation, reserved, err := lookupAdminCommandReservation(ctx, tx, command.CommandID)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	if reserved {
		if reservation.ActorAdminID != command.ActorAdminID || reservation.CommandDomain != adminCommandDomainAsset || reservation.CommandKind != kind {
			return GatewayCommandResult{}, ErrCommandConflict
		}
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
		if !reserved || !reservation.matchesReceipt(actor, storedKind, storedEncoding, storedHash, storedKeyVersion) {
			return GatewayCommandResult{}, ErrCommandRegistryInconsistent
		}
		if actor != command.ActorAdminID {
			return GatewayCommandResult{}, ErrCommandConflict
		}
		if storedKind != kind {
			return GatewayCommandResult{}, ErrCommandConflict
		}
		if storedEncoding != 1 && storedEncoding != 2 {
			return GatewayCommandResult{}, ErrReceiptEncodingUnknown
		}
		if storedKeyVersion != nil {
			if *storedKeyVersion != 1 || len(r.key) != 32 {
				return GatewayCommandResult{}, ErrReceiptKeyUnavailable
			}
		}
		intent, _, buildErr := buildIntent(true, storedKeyVersion, storedEncoding)
		if buildErr != nil {
			if errors.Is(buildErr, ErrInvalidGatewaySecret) || errors.Is(buildErr, ErrInvalidGateway) {
				return GatewayCommandResult{}, ErrCommandConflict
			}
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
	if reserved {
		return GatewayCommandResult{}, ErrCommandRegistryInconsistent
	}
	const newIntentEncodingVersion int16 = 2
	intent, keyVersion, err := buildIntent(false, nil, newIntentEncodingVersion)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	hash := sha256.Sum256(intent)
	if err = reserveAdminCommand(ctx, tx, command.CommandID, command.ActorAdminID, kind, newIntentEncodingVersion, hash[:], keyVersion); err != nil {
		return GatewayCommandResult{}, err
	}
	status, body, err := apply(tx)
	if err != nil {
		return GatewayCommandResult{}, translateGatewayDBError(err)
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	saved := GatewayCommandResult{HTTPStatus: status, Body: bodyBytes}
	err = insertControlledAssetAdminCommandReceipt(ctx, tx, command.CommandID, command.ActorAdminID, kind, newIntentEncodingVersion, hash[:], bodyBytes, status, nil, keyVersion)
	if err != nil {
		return GatewayCommandResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return GatewayCommandResult{}, err
	}
	return saved, nil
}

func (r *GatewayLifecycleRepository) intent(kind string, c GatewayCommand, replay bool, receiptKeyVersion *int16) ([]byte, *int16, error) {
	return r.intentForEncoding(kind, c, replay, receiptKeyVersion, 1)
}

func (r *GatewayLifecycleRepository) intentForEncoding(kind string, c GatewayCommand, replay bool, receiptKeyVersion *int16, encodingVersion int16) ([]byte, *int16, error) {
	triplet := []any{"absent", nil, nil}
	var keyVersion *int16
	switch c.Secret.Operation {
	case SecretAbsent:
	case SecretClear:
		triplet = []any{"clear", nil, nil}
	case SecretSet:
		if !validateCredentialPlaintext(c.Secret.Value) {
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
		if encodingVersion == 2 {
			mac.Write([]byte(assetIntentV2Domain))
		} else {
			mac.Write([]byte(assetIntentDomain))
		}
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
		value = []any{encodingVersion, kind, c.NewInstanceID.String(), c.DisplayName.Value, c.ManagementEndpoint.Value, triplet}
	case "gateway.edit":
		value = []any{encodingVersion, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), patchPair(c.DisplayName), patchPair(c.ManagementEndpoint), triplet}
	case "gateway.retire":
		value = []any{encodingVersion, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), "administrator_retire"}
	case "gateway.replace":
		value = []any{encodingVersion, kind, c.InstanceID.String(), fmt.Sprint(c.ExpectedRevision), c.NewInstanceID.String(), c.DisplayName.Value, c.ManagementEndpoint.Value, triplet, "replacement"}
	default:
		return nil, nil, ErrInvalidGateway
	}
	b, err := encodeCanonicalIntent(value)
	return b, keyVersion, err
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
		case int16:
			builder.WriteString(strconv.FormatInt(int64(typed), 10))
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

func translateGatewayDBError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "23505":
			return ErrGatewayIdentityExists
		case "23514":
			return ErrInvalidGateway
		case "P0002":
			return ErrGatewayNotFound
		case "P0003":
			return ErrGatewayRetired
		case "P0004":
			return ErrAssetRevisionExhausted
		case "P0005":
			return ErrStaleAssetRevision
		}
	}
	return err
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
