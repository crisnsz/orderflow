package charging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/payments/internal/domain"
)

func (s *Service) HandleEvent(ctx context.Context, message events.Message) error {
	if message.Type != events.InvoiceGenerated {
		slog.WarnContext(ctx, "ignoring unexpected event type", "type", message.Type)
		return nil
	}
	invoice, err := message.Decode()
	if err != nil {
		return fmt.Errorf("decode %s: %w: %w", message.Type, err, ErrPermanent)
	}
	ctx = telemetry.WithOrderID(ctx, invoice.OrderID)

	card, err := s.store.CardFor(ctx, invoice.UserID)
	if err != nil {
		return err
	}
	decision := domain.Decide(card, s.now())

	processed, err := s.store.Once(ctx, Consumer, message.ID, func(tx Tx) error {
		return s.settleInvoice(ctx, tx, invoice, card, decision)
	})
	if err == nil && !processed {
		slog.InfoContext(ctx, "duplicate message ignored", "type", message.Type, "message_id", message.ID)
	}
	return err
}

func (s *Service) settleInvoice(ctx context.Context, tx Tx, invoice events.Payload, card *domain.Card, decision domain.Decision) error {
	payment := domain.Payment{
		OrderID: invoice.OrderID, UserID: invoice.UserID, InvoiceID: invoice.InvoiceID, AmountCents: invoice.AmountCents}
	outgoingEvent := events.Payload{
		OrderID: invoice.OrderID, UserID: invoice.UserID, InvoiceID: invoice.InvoiceID,
		AmountCents: invoice.AmountCents, Currency: invoice.Currency}

	var eventType string
	if decision.Action == domain.AwaitManual {
		eventType = awaitManualPayment(ctx, &payment, &outgoingEvent, decision)
	} else {
		var err error
		eventType, err = s.chargeSavedCard(ctx, &payment, &outgoingEvent, card.Token)
		if err != nil {
			return err
		}
	}

	if err := tx.SavePayment(ctx, payment); err != nil {
		return err
	}
	message, err := events.New(eventType, outgoingEvent)
	if err != nil {
		return err
	}
	return tx.Outbox(ctx, message)
}

func awaitManualPayment(ctx context.Context, payment *domain.Payment, outgoingEvent *events.Payload, decision domain.Decision) (eventType string) {
	payment.Status, payment.Reason = domain.PendingManual, decision.Reason
	outgoingEvent.Reason = decision.Reason
	slog.InfoContext(ctx, "payment awaits manual action", "reason", decision.Reason)
	return events.PaymentPendingManual
}

func (s *Service) chargeSavedCard(ctx context.Context, payment *domain.Payment, outgoingEvent *events.Payload, cardToken string) (eventType string, err error) {
	authorizationRef, saleRef, chargeErr := s.charge(ctx, payment.OrderID, cardToken, payment.AmountCents)
	payment.AuthorizationRef, payment.SaleRef = authorizationRef, saleRef
	switch {
	case chargeErr == nil:
		payment.Status = domain.Succeeded
		outgoingEvent.Reference = saleRef
		slog.InfoContext(ctx, "payment succeeded", "auth_ref", authorizationRef, "sale_ref", saleRef, "amount_cents", payment.AmountCents)
		return events.PaymentSucceeded, nil
	case errors.Is(chargeErr, ErrDeclined):
		payment.Status, payment.Reason = domain.Failed, chargeErr.Error()
		outgoingEvent.Reason = chargeErr.Error()
		slog.WarnContext(ctx, "payment failed", "reason", chargeErr.Error())
		return events.PaymentFailed, nil
	default:
		return "", chargeErr
	}
}
