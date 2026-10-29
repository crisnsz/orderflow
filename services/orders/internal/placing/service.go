package placing

import (
	"errors"
	"time"

	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

var ErrInvalid = errors.New("invalid request")

const orderCurrency = "EUR"

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
