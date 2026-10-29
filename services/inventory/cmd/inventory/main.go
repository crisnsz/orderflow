package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	inventoryv1 "github.com/crisnsz/orderflow/gen/inventory/v1"
	"github.com/crisnsz/orderflow/internal/platform/config"
	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/health"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	grpcadapter "github.com/crisnsz/orderflow/services/inventory/internal/adapters/grpc"
	"github.com/crisnsz/orderflow/services/inventory/internal/adapters/postgres"
	"github.com/crisnsz/orderflow/services/inventory/internal/listing"
	"github.com/crisnsz/orderflow/services/inventory/internal/releasing"
	"github.com/crisnsz/orderflow/services/inventory/internal/reserving"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

const serviceName = "inventory"

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

	pool, err := database.Connect(ctx, config.String("DATABASE_URL", "postgres://orderflow:orderflow@localhost:5432/inventory"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	health.Serve(ctx, healthAddr, func(checkCtx context.Context) error { return pool.Ping(checkCtx) })

	store := postgres.NewStore(pool)
	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	inventoryv1.RegisterInventoryServiceServer(grpcServer, grpcadapter.NewServer(listing.NewService(store), reserving.NewService(store), releasing.NewService(store)))

	address := config.String("GRPC_ADDR", ":9002")
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); grpcServer.GracefulStop() }()
	slog.Info("inventory listening", "addr", address)
	return grpcServer.Serve(listener)
}
