package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	controlpoll "github.com/sunxu/relay-station-control/internal/inventorypoll"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

const authenticationResponseFloor = 250 * time.Millisecond

type Server struct {
	pollCapacity                  assetstore.InventoryPollCapacityReader
	pollCapacityConfig            controlpoll.ValidatedConfig
	pollCapacityEnabled           bool
	version                       string
	service                       *authn.Service
	resolver                      *authn.SourceResolver
	assets                        assetstore.AssetReader
	assetMetrics                  *AssetMetrics
	nodeCursor                    *assetstore.NodeCursorCodec
	jobs                          assetstore.JobReader
	jobCursor                     *assetstore.JobCursorCodec
	accountInventory              assetstore.AccountInventoryReader
	accountInventoryCursor        *assetstore.AccountInventoryCursorCodec
	accountInventoryMetrics       *AccountInventoryMetrics
	relayBindings                 *assetstore.RelayBindingRepository
	crossNodeDuplicateOccurrences assetstore.CrossNodeDuplicateOwnershipOccurrenceReader
	nodeDuplicateHistory          assetstore.CrossNodeDuplicateOwnershipHistoryReader
	providerStates                assetstore.AccountInventoryProviderStateReader
	accountQuality                interface {
		ListAccountQuality(context.Context, assetstore.AccountQualityQuery) (assetstore.AccountQualityPage, error)
	}
	accountRequestHistory interface {
		ListAccountRequestHistory(context.Context, assetstore.AccountRequestHistoryQuery) (assetstore.AccountRequestHistoryPage, error)
	}
}

type requestIDContextKey struct{}

var _ ServerInterface = (*Server)(nil)

func NewServer(version string) *Server {
	return &Server{version: version, resolver: authn.NewSourceResolver(nil)}
}

func NewAuthenticatedServer(version string, service *authn.Service) *Server {
	server := &Server{version: version, service: service, resolver: authn.NewSourceResolver(nil)}
	if service != nil {
		server.resolver = authn.NewSourceResolver(service.Config().TrustedProxies)
	}
	return server
}

func NewAuthenticatedServerWithAssets(version string, service *authn.Service, assets assetstore.AssetReader, metrics *AssetMetrics) (*Server, error) {
	server := NewAuthenticatedServer(version, service)
	if service == nil || assets == nil {
		return nil, errors.New("api: asset registry is unavailable")
	}
	codec, err := assetstore.NewNodeCursorCodec(service.Config().Keyring)
	if err != nil {
		return nil, errors.New("api: asset cursor initialization failed")
	}
	if metrics == nil {
		metrics = NewAssetMetrics()
	}
	server.assets = assets
	server.assetMetrics = metrics
	server.nodeCursor = codec
	return server, nil
}

func NewAuthenticatedServerWithAssetsAndJobs(version string, service *authn.Service, assets assetstore.AssetReader, metrics *AssetMetrics, jobs assetstore.JobReader) (*Server, error) {
	server, err := NewAuthenticatedServerWithAssets(version, service, assets, metrics)
	if err != nil {
		return nil, err
	}
	if jobs == nil {
		return nil, errors.New("api: durable job reader is unavailable")
	}
	codec, err := assetstore.NewJobCursorCodec(service.Config().Keyring)
	if err != nil {
		return nil, errors.New("api: durable job cursor initialization failed")
	}
	server.jobs = jobs
	server.jobCursor = codec
	return server, nil
}

func NewAuthenticatedServerWithAssetsJobsAndAccountInventory(
	version string,
	service *authn.Service,
	assets assetstore.AssetReader,
	metrics *AssetMetrics,
	jobs assetstore.JobReader,
	accountInventory assetstore.AccountInventoryReader,
) (*Server, error) {
	server, err := NewAuthenticatedServerWithAssetsAndJobs(version, service, assets, metrics, jobs)
	if err != nil {
		return nil, err
	}
	if accountInventory == nil {
		return nil, errors.New("api: account inventory reader is unavailable")
	}
	codec, err := assetstore.NewAccountInventoryCursorCodec(service.Config().Keyring)
	if err != nil {
		return nil, errors.New("api: account inventory cursor initialization failed")
	}
	server.accountInventory = accountInventory
	server.accountInventoryCursor = codec
	server.accountInventoryMetrics = NewAccountInventoryMetrics()
	return server, nil
}

func (s *Server) AccountInventoryMetrics() *AccountInventoryMetrics {
	return s.accountInventoryMetrics
}

func (s *Server) SetRelayBindingRepository(repository *assetstore.RelayBindingRepository) {
	s.relayBindings = repository
}

// SetCrossNodeDuplicateOwnershipOccurrenceReader wires the Phase 5 read-only
// occurrence store. Additive/optional, like SetRelayBindingRepository: read
// handlers return authn.ErrUnavailable until this is set.
func (s *Server) SetCrossNodeDuplicateOwnershipOccurrenceReader(reader assetstore.CrossNodeDuplicateOwnershipOccurrenceReader) {
	s.crossNodeDuplicateOccurrences = reader
}

func (s *Server) GetHealthz(w http.ResponseWriter, r *http.Request) {
	s.prepare(w, r, false)
	writeJSON(w, http.StatusOK, HealthResponse{Status: Ok, Version: s.version})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) bool {
	if s.service != nil {
		return true
	}
	s.writeError(w, r, authn.ErrUnavailable)
	return false
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) (authn.RequestMeta, bool) {
	address, err := s.resolver.ClientAddress(r)
	if err != nil {
		s.writeError(w, r, authn.ErrInvalid)
		return authn.RequestMeta{}, false
	}
	meta, err := s.service.RequestMeta(address, s.requestID(r))
	if err != nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return authn.RequestMeta{}, false
	}
	w.Header().Set("X-Request-ID", meta.RequestID)
	return meta, true
}

func (s *Server) requireSession(w http.ResponseWriter, r *http.Request, unsafe bool, csrf string) (authn.Session, bool) {
	if !s.ready(w, r) {
		return authn.Session{}, false
	}
	cookie, err := r.Cookie(s.sessionCookieName())
	if err != nil {
		s.auditSecurityRejection(r, nil, authn.AuditAuthorization, authn.ErrorCodeUnauthorized)
		s.clearSessionCookie(w)
		s.writeError(w, r, authn.ErrUnauthorized)
		return authn.Session{}, false
	}
	session, err := s.service.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		var serviceErr *authn.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.Code != authn.ErrorCodeInternal {
			s.auditSecurityRejection(r, nil, authn.AuditAuthorization, serviceErr.Code)
			s.clearSessionCookie(w)
		}
		s.writeError(w, r, err)
		return authn.Session{}, false
	}
	if unsafe && (!s.sameOrigin(r) || s.service.VerifyCSRF(r.Context(), session, csrf) != nil) {
		s.auditSecurityRejection(r, &session, authn.AuditCSRF, authn.ErrorCodeCSRFRejected)
		s.writeError(w, r, authn.ErrCSRF)
		return authn.Session{}, false
	}
	return session, true
}

func (s *Server) auditSecurityRejection(r *http.Request, session *authn.Session, action authn.AuditAction, code authn.ErrorCode) {
	if s.service == nil {
		return
	}
	address, err := s.resolver.ClientAddress(r)
	if err != nil {
		return
	}
	meta, err := s.service.RequestMeta(address, s.requestID(r))
	if err != nil {
		return
	}
	s.service.AuditSecurityRejection(r.Context(), action, session, code, meta)
}

func (s *Server) sameOrigin(r *http.Request) bool {
	expectedScheme := "https"
	if s.service.Config().Environment == authn.EnvironmentDev && !s.service.Config().CookieSecure {
		expectedScheme = "http"
	}
	check := func(value string) bool {
		parsed, err := url.Parse(value)
		return err == nil && parsed.Scheme == expectedScheme && parsed.Host == r.Host
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return check(origin)
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		return check(referer)
	}
	return s.service.Config().Environment == authn.EnvironmentDev
}

func (s *Server) prepare(w http.ResponseWriter, r *http.Request, sensitive bool) {
	w.Header().Set("Content-Type", "application/json")
	if sensitive {
		w.Header().Set("Cache-Control", "no-store")
	}
	if w.Header().Get("X-Request-ID") == "" {
		w.Header().Set("X-Request-ID", s.requestID(r))
	}
}

func (s *Server) requestID(r *http.Request) string {
	if requestID, ok := r.Context().Value(requestIDContextKey{}).(string); ok && requestID != "" {
		return requestID
	}
	requestID := s.resolver.RequestID(r)
	*r = *r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, requestID))
	return requestID
}

func (s *Server) sessionCookieName() string {
	if s.service != nil && s.service.Config().Environment == authn.EnvironmentDev && !s.service.Config().CookieSecure {
		return authn.DevSessionCookieName
	}
	return authn.SessionCookieName
}
func (s *Server) challengeCookieName() string {
	if s.service != nil && s.service.Config().Environment == authn.EnvironmentDev && !s.service.Config().CookieSecure {
		return authn.DevChallengeCookieName
	}
	return authn.ChallengeCookieName
}
func (s *Server) setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, sessionCookie(s.sessionCookieName(), value, s.service.Config().CookieSecure))
}

func sessionCookie(name, value string, secure bool) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", Secure: secure, HttpOnly: true, SameSite: http.SameSiteStrictMode}
}
func (s *Server) setChallengeCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: s.challengeCookieName(), Value: value, Path: "/api/auth", MaxAge: int((5 * time.Minute).Seconds()), Secure: s.service.Config().CookieSecure, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookieName(), Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), Secure: s.service != nil && s.service.Config().CookieSecure, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
func (s *Server) clearChallengeCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.challengeCookieName(), Value: "", Path: "/api/auth", MaxAge: -1, Expires: time.Unix(1, 0), Secure: s.service != nil && s.service.Config().CookieSecure, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, ErrorCodeInternalError, "The request could not be completed."
	var serviceErr *authn.ServiceError
	if errors.As(err, &serviceErr) {
		status = serviceErr.HTTPStatus
		switch serviceErr.Code {
		case authn.ErrorCodeAuthenticationFailed:
			code, message = ErrorCodeAuthenticationFailed, "Authentication failed."
		case authn.ErrorCodeRateLimited:
			code, message = ErrorCodeRateLimited, "Authentication is temporarily unavailable."
		case authn.ErrorCodeInvalidRequest:
			code, message = ErrorCodeValidationFailed, "The request is invalid."
		case authn.ErrorCodeUnauthorized:
			code, message = ErrorCodeUnauthorized, "Authentication is required."
		case authn.ErrorCodeForbidden:
			code, message = ErrorCodeForbidden, "The operation is not permitted."
		case authn.ErrorCodeCSRFRejected:
			code, message = ErrorCodeCsrfInvalid, "The request proof is invalid."
		case authn.ErrorCodeReauthentication:
			code, message = ErrorCodeReauthenticationRequired, "Recent reauthentication is required."
		case authn.ErrorCodeBootstrapClosed:
			code, message = ErrorCodeBootstrapUnavailable, "Bootstrap is unavailable."
		case authn.ErrorCodeInternal:
			code, message = ErrorCodeTemporarilyUnavailable, "The service is temporarily unavailable."
		}
		if serviceErr.Kind == "self_disable_forbidden" {
			code = ErrorCodeAdministratorSelfDisableForbidden
		}
		if serviceErr.Kind == "last_administrator_protected" {
			code = ErrorCodeLastAdministratorProtected
		}
	}
	requestID := w.Header().Get("X-Request-ID")
	if requestID == "" {
		requestID = s.requestID(r)
		w.Header().Set("X-Request-ID", requestID)
	}
	writeJSON(w, status, ErrorResponse{Code: code, Message: message, RequestId: requestID})
}

func (s *Server) PrepareGeneratedError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/account-inventory/query" && s.accountInventoryMetrics != nil {
		startedAt := time.Now()
		metricWriter := &accountInventoryMetricResponseWriter{ResponseWriter: w}
		w = metricWriter
		defer func() {
			result, errorCode := accountInventoryMetricStatus(metricWriter.status)
			_ = s.accountInventoryMetrics.record(result, errorCode, 0, time.Since(startedAt))
		}()
	}
	s.prepare(w, r, true)
	var headerErr *RequiredHeaderError
	if errors.As(err, &headerErr) && headerErr.ParamName == "X-CSRF-Token" {
		s.auditSecurityRejection(r, nil, authn.AuditCSRF, authn.ErrorCodeCSRFRejected)
		s.writeError(w, r, authn.ErrCSRF)
		return
	}
	s.writeError(w, r, authn.ErrInvalid)
}

func (s *Server) authenticationDelay(ctx context.Context, start time.Time) {
	var randomByte [1]byte
	_, _ = rand.Read(randomByte[:])
	budget := authenticationResponseFloor + time.Duration(randomByte[0]%26)*time.Millisecond
	remaining := budget - time.Since(start)
	if remaining <= 0 {
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	return decodeJSONWithLimit(w, r, target, 1<<20)
}

func decodeJSONWithLimit(w http.ResponseWriter, r *http.Request, target any, maximum int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maximum)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorCodeValidationFailed, Message: "The request is invalid.", RequestId: w.Header().Get("X-Request-ID")})
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorCodeValidationFailed, Message: "The request is invalid.", RequestId: w.Header().Get("X-Request-ID")})
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func administrator(value authn.Admin) Administrator {
	return Administrator{Id: value.ID, LoginName: value.LoginName, DisplayName: value.DisplayName, AuthSource: AdministratorAuthSource(value.AuthSource), Role: AdministratorRole(value.Role), Status: AdministratorStatus(value.Status), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ActivatedAt: value.ActivatedAt, DisabledAt: value.DisabledAt, LastLoginAt: value.LastLoginAt}
}
func sessionResponse(value authn.Session) SessionResponse {
	var method *MfaMethod
	if value.MFAMethod != authn.MFAMethodNone {
		converted := MfaMethod(value.MFAMethod)
		method = &converted
	}
	var reauth *time.Time
	if value.ReauthenticatedAt != nil {
		until := value.ReauthenticatedAt.Add(5 * time.Minute)
		reauth = &until
	}
	var csrf *string
	if value.CSRFToken != "" {
		proof := value.CSRFToken
		csrf = &proof
	}
	admin := authn.Admin{ID: value.AdminID, LoginName: value.LoginName, DisplayName: value.DisplayName, AuthSource: "local", Role: value.Role, Status: value.Status, CreatedAt: value.CreatedAt, UpdatedAt: value.CreatedAt}
	return SessionResponse{State: Authenticated, Administrator: administrator(admin), Mfa: SessionMfaAssurance{Required: value.MFARequired, Completed: value.MFAMethod != authn.MFAMethodNone, Method: method}, CreatedAt: value.CreatedAt, LastActivityAt: value.LastActivityAt, IdleExpiresAt: value.LastActivityAt.Add(30 * time.Minute), AbsoluteExpiresAt: value.AbsoluteExpiresAt, ReauthenticatedUntil: reauth, RecoveryCodesRemaining: int(value.RecoveryCodesRemaining), CsrfToken: csrf}
}

func (s *Server) SetNodeDuplicateHistoryReader(reader assetstore.CrossNodeDuplicateOwnershipHistoryReader) {
	s.nodeDuplicateHistory = reader
}
func (s *Server) SetAccountInventoryProviderStateReader(reader assetstore.AccountInventoryProviderStateReader) {
	s.providerStates = reader
}

func (s *Server) SetAccountQualityReader(reader interface {
	ListAccountQuality(context.Context, assetstore.AccountQualityQuery) (assetstore.AccountQualityPage, error)
}) {
	s.accountQuality = reader
}

func (s *Server) SetAccountRequestHistoryReader(reader interface {
	ListAccountRequestHistory(context.Context, assetstore.AccountRequestHistoryQuery) (assetstore.AccountRequestHistoryPage, error)
}) {
	s.accountRequestHistory = reader
}
