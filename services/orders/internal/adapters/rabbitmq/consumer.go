package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/services/orders/internal/advancing"
)

func Spec() rabbitmq.QueueSpec {
	return rabbitmq.QueueSpec{
		Name:  "orders.q",
		Keys:  []string{events.InvoiceGenerated, events.PaymentSucceeded, events.PaymentFailed, events.PaymentPendingManual},
		Retry: rabbitmq.DefaultRetryPolicy,
	}
}

func Handler(service *advancing.Service) rabbitmq.Handler {
	return func(ctx context.Context, message events.Message) error {
		err := service.HandleEvent(ctx, message)
		if errors.Is(err, advancing.ErrPermanent) {
			return fmt.Errorf("%w: %w", rabbitmq.ErrPermanent, err)
		}
		return err
	}
}
