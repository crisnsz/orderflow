package charging_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/payments/internal/charging"
	"github.com/crisnsz/orderflow/services/payments/internal/domain"
)

type fakeStore struct {
	card               *domain.Card
	payments           []domain.Payment
	outbox             []events.Message
	processedMessageID map[string]bool
}

type fakeTx struct{ store *fakeStore }

func (tx fakeTx) SavePayment(_ context.Context, payment domain.Payment) error {
	tx.store.payments = append(tx.store.payments, payment)
	return nil
}
func (tx fakeTx) Outbox(_ context.Context, message events.Message) error {
	tx.store.outbox = append(tx.store.outbox, message)
	return nil
}
func (s *fakeStore) CardFor(context.Context, string) (*domain.Card, error) { return s.card, nil }
func (s *fakeStore) Once(_ context.Context, consumer, messageID string, work func(charging.Tx) error) (bool, error) {
	if s.processedMessageID[consumer+messageID] {
		return false, nil
	}
	paymentsBefore, outboxBefore := len(s.payments), len(s.outbox)
	if err := work(fakeTx{s}); err != nil {
		s.payments, s.outbox = s.payments[:paymentsBefore], s.outbox[:outboxBefore]
		return false, err
	}
	s.processedMessageID[consumer+messageID] = true
	return true, nil
}

type fakeBank struct {
	authorizeErr, saleErr, voidErr error
	calls                          []string
}

func (b *fakeBank) Authorize(context.Context, string, string, int64) (string, error) {
	b.calls = append(b.calls, "auth")
	return "auth_1", b.authorizeErr
}
func (b *fakeBank) Sale(context.Context, string, string, int64) (string, error) {
	b.calls = append(b.calls, "sale")
	return "sale_1", b.saleErr
}
func (b *fakeBank) Void(context.Context, string, string) error {
	b.calls = append(b.calls, "void")
	return b.voidErr
}

func invoiceGenerated(t *testing.T) events.Message {
	t.Helper()
	message, err := events.New(events.InvoiceGenerated, events.Payload{OrderID: "o1", UserID: "u1", InvoiceID: "INV-1", AmountCents: 5000})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func savedCard(autopayConsent bool) *domain.Card {
	return &domain.Card{UserID: "u1", Token: "tok", AutopayConsent: autopayConsent, ExpiryMonth: 12, ExpiryYear: 2099}
}

func TestInvoiceHandling(t *testing.T) {
	decline := fmt.Errorf("%w: nope", charging.ErrDeclined)
	tests := []struct {
		name      string
		card      *domain.Card
		bank      *fakeBank
		wantEvent string
		wantCalls []string
		wantErr   bool
	}{
		{"autopay card is charged with auth then sale", savedCard(true), &fakeBank{}, events.PaymentSucceeded, []string{"auth", "sale"}, false},
		{"no consent asks for manual payment and never touches the bank", savedCard(false), &fakeBank{}, events.PaymentPendingManual, nil, false},
		{"no card asks for manual payment", nil, &fakeBank{}, events.PaymentPendingManual, nil, false},
		{"declined authorization fails the payment without a void", savedCard(true), &fakeBank{authorizeErr: decline}, events.PaymentFailed, []string{"auth"}, false},
		{"failed sale voids the authorization and fails the payment", savedCard(true), &fakeBank{saleErr: decline}, events.PaymentFailed, []string{"auth", "sale", "void"}, false},
		{"declined sale with failed void still fails the payment", savedCard(true), &fakeBank{saleErr: decline, voidErr: errors.New("bank unreachable")}, events.PaymentFailed, []string{"auth", "sale", "void"}, false},
		{"technical sale error with failed void is retried", savedCard(true), &fakeBank{saleErr: errors.New("timeout"), voidErr: errors.New("bank unreachable")}, "", []string{"auth", "sale", "void"}, true},
		{"technical bank error is retried, not published", savedCard(true), &fakeBank{authorizeErr: errors.New("timeout")}, "", []string{"auth"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{card: tt.card, processedMessageID: map[string]bool{}}
			err := charging.NewService(store, tt.bank).HandleEvent(context.Background(), invoiceGenerated(t))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if len(store.outbox) != 0 || len(store.payments) != 0 {
					t.Error("failed attempt must not persist anything")
				}
			} else if len(store.outbox) != 1 || store.outbox[0].Type != tt.wantEvent {
				t.Fatalf("outbox = %+v, want one %s", store.outbox, tt.wantEvent)
			}
			if fmt.Sprint(tt.bank.calls) != fmt.Sprint(tt.wantCalls) {
				t.Errorf("bank calls = %v, want %v", tt.bank.calls, tt.wantCalls)
			}
		})
	}
}

func TestDuplicateInvoiceMessageChargesOnce(t *testing.T) {
	store := &fakeStore{card: savedCard(true), processedMessageID: map[string]bool{}}
	bank := &fakeBank{}
	service := charging.NewService(store, bank)
	message := invoiceGenerated(t)
	for i := 0; i < 3; i++ {
		if err := service.HandleEvent(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.outbox) != 1 {
		t.Errorf("events published = %d, want 1", len(store.outbox))
	}
}
