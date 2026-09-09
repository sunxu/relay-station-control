package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/requestquality"
)

type AccountRequestQualityRepository struct{ pool *pgxpool.Pool }

type accountRequestQualityWireEvent struct {
	EventHash         string    `json:"event_hash"`
	RequestID         string    `json:"request_id"`
	NodeID            uuid.UUID `json:"node_id"`
	Provider          string    `json:"provider"`
	AccountKey        *string   `json:"account_key"`
	Model             string    `json:"model"`
	OccurredAt        time.Time `json:"occurred_at"`
	DurationMS        *int64    `json:"duration_ms"`
	Success           bool      `json:"success"`
	FailureClass      *string   `json:"failure_class"`
	AuthFailureReason *string   `json:"auth_failure_reason"`
}

var _ requestquality.Store = (*AccountRequestQualityRepository)(nil)

func NewAccountRequestQualityRepository(pool *pgxpool.Pool) (*AccountRequestQualityRepository, error) {
	if pool == nil {
		return nil, errors.New("store: account request quality database is unavailable")
	}
	return &AccountRequestQualityRepository{pool: pool}, nil
}

func (r *AccountRequestQualityRepository) InsertRequestEvents(ctx context.Context, events []requestquality.Event) error {
	if r == nil || r.pool == nil {
		return errors.New("store: account request quality database is unavailable")
	}
	if len(events) == 0 {
		return nil
	}
	wire := make([]accountRequestQualityWireEvent, len(events))
	for i, event := range events {
		wire[i] = accountRequestQualityWireEvent{EventHash: event.EventHash, RequestID: event.RequestID, NodeID: event.NodeID, Provider: event.Provider, AccountKey: event.AccountKey, Model: event.Model, OccurredAt: event.OccurredAt, DurationMS: event.DurationMS, Success: event.Success, FailureClass: event.FailureClass, AuthFailureReason: event.AuthFailureReason}
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `SELECT public.control_insert_account_request_quality_events_v2($1::jsonb)`, data)
	return err
}

func (r *AccountRequestQualityRepository) DeleteOldRequestEvents(ctx context.Context) (int64, error) {
	if r == nil || r.pool == nil {
		return 0, errors.New("store: account request quality database is unavailable")
	}
	var deleted int64
	err := r.pool.QueryRow(ctx, `SELECT public.control_delete_old_account_request_quality_events_v1()`).Scan(&deleted)
	return deleted, err
}

func (r *AccountRequestQualityRepository) AccountQuality(ctx context.Context, nodeID uuid.UUID, accountKey string, window time.Duration) (requestquality.Quality, error) {
	return r.quality(ctx, nodeID, &accountKey, nil, window)
}

func (r *AccountRequestQualityRepository) NodeProviderQuality(ctx context.Context, nodeID uuid.UUID, provider string, window time.Duration) (requestquality.Quality, error) {
	var providerPtr *string
	if provider != "" {
		providerPtr = &provider
	}
	return r.quality(ctx, nodeID, nil, providerPtr, window)
}

func (r *AccountRequestQualityRepository) quality(ctx context.Context, nodeID uuid.UUID, accountKey, provider *string, window time.Duration) (requestquality.Quality, error) {
	if r == nil || r.pool == nil || nodeID == uuid.Nil || (window != 15*time.Minute && window != time.Hour) {
		return requestquality.Quality{}, errors.New("store: invalid account request quality query")
	}
	var q requestquality.Quality
	var successRate, p95 *float64
	var lastSuccess, lastFailure *time.Time
	var lastClass *string
	err := r.pool.QueryRow(ctx, `SELECT request_count, success_count, failure_count, unresolved_request_count, success_rate, p95_latency_ms, last_success_at, last_failure_at, last_failure_class FROM public.control_query_account_request_quality_v1($1,$2,$3,$4::interval)`, nodeID, accountKey, provider, fmt.Sprintf("%.6f seconds", window.Seconds())).Scan(&q.RequestCount, &q.SuccessCount, &q.FailureCount, &q.UnresolvedRequestCount, &successRate, &p95, &lastSuccess, &lastFailure, &lastClass)
	if err != nil {
		return requestquality.Quality{}, err
	}
	q.SuccessRate, q.P95LatencyMS, q.LastSuccessAt, q.LastFailureAt, q.LastFailureClass = successRate, p95, lastSuccess, lastFailure, lastClass
	return q, nil
}

// ListRequestQualityTargets returns the currently monitored CLIProxy targets.
func (r *AccountRequestQualityRepository) ListRequestQualityTargets(ctx context.Context) ([]drivers.NodeTarget, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("store: account request quality database is unavailable")
	}
	rows, err := r.pool.Query(ctx, `SELECT instance_id, node_type, driver_contract_version, management_endpoint, reader_secret_ref, capabilities FROM public.control_query_account_request_quality_targets_v1()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []drivers.NodeTarget
	for rows.Next() {
		var id uuid.UUID
		var nodeType, contract, endpoint string
		var secret *string
		var caps []string
		if err := rows.Scan(&id, &nodeType, &contract, &endpoint, &secret, &caps); err != nil {
			return nil, err
		}
		ref := ""
		if secret != nil {
			ref = *secret
		}
		capabilities := make([]drivers.Capability, len(caps))
		for i := range caps {
			capabilities[i] = drivers.Capability(caps[i])
		}
		targets = append(targets, drivers.NodeTarget{InstanceID: id, NodeType: drivers.NodeType(nodeType), DriverContractVersion: drivers.DriverContractVersion(contract), ManagementEndpoint: endpoint, ReaderSecretReference: drivers.NewSecretReference(ref), Capabilities: capabilities})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}
