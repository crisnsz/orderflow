package domain

import "context"

type Tx interface {
	GetReservation(ctx context.Context, orderID string) (*Reservation, error)
	LockProducts(ctx context.Context, skus []string) (map[string]Product, error)
	AdjustStock(ctx context.Context, sku string, delta int32) error
	SaveReservation(ctx context.Context, reservation Reservation) error
	MarkReleased(ctx context.Context, orderID string) error
}

type Repository interface {
	ListProducts(ctx context.Context) ([]Product, error)
	InTx(ctx context.Context, work func(Tx) error) error
}
