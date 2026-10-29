package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

var fixedNow = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

func TestSagaPaths(t *testing.T) {
	tests := []struct {
		name             string
		steps            []domain.Trigger
		wantState        domain.State
		wantTerminal     bool
		wantCancelReason string
	}{
		{
			name:      "happy path ends completed",
			steps:     []domain.Trigger{domain.InventoryReservedTrigger, domain.InvoiceGenerated, domain.PaymentSucceeded, domain.OrderFulfilled},
			wantState: domain.Completed, wantTerminal: true,
		},
		{
			name:      "stock rejection cancels without compensation",
			steps:     []domain.Trigger{domain.InventoryRejected},
			wantState: domain.Cancelled, wantTerminal: true, wantCancelReason: "boom",
		},
		{
			name:      "payment failure enters compensation first",
			steps:     []domain.Trigger{domain.InventoryReservedTrigger, domain.InvoiceGenerated, domain.PaymentFailed},
			wantState: domain.Compensating, wantCancelReason: "boom",
		},
		{
			name:      "releasing stock completes the compensation",
			steps:     []domain.Trigger{domain.InventoryReservedTrigger, domain.InvoiceGenerated, domain.PaymentFailed, domain.InventoryReleased},
			wantState: domain.Cancelled, wantTerminal: true, wantCancelReason: "boom",
		},
		{
			name:      "payment failure before invoicing also compensates",
			steps:     []domain.Trigger{domain.InventoryReservedTrigger, domain.PaymentFailed, domain.InventoryReleased},
			wantState: domain.Cancelled, wantTerminal: true, wantCancelReason: "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := domain.NewOrder("o1", "u1", nil, fixedNow)
			for _, trigger := range tt.steps {
				if err := order.Apply(trigger, "boom", fixedNow); err != nil {
					t.Fatalf("apply %s: %v", trigger, err)
				}
			}
			if order.State != tt.wantState {
				t.Errorf("state = %s, want %s", order.State, tt.wantState)
			}
			if order.Terminal() != tt.wantTerminal {
				t.Errorf("terminal = %v, want %v", order.Terminal(), tt.wantTerminal)
			}
			if order.CancelReason != tt.wantCancelReason {
				t.Errorf("cancel reason = %q, want %q", order.CancelReason, tt.wantCancelReason)
			}
			if len(order.History) != len(tt.steps) {
				t.Errorf("history len = %d, want %d", len(order.History), len(tt.steps))
			}
		})
	}
}

func TestInvalidTransitionsAreRejectedAndLeaveStateUntouched(t *testing.T) {
	tests := []struct {
		name  string
		setup []domain.Trigger
		try   domain.Trigger
	}{
		{"cannot pay before inventory is reserved", nil, domain.PaymentSucceeded},
		{"cannot invoice a pending order", nil, domain.InvoiceGenerated},
		{"cannot complete before payment", []domain.Trigger{domain.InventoryReservedTrigger, domain.InvoiceGenerated}, domain.OrderFulfilled},
		{"completed orders ignore late payment failure", []domain.Trigger{domain.InventoryReservedTrigger, domain.InvoiceGenerated, domain.PaymentSucceeded, domain.OrderFulfilled}, domain.PaymentFailed},
		{"cancelled orders cannot be paid", []domain.Trigger{domain.InventoryRejected}, domain.PaymentSucceeded},
		{"cannot release stock that is not being compensated", []domain.Trigger{domain.InventoryReservedTrigger}, domain.InventoryReleased},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := domain.NewOrder("o1", "u1", nil, fixedNow)
			for _, trigger := range tt.setup {
				if err := order.Apply(trigger, "", fixedNow); err != nil {
					t.Fatal(err)
				}
			}
			stateBefore, historyLenBefore := order.State, len(order.History)
			err := order.Apply(tt.try, "", fixedNow)
			if !errors.Is(err, domain.ErrInvalidTransition) {
				t.Fatalf("err = %v, want ErrInvalidTransition", err)
			}
			if order.State != stateBefore || len(order.History) != historyLenBefore {
				t.Errorf("state/history changed on rejected transition")
			}
		})
	}
}

func TestPriceComputesTotal(t *testing.T) {
	order := domain.NewOrder("o1", "u1", nil, fixedNow)
	order.Price([]domain.Item{{SKU: "A", Quantity: 2, UnitPriceCents: 1000}, {SKU: "B", Quantity: 1, UnitPriceCents: 550}})
	if order.TotalCents != 2550 {
		t.Fatalf("total = %d, want 2550", order.TotalCents)
	}
}

func TestNewTransitionsTracksUnpersistedHistory(t *testing.T) {
	order := domain.NewOrder("o1", "u1", nil, fixedNow)
	_ = order.Apply(domain.InventoryReservedTrigger, "", fixedNow)
	if got := len(order.NewTransitions()); got != 1 {
		t.Fatalf("new transitions = %d, want 1", got)
	}
	order.MarkPersisted()
	_ = order.Apply(domain.InvoiceGenerated, "", fixedNow)
	if got := order.NewTransitions(); len(got) != 1 || got[0].To != domain.Invoiced {
		t.Fatalf("new transitions = %v, want only the INVOICED step", got)
	}
}
