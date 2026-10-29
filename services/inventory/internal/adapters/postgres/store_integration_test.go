//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/crisnsz/orderflow/services/inventory/internal/adapters/postgres"
	"github.com/crisnsz/orderflow/services/inventory/internal/domain"
	"github.com/crisnsz/orderflow/services/inventory/internal/releasing"
	"github.com/crisnsz/orderflow/services/inventory/internal/reserving"
	"github.com/crisnsz/orderflow/test/containers"
)

func setup(t *testing.T) (reservations *reserving.Service, releases *releasing.Service, stockOf func(sku string) int32) {
	t.Helper()
	ctx := context.Background()
	pool := containers.Postgres(t)
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(pool)
	stockOf = func(sku string) int32 {
		products, _ := store.ListProducts(ctx)
		for _, product := range products {
			if product.SKU == sku {
				return product.Stock
			}
		}
		t.Fatalf("sku %s missing", sku)
		return 0
	}
	return reserving.NewService(store), releasing.NewService(store), stockOf
}

func TestReserveIsIdempotentAndReleaseRestoresStockOnce(t *testing.T) {
	ctx := context.Background()
	reservations, releases, stockOf := setup(t)
	stockBefore := stockOf("KB-01")

	for i := 0; i < 3; i++ {
		reservation, err := reservations.Reserve(ctx, "order-1", []domain.Line{{SKU: "KB-01", Quantity: 2}})
		if err != nil || reservation.Total() != 2*8900 {
			t.Fatalf("reserve #%d: total=%d err=%v", i, reservation.Total(), err)
		}
	}
	if got := stockOf("KB-01"); got != stockBefore-2 {
		t.Fatalf("stock = %d after 3 identical reserves, want %d", got, stockBefore-2)
	}

	for i := 0; i < 2; i++ {
		released, err := releases.Release(ctx, "order-1")
		if err != nil || released != (i == 0) {
			t.Fatalf("release #%d: released=%v err=%v", i, released, err)
		}
	}
	if got := stockOf("KB-01"); got != stockBefore {
		t.Fatalf("stock = %d after release, want %d", got, stockBefore)
	}
	if _, err := reservations.Reserve(ctx, "order-1", []domain.Line{{SKU: "KB-01", Quantity: 2}}); !errors.Is(err, domain.ErrAlreadyReleased) {
		t.Fatalf("reserve after release: err = %v, want ErrAlreadyReleased", err)
	}
}

func TestConcurrentReservationsNeverOversell(t *testing.T) {
	ctx := context.Background()
	reservations, _, stockOf := setup(t)
	available := int(stockOf("MON-01"))

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := reservations.Reserve(ctx, fmt.Sprintf("order-c%d", i), []domain.Line{{SKU: "MON-01", Quantity: 1}})
			if err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			} else if !errors.Is(err, domain.ErrInsufficientStock) {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if succeeded != available {
		t.Fatalf("%d reservations succeeded, want exactly %d", succeeded, available)
	}
	if got := stockOf("MON-01"); got != 0 {
		t.Fatalf("stock = %d, want 0", got)
	}
}
