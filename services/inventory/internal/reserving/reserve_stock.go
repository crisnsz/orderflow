package reserving

import (
	"context"
	"log/slog"

	"github.com/crisnsz/orderflow/services/inventory/internal/domain"
)

type Service struct{ repository domain.Repository }

func NewService(repository domain.Repository) *Service { return &Service{repository: repository} }

func (s *Service) Reserve(ctx context.Context, orderID string, lines []domain.Line) (domain.Reservation, error) {
	var reservation domain.Reservation
	err := s.repository.InTx(ctx, func(tx domain.Tx) error {
		existing, err := tx.GetReservation(ctx, orderID)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.Status == domain.Released {
				return domain.ErrAlreadyReleased
			}
			reservation = *existing
			slog.InfoContext(ctx, "reserve replayed (idempotent)", "lines", len(existing.Lines))
			return nil
		}
		reservation, err = reserveNewStock(ctx, tx, orderID, lines)
		return err
	})
	if err != nil {
		return domain.Reservation{}, err
	}
	return reservation, nil
}

func reserveNewStock(ctx context.Context, tx domain.Tx, orderID string, lines []domain.Line) (domain.Reservation, error) {
	skus := make([]string, 0, len(lines))
	for _, line := range lines {
		skus = append(skus, line.SKU)
	}
	products, err := tx.LockProducts(ctx, skus)
	if err != nil {
		return domain.Reservation{}, err
	}
	pricedLines, err := domain.Plan(products, lines)
	if err != nil {
		return domain.Reservation{}, err
	}
	for _, line := range pricedLines {
		if err := tx.AdjustStock(ctx, line.SKU, -line.Quantity); err != nil {
			return domain.Reservation{}, err
		}
	}
	reservation := domain.Reservation{OrderID: orderID, Status: domain.Reserved, Lines: pricedLines}
	return reservation, tx.SaveReservation(ctx, reservation)
}
