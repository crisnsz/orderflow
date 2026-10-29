package orderstest

import (
	"context"

	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

const unitPriceCents = 1000

type FakeInventory struct {
	ReserveErr   error
	ReleaseErr   error
	ReleaseCalls int
	Released     int
}

var _ domain.Inventory = (*FakeInventory)(nil)

func (f *FakeInventory) Reserve(_ context.Context, _ string, items []domain.Item) ([]domain.Item, error) {
	if f.ReserveErr != nil {
		return nil, f.ReserveErr
	}
	pricedItems := make([]domain.Item, len(items))
	for index, item := range items {
		item.UnitPriceCents = unitPriceCents
		pricedItems[index] = item
	}
	return pricedItems, nil
}

func (f *FakeInventory) Release(context.Context, string) error {
	f.ReleaseCalls++
	if f.ReleaseErr != nil {
		return f.ReleaseErr
	}
	f.Released++
	return nil
}
