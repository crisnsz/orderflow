//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/orders/internal/adapters/postgres"
	"github.com/crisnsz/orderflow/services/orders/internal/advancing"
	"github.com/crisnsz/orderflow/services/orders/internal/compensating"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
	"github.com/crisnsz/orderflow/services/orders/internal/fetching"
	"github.com/crisnsz/orderflow/services/orders/internal/placing"
	"github.com/crisnsz/orderflow/test/containers"
)

type fakeInventory struct{ released int }

func (f *fakeInventory) Reserve(_ context.Context, _ string, items []domain.Item) ([]domain.Item, error) {
	pricedItems := make([]domain.Item, len(items))
	for index, item := range items {
		item.UnitPriceCents = 1000
		pricedItems[index] = item
	}
	return pricedItems, nil
}
func (f *fakeInventory) Release(context.Context, string) error { f.released++; return nil }

func TestOrderStateAndOutboxCommitOrRollbackTogether(t *testing.T) {
	ctx := context.Background()
	pool := containers.Postgres(t)
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(pool)

	order := domain.NewOrder("11111111-1111-1111-1111-111111111111", "u1", []domain.Item{{SKU: "A", Quantity: 1}}, time.Now().UTC())
	placedEvent, _ := events.New(events.OrderPlaced, events.Payload{OrderID: order.ID, UserID: "u1"})

	boom := errors.New("boom")
	err := store.InTx(ctx, func(tx domain.Tx) error {
		if err := tx.Insert(ctx, order); err != nil {
			return err
		}
		if err := tx.Outbox(ctx, placedEvent); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var orderCount, outboxCount int
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders), (SELECT count(*) FROM outbox)`).Scan(&orderCount, &outboxCount)
	if orderCount != 0 || outboxCount != 0 {
		t.Fatalf("rolled-back tx left orders=%d outbox=%d", orderCount, outboxCount)
	}

	if err := store.InTx(ctx, func(tx domain.Tx) error {
		if err := tx.Insert(ctx, order); err != nil {
			return err
		}
		return tx.Outbox(ctx, placedEvent)
	}); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders), (SELECT count(*) FROM outbox)`).Scan(&orderCount, &outboxCount)
	if orderCount != 1 || outboxCount != 1 {
		t.Fatalf("committed tx: orders=%d outbox=%d, want 1/1", orderCount, outboxCount)
	}
}

func TestSagaPersistenceWithInboxAndOutbox(t *testing.T) {
	ctx := context.Background()
	pool := containers.Postgres(t)
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	inventory := &fakeInventory{}
	store := postgres.NewStore(pool)
	placingOrders := placing.NewService(store, inventory)
	saga := advancing.NewService(store, compensating.NewService(inventory))
	fetchingOrders := fetching.NewService(store)

	placed, err := placingOrders.PlaceOrder(ctx, "u1", []domain.Item{{SKU: "A", Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if placed.State != domain.InventoryReserved || placed.TotalCents != 2000 {
		t.Fatalf("state=%s total=%d", placed.State, placed.TotalCents)
	}

	invoice, _ := events.New(events.InvoiceGenerated, events.Payload{OrderID: placed.ID, UserID: "u1", InvoiceID: "INV-1"})
	failed, _ := events.New(events.PaymentFailed, events.Payload{OrderID: placed.ID, UserID: "u1", Reason: "card declined"})
	for _, message := range []events.Message{invoice, failed, failed, failed} {
		if err := saga.HandleEvent(ctx, message); err != nil {
			t.Fatal(err)
		}
	}

	got, err := fetchingOrders.GetOrder(ctx, placed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.Cancelled || got.CancelReason != "card declined" {
		t.Fatalf("state=%s reason=%q", got.State, got.CancelReason)
	}
	var states []domain.State
	for _, transition := range got.History {
		states = append(states, transition.To)
	}
	wantStates := []domain.State{domain.InventoryReserved, domain.Invoiced, domain.Compensating, domain.Cancelled}
	if len(states) != len(wantStates) {
		t.Fatalf("history = %v, want %v", states, wantStates)
	}
	for i := range wantStates {
		if states[i] != wantStates[i] {
			t.Fatalf("history = %v, want %v", states, wantStates)
		}
	}
	if inventory.released != 1 {
		t.Errorf("stock released %d times, want exactly 1 despite 3 deliveries", inventory.released)
	}
	var cancelled int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE routing_key='order.cancelled'`).Scan(&cancelled)
	if cancelled != 1 {
		t.Errorf("order.cancelled events = %d, want 1", cancelled)
	}
}
