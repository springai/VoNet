package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"vonet/internal/loghub"
	"vonet/internal/store"
)

// recordAudit writes one security-relevant event to the audit trail. Failures
// are logged but never block the request being audited.
func (s *Server) recordAudit(
	ctx context.Context,
	actor string,
	action string,
	entityType string,
	entityID string,
	outcome string,
	remoteAddr string,
) {
	level := slog.LevelInfo
	if !strings.EqualFold(strings.TrimSpace(outcome), "success") {
		level = slog.LevelWarn
	}
	if s.logger != nil {
		s.logger.Log(ctx, level, "user operation",
			"category", auditLogCategory(action),
			"event", action,
			"actor", actor,
			"entity_type", entityType,
			"entity_id", entityID,
			"outcome", outcome,
		)
	}
	if s.store == nil {
		return
	}
	entityID = loghub.RedactString(entityID)
	remoteAddr = normalizeAuditRemoteAddr(remoteAddr)
	_, err := s.store.AppendAuditEvent(ctx, store.AuditEvent{
		Actor:      actor,
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		Outcome:    outcome,
		RemoteAddr: remoteAddr,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		s.logger.Warn("write audit event failed", "category", "system", "action", action, "raw_error", err)
	}
}

func (s *Server) recordAuditSilent(
	ctx context.Context,
	actor string,
	action string,
	entityType string,
	entityID string,
	outcome string,
	remoteAddr string,
) {
	if s.store == nil {
		return
	}
	entityID = loghub.RedactString(entityID)
	_, err := s.store.AppendAuditEvent(ctx, store.AuditEvent{
		Actor:      actor,
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		Outcome:    outcome,
		RemoteAddr: normalizeAuditRemoteAddr(remoteAddr),
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("write audit event failed", "category", "system", "action", action, "raw_error", err)
	}
}

func normalizeAuditRemoteAddr(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	if net.ParseIP(value) != nil {
		return value
	}
	return ""
}

func auditLogCategory(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	switch {
	case strings.Contains(action, ".sms") || strings.HasPrefix(action, "sms."):
		return "sms"
	case strings.Contains(action, ".call") || strings.HasPrefix(action, "call."):
		return "call"
	case strings.Contains(action, "vowifi") || strings.Contains(action, "ims"):
		return "vowifi"
	case strings.Contains(action, "device") || strings.Contains(action, "esim") ||
		strings.Contains(action, ".at.") || strings.Contains(action, ".ussd"):
		return "hardware"
	default:
		return "operation"
	}
}

// audit records an event for an already-authenticated request, resolving the
// actor from the bearer token or session and the source address from the raw
// connection (proxy headers are deliberately not trusted for the audit trail).
func (s *Server) audit(r *http.Request, action string, entityType string, entityID string, outcome string) {
	actor := s.actorForRequest(r)
	s.recordAudit(r.Context(), actor, action, entityType, entityID, outcome, requestRemoteHost(r))
}

func (s *Server) actorForRequest(r *http.Request) string {
	if principal, ok := bearerPrincipalFromRequest(r); ok {
		return "token:" + principal.Name
	}
	if s.auth != nil {
		if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			if session, authErr := s.auth.Authenticate(r.Context(), cookie.Value); authErr == nil {
				return session.Principal.Username
			}
		}
	}
	return ""
}

// auditAuth records an authentication event where no session exists yet (the
// actor is the username that was attempted).
func (s *Server) auditAuth(r *http.Request, username string, outcome string) {
	s.recordAudit(r.Context(), username, "auth.login", "session", username, outcome, requestRemoteHost(r))
}

func requestRemoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
