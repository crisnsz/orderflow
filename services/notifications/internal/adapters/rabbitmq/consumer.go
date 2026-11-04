package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/services/notifications/internal/notifying"
)

func Spec() rabbitmq.QueueSpec {
	return rabbitmq.QueueSpec{
		Name: "notifications.q",
		Keys: []string{events.OrderConfirmed, events.InvoiceGenerated, events.PaymentSucceeded, events.PaymentFailed,
			events.PaymentPendingManual, events.OrderCancelled, events.OrderCompleted},
		Retry: rabbitmq.DefaultRetryPolicy,
	}
}

func Handler(service *notifying.Service) rabbitmq.Handler {
	return func(ctx context.Context, message events.Message) error {
		err := service.HandleEvent(ctx, message)
		if errors.Is(err, notifying.ErrPermanent) {
			return fmt.Errorf("%w: %w", rabbitmq.ErrPermanent, err)
		}
		return err
	}
}
