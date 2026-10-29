package placing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
	"github.com/google/uuid"
)

func toEventItems(items []domain.Item) []events.Item {
	eventItems := make([]events.Item, 0, len(items))
	for _, item := range items {
		eventItems = append(eventItems, events.Item{SKU: item.SKU, Quantity: item.Quantity, UnitPriceCents: item.UnitPriceCents})
	}
	return eventItems
}

func (s *Service) PlaceOrder(ctx context.Context, userID string, items []domain.Item) (*domain.Order, error) {
	if err := validatePlaceOrder(userID, items); err != nil {
		return nil, err
	}
	order := domain.NewOrder(uuid.NewString(), userID, items, s.now())
	ctx = telemetry.WithOrderID(ctx, order.ID)

	if err := s.recordPlacedOrder(ctx, order); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "order placed", "user_id", userID, "items", len(items))

	pricedItems, err := s.inventory.Reserve(ctx, order.ID, items)
	if err != nil {
		return s.cancelAfterReservationFailure(ctx, order.ID, err)
	}
	if err := s.confirmReservedOrder(ctx, order.ID, pricedItems); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "inventory reserved; order confirmed", "state", domain.InventoryReserved)
	return s.repository.Get(ctx, order.ID)
}

func validatePlaceOrder(userID string, items []domain.Item) error {
	if userID == "" || len(items) == 0 {
		return fmt.Errorf("%w: user and at least one item are required", ErrInvalid)
	}
	for _, item := range items {
		if item.SKU == "" || item.Quantity <= 0 {
			return fmt.Errorf("%w: every item needs a sku and a positive quantity", ErrInvalid)
		}
	}
	return nil
}

func (s *Service) recordPlacedOrder(ctx context.Context, order *domain.Order) error {
	return s.repository.InTx(ctx, func(tx domain.Tx) error {
		if err := tx.Insert(ctx, order); err != nil {
			return err
		}
		return domain.QueueEvent(ctx, tx, events.OrderPlaced, events.Payload{
			OrderID: order.ID, UserID: order.UserID, Items: toEventItems(order.Items)})
	})
}

func (s *Service) confirmReservedOrder(ctx context.Context, orderID string, pricedItems []domain.Item) error {
	return s.repository.InTx(ctx, func(tx domain.Tx) error {
		order, err := tx.Load(ctx, orderID)
		if err != nil {
			return err
		}
		order.Price(pricedItems)
		if err := order.Apply(domain.InventoryReservedTrigger, "", s.now()); err != nil {
			return err
		}
		if err := tx.Save(ctx, order); err != nil {
			return err
		}
		return domain.QueueEvent(ctx, tx, events.OrderConfirmed, events.Payload{
			OrderID: order.ID, UserID: order.UserID, AmountCents: order.TotalCents,
			Currency: orderCurrency, Items: toEventItems(order.Items)})
	})
}

func (s *Service) cancelAfterReservationFailure(ctx context.Context, orderID string, reserveErr error) (*domain.Order, error) {
	rejected := errors.Is(reserveErr, domain.ErrRejected)
	reason := "inventory rejected the order: " + reserveErr.Error()
	if !rejected {
		reason = "inventory unavailable"
		_ = s.inventory.Release(ctx, orderID)
	}
	if err := s.cancelBeforeReservation(ctx, orderID, reason); err != nil {
		return nil, err
	}
	slog.WarnContext(ctx, "order cancelled at reservation", "reason", reason)
	if !rejected {
		return nil, fmt.Errorf("reserve stock: %w", reserveErr)
	}
	return s.repository.Get(ctx, orderID)
}

func (s *Service) cancelBeforeReservation(ctx context.Context, orderID, reason string) error {
	return s.repository.InTx(ctx, func(tx domain.Tx) error {
		order, err := tx.Load(ctx, orderID)
		if err != nil {
			return err
		}
		if err := order.Apply(domain.InventoryRejected, reason, s.now()); err != nil {
			return err
		}
		if err := tx.Save(ctx, order); err != nil {
			return err
		}
		return domain.QueueEvent(ctx, tx, events.OrderCancelled, events.Payload{
			OrderID: order.ID, UserID: order.UserID, Reason: reason})
	})
}
