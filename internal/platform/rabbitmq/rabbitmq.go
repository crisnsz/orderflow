package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	headerTraceparent = "traceparent"
	headerRetryCount  = "x-retry-count"
	headerLastError   = "x-last-error"
)

const (
	dialMaxAttempts = 30
	dialRetryDelay  = 2 * time.Second

	publisherConfirmTimeout = 10 * time.Second

	consumerPrefetchCount = 5

	directExchange = ""
)

func Dial(ctx context.Context, url string) (*amqp.Connection, error) {
	var lastErr error
	for attempt := 0; attempt < dialMaxAttempts; attempt++ {
		connection, err := amqp.Dial(url)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(dialRetryDelay):
		}
	}
	return nil, fmt.Errorf("rabbitmq not reachable: %w", lastErr)
}

func DeclareExchange(channel *amqp.Channel) error {
	return channel.ExchangeDeclare(events.Exchange, "topic", true, false, false, false, nil)
}

type Publisher struct {
	mu       sync.Mutex
	channel  *amqp.Channel
	confirms chan amqp.Confirmation
}

func NewPublisher(connection *amqp.Connection) (*Publisher, error) {
	channel, err := connection.Channel()
	if err != nil {
		return nil, err
	}
	if err := DeclareExchange(channel); err != nil {
		return nil, err
	}
	if err := channel.Confirm(false); err != nil {
		return nil, err
	}
	confirms := channel.NotifyPublish(make(chan amqp.Confirmation, 1))
	return &Publisher{channel: channel, confirms: confirms}, nil
}

func (p *Publisher) Close() { _ = p.channel.Close() }

func (p *Publisher) Publish(ctx context.Context, message events.Message) error {
	ctx, span := telemetry.Tracer().Start(ctx, "publish "+message.Type,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", events.Exchange),
			attribute.String("messaging.message.id", message.ID),
			attribute.String("order_id", message.OrderID)))
	defer span.End()

	err := p.publishAndAwaitConfirm(ctx, events.Exchange, message.Type, amqp.Publishing{
		ContentType:   "application/json",
		DeliveryMode:  amqp.Persistent,
		MessageId:     message.ID,
		CorrelationId: message.OrderID,
		Type:          message.Type,
		Timestamp:     message.OccurredAt,
		Headers:       amqp.Table{headerTraceparent: telemetry.Traceparent(ctx)},
		Body:          message.Body,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

func (p *Publisher) publishAndAwaitConfirm(ctx context.Context, exchange, routingKey string, publishing amqp.Publishing) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.channel.PublishWithContext(ctx, exchange, routingKey, false, false, publishing); err != nil {
		return err
	}
	select {
	case confirmation, open := <-p.confirms:
		if !open || !confirmation.Ack {
			return errors.New("broker did not confirm message")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(publisherConfirmTimeout):
		return errors.New("timeout waiting for publisher confirm")
	}
}

type QueueSpec struct {
	Name        string
	Keys        []string
	RetryDelays []time.Duration
}

var DefaultRetryDelays = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

func retryQueueName(queueName string, attempt int) string {
	return fmt.Sprintf("%s.retry.%d", queueName, attempt)
}

func DLQName(queueName string) string { return queueName + ".dlq" }

func Declare(channel *amqp.Channel, spec QueueSpec) error {
	if err := DeclareExchange(channel); err != nil {
		return err
	}
	if err := declareRetryQueues(channel, spec); err != nil {
		return err
	}
	if _, err := channel.QueueDeclare(DLQName(spec.Name), true, false, false, false, nil); err != nil {
		return err
	}
	return declareMainQueue(channel, spec)
}

func declareRetryQueues(channel *amqp.Channel, spec QueueSpec) error {
	for index, delay := range spec.RetryDelays {
		arguments := amqp.Table{
			"x-message-ttl":             int64(delay / time.Millisecond),
			"x-dead-letter-exchange":    directExchange,
			"x-dead-letter-routing-key": spec.Name,
		}
		attempt := index + 1
		if _, err := channel.QueueDeclare(retryQueueName(spec.Name, attempt), true, false, false, false, arguments); err != nil {
			return err
		}
	}
	return nil
}

func declareMainQueue(channel *amqp.Channel, spec QueueSpec) error {
	if _, err := channel.QueueDeclare(spec.Name, true, false, false, false, nil); err != nil {
		return err
	}
	for _, routingKey := range spec.Keys {
		if err := channel.QueueBind(spec.Name, routingKey, events.Exchange, false, nil); err != nil {
			return err
		}
	}
	return nil
}

var ErrPermanent = errors.New("permanent failure")

type Handler func(ctx context.Context, message events.Message) error

func Consume(ctx context.Context, connection *amqp.Connection, spec QueueSpec, handle Handler) error {
	channel, err := connection.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = channel.Close() }()
	if err := Declare(channel, spec); err != nil {
		return err
	}
	if err := channel.Qos(consumerPrefetchCount, 0, false); err != nil {
		return err
	}
	republisher, err := NewPublisher(connection)
	if err != nil {
		return err
	}
	defer republisher.Close()
	deliveries, err := channel.Consume(spec.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, open := <-deliveries:
			if !open {
				return errors.New("delivery channel closed")
			}
			processDelivery(ctx, republisher, spec, handle, delivery)
		}
	}
}

func processDelivery(ctx context.Context, republisher *Publisher, spec QueueSpec, handle Handler, delivery amqp.Delivery) {
	message := events.Message{
		ID:         delivery.MessageId,
		Type:       delivery.Type,
		OrderID:    delivery.CorrelationId,
		OccurredAt: delivery.Timestamp,
		Body:       delivery.Body,
	}
	traceparent, _ := delivery.Headers[headerTraceparent].(string)
	attempt := retryCount(delivery.Headers)

	consumerCtx := telemetry.ContextWithTraceparent(ctx, traceparent)
	consumerCtx = telemetry.WithOrderID(consumerCtx, message.OrderID)
	consumerCtx, span := telemetry.Tracer().Start(consumerCtx, "consume "+message.Type,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", spec.Name),
			attribute.String("messaging.message.id", message.ID),
			attribute.Int("messaging.retry_count", attempt),
			attribute.String("order_id", message.OrderID)))
	defer span.End()

	handlerErr := handleRecoveringPanic(consumerCtx, handle, message)
	if handlerErr == nil {
		_ = delivery.Ack(false)
		return
	}
	span.RecordError(handlerErr)
	span.SetStatus(codes.Error, handlerErr.Error())

	targetQueue, deadLettered := chooseFailureQueue(spec, attempt, handlerErr)
	republishErr := republishFailedDelivery(consumerCtx, republisher, delivery, targetQueue, attempt, handlerErr)
	if republishErr != nil {
		slog.ErrorContext(consumerCtx, "could not schedule retry; requeueing", "error", republishErr, "type", message.Type)
		_ = delivery.Nack(false, true)
		return
	}
	_ = delivery.Ack(false)
	logFailureOutcome(consumerCtx, message, attempt, targetQueue, deadLettered, handlerErr)
}

func chooseFailureQueue(spec QueueSpec, attempt int, handlerErr error) (queueName string, deadLettered bool) {
	if errors.Is(handlerErr, ErrPermanent) || attempt >= len(spec.RetryDelays) {
		return DLQName(spec.Name), true
	}
	return retryQueueName(spec.Name, attempt+1), false
}

func republishFailedDelivery(ctx context.Context, republisher *Publisher, delivery amqp.Delivery, targetQueue string, attempt int, handlerErr error) error {
	headers := amqp.Table{}
	for name, value := range delivery.Headers {
		headers[name] = value
	}
	headers[headerRetryCount] = int32(attempt + 1)
	headers[headerLastError] = handlerErr.Error()
	headers[headerTraceparent] = telemetry.Traceparent(ctx)
	return republisher.publishAndAwaitConfirm(ctx, directExchange, targetQueue, amqp.Publishing{
		ContentType:   delivery.ContentType,
		DeliveryMode:  amqp.Persistent,
		MessageId:     delivery.MessageId,
		CorrelationId: delivery.CorrelationId,
		Type:          delivery.Type,
		Timestamp:     delivery.Timestamp,
		Headers:       headers,
		Body:          delivery.Body,
	})
}

func logFailureOutcome(ctx context.Context, message events.Message, attempt int, targetQueue string, deadLettered bool, handlerErr error) {
	if deadLettered {
		slog.ErrorContext(ctx, "message moved to DLQ", "type", message.Type, "message_id", message.ID, "attempts", attempt+1, "dlq", targetQueue, "error", handlerErr)
		return
	}
	slog.WarnContext(ctx, "message handling failed; retry scheduled", "type", message.Type, "message_id", message.ID, "attempt", attempt+1, "retry_queue", targetQueue, "error", handlerErr)
}

func handleRecoveringPanic(ctx context.Context, handle Handler, message events.Message) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("handler panic: %v", recovered)
		}
	}()
	return handle(ctx, message)
}

func retryCount(headers amqp.Table) int {
	switch count := headers[headerRetryCount].(type) {
	case int32:
		return int(count)
	case int64:
		return int(count)
	case int:
		return count
	}
	return 0
}
