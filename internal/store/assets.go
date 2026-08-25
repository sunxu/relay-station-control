package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrAssetNotFound             = errors.New("store: asset not found")
	ErrInvalidAssetQuery         = errors.New("store: invalid asset query")
	ErrAssetRegistryInconsistent = errors.New("store: asset registry is inconsistent")
)

type EnvironmentAsset struct {
	ID   string
	Name string
	Type string
}

type GatewayAsset struct {
	InstanceID         uuid.UUID
	DisplayName        string
	ManagementEndpoint string
	SecretConfigured   bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type NodeMonitoring struct {
	Active        bool
	EffectiveFrom *time.Time
	EffectiveTo   *time.Time
}

type NodeAsset struct {
	InstanceID            uuid.UUID
	DisplayName           string
	NodeType              string
	DriverContractVersion string
	ManagementEndpoint    string
	SecretConfigured      bool
	Capabilities          []string
	Monitoring            NodeMonitoring
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type NodeAssetPage struct {
	Items   []NodeAsset
	HasMore bool
}

type NodeDriver struct {
	NodeType              string
	DriverContractVersion string
	DisplayName           string
	LifecycleStatus       string
	Capabilities          []string
	CreatedAt             time.Time
}

type ProviderInventoryPolicy struct {
	VersionID             uuid.UUID
	NodeType              string
	DriverContractVersion string
	ActiveProviders       []string
	OutOfScopeProviders   []string
	EffectiveFrom         time.Time
	EffectiveTo           *time.Time
	CreatedAt             time.Time
}

type AssetCounts struct {
	Environments     int64
	Gateways         int64
	Nodes            int64
	Drivers          int64
	ProviderPolicies int64
}

type AssetReader interface {
	Environment(context.Context) (EnvironmentAsset, error)
	Gateway(context.Context) (*GatewayAsset, error)
	ListNodes(context.Context, NodeListFilters, uuid.UUID, int) (NodeAssetPage, error)
	Node(context.Context, uuid.UUID) (NodeAsset, error)
	Drivers(context.Context) ([]NodeDriver, error)
	CurrentProviderPolicy(context.Context, string, string) (*ProviderInventoryPolicy, error)
	Counts(context.Context) (AssetCounts, error)
}

type AssetRepository struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

func NewAssetRepository(pool *pgxpool.Pool) (*AssetRepository, error) {
	if pool == nil {
		return nil, errors.New("store: asset database is unavailable")
	}
	return &AssetRepository{pool: pool, queries: generated.New(pool)}, nil
}

func (repository *AssetRepository) Environment(ctx context.Context) (EnvironmentAsset, error) {
	row, err := repository.queries.GetEnvironment(ctx)
	if err != nil {
		return EnvironmentAsset{}, err
	}
	return EnvironmentAsset{ID: row.EnvironmentID, Name: row.Name, Type: row.EnvironmentType}, nil
}

func (repository *AssetRepository) Gateway(ctx context.Context) (*GatewayAsset, error) {
	row, err := repository.queries.GetGatewayAsset(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &GatewayAsset{
		InstanceID:         uuidFromPG(row.InstanceID),
		DisplayName:        row.DisplayName,
		ManagementEndpoint: row.ManagementEndpoint,
		SecretConfigured:   row.SecretConfigured,
		CreatedAt:          row.CreatedAt.Time.UTC(),
		UpdatedAt:          row.UpdatedAt.Time.UTC(),
	}, nil
}

func (repository *AssetRepository) ListNodes(ctx context.Context, filters NodeListFilters, after uuid.UUID, limit int) (NodeAssetPage, error) {
	if limit < 1 || limit > 200 {
		return NodeAssetPage{}, ErrInvalidAssetQuery
	}
	var page NodeAssetPage
	err := pgx.BeginTxFunc(ctx, repository.pool, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		rows, err := repository.queries.WithTx(tx).ListNodeAssets(ctx, generated.ListNodeAssetsParams{
			NodeType:         nullableText(filters.NodeType),
			Capability:       nullableText(filters.Capability),
			MonitoringActive: nullableBool(filters.MonitoringActive),
			AfterInstanceID:  nullableUUID(after),
			PageSize:         int32(limit + 1),
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.MonitoringMatchCount > 1 {
				return ErrAssetRegistryInconsistent
			}
		}
		page.HasMore = len(rows) > limit
		if page.HasMore {
			rows = rows[:limit]
		}
		page.Items = make([]NodeAsset, 0, len(rows))
		for _, row := range rows {
			page.Items = append(page.Items, nodeAssetFromListRow(row))
		}
		return nil
	})
	return page, err
}

func (repository *AssetRepository) Node(ctx context.Context, instanceID uuid.UUID) (NodeAsset, error) {
	if instanceID == uuid.Nil {
		return NodeAsset{}, ErrInvalidAssetQuery
	}
	var asset NodeAsset
	err := pgx.BeginTxFunc(ctx, repository.pool, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		row, err := repository.queries.WithTx(tx).GetNodeAsset(ctx, nullableUUID(instanceID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAssetNotFound
		}
		if err != nil {
			return err
		}
		if row.MonitoringMatchCount > 1 {
			return ErrAssetRegistryInconsistent
		}
		asset = nodeAssetFromDetailRow(row)
		return nil
	})
	return asset, err
}

func (repository *AssetRepository) Drivers(ctx context.Context) ([]NodeDriver, error) {
	rows, err := repository.queries.ListNodeDrivers(ctx)
	if err != nil {
		return nil, err
	}
	drivers := make([]NodeDriver, 0, len(rows))
	for _, row := range rows {
		drivers = append(drivers, NodeDriver{
			NodeType:              row.NodeType,
			DriverContractVersion: row.DriverContractVersion,
			DisplayName:           row.DisplayName,
			LifecycleStatus:       row.LifecycleStatus,
			Capabilities:          append([]string(nil), row.Capabilities...),
			CreatedAt:             row.CreatedAt.Time.UTC(),
		})
	}
	return drivers, nil
}

func (repository *AssetRepository) CurrentProviderPolicy(ctx context.Context, nodeType, driverVersion string) (*ProviderInventoryPolicy, error) {
	if nodeType == "" || driverVersion == "" {
		return nil, ErrInvalidAssetQuery
	}
	row, err := repository.queries.GetCurrentProviderInventoryPolicy(ctx, generated.GetCurrentProviderInventoryPolicyParams{
		NodeType: nodeType, DriverContractVersion: driverVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.CurrentMatchCount > 1 {
		return nil, ErrAssetRegistryInconsistent
	}
	if row.CurrentMatchCount == 0 {
		return nil, nil
	}
	return &ProviderInventoryPolicy{
		VersionID:             uuidFromPG(row.PolicyVersionID),
		NodeType:              row.NodeType.String,
		DriverContractVersion: row.DriverContractVersion.String,
		ActiveProviders:       append([]string(nil), row.ActiveProviders...),
		OutOfScopeProviders:   append([]string(nil), row.OutOfScopeProviders...),
		EffectiveFrom:         row.EffectiveFrom.Time.UTC(),
		EffectiveTo:           nullableTime(row.EffectiveTo),
		CreatedAt:             row.CreatedAt.Time.UTC(),
	}, nil
}

func (repository *AssetRepository) Counts(ctx context.Context) (AssetCounts, error) {
	row, err := repository.queries.GetAssetCounts(ctx)
	if err != nil {
		return AssetCounts{}, err
	}
	return AssetCounts{
		Environments: 1, Gateways: row.Gateways, Nodes: row.Nodes,
		Drivers: row.Drivers, ProviderPolicies: row.ProviderPolicies,
	}, nil
}

func nodeAssetFromListRow(row generated.ListNodeAssetsRow) NodeAsset {
	return NodeAsset{
		InstanceID: uuidFromPG(row.InstanceID), DisplayName: row.DisplayName,
		NodeType: row.NodeType, DriverContractVersion: row.DriverContractVersion,
		ManagementEndpoint: row.ManagementEndpoint, SecretConfigured: row.SecretConfigured,
		Capabilities: append([]string(nil), row.Capabilities...),
		Monitoring:   NodeMonitoring{Active: row.MonitoringActive, EffectiveFrom: nullableTime(row.MonitoringEffectiveFrom), EffectiveTo: nullableTime(row.MonitoringEffectiveTo)},
		CreatedAt:    row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func nodeAssetFromDetailRow(row generated.GetNodeAssetRow) NodeAsset {
	return NodeAsset{
		InstanceID: uuidFromPG(row.InstanceID), DisplayName: row.DisplayName,
		NodeType: row.NodeType, DriverContractVersion: row.DriverContractVersion,
		ManagementEndpoint: row.ManagementEndpoint, SecretConfigured: row.SecretConfigured,
		Capabilities: append([]string(nil), row.Capabilities...),
		Monitoring:   NodeMonitoring{Active: row.MonitoringActive, EffectiveFrom: nullableTime(row.MonitoringEffectiveFrom), EffectiveTo: nullableTime(row.MonitoringEffectiveTo)},
		CreatedAt:    row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func nullableText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func nullableBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}

func nullableUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: value, Valid: value != uuid.Nil}
}

func uuidFromPG(value pgtype.UUID) uuid.UUID {
	if !value.Valid {
		return uuid.Nil
	}
	return uuid.UUID(value.Bytes)
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()
	return &timestamp
}

var _ AssetReader = (*AssetRepository)(nil)
