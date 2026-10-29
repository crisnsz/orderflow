package fetching

import (
	"context"

	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

const recentOrdersLimit = 20

func (s *Service) GetOrder(ctx context.Context, orderID string) (*domain.Order, error) {
	return s.repository.Get(ctx, orderID)
}

func (s *Service) ListOrders(ctx context.Context, userID string) ([]*domain.Order, error) {
	return s.repository.ListByUser(ctx, userID, recentOrdersLimit)
}
