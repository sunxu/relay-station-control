package api

import (
	"context"
	"math"
	"net/http"
	"time"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	poll "github.com/sunxu/relay-station-control/internal/inventorypoll"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func (s *Server) SetInventoryPollCapacityReader(reader store.InventoryPollCapacityReader, enabled bool, config poll.ValidatedConfig) {
	s.pollCapacity = reader
	s.pollCapacityEnabled = enabled
	s.pollCapacityConfig = config
}

func (s *Server) GetAccountInventoryPollCapacity(w http.ResponseWriter, r *http.Request) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	if s.pollCapacity == nil || s.pollCapacityConfig.EffectiveCapacity() < 1 {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	snapshot, err := s.pollCapacity.GetInventoryPollCapacity(ctx)
	if err != nil || snapshot.EligibleNodeCount < 0 || snapshot.EligibleNodeCount > math.MaxInt32 || snapshot.EvaluatedAt.IsZero() || snapshot.EvaluatedSlot.IsZero() || !snapshot.EvaluatedSlot.Equal(snapshot.EvaluatedAt.Truncate(5*time.Minute)) {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	c := s.pollCapacityConfig
	state := AccountInventoryPollCapacityStatusReady
	if !s.pollCapacityEnabled {
		state = AccountInventoryPollCapacityStatusDisabled
	} else if snapshot.EligibleNodeCount > int64(c.EffectiveCapacity()) {
		state = AccountInventoryPollCapacityStatusCapacityExceeded
	}
	writeJSON(w, http.StatusOK, AccountInventoryPollCapacity{
		Status: state, Enabled: s.pollCapacityEnabled, EligibleNodeCount: snapshot.EligibleNodeCount, EffectiveCapacity: int64(c.EffectiveCapacity()), Concurrency: int64(c.Concurrency()),
		RequestTimeoutMs: c.RequestTimeout().Milliseconds(), FinalizeTimeoutMs: c.FinalizeMargin().Milliseconds(), LifecycleTimeoutMs: c.LifecycleObserverBudget().Milliseconds(), ClaimTimeoutMs: c.ClaimBudget().Milliseconds(), DispatchMarginMs: c.DispatchMargin().Milliseconds(), PollStartGraceMs: c.PollStartGrace().Milliseconds(), EvaluatedAt: snapshot.EvaluatedAt, EvaluatedSlot: snapshot.EvaluatedSlot,
	})
}
