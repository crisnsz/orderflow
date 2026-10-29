package releasing

import (
	"context"
	"log/slog"

	"github.com/crisnsz/orderflow/services/inventory/internal/domain"
)

type Service struct{ repository domain.Repository }

func NewService(repository domain.Repository) *Service { return &Service{repository: repository} }

func (s *Service) Release(ctx context.Context, orderID string) (bool, error) {
	released := false
	err := s.repository.InTx(ctx, func(tx domain.Tx) error {
		reservation, err := tx.GetReservation(ctx, orderID)
		if err != nil {
			return err
		}
		if reservation == nil || reservation.Status == domain.Released {
			return nil
		}
		for _, line := range reservation.Lines {
			if err := tx.AdjustStock(ctx, line.SKU, line.Quantity); err != nil {
				return err
			}
		}
		released = true
		return tx.MarkReleased(ctx, orderID)
	})
	if err == nil && released {
		slog.InfoContext(ctx, "stock released")
	}
	return released, err
}
