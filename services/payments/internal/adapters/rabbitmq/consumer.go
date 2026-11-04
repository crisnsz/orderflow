package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/services/payments/internal/charging"
)

func Spec() rabbitmq.QueueSpec {
	return rabbitmq.QueueSpec{Name: "payments.q", Keys: []string{events.InvoiceGenerated}, Retry: rabbitmq.DefaultRetryPolicy}
}

func Handler(service *charging.Service) rabbitmq.Handler {
	return func(ctx context.Context, message events.Message) error {
		err := service.HandleEvent(ctx, message)
		if errors.Is(err, charging.ErrPermanent) {
			return fmt.Errorf("%w: %w", rabbitmq.ErrPermanent, err)
		}
		return err
	}
}
