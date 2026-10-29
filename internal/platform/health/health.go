package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	checkTimeout      = 2 * time.Second
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 3 * time.Second
	selfProbeTimeout  = 2 * time.Second
	loopbackHost      = "127.0.0.1"
)

type Check func(ctx context.Context) error

func Handler(checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		defer cancel()
		for _, check := range checks {
			if err := check(ctx); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = w.Write([]byte("ok"))
	})
}

func Serve(ctx context.Context, addr string, checks ...Check) {
	mux := http.NewServeMux()
	mux.Handle("/healthz", Handler(checks...))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(ctx, "health server failed", "error", err)
		}
	}()
}

func ProbeIfRequested(addr string) {
	if len(os.Args) < 2 || os.Args[1] != "healthcheck" {
		return
	}
	hostAndPort := addr
	if len(hostAndPort) > 0 && hostAndPort[0] == ':' {
		hostAndPort = loopbackHost + hostAndPort
	}
	client := http.Client{Timeout: selfProbeTimeout}
	response, err := client.Get(fmt.Sprintf("http://%s/healthz", hostAndPort))
	healthy := err == nil && response.StatusCode == http.StatusOK
	if response != nil {
		_ = response.Body.Close()
	}
	if !healthy {
		os.Exit(1)
	}
	os.Exit(0)
}
