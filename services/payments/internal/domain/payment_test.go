package domain_test

import (
	"testing"
	"time"

	"github.com/crisnsz/orderflow/services/payments/internal/domain"
)

func TestDecide(t *testing.T) {
	today := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	valid := domain.Card{UserID: "u", AutopayConsent: true, ExpiryMonth: 12, ExpiryYear: 2030}
	noConsent := valid
	noConsent.AutopayConsent = false
	expired := valid
	expired.ExpiryMonth, expired.ExpiryYear = 5, 2026
	lastMonth := valid
	lastMonth.ExpiryMonth, lastMonth.ExpiryYear = 6, 2026

	tests := []struct {
		name string
		card *domain.Card
		want domain.Action
	}{
		{"charges when consent is given and card is valid", &valid, domain.ChargeNow},
		{"card expiring this month is still valid", &lastMonth, domain.ChargeNow},
		{"waits for manual payment without consent", &noConsent, domain.AwaitManual},
		{"waits for manual payment when the card expired", &expired, domain.AwaitManual},
		{"waits for manual payment with no card", nil, domain.AwaitManual},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := domain.Decide(tt.card, today)
			if decision.Action != tt.want {
				t.Errorf("action = %s (%s), want %s", decision.Action, decision.Reason, tt.want)
			}
			if decision.Reason == "" {
				t.Error("decision must explain itself")
			}
		})
	}
}
