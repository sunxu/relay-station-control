package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

var accountQualityProviderPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type nodeAccountQualityCursor struct {
	InstanceID      uuid.UUID `json:"instance_id"`
	Window          string    `json:"window"`
	Provider        string    `json:"provider"`
	Quality         string    `json:"quality"`
	AfterAccountKey string    `json:"after_account_key"`
}

func (s *Server) GetNodeAccountQuality(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params GetNodeAccountQualityParams) {
	s.prepare(w, r, true)
	if !s.authorizeTopologyRead(w, r) {
		return
	}
	nodeID := uuid.UUID(instanceID)
	window := "15m"
	if params.Window != nil {
		window = string(*params.Window)
	}
	windowDuration := 15 * time.Minute
	if window == "1h" {
		windowDuration = time.Hour
	}
	provider := ""
	if params.Provider != nil {
		provider = *params.Provider
	}
	quality := ""
	if params.Quality != nil {
		quality = string(*params.Quality)
	}
	limit := 25
	if params.Limit != nil {
		limit = *params.Limit
	}
	if nodeID == uuid.Nil || (window != "15m" && window != "1h") || (provider != "" && !accountQualityProviderPattern.MatchString(provider)) || (quality != "" && quality != "good" && quality != "degraded" && quality != "bad" && quality != "unknown") || limit < 1 || limit > 100 {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	after := ""
	if params.Cursor != nil {
		if len(*params.Cursor) == 0 || len(*params.Cursor) > 2048 {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(*params.Cursor)
		var cursor nodeAccountQualityCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.InstanceID != nodeID || cursor.Window != window || cursor.Provider != provider || cursor.Quality != quality || cursor.AfterAccountKey == "" || len(cursor.AfterAccountKey) > 385 || !strings.Contains(cursor.AfterAccountKey, ":") {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		after = cursor.AfterAccountKey
	}
	if s.accountQuality == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.accountQuality.ListAccountQuality(ctx, assetstore.AccountQualityQuery{InstanceID: nodeID, Window: windowDuration, Provider: provider, Quality: quality, AfterAccountKey: after, Limit: limit})
	if err != nil {
		s.topologyReadError(w, r, err)
		return
	}
	items := make([]NodeAccountQualityItem, 0, len(page.Items))
	for _, item := range page.Items {
		stats := item.Stats
		var lastClass *NodeAccountQualityItemLastFailureClass
		if stats.LastFailureClass != nil {
			v := NodeAccountQualityItemLastFailureClass(*stats.LastFailureClass)
			lastClass = &v
		}
		items = append(items, NodeAccountQualityItem{AccountKey: item.AccountKey, Email: item.Email, Provider: item.Provider, Quality: NodeAccountQualityItemQuality(item.Quality), RequestCount: stats.RequestCount, SuccessCount: stats.SuccessCount, FailureCount: stats.FailureCount, SuccessRate: stats.SuccessRate, P95LatencyMs: stats.P95LatencyMS, LastSuccessAt: stats.LastSuccessAt, LastFailureAt: stats.LastFailureAt, LastFailureClass: lastClass})
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		raw, err := json.Marshal(nodeAccountQualityCursor{InstanceID: nodeID, Window: window, Provider: provider, Quality: quality, AfterAccountKey: last.AccountKey})
		if err != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		v := base64.RawURLEncoding.EncodeToString(raw)
		next = &v
	}
	writeJSON(w, http.StatusOK, NodeAccountQualityResponse{InstanceId: nodeID, Window: NodeAccountQualityResponseWindow(window), Items: items, NextCursor: next})
}
