package charging

import (
	"context"
	"errors"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/payments/internal/domain"
)

var ErrPermanent = errors.New("permanent failure")

var ErrDeclined = errors.New("declined by bank")

type Bank interface {
	Authorize(ctx context.Context, idempotencyKey, cardToken string, amountCents int64) (authorizationRef string, err error)
	Sale(ctx context.Context, idempotencyKey, authorizationRef string, amountCents int64) (saleRef string, err error)
	Void(ctx context.Context, idempotencyKey, authorizationRef string) error
}

type Tx interface {
	SavePayment(ctx context.Context, payment domain.Payment) error
	Outbox(ctx context.Context, message events.Message) error
}

type Store interface {
	Once(ctx context.Context, consumer, messageID string, work func(Tx) error) (bool, error)
	CardFor(ctx context.Context, userID string) (*domain.Card, error)
}
