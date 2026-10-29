package http

import (
	"log/slog"
	nethttp "net/http"

	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
)

func (s *Server) listUsers(w nethttp.ResponseWriter, _ *nethttp.Request) {
	writeJSON(w, nethttp.StatusOK, domain.DemoUsers)
}

func (s *Server) login(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request struct {
		UserID string `json:"user_id"`
	}
	if !decodeJSONBody(w, r, &request) {
		return
	}
	user, ok := domain.FindUser(request.UserID)
	if !ok {
		writeError(w, nethttp.StatusUnauthorized, "unknown demo user")
		return
	}
	nethttp.SetCookie(w, &nethttp.Cookie{
		Name: sessionCookieName, Value: s.Signer.Issue(user.ID), Path: "/",
		MaxAge: int(s.SessionTTL.Seconds()), HttpOnly: true, Secure: s.SecureCookie, SameSite: nethttp.SameSiteStrictMode,
	})
	slog.InfoContext(r.Context(), "demo login", "user_id", user.ID)
	writeJSON(w, nethttp.StatusOK, user)
}

func (s *Server) logout(w nethttp.ResponseWriter, _ *nethttp.Request) {
	nethttp.SetCookie(w, &nethttp.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.SecureCookie, SameSite: nethttp.SameSiteStrictMode})
	writeJSON(w, nethttp.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w nethttp.ResponseWriter, r *nethttp.Request, user domain.User) {
	cards, err := s.Payments.Cards(r.Context(), user.ID)
	if err != nil {
		writeUpstreamError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"user": user, "cards": cards})
}
