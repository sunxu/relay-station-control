package requestquality

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/drivers"
)

type TargetReader interface {
	ListRequestQualityTargets(context.Context) ([]drivers.NodeTarget, error)
}

// Collector runs in the existing single-Control process. No other consumer may
// drain the same Node queue. HTTP pop has no ACK: uncommitted events can be lost
// if this process crashes, even though committed event replay is idempotent.
type Collector struct {
	store                                         Store
	source                                        Source
	targets                                       TargetReader
	logger                                        *slog.Logger
	poll, maxBackoff, refresh, retention, timeout time.Duration
}

func NewCollector(store Store, source Source, targets TargetReader, logger *slog.Logger) (*Collector, error) {
	if store == nil || source == nil || targets == nil || logger == nil {
		return nil, errors.New("request quality collector configuration is invalid")
	}
	return &Collector{store: store, source: source, targets: targets, logger: logger,
		poll: time.Second, maxBackoff: 30 * time.Second, refresh: 30 * time.Second, retention: time.Hour, timeout: 30 * time.Second}, nil
}

type nodeLoop struct {
	target drivers.NodeTarget
	cancel context.CancelFunc
	done   chan struct{}
}

func (c *Collector) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("request quality context is required")
	}
	loops := make(map[uuid.UUID]nodeLoop)
	stop := func(id uuid.UUID) { run := loops[id]; run.cancel(); <-run.done; delete(loops, id) }
	defer func() {
		for id := range loops {
			stop(id)
		}
	}()
	nextRetention := time.Time{}
	for ctx.Err() == nil {
		readCtx, cancel := context.WithTimeout(ctx, c.timeout)
		targets, err := c.targets.ListRequestQualityTargets(readCtx)
		cancel()
		desired := make(map[uuid.UUID]drivers.NodeTarget, len(targets))
		if err == nil {
			for _, target := range targets {
				if target.InstanceID == uuid.Nil {
					err = errors.New("invalid target")
					break
				}
				if _, exists := desired[target.InstanceID]; exists {
					err = errors.New("duplicate target")
					break
				}
				desired[target.InstanceID] = target
			}
		}
		if err != nil {
			c.logger.Warn("request quality target read failed", "component", "account_request_quality")
			// Fail closed when current monitoring/target configuration cannot be read.
			desired = map[uuid.UUID]drivers.NodeTarget{}
		}
		for id, run := range loops {
			target, ok := desired[id]
			if !ok || !reflect.DeepEqual(run.target, target) {
				stop(id)
			}
		}
		for id, target := range desired {
			if _, ok := loops[id]; ok {
				continue
			}
			nodeCtx, nodeCancel := context.WithCancel(ctx)
			done := make(chan struct{})
			loops[id] = nodeLoop{target: target, cancel: nodeCancel, done: done}
			go func() { defer close(done); c.collectNode(nodeCtx, target) }()
		}
		if !time.Now().Before(nextRetention) {
			retentionCtx, retentionCancel := context.WithTimeout(ctx, c.timeout)
			var err error
			for retentionCtx.Err() == nil {
				var deleted int64
				deleted, err = c.store.DeleteOldRequestEvents(retentionCtx)
				if err != nil || deleted == 0 {
					break
				}
			}
			if err == nil {
				err = retentionCtx.Err()
			}
			retentionCancel()
			if err != nil && ctx.Err() == nil {
				c.logger.Warn("request quality retention failed", "component", "account_request_quality")
			}
			nextRetention = time.Now().Add(c.retention)
		}
		if !wait(ctx, c.refresh) {
			break
		}
	}
	return ctx.Err()
}

func (c *Collector) collectNode(ctx context.Context, target drivers.NodeTarget) {
	delay := c.poll
	for ctx.Err() == nil {
		lookupCtx, cancel := context.WithTimeout(ctx, c.timeout)
		identities, lookupErr := c.source.CurrentIdentities(lookupCtx, target)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if lookupErr != nil {
			identities = nil
			c.logger.Warn("request quality identity lookup unavailable", "component", "account_request_quality", "node_id", target.InstanceID)
		}
		pullCtx, pullCancel := context.WithTimeout(ctx, c.timeout)
		raw, err := c.source.PopUsage(pullCtx, target)
		pullCancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Warn("request quality queue pull failed", "component", "account_request_quality", "node_id", target.InstanceID)
			if !wait(ctx, delay) {
				return
			}
			delay = min(delay*2, c.maxBackoff)
			continue
		}
		batch := make([]Event, 0, len(raw))
		invalid := 0
		for _, payload := range raw {
			event, err := Normalize(target.InstanceID, payload, identities)
			if err != nil {
				invalid++
				continue
			}
			batch = append(batch, event)
		}
		if invalid > 0 {
			c.logger.Warn("request quality malformed events", "component", "account_request_quality", "node_id", target.InstanceID, "count", invalid)
		}
		// Retry the SAME normalized batch. Never pop again until commit succeeds.
		for len(batch) > 0 && ctx.Err() == nil {
			writeCtx, writeCancel := context.WithTimeout(ctx, c.timeout)
			err = c.store.InsertRequestEvents(writeCtx, batch)
			writeCancel()
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return
			}
			c.logger.Warn("request quality event write failed", "component", "account_request_quality", "node_id", target.InstanceID)
			if !wait(ctx, delay) {
				return
			}
			delay = min(delay*2, c.maxBackoff)
		}
		delay = c.poll
		if !wait(ctx, c.poll) {
			return
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
