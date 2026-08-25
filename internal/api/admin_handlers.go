package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func (s *Server) CompleteAdministratorActivation(w http.ResponseWriter, r *http.Request) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	var body AdministratorActivationRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	value, err := body.ValueByDiscriminator()
	if err != nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	var response AdministratorActivationResponse
	switch request := value.(type) {
	case AdministratorActivationStartRequest:
		if request.ActivationToken == nil {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		enrollment, err := s.service.StartActivation(r.Context(), *request.ActivationToken, meta)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		uri := enrollment.URI
		if response.FromAdministratorActivationEnrollmentResponse(AdministratorActivationEnrollmentResponse{State: EnrollmentRequired, TotpEnrollment: TotpEnrollment{Algorithm: SHA1, Digits: N6, PeriodSeconds: N30, OtpauthUri: &uri}}) != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
	case AdministratorActivationCompleteRequest:
		if request.ActivationToken == nil || request.Password == nil || request.TotpCode == nil {
			s.writeError(w, r, authn.ErrInvalid)
			return
		}
		session, codes, err := s.service.CompleteActivation(r.Context(), *request.ActivationToken, *request.Password, *request.TotpCode, meta)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		s.setSessionCookie(w, session.Token)
		recovery := RecoveryCodes(codes)
		if response.FromAdministratorActivationCompleteResponse(AdministratorActivationCompleteResponse{State: Activated, Session: sessionResponse(session), RecoveryCodes: &recovery}) != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
	default:
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) ListAdministrators(w http.ResponseWriter, r *http.Request, params ListAdministratorsParams) {
	s.prepare(w, r, true)
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return
	}
	admins, err := s.service.ListAdmins(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]Administrator, 0, len(admins))
	for _, admin := range admins {
		if params.Status == nil || string(*params.Status) == admin.Status {
			items = append(items, administrator(admin))
		}
	}
	writeJSON(w, http.StatusOK, AdministratorListResponse{Items: items})
}

func (s *Server) CreateAdministrator(w http.ResponseWriter, r *http.Request, params CreateAdministratorParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, params.XCSRFToken)
	if !ok {
		return
	}
	var body CreateAdministratorRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	created, err := s.service.CreateAdmin(r.Context(), session, body.LoginName, body.DisplayName, body.Reason, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	token := ActivationToken(created.Token)
	writeJSON(w, http.StatusCreated, AdministratorActivationTokenResponse{ActivationToken: &token, Administrator: administrator(created.Admin), ExpiresAt: created.ExpiresAt})
}

func (s *Server) RegenerateAdministratorActivationToken(w http.ResponseWriter, r *http.Request, id AdministratorId, params RegenerateAdministratorActivationTokenParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, params.XCSRFToken)
	if !ok {
		return
	}
	var body ReasonRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	created, err := s.service.RegenerateActivationToken(r.Context(), session, uuid.UUID(id), body.Reason, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	token := ActivationToken(created.Token)
	writeJSON(w, http.StatusOK, AdministratorActivationTokenResponse{ActivationToken: &token, Administrator: administrator(created.Admin), ExpiresAt: created.ExpiresAt})
}

func (s *Server) DisableAdministrator(w http.ResponseWriter, r *http.Request, id AdministratorId, params DisableAdministratorParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, params.XCSRFToken)
	if !ok {
		return
	}
	var body ReasonRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	disabled, err := s.service.DisableAdmin(r.Context(), session, uuid.UUID(id), body.Reason, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, administrator(disabled))
}

func (s *Server) ResetAdministratorMfa(w http.ResponseWriter, r *http.Request, id AdministratorId, params ResetAdministratorMfaParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, params.XCSRFToken)
	if !ok {
		return
	}
	var body ReasonRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	created, err := s.service.ResetAdminMFA(r.Context(), session, uuid.UUID(id), body.Reason, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	token := ActivationToken(created.Token)
	writeJSON(w, http.StatusOK, AdministratorActivationTokenResponse{ActivationToken: &token, Administrator: administrator(created.Admin), ExpiresAt: created.ExpiresAt})
}
