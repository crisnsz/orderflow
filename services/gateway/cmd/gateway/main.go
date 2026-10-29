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
	"github.com/crisnsz/orderflow/internal/platform/health"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	grpcadapter "github.com/crisnsz/orderflow/services/gateway/internal/adapters/grpc"
	httpadapter "github.com/crisnsz/orderflow/services/gateway/internal/adapters/http"
	"github.com/crisnsz/orderflow/services/gateway/internal/adapters/payments"
	mq "github.com/crisnsz/orderflow/services/gateway/internal/adapters/rabbitmq"
	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
	"github.com/crisnsz/orderflow/services/gateway/internal/timeline"
	"golang.org/x/sync/errgroup"
)

const serviceName = "gateway"

const (
	defaultSessionTTL = 8 * time.Hour
	maxTrackedOrders  = 500
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

	sessionTTL := config.Duration("SESSION_TTL", defaultSessionTTL)
	signer, err := domain.NewSigner([]byte(config.String("SESSION_SECRET", "")), sessionTTL, nil)
	if err != nil {
		return errors.New("SESSION_SECRET must be set (at least 16 bytes); see README")
	}

	orders, ordersConn, err := grpcadapter.DialOrders(config.String("ORDERS_ADDR", "localhost:9001"))
	if err != nil {
		return err
	}
	defer func() { _ = ordersConn.Close() }()
	catalog, catalogConn, err := grpcadapter.DialCatalog(config.String("INVENTORY_ADDR", "localhost:9002"))
	if err != nil {
		return err
	}
	defer func() { _ = catalogConn.Close() }()

	brokerConn, err := rabbitmq.Dial(ctx, config.String("RABBITMQ_URL", "amqp://orderflow:orderflow@localhost:5672/"))
	if err != nil {
		return err
	}
	defer func() { _ = brokerConn.Close() }()

	hub := timeline.NewHub(maxTrackedOrders)
	health.Serve(ctx, healthAddr, func(context.Context) error {
		if brokerConn.IsClosed() {
			return errors.New("rabbitmq connection closed")
		}
		return nil
	})

	server := &nethttp.Server{
		Addr: config.String("HTTP_ADDR", ":8080"),
		Handler: httpadapter.NewHandler(httpadapter.Deps{
			Signer: signer, Orders: orders, Catalog: catalog, Payments: payments.New(config.String("PAYMENTS_URL", "http://localhost:8083")),
			Hub: hub, SecureCookie: config.Bool("COOKIE_SECURE", false), SessionTTL: sessionTTL,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return mq.Run(groupCtx, brokerConn, hub) })
	group.Go(func() error {
		go func() {
			<-groupCtx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
		slog.Info("gateway listening", "addr", server.Addr)
		if err := server.ListenAndServe(); !errors.Is(err, nethttp.ErrServerClosed) {
			return err
		}
		return nil
	})
	return group.Wait()
}
