package invoicing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/billing/internal/domain"
	"github.com/crisnsz/orderflow/services/billing/internal/invoicing"
)

type inMemoryStore struct {
	invoicesByOrderID  map[string]domain.Invoice
	outbox             []events.Message
	processedMessageID map[string]bool
}
type inMemoryTx struct{ store *inMemoryStore }

func newStore() *inMemoryStore {
	return &inMemoryStore{invoicesByOrderID: map[string]domain.Invoice{}, processedMessageID: map[string]bool{}}
}

func (tx inMemoryTx) SaveInvoice(_ context.Context, invoice domain.Invoice) (string, bool, error) {
	if existing, ok := tx.store.invoicesByOrderID[invoice.OrderID]; ok {
		return existing.Number, false, nil
	}
	invoice.Number = "INV-1"
	tx.store.invoicesByOrderID[invoice.OrderID] = invoice
	return invoice.Number, true, nil
}
func (tx inMemoryTx) Outbox(_ context.Context, message events.Message) error {
	tx.store.outbox = append(tx.store.outbox, message)
	return nil
}
func (s *inMemoryStore) Once(_ context.Context, consumer, messageID string, work func(invoicing.Tx) error) (bool, error) {
	if s.processedMessageID[consumer+messageID] {
		return false, nil
	}
	if err := work(inMemoryTx{s}); err != nil {
		return false, err
	}
	s.processedMessageID[consumer+messageID] = true
	return true, nil
}

func orderConfirmed(t *testing.T, items []events.Item) events.Message {
	t.Helper()
	message, err := events.New(events.OrderConfirmed, events.Payload{OrderID: "o1", UserID: "u1", Items: items, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestConfirmedOrderProducesOneInvoiceEvent(t *testing.T) {
	store := newStore()
	service := invoicing.NewService(store)
	confirmedEvent := orderConfirmed(t, []events.Item{{SKU: "A", Quantity: 2, UnitPriceCents: 1000}})

	for i := 0; i < 2; i++ {
		if err := service.HandleEvent(context.Background(), confirmedEvent); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.outbox) != 1 || store.outbox[0].Type != events.InvoiceGenerated {
		t.Fatalf("outbox = %+v, want one invoice.generated", store.outbox)
	}
	payload, _ := store.outbox[0].Decode()
	if payload.AmountCents != 2000 || payload.InvoiceID != "INV-1" {
		t.Errorf("payload = %+v", payload)
	}
}

func TestSecondMessageForSameOrderDoesNotInvoiceTwice(t *testing.T) {
	store := newStore()
	service := invoicing.NewService(store)
	items := []events.Item{{SKU: "A", Quantity: 1, UnitPriceCents: 500}}
	_ = service.HandleEvent(context.Background(), orderConfirmed(t, items))
	_ = service.HandleEvent(context.Background(), orderConfirmed(t, items))
	if len(store.outbox) != 1 {
		t.Fatalf("invoice events = %d, want 1", len(store.outbox))
	}
}

func TestOrderWithoutItemsIsAPermanentFailure(t *testing.T) {
	service := invoicing.NewService(newStore())
	err := service.HandleEvent(context.Background(), orderConfirmed(t, nil))
	if !errors.Is(err, invoicing.ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
}
