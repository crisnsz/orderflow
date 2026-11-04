package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/services/billing/internal/invoicing"
)

func Spec() rabbitmq.QueueSpec {
	return rabbitmq.QueueSpec{Name: "billing.q", Keys: []string{events.OrderConfirmed}, Retry: rabbitmq.DefaultRetryPolicy}
}

func Handler(service *invoicing.Service) rabbitmq.Handler {
	return func(ctx context.Context, message events.Message) error {
		err := service.HandleEvent(ctx, message)
		if errors.Is(err, invoicing.ErrPermanent) {
			return fmt.Errorf("%w: %w", rabbitmq.ErrPermanent, err)
		}
		return err
	}
}
