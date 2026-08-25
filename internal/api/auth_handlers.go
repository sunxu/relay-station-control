package api

import (
	"errors"
	"net/http"
	"time"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func (s *Server) GetBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	status, err := s.service.BootstrapStatus(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BootstrapStatusResponse{Status: BootstrapState(status)})
}

func (s *Server) StartBootstrap(w http.ResponseWriter, r *http.Request, params StartBootstrapParams) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	var body BootstrapStartRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Password == nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	enrollment, err := s.service.StartBootstrap(r.Context(), params.XBootstrapSecret, body.LoginName, body.DisplayName, *body.Password, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	uri := enrollment.URI
	writeJSON(w, http.StatusOK, BootstrapStartResponse{Status: BootstrapStartResponseStatusInProgress, TotpEnrollment: TotpEnrollment{Algorithm: SHA1, Digits: N6, PeriodSeconds: N30, OtpauthUri: &uri}})
}

func (s *Server) CompleteBootstrap(w http.ResponseWriter, r *http.Request, params CompleteBootstrapParams) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	var body TotpConfirmationRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.TotpCode == nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	session, codes, err := s.service.CompleteBootstrap(r.Context(), params.XBootstrapSecret, *body.TotpCode, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.setSessionCookie(w, session.Token)
	s.clearChallengeCookie(w)
	recovery := RecoveryCodes(codes)
	writeJSON(w, http.StatusOK, BootstrapCompleteResponse{Status: BootstrapCompleteResponseStatusCompleted, RecoveryCodes: &recovery, Session: sessionResponse(session)})
}

func (s *Server) ResetPendingBootstrap(w http.ResponseWriter, r *http.Request, params ResetPendingBootstrapParams) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	if err := s.service.ResetBootstrap(r.Context(), params.XBootstrapSecret, meta); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BootstrapStatusResponse{Status: BootstrapStateRequired})
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	var body LoginRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Password == nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	result, err := s.service.Login(r.Context(), body.LoginName, *body.Password, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var response LoginResponse
	if result.Challenge != nil {
		s.setChallengeCookie(w, result.Challenge.Token)
		if err = response.FromMfaChallengeResponse(MfaChallengeResponse{State: MfaChallengeResponseStateMfaRequired, ExpiresAt: result.Challenge.ExpiresAt, Methods: []MfaMethod{Totp, RecoveryCode}}); err != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
	} else {
		s.setSessionCookie(w, result.Session.Token)
		if err = response.FromSessionResponse(sessionResponse(*result.Session)); err != nil {
			s.writeError(w, r, authn.ErrUnavailable)
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CompleteLoginMfa(w http.ResponseWriter, r *http.Request) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	var body MfaChallengeRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Code == nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	cookie, err := r.Cookie(s.challengeCookieName())
	if err != nil {
		s.writeError(w, r, authn.ErrAuthentication)
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	session, err := s.service.CompleteLoginMFA(r.Context(), cookie.Value, authn.MFAMethod(body.Method), *body.Code, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.clearChallengeCookie(w)
	s.setSessionCookie(w, session.Token)
	writeJSON(w, http.StatusOK, sessionResponse(session))
}

func (s *Server) GetSession(w http.ResponseWriter, r *http.Request) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, false, "")
	if !ok {
		return
	}
	session, err := s.service.RotateCSRF(r.Context(), session)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(session))
}

func (s *Server) Logout(w http.ResponseWriter, r *http.Request, params LogoutParams) {
	defer s.authenticationDelay(r.Context(), time.Now())
	s.prepare(w, r, true)
	if !s.ready(w, r) {
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	var session *authn.Session
	if cookie, err := r.Cookie(s.sessionCookieName()); err == nil {
		if authenticated, authErr := s.service.Authenticate(r.Context(), cookie.Value); authErr == nil {
			if params.XCSRFToken == nil || s.service.VerifyCSRF(r.Context(), authenticated, *params.XCSRFToken) != nil || !s.sameOrigin(r) {
				s.auditSecurityRejection(r, &authenticated, authn.AuditCSRF, authn.ErrorCodeCSRFRejected)
				s.writeError(w, r, authn.ErrCSRF)
				return
			}
			session = &authenticated
		} else {
			var serviceErr *authn.ServiceError
			if errors.As(authErr, &serviceErr) && serviceErr.Code == authn.ErrorCodeInternal {
				s.writeError(w, r, authErr)
				return
			}
		}
	}
	if err := s.service.Logout(r.Context(), session, meta); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Reauthenticate(w http.ResponseWriter, r *http.Request, params ReauthenticateParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, params.XCSRFToken)
	if !ok {
		return
	}
	var body ReauthenticateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Password == nil || body.MfaMethod == nil || body.MfaCode == nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	rotated, err := s.service.Reauthenticate(r.Context(), session, *body.Password, authn.MFAMethod(*body.MfaMethod), *body.MfaCode, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.setSessionCookie(w, rotated.Token)
	writeJSON(w, http.StatusOK, sessionResponse(rotated))
}

func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request, params ChangePasswordParams) {
	s.prepare(w, r, true)
	session, ok := s.requireSession(w, r, true, params.XCSRFToken)
	if !ok {
		return
	}
	var body ChangePasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.CurrentPassword == nil || body.NewPassword == nil {
		s.writeError(w, r, authn.ErrInvalid)
		return
	}
	method, code := authn.MFAMethodNone, ""
	if body.MfaMethod != nil {
		method = authn.MFAMethod(*body.MfaMethod)
	}
	if body.MfaCode != nil {
		code = *body.MfaCode
	}
	meta, ok := s.meta(w, r)
	if !ok {
		return
	}
	rotated, err := s.service.ChangePassword(r.Context(), session, *body.CurrentPassword, *body.NewPassword, method, code, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.setSessionCookie(w, rotated.Token)
	writeJSON(w, http.StatusOK, sessionResponse(rotated))
}

func (s *Server) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request, params RegenerateRecoveryCodesParams) {
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
	codes, err := s.service.RegenerateRecoveryCodes(r.Context(), session, body.Reason, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	recovery := RecoveryCodes(codes)
	writeJSON(w, http.StatusOK, RecoveryCodesResponse{RecoveryCodes: &recovery, Remaining: N10})
}
