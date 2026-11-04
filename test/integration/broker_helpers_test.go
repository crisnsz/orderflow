//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

const (
	drainIdleTimeout     = time.Second
	deadLetterWaitLimit  = 30 * time.Second
	waitForPollInterval  = 50 * time.Millisecond
	waitForDefaultLimit  = 20 * time.Second
	testConnectionBudget = 30 * time.Second
)

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitForDefaultLimit)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(waitForPollInterval)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func connect(t *testing.T, brokerURL string, options ...rabbitmq.BrokerOption) *rabbitmqamqp.AmqpConnection {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testConnectionBudget)
	defer cancel()
	broker := rabbitmq.NewBroker(brokerURL, options...)
	connection, err := broker.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	return connection
}

func drainQueue(t *testing.T, connection *rabbitmqamqp.AmqpConnection, queueName string) []*amqp.Message {
	t.Helper()
	consumer, err := connection.NewConsumer(context.Background(), queueName, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consumer.Close(context.Background()) }()
	var drained []*amqp.Message
	for {
		receiveCtx, cancel := context.WithTimeout(context.Background(), drainIdleTimeout)
		delivery, err := consumer.Receive(receiveCtx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return drained
		}
		if err != nil {
			t.Fatal(err)
		}
		drained = append(drained, delivery.Message())
		_ = delivery.Accept(context.Background())
	}
}

func receiveDeadLetter(t *testing.T, connection *rabbitmqamqp.AmqpConnection, queueName string) *amqp.Message {
	t.Helper()
	consumer, err := connection.NewConsumer(context.Background(), rabbitmq.DLQName(queueName), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consumer.Close(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), deadLetterWaitLimit)
	defer cancel()
	delivery, err := consumer.Receive(ctx)
	if err != nil {
		t.Fatalf("no message reached %s: %v", rabbitmq.DLQName(queueName), err)
	}
	_ = delivery.Accept(context.Background())
	return delivery.Message()
}

func queueDepth(t *testing.T, connection *rabbitmqamqp.AmqpConnection, queueName string) int {
	t.Helper()
	info, err := connection.Management().QueueInfo(context.Background(), queueName)
	if err != nil {
		t.Fatal(err)
	}
	return int(info.MessageCount())
}
