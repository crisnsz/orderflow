package advancing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/orders/internal/advancing"
	"github.com/crisnsz/orderflow/services/orders/internal/compensating"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
	"github.com/crisnsz/orderflow/services/orders/internal/orderstest"
	"github.com/crisnsz/orderflow/services/orders/internal/placing"
)

type sagaFixture struct {
	repository *orderstest.InMemoryRepository
	inventory  *orderstest.FakeInventory
	saga       *advancing.Service
	order      *domain.Order
}

func newSagaFixture(t *testing.T) sagaFixture {
	t.Helper()
	repository, inventory := orderstest.NewInMemoryRepository(), &orderstest.FakeInventory{}
	order, err := placing.NewService(repository, inventory).
		PlaceOrder(context.Background(), "u1", []domain.Item{{SKU: "KB-01", Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	saga := advancing.NewService(repository, compensating.NewService(inventory))
	return sagaFixture{repository: repository, inventory: inventory, saga: saga, order: order}
}

func (f sagaFixture) event(t *testing.T, eventType, reason string) events.Message {
	t.Helper()
	message, err := events.New(eventType, events.Payload{OrderID: f.order.ID, UserID: "u1", Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func (f sagaFixture) storedOrder(t *testing.T) *domain.Order {
	t.Helper()
	stored, err := f.repository.Get(context.Background(), f.order.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestSagaHappyPath(t *testing.T) {
	fixture := newSagaFixture(t)
	ctx := context.Background()
	for _, eventType := range []string{events.InvoiceGenerated, events.PaymentSucceeded} {
		if err := fixture.saga.HandleEvent(ctx, fixture.event(t, eventType, "")); err != nil {
			t.Fatal(err)
		}
	}
	if stored := fixture.storedOrder(t); stored.State != domain.Completed {
		t.Fatalf("state = %s, want COMPLETED", stored.State)
	}
	if published := fixture.repository.PublishedEventTypes(); published[len(published)-1] != events.OrderCompleted {
		t.Errorf("events = %v", published)
	}
}

func TestPaymentFailureCompensatesAndCancels(t *testing.T) {
	fixture := newSagaFixture(t)
	ctx := context.Background()
	_ = fixture.saga.HandleEvent(ctx, fixture.event(t, events.InvoiceGenerated, ""))
	if err := fixture.saga.HandleEvent(ctx, fixture.event(t, events.PaymentFailed, "card declined")); err != nil {
		t.Fatal(err)
	}
	stored := fixture.storedOrder(t)
	if stored.State != domain.Cancelled || stored.CancelReason != "card declined" {
		t.Fatalf("state=%s reason=%q", stored.State, stored.CancelReason)
	}
	if fixture.inventory.Released != 1 {
		t.Errorf("stock released %d times, want 1", fixture.inventory.Released)
	}
}

func TestCompensationFailureRollsBackSoTheMessageCanBeRetried(t *testing.T) {
	fixture := newSagaFixture(t)
	ctx := context.Background()
	_ = fixture.saga.HandleEvent(ctx, fixture.event(t, events.InvoiceGenerated, ""))

	paymentFailed := fixture.event(t, events.PaymentFailed, "card declined")
	fixture.inventory.ReleaseErr = errors.New("inventory down")
	if err := fixture.saga.HandleEvent(ctx, paymentFailed); err == nil {
		t.Fatal("want error so the message is retried")
	}
	if stored := fixture.storedOrder(t); stored.State != domain.Invoiced {
		t.Fatalf("state = %s, want INVOICED (rolled back)", stored.State)
	}

	fixture.inventory.ReleaseErr = nil
	if err := fixture.saga.HandleEvent(ctx, paymentFailed); err != nil {
		t.Fatal(err)
	}
	if stored := fixture.storedOrder(t); stored.State != domain.Cancelled {
		t.Fatalf("state = %s, want CANCELLED", stored.State)
	}
}

func TestDuplicateMessageIsIgnored(t *testing.T) {
	fixture := newSagaFixture(t)
	ctx := context.Background()
	_ = fixture.saga.HandleEvent(ctx, fixture.event(t, events.InvoiceGenerated, ""))
	paymentSucceeded := fixture.event(t, events.PaymentSucceeded, "")
	_ = fixture.saga.HandleEvent(ctx, paymentSucceeded)
	outboxSizeBefore := len(fixture.repository.Outbox)
	if err := fixture.saga.HandleEvent(ctx, paymentSucceeded); err != nil {
		t.Fatal(err)
	}
	if len(fixture.repository.Outbox) != outboxSizeBefore {
		t.Errorf("duplicate produced new events")
	}
}

func TestLateEventsOnTerminalOrdersAreSwallowed(t *testing.T) {
	fixture := newSagaFixture(t)
	ctx := context.Background()
	_ = fixture.saga.HandleEvent(ctx, fixture.event(t, events.InvoiceGenerated, ""))
	_ = fixture.saga.HandleEvent(ctx, fixture.event(t, events.PaymentSucceeded, ""))
	if err := fixture.saga.HandleEvent(ctx, fixture.event(t, events.PaymentFailed, "late")); err != nil {
		t.Fatalf("late event should be ignored, got %v", err)
	}
	if fixture.inventory.ReleaseCalls != 0 {
		t.Errorf("stock of a completed order was released")
	}
}

func TestMalformedEventIsPermanentFailure(t *testing.T) {
	fixture := newSagaFixture(t)
	malformed := events.Message{ID: "x", Type: events.InvoiceGenerated, Body: []byte("{nope")}
	if err := fixture.saga.HandleEvent(context.Background(), malformed); !errors.Is(err, advancing.ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
}
