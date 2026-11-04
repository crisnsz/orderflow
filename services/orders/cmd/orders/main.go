package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	ordersv1 "github.com/crisnsz/orderflow/gen/orders/v1"
	"github.com/crisnsz/orderflow/internal/platform/config"
	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/health"
	"github.com/crisnsz/orderflow/internal/platform/outbox"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	grpcadapter "github.com/crisnsz/orderflow/services/orders/internal/adapters/grpc"
	"github.com/crisnsz/orderflow/services/orders/internal/adapters/inventory"
	"github.com/crisnsz/orderflow/services/orders/internal/adapters/postgres"
	mq "github.com/crisnsz/orderflow/services/orders/internal/adapters/rabbitmq"
	"github.com/crisnsz/orderflow/services/orders/internal/advancing"
	"github.com/crisnsz/orderflow/services/orders/internal/compensating"
	"github.com/crisnsz/orderflow/services/orders/internal/fetching"
	"github.com/crisnsz/orderflow/services/orders/internal/placing"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

const serviceName = "orders"

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

	pool, err := database.Connect(ctx, config.String("DATABASE_URL", "postgres://orderflow:orderflow@localhost:5432/orders"))
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

	inventoryClient, inventoryConn, err := inventory.Dial(config.String("INVENTORY_ADDR", "localhost:9002"))
	if err != nil {
		return err
	}
	defer func() { _ = inventoryConn.Close() }()

	store := postgres.NewStore(pool)
	placingOrders := placing.NewService(store, inventoryClient)
	advancingSaga := advancing.NewService(store, compensating.NewService(inventoryClient))
	fetchingOrders := fetching.NewService(store)
	health.Serve(ctx, healthAddr, func(checkCtx context.Context) error { return pool.Ping(checkCtx) }, broker.Check)

	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	ordersv1.RegisterOrdersServiceServer(grpcServer, grpcadapter.NewServer(placingOrders, fetchingOrders))
	listener, err := net.Listen("tcp", config.String("GRPC_ADDR", ":9001"))
	if err != nil {
		return err
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		(&outbox.Relay{Pool: pool, Publisher: publisher}).Run(groupCtx)
		return nil
	})
	group.Go(func() error { return broker.Consume(groupCtx, mq.Spec(), mq.Handler(advancingSaga)) })
	group.Go(func() error {
		go func() { <-groupCtx.Done(); grpcServer.GracefulStop() }()
		slog.Info("orders listening", "addr", listener.Addr().String())
		return grpcServer.Serve(listener)
	})
	return group.Wait()
}
