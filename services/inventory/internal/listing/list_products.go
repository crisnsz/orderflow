package listing

import (
	"context"

	"github.com/crisnsz/orderflow/services/inventory/internal/domain"
)

type Service struct{ repository domain.Repository }

func NewService(repository domain.Repository) *Service { return &Service{repository: repository} }

func (s *Service) ListProducts(ctx context.Context) ([]domain.Product, error) {
	return s.repository.ListProducts(ctx)
}
