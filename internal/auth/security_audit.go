package auth

import "context"

// AuditSecurityRejection records an authorization or CSRF denial without
// allowing an audit-store failure to turn the denial into an allow decision.
// Details are deliberately limited to the stable, low-cardinality error code.
func (s *Service) AuditSecurityRejection(ctx context.Context, action AuditAction, session *Session, code ErrorCode, meta RequestMeta) {
	if action != AuditAuthorization && action != AuditCSRF {
		return
	}
	intent := AuditIntent{
		Action:            action,
		Result:            AuditResultDenied,
		SourceFingerprint: meta.SourceFingerprint.Sum[:],
		RequestID:         meta.RequestID,
		Details:           map[string]any{"error_code": string(code)},
	}
	if session != nil {
		intent.ActorAdminID = session.AdminID
	}
	s.failureAudit(ctx, intent)
}
