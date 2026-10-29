package domain

import (
	"errors"
	"fmt"
	"time"
)

const VATPercent = 21

const (
	percentBase     = 100
	defaultCurrency = "EUR"
)

var ErrNoLines = errors.New("invoice needs at least one line")

type Line struct {
	SKU            string
	Quantity       int32
	UnitPriceCents int64
}

type Invoice struct {
	Number     string
	OrderID    string
	UserID     string
	Currency   string
	Lines      []Line
	NetCents   int64
	TaxCents   int64
	TotalCents int64
	IssuedAt   time.Time
}

func NewInvoice(orderID, userID, currency string, lines []Line, issuedAt time.Time) (Invoice, error) {
	if len(lines) == 0 {
		return Invoice{}, ErrNoLines
	}
	var total int64
	for _, line := range lines {
		if line.Quantity <= 0 || line.UnitPriceCents < 0 {
			return Invoice{}, fmt.Errorf("invalid line %s: quantity %d, price %d", line.SKU, line.Quantity, line.UnitPriceCents)
		}
		total += int64(line.Quantity) * line.UnitPriceCents
	}
	taxInclusiveFactor := int64(percentBase + VATPercent)
	net := (total*percentBase + taxInclusiveFactor/2) / taxInclusiveFactor
	if currency == "" {
		currency = defaultCurrency
	}
	return Invoice{
		OrderID: orderID, UserID: userID, Currency: currency, Lines: lines,
		NetCents: net, TaxCents: total - net, TotalCents: total, IssuedAt: issuedAt,
	}, nil
}
