package storefront

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("not found")

var ErrInvalid = errors.New("invalid request")

type Item struct {
	SKU            string `json:"sku"`
	Quantity       int32  `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents,omitempty"`
}

type Transition struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Trigger string `json:"trigger"`
	At      string `json:"at"`
}

type Order struct {
	ID           string       `json:"id"`
	UserID       string       `json:"user_id"`
	State        string       `json:"state"`
	TotalCents   int64        `json:"total_cents"`
	CancelReason string       `json:"cancel_reason,omitempty"`
	Items        []Item       `json:"items"`
	History      []Transition `json:"history"`
	CreatedAt    string       `json:"created_at"`
}

type Product struct {
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	Stock      int32  `json:"stock"`
}

type Card struct {
	Brand          string `json:"brand"`
	Last4          string `json:"last4"`
	ExpiryMonth    int    `json:"exp_month"`
	ExpiryYear     int    `json:"exp_year"`
	AutopayConsent bool   `json:"autopay_consent"`
}

type Orders interface {
	PlaceOrder(ctx context.Context, userID string, items []Item) (Order, error)
	GetOrder(ctx context.Context, orderID string) (Order, error)
	ListOrders(ctx context.Context, userID string) ([]Order, error)
}

type Catalog interface {
	ListProducts(ctx context.Context) ([]Product, error)
}

type Payments interface {
	Cards(ctx context.Context, userID string) ([]Card, error)
	FailureStage(ctx context.Context) (string, error)
	SetFailureStage(ctx context.Context, stage string) error
}
