package domain_test

import (
	"errors"
	"testing"

	"github.com/crisnsz/orderflow/services/inventory/internal/domain"
)

func catalog() map[string]domain.Product {
	return map[string]domain.Product{
		"KB-01": {SKU: "KB-01", Name: "Keyboard", PriceCents: 8000, Stock: 5},
		"MS-01": {SKU: "MS-01", Name: "Mouse", PriceCents: 2500, Stock: 1},
	}
}

func TestPlan(t *testing.T) {
	tests := []struct {
		name      string
		lines     []domain.Line
		wantErr   error
		wantLines []domain.PricedLine
	}{
		{
			name:      "reserves available stock and prices the lines",
			lines:     []domain.Line{{SKU: "MS-01", Quantity: 1}, {SKU: "KB-01", Quantity: 2}},
			wantLines: []domain.PricedLine{{SKU: "KB-01", Quantity: 2, UnitPriceCents: 8000}, {SKU: "MS-01", Quantity: 1, UnitPriceCents: 2500}},
		},
		{
			name:      "merges duplicate SKUs before checking stock",
			lines:     []domain.Line{{SKU: "KB-01", Quantity: 2}, {SKU: "KB-01", Quantity: 3}},
			wantLines: []domain.PricedLine{{SKU: "KB-01", Quantity: 5, UnitPriceCents: 8000}},
		},
		{name: "rejects when merged quantity exceeds stock", lines: []domain.Line{{SKU: "KB-01", Quantity: 3}, {SKU: "KB-01", Quantity: 3}}, wantErr: domain.ErrInsufficientStock},
		{name: "is all-or-nothing when one line is short", lines: []domain.Line{{SKU: "KB-01", Quantity: 1}, {SKU: "MS-01", Quantity: 2}}, wantErr: domain.ErrInsufficientStock},
		{name: "rejects unknown SKU", lines: []domain.Line{{SKU: "NOPE", Quantity: 1}}, wantErr: domain.ErrUnknownProduct},
		{name: "rejects zero quantity", lines: []domain.Line{{SKU: "KB-01", Quantity: 0}}, wantErr: domain.ErrInvalidQuantity},
		{name: "rejects negative quantity", lines: []domain.Line{{SKU: "KB-01", Quantity: -1}}, wantErr: domain.ErrInvalidQuantity},
		{name: "rejects empty reservation", lines: nil, wantErr: domain.ErrEmptyOrder},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricedLines, err := domain.Plan(catalog(), tt.lines)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if len(pricedLines) != len(tt.wantLines) {
				t.Fatalf("lines = %v, want %v", pricedLines, tt.wantLines)
			}
			for i := range pricedLines {
				if pricedLines[i] != tt.wantLines[i] {
					t.Errorf("line %d = %v, want %v", i, pricedLines[i], tt.wantLines[i])
				}
			}
		})
	}
}

func TestReservationTotal(t *testing.T) {
	reservation := domain.Reservation{Lines: []domain.PricedLine{{Quantity: 2, UnitPriceCents: 8000}, {Quantity: 1, UnitPriceCents: 2500}}}
	if got := reservation.Total(); got != 18500 {
		t.Fatalf("total = %d, want 18500", got)
	}
}
