package http

import (
	nethttp "net/http"

	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
)

const sessionCookieName = "orderflow_session"

type userHandler func(w nethttp.ResponseWriter, r *nethttp.Request, user domain.User)

func (s *Server) authenticated(next userHandler) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, nethttp.StatusUnauthorized, "not logged in")
			return
		}
		userID, err := s.Signer.Verify(cookie.Value)
		if err != nil {
			writeError(w, nethttp.StatusUnauthorized, "invalid session")
			return
		}
		user, ok := domain.FindUser(userID)
		if !ok {
			writeError(w, nethttp.StatusUnauthorized, "unknown user")
			return
		}
		next(w, r, user)
	}
}
