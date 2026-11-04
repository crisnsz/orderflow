package rabbitmq

import (
	"context"
	"fmt"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const publisherConfirmTimeout = 10 * time.Second

type Publisher struct {
	publisher *rabbitmqamqp.Publisher
}

func NewPublisher(ctx context.Context, connection *rabbitmqamqp.AmqpConnection) (*Publisher, error) {
	if err := declareExchanges(ctx, connection.Management()); err != nil {
		return nil, err
	}
	publisher, err := connection.NewPublisher(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	return &Publisher{publisher: publisher}, nil
}

func (p *Publisher) Close() { _ = p.publisher.Close(context.Background()) }

func (p *Publisher) Publish(ctx context.Context, message events.Message) error {
	ctx, span := telemetry.Tracer().Start(ctx, "publish "+message.Type,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", events.Exchange),
			attribute.String("messaging.message.id", message.ID),
			attribute.String("order_id", message.OrderID)))
	defer span.End()

	err := p.publishAndAwaitOutcome(ctx, message)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

func (p *Publisher) publishAndAwaitOutcome(ctx context.Context, message events.Message) error {
	encoded := encodeMessage(message, telemetry.Traceparent(ctx))
	address := &rabbitmqamqp.ExchangeAddress{Exchange: events.Exchange, Key: message.Type}
	if err := rabbitmqamqp.MessagePropertyToAddress(encoded, address); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, publisherConfirmTimeout)
	defer cancel()
	result, err := p.publisher.Publish(ctx, encoded)
	if err != nil {
		return err
	}
	if _, accepted := result.Outcome.(*rabbitmqamqp.StateAccepted); accepted {
		return nil
	}
	if result.MessageRejectedError != nil {
		return result.MessageRejectedError
	}
	return fmt.Errorf("broker did not accept message: %T", result.Outcome)
}
