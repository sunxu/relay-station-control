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

func validQualityBasicStatus(value string) bool {
	switch value {
	case "reported_active", "disabled", "unavailable", "error", "unknown":
		return true
	default:
		return false
	}
}

type nodeAccountQualityCursor struct {
	InstanceID      uuid.UUID `json:"instance_id"`
	Window          string    `json:"window"`
	Provider        string    `json:"provider"`
	Quality         string    `json:"quality"`
	Lifecycle       string    `json:"lifecycle,omitempty"`
	BasicStatus     string    `json:"basic_status,omitempty"`
	Email           string    `json:"email,omitempty"`
	AfterAccountKey string    `json:"after_account_key"`
}

func (s *Server) GetNodeAccountQuality(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params GetNodeAccountQualityParams) {
	s.getNodeAccountQuality(w, r, instanceID, params, "", uuid.Nil, nil)
}

func (s *Server) QueryNodeAccountQuality(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params QueryNodeAccountQualityParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, string(params.XCSRFToken))
	if !ok {
		return
	}
	var body NodeAccountQualityQueryRequest
	if !decodeJSONWithLimit(w, r, &body, 16<<10) {
		return
	}
	getParams := GetNodeAccountQualityParams{Cursor: body.Cursor}
	limit := 25
	if body.Limit != nil {
		limit = *body.Limit
	}
	getParams.Limit = &limit
	if body.Provider != nil && *body.Provider != "" {
		getParams.Provider = body.Provider
	}
	if body.Quality != nil {
		value := GetNodeAccountQualityParamsQuality(*body.Quality)
		getParams.Quality = &value
	}
	if body.Lifecycle != nil {
		value := GetNodeAccountQualityParamsLifecycle(*body.Lifecycle)
		getParams.Lifecycle = &value
	}
	if body.BasicStatus != nil {
		value := GetNodeAccountQualityParamsBasicStatus(*body.BasicStatus)
		getParams.BasicStatus = &value
	}
	if body.Window != nil {
		value := GetNodeAccountQualityParamsWindow(*body.Window)
		getParams.Window = &value
	}
	email := ""
	if body.Email != nil {
		email = string(*body.Email)
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	audit := &assetstore.AccountQualityViewAudit{ActorAdminID: session.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID}
	s.getNodeAccountQuality(w, r, instanceID, getParams, email, session.AdminID, audit)
}

func (s *Server) getNodeAccountQuality(w http.ResponseWriter, r *http.Request, instanceID NodeInstanceId, params GetNodeAccountQualityParams, emailOverride string, actorAdminID uuid.UUID, audit *assetstore.AccountQualityViewAudit) {
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
	lifecycle := ""
	if params.Lifecycle != nil {
		lifecycle = string(*params.Lifecycle)
	}
	basicStatus := ""
	if params.BasicStatus != nil {
		basicStatus = string(*params.BasicStatus)
	}
	email := emailOverride
	if params.Lifecycle != nil && lifecycle != "present" && lifecycle != "suspected_missing" && lifecycle != "missing" && lifecycle != "out_of_scope" {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	limit := 25
	if params.Limit != nil {
		limit = *params.Limit
	}
	if nodeID == uuid.Nil || (window != "15m" && window != "1h") || (provider != "" && !accountQualityProviderPattern.MatchString(provider)) || (quality != "" && quality != "good" && quality != "degraded" && quality != "bad" && quality != "unknown") || (basicStatus != "" && !validQualityBasicStatus(basicStatus)) || (email != "" && !validNormalizedAccountEmail(email)) || limit < 1 || limit > 100 {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	if actorAdminID != uuid.Nil && s.accountInventoryCursor == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	after := ""
	if params.Cursor != nil {
		if len(*params.Cursor) == 0 || len(*params.Cursor) > 2048 {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		if actorAdminID != uuid.Nil && s.accountInventoryCursor != nil {
			decoded, err := s.accountInventoryCursor.Decode(*params.Cursor, actorAdminID, nodeID, assetstore.AccountInventoryCursorFilters{Provider: provider, Lifecycle: lifecycle, BasicStatus: basicStatus, Email: email, Window: window, Quality: quality, Scope: "node-account-quality"})
			if err != nil {
				s.writeError(w, r, authn.ErrInvalid)
				return
			}
			after = decoded
		} else {
			raw, err := base64.RawURLEncoding.DecodeString(*params.Cursor)
			var cursor nodeAccountQualityCursor
			if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.InstanceID != nodeID || cursor.Window != window || cursor.Provider != provider || cursor.Quality != quality || cursor.Lifecycle != lifecycle || cursor.BasicStatus != basicStatus || cursor.Email != email || cursor.AfterAccountKey == "" || len(cursor.AfterAccountKey) > 385 || !strings.Contains(cursor.AfterAccountKey, ":") {
				s.writeError(w, r, authn.ErrInvalid)
				return
			}
			after = cursor.AfterAccountKey
		}
	}
	if s.accountQuality == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	query := assetstore.AccountQualityQuery{InstanceID: nodeID, Window: windowDuration, Provider: provider, Quality: quality, Lifecycle: lifecycle, BasicStatus: basicStatus, Email: email, AfterAccountKey: after, Limit: limit}
	var page assetstore.AccountQualityPage
	var err error
	if audit != nil {
		reader, ok := s.accountQuality.(interface {
			ListAccountQualityAndAudit(context.Context, assetstore.AccountQualityQuery, assetstore.AccountQualityViewAudit) (assetstore.AccountQualityPage, error)
		})
		if !ok {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		page, err = reader.ListAccountQualityAndAudit(ctx, query, *audit)
	} else {
		page, err = s.accountQuality.ListAccountQuality(ctx, query)
	}
	if err != nil {
		s.topologyReadError(w, r, err)
		return
	}
	// One bounded read for the page; non-Antigravity accounts remain outside this capability.
	availability := make(map[string]assetstore.AccountAvailability)
	keys := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		if item.Provider == "antigravity" {
			keys = append(keys, item.AccountKey)
		}
	}
	if len(keys) > 0 {
		if s.accountAvailability == nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
		availability, err = s.accountAvailability.BatchAccountAvailability(ctx, nodeID, keys)
		if err != nil {
			s.topologyReadError(w, r, err)
			return
		}
		// A missing row could be a concurrent Inventory change; never fabricate Unknown.
		for _, key := range keys {
			if _, ok := availability[key]; !ok {
				s.writeError(w, r, authn.ErrUnavailable)
				return
			}
		}
	}
	items := make([]NodeAccountQualityItem, 0, len(page.Items))
	for _, item := range page.Items {
		var availabilityItem *AccountAvailability
		if value, ok := availability[item.AccountKey]; ok {
			availabilityItem = &AccountAvailability{State: AccountAvailabilityState(value.State), Reason: AccountAvailabilityReason(value.Reason), Since: value.Since}
		}
		stats := item.Stats
		var lastClass *NodeAccountQualityItemLastFailureClass
		if stats.LastFailureClass != nil {
			v := NodeAccountQualityItemLastFailureClass(*stats.LastFailureClass)
			lastClass = &v
		}
		recent := make([]NodeAccountRequestHistoryItem, 0, len(item.RecentRequests))
		for _, event := range item.RecentRequests {
			var class *NodeAccountRequestHistoryItemFailureClass
			if event.FailureClass != nil {
				v := NodeAccountRequestHistoryItemFailureClass(*event.FailureClass)
				class = &v
			}
			recent = append(recent, NodeAccountRequestHistoryItem{OccurredAt: event.OccurredAt, Model: event.Model, Success: event.Success, FailureClass: class, DurationMs: event.DurationMS, RequestId: event.RequestID})
		}
		items = append(items, NodeAccountQualityItem{AccountKey: item.AccountKey, Email: item.Email, Provider: item.Provider, Quality: NodeAccountQualityItemQuality(item.Quality), RequestCount: stats.RequestCount, SuccessCount: stats.SuccessCount, FailureCount: stats.FailureCount, SuccessRate: stats.SuccessRate, P95LatencyMs: stats.P95LatencyMS, LastSuccessAt: stats.LastSuccessAt, LastFailureAt: stats.LastFailureAt, LastFailureClass: lastClass, Inventory: accountInventoryResponseItem(item.Inventory), RecentRequests: recent, Availability: availabilityItem})
	}
	var next *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		if actorAdminID != uuid.Nil && s.accountInventoryCursor != nil {
			value, err := s.accountInventoryCursor.Encode(actorAdminID, nodeID, assetstore.AccountInventoryCursorFilters{Provider: provider, Lifecycle: lifecycle, BasicStatus: basicStatus, Email: email, Window: window, Quality: quality, Scope: "node-account-quality"}, last.AccountKey)
			if err != nil {
				s.writeError(w, r, authn.ErrUnavailable)
				return
			}
			next = &value
		} else {
			raw, err := json.Marshal(nodeAccountQualityCursor{InstanceID: nodeID, Window: window, Provider: provider, Quality: quality, Lifecycle: lifecycle, BasicStatus: basicStatus, Email: email, AfterAccountKey: last.AccountKey})
			if err != nil {
				s.writeError(w, r, authn.ErrUnavailable)
				return
			}
			v := base64.RawURLEncoding.EncodeToString(raw)
			next = &v
		}
	}
	writeJSON(w, http.StatusOK, NodeAccountQualityResponse{InstanceId: nodeID, Window: NodeAccountQualityResponseWindow(window), Items: items, NextCursor: next})
}
