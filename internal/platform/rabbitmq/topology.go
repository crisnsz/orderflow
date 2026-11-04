package rabbitmq

import (
	"context"
	"sync"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

const DeadLetterExchange = "orderflow.dlx"

var managementRequests sync.Mutex

type QueueSpec struct {
	Name  string
	Keys  []string
	Retry RetryPolicy
}

func DLQName(queueName string) string { return queueName + ".dlq" }

func Declare(ctx context.Context, connection *rabbitmqamqp.AmqpConnection, spec QueueSpec) error {
	managementRequests.Lock()
	defer managementRequests.Unlock()
	management := connection.Management()
	if err := declareExchanges(ctx, management); err != nil {
		return err
	}
	if err := declareDeadLetterQueue(ctx, management, spec); err != nil {
		return err
	}
	if _, err := management.DeclareQueue(ctx, mainQueueSpecification(spec)); err != nil {
		return err
	}
	return bindQueue(ctx, management, spec)
}

func declareExchanges(ctx context.Context, management *rabbitmqamqp.AmqpManagement) error {
	if _, err := management.DeclareExchange(ctx, &rabbitmqamqp.TopicExchangeSpecification{Name: events.Exchange}); err != nil {
		return err
	}
	_, err := management.DeclareExchange(ctx, &rabbitmqamqp.DirectExchangeSpecification{Name: DeadLetterExchange})
	return err
}

func declareDeadLetterQueue(ctx context.Context, management *rabbitmqamqp.AmqpManagement, spec QueueSpec) error {
	deadLetterQueue := DLQName(spec.Name)
	if _, err := management.DeclareQueue(ctx, &rabbitmqamqp.QuorumQueueSpecification{Name: deadLetterQueue}); err != nil {
		return err
	}
	_, err := management.Bind(ctx, &rabbitmqamqp.ExchangeToQueueBindingSpecification{
		SourceExchange: DeadLetterExchange, DestinationQueue: deadLetterQueue, BindingKey: deadLetterQueue,
	})
	return err
}

func mainQueueSpecification(spec QueueSpec) *rabbitmqamqp.QuorumQueueSpecification {
	queue := &rabbitmqamqp.QuorumQueueSpecification{
		Name:                 spec.Name,
		DeadLetterExchange:   DeadLetterExchange,
		DeadLetterRoutingKey: DLQName(spec.Name),
		OverflowStrategy:     &rabbitmqamqp.RejectPublishOverflowStrategy{},
		Arguments:            map[string]any{"x-dead-letter-strategy": "at-least-once"},
	}
	if spec.Retry.retriesEnabled() {
		queue.DeliveryLimit = spec.Retry.deliveryLimit()
		queue.DelayedRetryType = rabbitmqamqp.QuorumQueueDelayedRetryFailed
		queue.DelayedRetryMin = spec.Retry.BaseDelay
		queue.DelayedRetryMax = spec.Retry.MaxDelay
	}
	return queue
}

func bindQueue(ctx context.Context, management *rabbitmqamqp.AmqpManagement, spec QueueSpec) error {
	for _, routingKey := range spec.Keys {
		_, err := management.Bind(ctx, &rabbitmqamqp.ExchangeToQueueBindingSpecification{
			SourceExchange: events.Exchange, DestinationQueue: spec.Name, BindingKey: routingKey,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func DeclareExchanges(ctx context.Context, connection *rabbitmqamqp.AmqpConnection) error {
	managementRequests.Lock()
	defer managementRequests.Unlock()
	return declareExchanges(ctx, connection.Management())
}

func QueueMessageCount(ctx context.Context, connection *rabbitmqamqp.AmqpConnection, queueName string) (uint64, error) {
	managementRequests.Lock()
	defer managementRequests.Unlock()
	info, err := connection.Management().QueueInfo(ctx, queueName)
	if err != nil {
		return 0, err
	}
	return info.MessageCount(), nil
}
