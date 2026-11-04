package placing

import (
	"errors"
	"time"

	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

var ErrInvalid = errors.New("invalid request")

const (
	orderCurrency = "EUR"

	releaseMaxAttempts = 3
	releaseRetryDelay  = 100 * time.Millisecond
)

type Service struct {
	repository domain.Repository
	inventory  domain.Inventory
	now        func() time.Time
}

func NewService(repository domain.Repository, inventory domain.Inventory) *Service {
	return &Service{
		repository: repository,
		inventory:  inventory,
		now:        func() time.Time { return time.Now().UTC() },
	}
}
