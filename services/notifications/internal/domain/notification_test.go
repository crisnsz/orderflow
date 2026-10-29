package domain_test

import (
	"testing"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/notifications/internal/domain"
)

func TestPlan(t *testing.T) {
	allChannels := domain.Preferences{UserID: "u", Channels: map[domain.Channel]string{
		domain.SMS: "+34000", domain.Email: "a@example.test", domain.Push: "dev-1"}}
	noChannels := domain.Preferences{UserID: "u"}

	tests := []struct {
		name        string
		preferences domain.Preferences
		event       string
		want        []domain.Channel
	}{
		{"every enabled channel gets the message in stable order", allChannels, events.PaymentSucceeded, []domain.Channel{domain.Email, domain.Push, domain.SMS}},
		{"a customer with no channels gets nothing", noChannels, events.PaymentSucceeded, nil},
		{"failures notify too", allChannels, events.OrderCancelled, []domain.Channel{domain.Email, domain.Push, domain.SMS}},
		{"order.placed is not customer-facing", allChannels, events.OrderPlaced, nil},
		{"unknown events are ignored", allChannels, "something.else", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifications := domain.Plan(tt.preferences, domain.Event{Type: tt.event, OrderID: "12345678-aaaa", AmountCents: 1999, Reason: "r"})
			if len(notifications) != len(tt.want) {
				t.Fatalf("got %d notifications, want %d", len(notifications), len(tt.want))
			}
			for i, notification := range notifications {
				if notification.Channel != tt.want[i] {
					t.Errorf("notification %d channel = %s, want %s", i, notification.Channel, tt.want[i])
				}
				if notification.Subject == "" || notification.Body == "" || notification.Destination == "" {
					t.Errorf("notification %d is incomplete: %+v", i, notification)
				}
			}
		})
	}
}

func TestComposeFormatsAmount(t *testing.T) {
	_, body, ok := domain.Compose(domain.Event{Type: events.InvoiceGenerated, OrderID: "abc", InvoiceID: "INV-1", AmountCents: 1905})
	if !ok || body != "Invoice INV-1 for order abc: 19.05 EUR." {
		t.Fatalf("body = %q ok=%v", body, ok)
	}
}
