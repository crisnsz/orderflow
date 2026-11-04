package placing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
	"github.com/crisnsz/orderflow/services/orders/internal/orderstest"
	"github.com/crisnsz/orderflow/services/orders/internal/placing"
)

func placeOrder(t *testing.T, service *placing.Service) *domain.Order {
	t.Helper()
	order, err := service.PlaceOrder(context.Background(), "u1", []domain.Item{{SKU: "KB-01", Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func TestPlaceOrderConfirmsAndPublishes(t *testing.T) {
	repository, inventory := orderstest.NewInMemoryRepository(), &orderstest.FakeInventory{}
	order := placeOrder(t, placing.NewService(repository, inventory))
	if order.State != domain.InventoryReserved || order.TotalCents != 2000 {
		t.Fatalf("state=%s total=%d", order.State, order.TotalCents)
	}
	publishedTypes := repository.PublishedEventTypes()
	if len(publishedTypes) != 2 || publishedTypes[0] != events.OrderPlaced || publishedTypes[1] != events.OrderConfirmed {
		t.Fatalf("outbox = %v", publishedTypes)
	}
}

func TestPlaceOrderStockRejectionCancelsWithoutCompensation(t *testing.T) {
	repository, inventory := orderstest.NewInMemoryRepository(), &orderstest.FakeInventory{ReserveErr: domain.ErrRejected}
	order := placeOrder(t, placing.NewService(repository, inventory))
	if order.State != domain.Cancelled || order.CancelReason == "" {
		t.Fatalf("state=%s reason=%q", order.State, order.CancelReason)
	}
	if inventory.ReleaseCalls != 0 {
		t.Errorf("release called %d times, want 0", inventory.ReleaseCalls)
	}
	if published := repository.PublishedEventTypes(); published[len(published)-1] != events.OrderCancelled {
		t.Errorf("last event = %v, want order.cancelled", published)
	}
}

func TestPlaceOrderValidation(t *testing.T) {
	service := placing.NewService(orderstest.NewInMemoryRepository(), &orderstest.FakeInventory{})
	for name, items := range map[string][]domain.Item{
		"no items":      nil,
		"zero quantity": {{SKU: "A", Quantity: 0}},
		"missing sku":   {{Quantity: 1}},
	} {
		if _, err := service.PlaceOrder(context.Background(), "u1", items); !errors.Is(err, placing.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestTechnicalReservationFailureReleasesStockAndCancels(t *testing.T) {
	tests := []struct {
		name         string
		releaseErr   error
		failures     int
		wantReleases int
	}{
		{"release succeeds first time", nil, 0, 1},
		{"release is retried after a transient failure", errors.New("inventory down"), 2, 3},
		{"release is attempted a bounded number of times", errors.New("inventory down"), 0, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := orderstest.NewInMemoryRepository()
			inventory := &orderstest.FakeInventory{
				ReserveErr: errors.New("connection reset"), ReleaseErr: tt.releaseErr, ReleaseFailures: tt.failures}
			_, err := placing.NewService(repository, inventory).
				PlaceOrder(context.Background(), "u1", []domain.Item{{SKU: "KB-01", Quantity: 1}})
			if err == nil {
				t.Fatal("want the reservation error returned to the caller")
			}
			if inventory.ReleaseCalls != tt.wantReleases {
				t.Errorf("release calls = %d, want %d", inventory.ReleaseCalls, tt.wantReleases)
			}
			if published := repository.PublishedEventTypes(); published[len(published)-1] != events.OrderCancelled {
				t.Errorf("last event = %v, want order.cancelled", published)
			}
		})
	}
}
