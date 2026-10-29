//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/test/containers"
	amqp "github.com/rabbitmq/amqp091-go"
)

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func startConsumer(t *testing.T, queueName, routingKey string, handle rabbitmq.Handler) (*amqp.Channel, context.CancelFunc) {
	t.Helper()
	connection, err := rabbitmq.Dial(context.Background(), containers.RabbitMQ(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	spec := rabbitmq.QueueSpec{Name: queueName, Keys: []string{routingKey}, RetryDelays: []time.Duration{150 * time.Millisecond, 150 * time.Millisecond}}

	channel, _ := connection.Channel()
	if err := rabbitmq.Declare(channel, spec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = rabbitmq.Consume(ctx, connection, spec, handle) }()

	publisher, err := rabbitmq.NewPublisher(connection)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := events.New(routingKey, events.Payload{OrderID: "order-1", UserID: "u"})
	if err := publisher.Publish(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	return channel, cancel
}

func queueDepth(channel *amqp.Channel, queueName string) int {
	info, err := channel.QueueDeclarePassive(queueName, true, false, false, false, nil)
	if err != nil {
		return -1
	}
	return info.Messages
}

func TestFailingMessageIsRetriedThenDeadLettered(t *testing.T) {
	var calls atomic.Int64
	channel, cancel := startConsumer(t, "test.dlq.fail.q", "test.dlq.fail", func(context.Context, events.Message) error {
		calls.Add(1)
		return errors.New("downstream unavailable")
	})
	defer cancel()

	deadLetterQueue := rabbitmq.DLQName("test.dlq.fail.q")
	waitFor(t, "message in DLQ", func() bool { return queueDepth(channel, deadLetterQueue) == 1 })
	time.Sleep(600 * time.Millisecond)
	if got := calls.Load(); got != 3 {
		t.Errorf("handler calls = %d, want 3", got)
	}
	deadLetter, ok, _ := channel.Get(deadLetterQueue, true)
	if !ok {
		t.Fatal("DLQ empty")
	}
	if deadLetter.Type != "test.dlq.fail" || deadLetter.CorrelationId != "order-1" || deadLetter.Headers["x-last-error"] == nil {
		t.Errorf("dead letter lost its metadata: type=%q corr=%q headers=%v", deadLetter.Type, deadLetter.CorrelationId, deadLetter.Headers)
	}
	if queueDepth(channel, "test.dlq.fail.q") != 0 {
		t.Error("main queue should be empty")
	}
}

func TestPermanentFailureSkipsRetriesAndGoesStraightToDLQ(t *testing.T) {
	var calls atomic.Int64
	channel, cancel := startConsumer(t, "test.dlq.perm.q", "test.dlq.perm", func(context.Context, events.Message) error {
		calls.Add(1)
		return errors.Join(rabbitmq.ErrPermanent, errors.New("malformed body"))
	})
	defer cancel()
	waitFor(t, "message in DLQ", func() bool { return queueDepth(channel, rabbitmq.DLQName("test.dlq.perm.q")) == 1 })
	if got := calls.Load(); got != 1 {
		t.Errorf("handler calls = %d, want 1", got)
	}
}

func TestTransientFailureRecoversWithoutDLQ(t *testing.T) {
	var calls atomic.Int64
	channel, cancel := startConsumer(t, "test.dlq.ok.q", "test.dlq.ok", func(context.Context, events.Message) error {
		if calls.Add(1) < 3 {
			return errors.New("temporary")
		}
		return nil
	})
	defer cancel()
	waitFor(t, "third attempt", func() bool { return calls.Load() == 3 })
	time.Sleep(600 * time.Millisecond)
	if calls.Load() != 3 {
		t.Errorf("handler calls = %d, want 3", calls.Load())
	}
	if depth := queueDepth(channel, rabbitmq.DLQName("test.dlq.ok.q")); depth != 0 {
		t.Errorf("DLQ depth = %d, want 0", depth)
	}
	if depth := queueDepth(channel, "test.dlq.ok.q"); depth != 0 {
		t.Errorf("main queue depth = %d, want 0", depth)
	}
}
