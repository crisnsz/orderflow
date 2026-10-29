package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type orderKey struct{}

func WithOrderID(ctx context.Context, orderID string) context.Context {
	return context.WithValue(ctx, orderKey{}, orderID)
}

func OrderID(ctx context.Context) string {
	orderID, _ := ctx.Value(orderKey{}).(string)
	return orderID
}

type ctxHandler struct{ slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, record slog.Record) error {
	if orderID := OrderID(ctx); orderID != "" {
		record.AddAttrs(slog.String("order_id", orderID))
	}
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		record.AddAttrs(slog.String("trace_id", spanContext.TraceID().String()), slog.String("span_id", spanContext.SpanID().String()))
	}
	return h.Handler.Handle(ctx, record)
}

func (h ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ctxHandler{h.Handler.WithAttrs(attrs)}
}

func (h ctxHandler) WithGroup(name string) slog.Handler { return ctxHandler{h.Handler.WithGroup(name)} }

func NewLogger(service string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(ctxHandler{jsonHandler}).With("service", service)
	slog.SetDefault(logger)
	return logger
}
