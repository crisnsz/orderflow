package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	nethttp "net/http"

	"github.com/crisnsz/orderflow/services/gateway/internal/storefront"
)

const maxRequestBodyBytes = 1 << 14

func writeJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w nethttp.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSONBody(w nethttp.ResponseWriter, r *nethttp.Request, target any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		writeError(w, nethttp.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	if err := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, maxRequestBodyBytes)).Decode(target); err != nil {
		writeError(w, nethttp.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeUpstreamError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	switch {
	case errors.Is(err, storefront.ErrNotFound):
		writeError(w, nethttp.StatusNotFound, "not found")
	case errors.Is(err, storefront.ErrInvalid):
		writeError(w, nethttp.StatusBadRequest, "invalid request")
	default:
		slog.ErrorContext(r.Context(), "upstream call failed", "error", err, "path", r.URL.Path)
		writeError(w, nethttp.StatusBadGateway, "upstream service unavailable")
	}
}
