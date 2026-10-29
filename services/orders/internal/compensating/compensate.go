package compensating

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

const defaultCancelReason = "payment failed"

func (s *Service) Compensate(ctx context.Context, tx domain.Tx, order *domain.Order, reason string) error {
	if reason == "" {
		reason = defaultCancelReason
	}
	if err := order.Apply(domain.PaymentFailed, reason, s.now()); err != nil {
		if errors.Is(err, domain.ErrInvalidTransition) {
			slog.WarnContext(ctx, "payment.failed ignored: not valid in current state", "state", order.State)
			return nil
		}
		return err
	}
	slog.WarnContext(ctx, "payment failed; compensating", "reason", reason)
	if err := s.inventory.Release(ctx, order.ID); err != nil {
		return fmt.Errorf("release stock: %w", err)
	}
	if err := order.Apply(domain.InventoryReleased, "", s.now()); err != nil {
		return err
	}
	if err := tx.Save(ctx, order); err != nil {
		return err
	}
	slog.WarnContext(ctx, "order cancelled after compensation", "reason", order.CancelReason)
	return domain.QueueEvent(ctx, tx, events.OrderCancelled, events.Payload{
		OrderID: order.ID, UserID: order.UserID, Reason: order.CancelReason})
}
