package invoicing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/billing/internal/domain"
)

func (s *Service) HandleEvent(ctx context.Context, message events.Message) error {
	if message.Type != events.OrderConfirmed {
		slog.WarnContext(ctx, "ignoring unexpected event type", "type", message.Type)
		return nil
	}
	payload, err := message.Decode()
	if err != nil {
		return fmt.Errorf("decode %s: %w: %w", message.Type, err, ErrPermanent)
	}
	ctx = telemetry.WithOrderID(ctx, payload.OrderID)

	invoice, err := domain.NewInvoice(payload.OrderID, payload.UserID, payload.Currency, billableLines(payload.Items), s.now())
	if err != nil {
		return fmt.Errorf("build invoice: %w: %w", err, ErrPermanent)
	}
	processed, err := s.store.Once(ctx, Consumer, message.ID, func(tx Tx) error {
		return s.issueInvoice(ctx, tx, payload, invoice)
	})
	if err == nil && !processed {
		slog.InfoContext(ctx, "duplicate message ignored", "type", message.Type, "message_id", message.ID)
	}
	return err
}

func billableLines(items []events.Item) []domain.Line {
	lines := make([]domain.Line, 0, len(items))
	for _, item := range items {
		lines = append(lines, domain.Line{SKU: item.SKU, Quantity: item.Quantity, UnitPriceCents: item.UnitPriceCents})
	}
	return lines
}

func (s *Service) issueInvoice(ctx context.Context, tx Tx, payload events.Payload, invoice domain.Invoice) error {
	number, created, err := tx.SaveInvoice(ctx, invoice)
	if err != nil {
		return err
	}
	if !created {
		slog.InfoContext(ctx, "invoice already exists for order", "invoice", number)
		return nil
	}
	invoiceGenerated, err := events.New(events.InvoiceGenerated, events.Payload{
		OrderID: payload.OrderID, UserID: payload.UserID, InvoiceID: number,
		AmountCents: invoice.TotalCents, Currency: invoice.Currency})
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "invoice generated", "invoice", number, "amount_cents", invoice.TotalCents)
	return tx.Outbox(ctx, invoiceGenerated)
}
