package http

import (
	"encoding/json"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
	"github.com/crisnsz/orderflow/services/gateway/internal/timeline"
)

const keepAliveInterval = 15 * time.Second

func (s *Server) streamEvents(w nethttp.ResponseWriter, r *nethttp.Request, user domain.User) {
	order, ok := s.ownedOrder(w, r, user)
	if !ok {
		return
	}
	flusher, ok := w.(nethttp.Flusher)
	if !ok {
		writeError(w, nethttp.StatusInternalServerError, "streaming unsupported")
		return
	}
	ctx := telemetry.WithOrderID(r.Context(), order.ID)
	writeEventStreamHeaders(w)

	replay, live, cancel := s.Hub.Subscribe(order.ID)
	defer cancel()
	slog.InfoContext(ctx, "sse client connected", "replay", len(replay))
	defer slog.InfoContext(ctx, "sse client disconnected")

	send := func(event timeline.Event) error {
		data, _ := json.Marshal(event)
		_, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
		flusher.Flush()
		return err
	}
	for _, event := range replay {
		if send(event) != nil {
			return
		}
	}
	flusher.Flush()
	keepAlive := time.NewTicker(keepAliveInterval)
	defer keepAlive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-live:
			if send(event) != nil {
				return
			}
		case <-keepAlive.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeEventStreamHeaders(w nethttp.ResponseWriter) {
	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("X-Accel-Buffering", "no")
	w.WriteHeader(nethttp.StatusOK)
}
