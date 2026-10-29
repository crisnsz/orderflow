package orderstest

import (
	"context"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
)

type InMemoryRepository struct {
	orders             map[string]domain.Order
	Outbox             []events.Message
	processedMessageID map[string]bool
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{orders: map[string]domain.Order{}, processedMessageID: map[string]bool{}}
}

var _ domain.Repository = (*InMemoryRepository)(nil)

type inMemoryTx struct{ repository *InMemoryRepository }

func (tx inMemoryTx) Insert(_ context.Context, order *domain.Order) error {
	tx.repository.orders[order.ID] = copyOrder(order)
	return nil
}

func (tx inMemoryTx) Load(_ context.Context, orderID string) (*domain.Order, error) {
	stored, ok := tx.repository.orders[orderID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	loaded := copyOrder(&stored)
	return &loaded, nil
}

func (tx inMemoryTx) Save(_ context.Context, order *domain.Order) error {
	tx.repository.orders[order.ID] = copyOrder(order)
	return nil
}

func (tx inMemoryTx) Outbox(_ context.Context, message events.Message) error {
	tx.repository.Outbox = append(tx.repository.Outbox, message)
	return nil
}

func copyOrder(order *domain.Order) domain.Order {
	copied := *order
	copied.History = append([]domain.Transition(nil), order.History...)
	copied.Items = append([]domain.Item(nil), order.Items...)
	return copied
}

func (r *InMemoryRepository) InTx(_ context.Context, work func(domain.Tx) error) error {
	ordersBefore := map[string]domain.Order{}
	for orderID, order := range r.orders {
		ordersBefore[orderID] = copyOrder(&order)
	}
	outboxBefore := append([]events.Message(nil), r.Outbox...)
	if err := work(inMemoryTx{r}); err != nil {
		r.orders, r.Outbox = ordersBefore, outboxBefore
		return err
	}
	return nil
}

func (r *InMemoryRepository) Once(ctx context.Context, consumer, messageID string, work func(domain.Tx) error) (bool, error) {
	if r.processedMessageID[consumer+messageID] {
		return false, nil
	}
	if err := r.InTx(ctx, work); err != nil {
		return false, err
	}
	r.processedMessageID[consumer+messageID] = true
	return true, nil
}

func (r *InMemoryRepository) Get(_ context.Context, orderID string) (*domain.Order, error) {
	stored, ok := r.orders[orderID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	loaded := copyOrder(&stored)
	return &loaded, nil
}

func (r *InMemoryRepository) ListByUser(context.Context, string, int) ([]*domain.Order, error) {
	return nil, nil
}

func (r *InMemoryRepository) PublishedEventTypes() []string {
	var eventTypes []string
	for _, message := range r.Outbox {
		eventTypes = append(eventTypes, message.Type)
	}
	return eventTypes
}
