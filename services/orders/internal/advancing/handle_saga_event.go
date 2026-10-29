package advancing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

func (s *Service) HandleEvent(ctx context.Context, message events.Message) error {
	payload, err := message.Decode()
	if err != nil {
		return fmt.Errorf("decode %s: %w: %w", message.Type, err, ErrPermanent)
	}
	ctx = telemetry.WithOrderID(ctx, payload.OrderID)
	processed, err := s.repository.Once(ctx, Consumer, message.ID, func(tx domain.Tx) error {
		order, err := tx.Load(ctx, payload.OrderID)
		if err != nil {
			return err
		}
		return s.react(ctx, tx, order, message.Type, payload)
	})
	if err != nil {
		return err
	}
	if !processed {
		slog.InfoContext(ctx, "duplicate message ignored", "type", message.Type, "message_id", message.ID)
	}
	return nil
}

func (s *Service) react(ctx context.Context, tx domain.Tx, order *domain.Order, eventType string, payload events.Payload) error {
	switch eventType {
	case events.InvoiceGenerated:
		return s.applyTrigger(ctx, tx, order, domain.InvoiceGenerated)
	case events.PaymentSucceeded:
		return s.completeAfterPayment(ctx, tx, order)
	case events.PaymentFailed:
		return s.compensator.Compensate(ctx, tx, order, payload.Reason)
	case events.PaymentPendingManual:
		slog.InfoContext(ctx, "payment requires manual action; order stays invoiced", "state", order.State)
		return nil
	default:
		slog.WarnContext(ctx, "ignoring unexpected event type", "type", eventType)
		return nil
	}
}

func (s *Service) completeAfterPayment(ctx context.Context, tx domain.Tx, order *domain.Order) error {
	if err := s.applyTrigger(ctx, tx, order, domain.PaymentSucceeded); err != nil {
		return err
	}
	if order.State != domain.Paid {
		return nil
	}
	if err := order.Apply(domain.OrderFulfilled, "", s.now()); err != nil {
		return err
	}
	if err := tx.Save(ctx, order); err != nil {
		return err
	}
	slog.InfoContext(ctx, "order completed")
	return domain.QueueEvent(ctx, tx, events.OrderCompleted, events.Payload{
		OrderID: order.ID, UserID: order.UserID, AmountCents: order.TotalCents})
}

func (s *Service) applyTrigger(ctx context.Context, tx domain.Tx, order *domain.Order, trigger domain.Trigger) error {
	if err := order.Apply(trigger, "", s.now()); err != nil {
		if errors.Is(err, domain.ErrInvalidTransition) {
			slog.WarnContext(ctx, "event ignored: not valid in current state", "state", order.State, "trigger", trigger)
			return nil
		}
		return err
	}
	slog.InfoContext(ctx, "saga advanced", "state", order.State, "trigger", trigger)
	return tx.Save(ctx, order)
}
