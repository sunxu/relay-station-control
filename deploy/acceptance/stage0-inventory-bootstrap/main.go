package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	assetcredentialruntime "github.com/sunxu/relay-station-control/internal/assetcredentialruntime"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	"github.com/sunxu/relay-station-control/internal/store"
)

const (
	bootstrapTimeout = 6 * time.Minute
	pollGrace        = 299 * time.Second
	pollPeriod       = 5 * time.Minute
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "INVENTORY_BOOTSTRAP_FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("BOOTSTRAP_PROCESS=PASS")
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), bootstrapTimeout)
	defer cancel()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("database configuration: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	fmt.Println("CURRENT_POSTGRES=PASS")

	keyPath := os.Getenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE")
	sealer, err := assetcredentialruntime.Load(ctx, pool, keyPath, uint32(os.Getuid()), slog.Default())
	if err != nil {
		return fmt.Errorf("Stage 0 runtime initialization: %w", err)
	}
	if !sealer.Available() {
		return errors.New("Stage 0 credential capability unavailable")
	}
	fmt.Println("STAGE0_RESOLVER=PASS")
	fmt.Println("K2_OPEN=PASS")

	resolver := assetcredentialruntime.NewResolver(pool, sealer)
	driver, err := cliproxyapi.NewDriver(cliproxyapi.DriverConfig{
		Management:    drivers.ManagementConfig{},
		AssetResolver: resolver,
	})
	if err != nil {
		return fmt.Errorf("node driver: %w", err)
	}
	registry, err := drivers.NewRegistry(drivers.Registration{
		NodeType:              drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []drivers.Capability{
			drivers.CapabilityManagementHealthRead,
			drivers.CapabilityManagementAccountInventoryRead,
		},
		Driver: driver,
	})
	if err != nil {
		return fmt.Errorf("node driver registry: %w", err)
	}
	repository, err := store.NewInventoryPollRepository(pool)
	if err != nil {
		return fmt.Errorf("inventory repository: %w", err)
	}
	scheduler, err := inventorypoll.NewScheduler(repository, inventorypoll.Config{
		PollStartGrace: pollGrace,
		Concurrency:    1,
	}, nil)
	if err != nil {
		return fmt.Errorf("inventory scheduler: %w", err)
	}
	worker, err := inventorypoll.NewWorker(repository, registry, inventorypoll.Config{
		PollStartGrace: pollGrace,
		Concurrency:    1,
	})
	if err != nil {
		return fmt.Errorf("inventory worker: %w", err)
	}
	scheduled, err := scheduleAfterCurrentSlot(ctx, pool, scheduler)
	if err != nil {
		return fmt.Errorf("schedule current poll: %w", err)
	}
	fmt.Printf("POLL_SCHEDULE_ELIGIBLE=%d\nPOLL_SCHEDULE_CREATED=%d\nPOLL_SCHEDULE_EXISTING=%d\n", scheduled.Eligible, scheduled.Created, scheduled.Existing)
	nodeID, err := uuid.Parse(os.Getenv("ACCOUNT_INVENTORY_NODE_ID"))
	if err != nil || nodeID == uuid.Nil {
		return errors.New("ACCOUNT_INVENTORY_NODE_ID is required")
	}
	pollID, err := pollRunID(ctx, pool, nodeID, scheduled.ScheduledAt)
	if err != nil {
		return err
	}
	fmt.Println("REAL_POLL_RUN=PASS")

	workerErr := make(chan error, 1)
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	go func() { workerErr <- worker.Run(workerCtx) }()
	if err := waitForFinalized(ctx, pool, pollID); err != nil {
		return err
	}
	stopWorker()
	select {
	case err := <-workerErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("inventory worker: %w", err)
		}
	case <-time.After(5 * time.Second):
		return errors.New("inventory worker did not stop")
	}

	var snapshots, providers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1`, pollID).Scan(&snapshots); err != nil {
		return fmt.Errorf("snapshot verification: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_inventory_provider_states WHERE current_poll_run_id=$1`, pollID).Scan(&providers); err != nil {
		return fmt.Errorf("provider source verification: %w", err)
	}
	if snapshots == 0 || providers == 0 {
		return fmt.Errorf("finalized poll produced no current snapshot linkage")
	}
	targetEmail := os.Getenv("ACCOUNT_INVENTORY_TARGET_EMAIL")
	if targetEmail == "" {
		return errors.New("ACCOUNT_INVENTORY_TARGET_EMAIL is required")
	}
	var targetCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM account_inventory_snapshot_items
		WHERE poll_run_id=$1 AND normalized_email=$2`, pollID, targetEmail).Scan(&targetCount); err != nil {
		return fmt.Errorf("target account verification: %w", err)
	}
	if targetCount == 0 {
		return errors.New("target account was not present in the real poll snapshot")
	}
	fmt.Println("POLL_RUN_STATUS=finalized")
	fmt.Println("ACCOUNT_INVENTORY_SOURCE_POLL=PASS")
	fmt.Println("TARGET_ACCOUNT_DISCOVERED=PASS")
	fmt.Println("BOOTSTRAP_PROCESS_EXITED=PASS")
	return nil
}

func scheduleAfterCurrentSlot(ctx context.Context, pool *pgxpool.Pool, scheduler *inventorypoll.Scheduler) (inventorypoll.ScheduleResult, error) {
	result, err := scheduler.ScheduleOnce(ctx)
	if err != nil {
		return inventorypoll.ScheduleResult{}, err
	}
	if result.Eligible > 0 {
		return result, nil
	}

	var databaseNow, currentSlot time.Time
	if err := pool.QueryRow(ctx, `
		SELECT clock_timestamp(), to_timestamp(floor(extract(epoch FROM clock_timestamp()) / 300) * 300)`).Scan(&databaseNow, &currentSlot); err != nil {
		return inventorypoll.ScheduleResult{}, fmt.Errorf("inventory slot clock: %w", err)
	}
	nextSlot := currentSlot.Add(pollPeriod)
	wait := time.Until(nextSlot) + 250*time.Millisecond
	if wait <= 0 {
		return inventorypoll.ScheduleResult{}, errors.New("inventory slot boundary calculation was not in the future")
	}
	fmt.Printf("POLL_WAITING_FOR_NEXT_SLOT=%s\n", nextSlot.UTC().Format(time.RFC3339))
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return inventorypoll.ScheduleResult{}, fmt.Errorf("waiting for inventory slot: %w", ctx.Err())
	case <-timer.C:
	}

	result, err = scheduler.ScheduleOnce(ctx)
	if err != nil {
		return inventorypoll.ScheduleResult{}, err
	}
	if result.Eligible == 0 {
		return inventorypoll.ScheduleResult{}, fmt.Errorf("no eligible nodes after next inventory slot (database_now=%s)", databaseNow.UTC().Format(time.RFC3339))
	}
	return result, nil
}

func pollRunID(ctx context.Context, pool *pgxpool.Pool, nodeID uuid.UUID, scheduledAt time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT poll_run_id
		FROM account_inventory_poll_runs
		WHERE instance_id=$1 AND scheduled_at=$2
		ORDER BY created_at DESC, poll_run_id DESC
		LIMIT 1`, nodeID, scheduledAt).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("poll run lookup: %w", err)
	}
	return id, nil
}

func waitForFinalized(ctx context.Context, pool *pgxpool.Pool, pollID uuid.UUID) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM account_inventory_poll_runs WHERE poll_run_id=$1`, pollID).Scan(&status); err != nil {
			return fmt.Errorf("poll status: %w", err)
		}
		if status == string(inventorypoll.StatusFinalized) {
			return nil
		}
		if status == string(inventorypoll.StatusAbandoned) {
			return errors.New("inventory poll abandoned")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("inventory poll did not finalize: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
