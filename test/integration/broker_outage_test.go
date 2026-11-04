//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ordersv1 "github.com/crisnsz/orderflow/gen/orders/v1"
	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/outbox"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/test/containers"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	ordersPackage        = "github.com/crisnsz/orderflow/services/orders/cmd/orders"
	outageRoutingKey     = "test.outage.event"
	outageQueue          = "test.outage.q"
	outageObservationFor = 4 * time.Second
	brokerRecoveryWait   = 60 * time.Second
	drainAttemptTimeout  = 10 * time.Second
)

type safeBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *safeBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type ordersProcess struct {
	command    *exec.Cmd
	exited     chan struct{}
	logs       *safeBuffer
	grpcAddr   string
	healthAddr string
}

func startOrdersProcess(t *testing.T, pool *pgxpool.Pool, brokerURL string) *ordersProcess {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "orders")
	if output, err := exec.Command("go", "build", "-o", binary, ordersPackage).CombinedOutput(); err != nil {
		t.Fatalf("build orders: %v\n%s", err, output)
	}
	process := &ordersProcess{
		exited: make(chan struct{}), logs: &safeBuffer{},
		grpcAddr: containers.FreeTCPAddress(t), healthAddr: containers.FreeTCPAddress(t),
	}
	process.command = exec.Command(binary)
	process.command.Env = append(os.Environ(),
		"DATABASE_URL="+pool.Config().ConnString(),
		"RABBITMQ_URL="+brokerURL,
		"GRPC_ADDR="+process.grpcAddr,
		"HEALTH_ADDR="+process.healthAddr,
		"INVENTORY_ADDR=127.0.0.1:1",
		"OTEL_EXPORTER_OTLP_ENDPOINT=")
	process.command.Stdout, process.command.Stderr = process.logs, process.logs
	if err := process.command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = process.command.Wait(); close(process.exited) }()
	t.Cleanup(func() {
		_ = process.command.Process.Kill()
		<-process.exited
		if t.Failed() {
			t.Logf("orders logs:\n%s", process.logs.String())
		}
	})
	return process
}

func (p *ordersProcess) running() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

func (p *ordersProcess) listOrders(t *testing.T) error {
	t.Helper()
	connection, err := grpc.NewClient(p.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = ordersv1.NewOrdersServiceClient(connection).ListOrders(ctx, &ordersv1.ListOrdersRequest{UserId: "u-alice"})
	return err
}

func (p *ordersProcess) healthStatus() int {
	response, err := http.Get("http://" + p.healthAddr + "/healthz")
	if err != nil {
		return 0
	}
	_ = response.Body.Close()
	return response.StatusCode
}

func queueOutageEvent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	message, err := events.New(outageRoutingKey, events.Payload{OrderID: "order-outage", UserID: "u-alice"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(ctx, tx, message); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return message.ID
}

func drainMessageIDs(brokerURL string) map[string]bool {
	messageIDs := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), drainAttemptTimeout)
	defer cancel()
	broker := rabbitmq.NewBroker(brokerURL)
	connection, err := broker.Connect(ctx)
	if err != nil {
		return messageIDs
	}
	defer func() { _ = broker.Close() }()
	consumer, err := connection.NewConsumer(ctx, outageQueue, nil)
	if err != nil {
		return messageIDs
	}
	for {
		receiveCtx, receiveCancel := context.WithTimeout(ctx, drainIdleTimeout)
		delivery, err := consumer.Receive(receiveCtx)
		receiveCancel()
		if err != nil {
			return messageIDs
		}
		_ = delivery.Accept(ctx)
		messageIDs[delivery.Message().Properties.MessageID.(string)] = true
	}
}

func TestBrokerOutagePausesMessagingButKeepsOrdersServing(t *testing.T) {
	pool := containers.Postgres(t)
	broker := containers.StartRestartableRabbitMQ(t)
	process := startOrdersProcess(t, pool, broker.URL)

	waitFor(t, "orders gRPC to answer", func() bool { return process.listOrders(t) == nil })
	waitFor(t, "orders to report the broker connected", func() bool { return process.healthStatus() == http.StatusOK })

	setupConnection := connect(t, broker.URL)
	if err := rabbitmq.Declare(context.Background(), setupConnection, rabbitmq.QueueSpec{Name: outageQueue, Keys: []string{outageRoutingKey}}); err != nil {
		t.Fatal(err)
	}

	broker.Stop()
	duringOutage := queueOutageEvent(t, pool)

	deadline := time.Now().Add(outageObservationFor)
	for time.Now().Before(deadline) {
		if !process.running() {
			t.Fatalf("orders exited during the broker outage:\n%s", process.logs.String())
		}
		if err := process.listOrders(t); err != nil {
			t.Fatalf("gRPC failed during the broker outage: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	waitFor(t, "orders to report the broker unavailable", func() bool { return process.healthStatus() == http.StatusServiceUnavailable })

	broker.Start()
	deadline = time.Now().Add(brokerRecoveryWait)
	for !drainMessageIDs(broker.URL)[duringOutage] {
		if time.Now().After(deadline) {
			t.Fatalf("event queued during the outage was never published:\n%s", process.logs.String())
		}
		time.Sleep(500 * time.Millisecond)
	}
	waitFor(t, "orders to report the broker connected again", func() bool { return process.healthStatus() == http.StatusOK })
	if err := process.listOrders(t); err != nil {
		t.Fatalf("gRPC failed after reconnect: %v", err)
	}
}
