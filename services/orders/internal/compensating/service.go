package compensating

import (
	"time"

	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

type Service struct {
	inventory domain.Inventory
	now       func() time.Time
}

func NewService(inventory domain.Inventory) *Service {
	return &Service{inventory: inventory, now: func() time.Time { return time.Now().UTC() }}
}
