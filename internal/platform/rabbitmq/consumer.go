package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	consumerPrefetchCount = 5
	settleTimeout         = 5 * time.Second
	maxLastErrorLength    = 1024
	annotationDeliveryAt  = "x-opt-delivery-time"
)

type Handler func(ctx context.Context, message events.Message) error

func Consume(ctx context.Context, connection *rabbitmqamqp.AmqpConnection, spec QueueSpec, handle Handler) error {
	if err := Declare(ctx, connection, spec); err != nil {
		return err
	}
	consumer, err := connection.NewConsumer(ctx, spec.Name, &rabbitmqamqp.ConsumerOptions{InitialCredits: consumerPrefetchCount})
	if err != nil {
		return err
	}
	defer func() { _ = consumer.Close(context.WithoutCancel(ctx)) }()
	for {
		delivery, err := consumer.Receive(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		processDelivery(ctx, spec, handle, delivery)
	}
}

func processDelivery(ctx context.Context, spec QueueSpec, handle Handler, delivery rabbitmqamqp.IDeliveryContext) {
	encoded := delivery.Message()
	failedAttempts := failedDeliveries(encoded)
	message, decodeErr := decodeMessage(encoded)

	consumerCtx := telemetry.ContextWithTraceparent(ctx, message.Traceparent)
	consumerCtx = telemetry.WithOrderID(consumerCtx, message.OrderID)
	consumerCtx, span := telemetry.Tracer().Start(consumerCtx, "consume "+message.Type,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", spec.Name),
			attribute.String("messaging.message.id", message.ID),
			attribute.Int("messaging.retry_count", failedAttempts),
			attribute.String("order_id", message.OrderID)))
	defer span.End()

	handlerErr := decodeErr
	if handlerErr == nil {
		handlerErr = handleRecoveringPanic(consumerCtx, handle, message)
	}
	if handlerErr == nil {
		settle(ctx, "accept", delivery.Accept)
		return
	}
	span.RecordError(handlerErr)
	span.SetStatus(codes.Error, handlerErr.Error())
	settleFailure(consumerCtx, spec, delivery, message, failedAttempts+1, handlerErr)
}

func settleFailure(ctx context.Context, spec QueueSpec, delivery rabbitmqamqp.IDeliveryContext, message events.Message, attempt int, handlerErr error) {
	if ctx.Err() != nil {
		settle(ctx, "release", delivery.Requeue)
		return
	}
	annotations := amqp.Annotations{annotationLastError: truncate(handlerErr.Error(), maxLastErrorLength)}
	if errors.Is(handlerErr, ErrPermanent) || !spec.Retry.retriesEnabled() {
		settle(ctx, "discard", func(settleCtx context.Context) error { return delivery.DiscardWithAnnotations(settleCtx, annotations) })
		logDeadLettered(ctx, spec, message, attempt, discardReason(spec, handlerErr), handlerErr)
		return
	}
	delay := spec.Retry.DelayAfter(attempt)
	annotations[annotationDeliveryAt] = time.Now().Add(delay).UnixMilli()
	settle(ctx, "delay retry", func(settleCtx context.Context) error {
		return delivery.RequeueWithAnnotationsAndDeliveryFailed(settleCtx, annotations, true)
	})
	if spec.Retry.exhaustedAfter(attempt) {
		logDeadLettered(ctx, spec, message, attempt, "delivery limit reached", handlerErr)
		return
	}
	slog.WarnContext(ctx, "message handling failed; retry scheduled",
		"type", message.Type, "message_id", message.ID, "attempt", attempt, "retry_in", delay, "error", handlerErr)
}

func discardReason(spec QueueSpec, handlerErr error) string {
	if errors.Is(handlerErr, ErrPermanent) {
		return "permanent failure"
	}
	return "retries disabled for " + spec.Name
}

func logDeadLettered(ctx context.Context, spec QueueSpec, message events.Message, attempt int, reason string, handlerErr error) {
	slog.ErrorContext(ctx, "message moved to DLQ",
		"type", message.Type, "message_id", message.ID, "attempts", attempt, "dlq", DLQName(spec.Name), "reason", reason, "error", handlerErr)
}

func settle(ctx context.Context, action string, settleDelivery func(context.Context) error) {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err := settleDelivery(settleCtx); err != nil {
		slog.WarnContext(ctx, "could not settle delivery", "action", action, "error", err)
	}
}

func failedDeliveries(encoded *amqp.Message) int {
	if encoded.Header == nil {
		return 0
	}
	return int(encoded.Header.DeliveryCount)
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

func handleRecoveringPanic(ctx context.Context, handle Handler, message events.Message) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("handler panic: %v", recovered)
		}
	}()
	return handle(ctx, message)
}
