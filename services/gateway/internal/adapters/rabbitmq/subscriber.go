package rabbitmq

import (
	"context"
	"log/slog"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/services/gateway/internal/timeline"
)

func Run(ctx context.Context, broker *rabbitmq.Broker, hub *timeline.Hub) error {
	spec := rabbitmq.QueueSpec{Name: "gateway.sse", Keys: []string{"#"}}
	return broker.Consume(ctx, spec, func(ctx context.Context, message events.Message) error {
		payload, err := message.Decode()
		if err != nil {
			slog.WarnContext(ctx, "dropping undecodable event", "type", message.Type, "error", err)
			return nil
		}
		occurredAt := message.OccurredAt
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		hub.Publish(timeline.Event{
			ID: message.ID, Type: message.Type, OrderID: payload.OrderID, UserID: payload.UserID,
			At: occurredAt, Data: message.Body})
		slog.InfoContext(ctx, "event forwarded to sse hub", "type", message.Type, "message_id", message.ID)
		return nil
	})
}
