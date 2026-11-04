package main

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/config"
	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/health"
	"github.com/crisnsz/orderflow/internal/platform/outbox"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/payments/internal/adapters/bank"
	adminhttp "github.com/crisnsz/orderflow/services/payments/internal/adapters/http"
	"github.com/crisnsz/orderflow/services/payments/internal/adapters/postgres"
	mq "github.com/crisnsz/orderflow/services/payments/internal/adapters/rabbitmq"
	"github.com/crisnsz/orderflow/services/payments/internal/charging"
	"golang.org/x/sync/errgroup"
)

const serviceName = "payments"

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 3 * time.Second
)

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

	pool, err := database.Connect(ctx, config.String("DATABASE_URL", "postgres://orderflow:orderflow@localhost:5432/payments"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	broker := rabbitmq.NewBroker(config.String("RABBITMQ_URL", "amqp://orderflow:orderflow@localhost:5672/"))
	defer func() { _ = broker.Close() }()
	publisher := rabbitmq.NewResilientPublisher(broker)
	health.Serve(ctx, healthAddr, func(checkCtx context.Context) error { return pool.Ping(checkCtx) }, broker.Check)

	failureSwitch := bank.NewSwitch(os.Getenv("PAYMENTS_FAIL_STAGE"))
	store := postgres.NewStore(pool)
	service := charging.NewService(store, bank.NewFake(failureSwitch))

	adminServer := &nethttp.Server{
		Addr:              config.String("HTTP_ADDR", ":8083"),
		Handler:           adminhttp.Handler(store, failureSwitch),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { (&outbox.Relay{Pool: pool, Publisher: publisher}).Run(groupCtx); return nil })
	group.Go(func() error { return broker.Consume(groupCtx, mq.Spec(), mq.Handler(service)) })
	group.Go(func() error {
		go func() {
			<-groupCtx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			_ = adminServer.Shutdown(shutdownCtx)
		}()
		slog.Info("payments admin listening", "addr", adminServer.Addr, "failure_stage", failureSwitch.Stage())
		if err := adminServer.ListenAndServe(); !errors.Is(err, nethttp.ErrServerClosed) {
			return err
		}
		return nil
	})
	return group.Wait()
}
