package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/crisnsz/orderflow/internal/platform/config"
	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/health"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/notifications/internal/adapters/channels"
	"github.com/crisnsz/orderflow/services/notifications/internal/adapters/postgres"
	mq "github.com/crisnsz/orderflow/services/notifications/internal/adapters/rabbitmq"
	"github.com/crisnsz/orderflow/services/notifications/internal/notifying"
)

const serviceName = "notifications"

func main() {
	healthAddr := config.String("HEALTH_ADDR", ":8081")
	health.ProbeIfRequested(healthAddr)
	if err := run(healthAddr); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run(healthAddr string) error {
	telemetry.NewLogger(serviceName)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.SetupTracing(ctx, serviceName)
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	pool, err := database.Connect(ctx, config.String("DATABASE_URL", "postgres://orderflow:orderflow@localhost:5432/notifications"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	brokerConn, err := rabbitmq.Dial(ctx, config.String("RABBITMQ_URL", "amqp://orderflow:orderflow@localhost:5672/"))
	if err != nil {
		return err
	}
	defer func() { _ = brokerConn.Close() }()
	health.Serve(ctx, healthAddr, func(checkCtx context.Context) error { return pool.Ping(checkCtx) })

	notifications := notifying.NewService(postgres.NewStore(pool), channels.All()...)
	slog.Info("notifications started")
	return rabbitmq.Consume(ctx, brokerConn, mq.Spec(), mq.Handler(notifications))
}
