package domain

import (
	"context"
	"errors"

	"github.com/crisnsz/orderflow/internal/platform/events"
)

var ErrNotFound = errors.New("order not found")

var ErrRejected = errors.New("inventory rejected the reservation")

type Inventory interface {
	Reserve(ctx context.Context, orderID string, items []Item) ([]Item, error)
	Release(ctx context.Context, orderID string) error
}

type Tx interface {
	Insert(ctx context.Context, order *Order) error
	Load(ctx context.Context, orderID string) (*Order, error)
	Save(ctx context.Context, order *Order) error
	Outbox(ctx context.Context, message events.Message) error
}

type Repository interface {
	InTx(ctx context.Context, work func(Tx) error) error
	Once(ctx context.Context, consumer, messageID string, work func(Tx) error) (bool, error)
	Get(ctx context.Context, orderID string) (*Order, error)
	ListByUser(ctx context.Context, userID string, limit int) ([]*Order, error)
}

func QueueEvent(ctx context.Context, tx Tx, eventType string, payload events.Payload) error {
	message, err := events.New(eventType, payload)
	if err != nil {
		return err
	}
	return tx.Outbox(ctx, message)
}
