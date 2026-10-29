package fetching

import "github.com/crisnsz/orderflow/services/orders/internal/domain"

type Service struct{ repository domain.Repository }

func NewService(repository domain.Repository) *Service { return &Service{repository: repository} }
