package notifying

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/notifications/internal/domain"
)

func (s *Service) HandleEvent(ctx context.Context, message events.Message) error {
	payload, err := message.Decode()
	if err != nil {
		return fmt.Errorf("decode %s: %w: %w", message.Type, err, ErrPermanent)
	}
	ctx = telemetry.WithOrderID(ctx, payload.OrderID)
	preferences, err := s.store.Preferences(ctx, payload.UserID)
	if err != nil {
		return err
	}
	notifications := domain.Plan(preferences, domain.Event{
		Type: message.Type, OrderID: payload.OrderID, AmountCents: payload.AmountCents,
		InvoiceID: payload.InvoiceID, Reason: payload.Reason})
	if len(notifications) == 0 {
		slog.DebugContext(ctx, "nothing to notify", "type", message.Type, "user_id", payload.UserID)
		return nil
	}
	processed, err := s.store.Once(ctx, Consumer, message.ID, func(tx Tx) error {
		return s.sendAll(ctx, tx, message.ID, notifications)
	})
	if err == nil && !processed {
		slog.InfoContext(ctx, "duplicate message ignored", "type", message.Type, "message_id", message.ID)
	}
	return err
}

func (s *Service) sendAll(ctx context.Context, tx Tx, messageID string, notifications []domain.Notification) error {
	for _, notification := range notifications {
		sender, ok := s.senders[notification.Channel]
		if !ok {
			return fmt.Errorf("no sender for channel %q: %w", notification.Channel, ErrPermanent)
		}
		if err := sender.Send(ctx, notification); err != nil {
			return fmt.Errorf("send %s: %w", notification.Channel, err)
		}
		if err := tx.RecordSent(ctx, messageID, notification); err != nil {
			return err
		}
	}
	return nil
}
