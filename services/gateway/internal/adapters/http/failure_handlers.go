package http

import (
	nethttp "net/http"

	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
)

func (s *Server) getFailure(w nethttp.ResponseWriter, r *nethttp.Request, _ domain.User) {
	stage, err := s.Payments.FailureStage(r.Context())
	if err != nil {
		writeUpstreamError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"stage": stage})
}

func (s *Server) setFailure(w nethttp.ResponseWriter, r *nethttp.Request, _ domain.User) {
	var request struct {
		Stage string `json:"stage"`
	}
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if err := s.Payments.SetFailureStage(r.Context(), request.Stage); err != nil {
		writeUpstreamError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"stage": request.Stage})
}
