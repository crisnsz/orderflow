package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/crisnsz/orderflow/services/billing/internal/domain"
)

func TestNewInvoice(t *testing.T) {
	issuedAt := time.Now()
	tests := []struct {
		name      string
		lines     []domain.Line
		wantErr   bool
		wantTotal int64
		wantNet   int64
		wantTax   int64
	}{
		{name: "extracts VAT from tax-inclusive total", lines: []domain.Line{{SKU: "A", Quantity: 2, UnitPriceCents: 6050}}, wantTotal: 12100, wantNet: 10000, wantTax: 2100},
		{name: "rounds net half up and keeps net+tax equal to total", lines: []domain.Line{{SKU: "A", Quantity: 1, UnitPriceCents: 8900}}, wantTotal: 8900, wantNet: 7355, wantTax: 1545},
		{name: "sums several lines", lines: []domain.Line{{SKU: "A", Quantity: 1, UnitPriceCents: 1000}, {SKU: "B", Quantity: 3, UnitPriceCents: 500}}, wantTotal: 2500, wantNet: 2066, wantTax: 434},
		{name: "rejects empty invoice", wantErr: true},
		{name: "rejects non-positive quantity", lines: []domain.Line{{SKU: "A", Quantity: 0, UnitPriceCents: 100}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invoice, err := domain.NewInvoice("o1", "u1", "", tt.lines, issuedAt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if invoice.TotalCents != tt.wantTotal || invoice.NetCents != tt.wantNet || invoice.TaxCents != tt.wantTax {
				t.Errorf("total/net/tax = %d/%d/%d, want %d/%d/%d", invoice.TotalCents, invoice.NetCents, invoice.TaxCents, tt.wantTotal, tt.wantNet, tt.wantTax)
			}
			if invoice.NetCents+invoice.TaxCents != invoice.TotalCents {
				t.Errorf("net+tax != total")
			}
			if invoice.Currency != "EUR" {
				t.Errorf("currency = %q, want default EUR", invoice.Currency)
			}
		})
	}
}

func TestNoLinesErrorIsExported(t *testing.T) {
	if _, err := domain.NewInvoice("o", "u", "EUR", nil, time.Now()); !errors.Is(err, domain.ErrNoLines) {
		t.Fatalf("err = %v", err)
	}
}
