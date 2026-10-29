package advancing

import (
	"context"
	"errors"
	"time"

	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

const Consumer = "orders"

var ErrPermanent = errors.New("permanent failure")

type Compensator interface {
	Compensate(ctx context.Context, tx domain.Tx, order *domain.Order, reason string) error
}

type Service struct {
	repository  domain.Repository
	compensator Compensator
	now         func() time.Time
}

func NewService(repository domain.Repository, compensator Compensator) *Service {
	return &Service{
		repository:  repository,
		compensator: compensator,
		now:         func() time.Time { return time.Now().UTC() },
	}
}
