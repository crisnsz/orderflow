package domain

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrEmptyOrder        = errors.New("reservation has no lines")
	ErrInvalidQuantity   = errors.New("quantity must be positive")
	ErrUnknownProduct    = errors.New("unknown product")
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrAlreadyReleased   = errors.New("reservation already released")
)

type Product struct {
	SKU        string
	Name       string
	PriceCents int64
	Stock      int32
}

type Line struct {
	SKU      string
	Quantity int32
}

type PricedLine struct {
	SKU            string
	Quantity       int32
	UnitPriceCents int64
}

type Status string

const (
	Reserved Status = "RESERVED"
	Released Status = "RELEASED"
)

type Reservation struct {
	OrderID string
	Status  Status
	Lines   []PricedLine
}

func (r Reservation) Total() int64 {
	var total int64
	for _, line := range r.Lines {
		total += int64(line.Quantity) * line.UnitPriceCents
	}
	return total
}

func Plan(products map[string]Product, lines []Line) ([]PricedLine, error) {
	if len(lines) == 0 {
		return nil, ErrEmptyOrder
	}
	quantityBySKU := map[string]int32{}
	for _, line := range lines {
		if line.Quantity <= 0 {
			return nil, fmt.Errorf("%w: %s", ErrInvalidQuantity, line.SKU)
		}
		quantityBySKU[line.SKU] += line.Quantity
	}
	skus := make([]string, 0, len(quantityBySKU))
	for sku := range quantityBySKU {
		skus = append(skus, sku)
	}
	sort.Strings(skus)

	pricedLines := make([]PricedLine, 0, len(skus))
	for _, sku := range skus {
		product, ok := products[sku]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownProduct, sku)
		}
		requested := quantityBySKU[sku]
		if requested > product.Stock {
			return nil, fmt.Errorf("%w: %s requested %d, available %d", ErrInsufficientStock, sku, requested, product.Stock)
		}
		pricedLines = append(pricedLines, PricedLine{SKU: sku, Quantity: requested, UnitPriceCents: product.PriceCents})
	}
	return pricedLines, nil
}
