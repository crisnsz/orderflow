package invoicing

import (
	"context"
	"errors"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/billing/internal/domain"
)

var ErrPermanent = errors.New("permanent failure")

type Tx interface {
	SaveInvoice(ctx context.Context, invoice domain.Invoice) (number string, created bool, err error)
	Outbox(ctx context.Context, message events.Message) error
}

type Store interface {
	Once(ctx context.Context, consumer, messageID string, work func(Tx) error) (bool, error)
}
