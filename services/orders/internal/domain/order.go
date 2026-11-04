package domain

import (
	"errors"
	"fmt"
	"time"
)

type State string

const (
	Pending           State = "PENDING"
	InventoryReserved State = "INVENTORY_RESERVED"
	Invoiced          State = "INVOICED"
	Paid              State = "PAID"
	Completed         State = "COMPLETED"
	Compensating      State = "COMPENSATING"
	Cancelled         State = "CANCELLED"
)

type Trigger string

const (
	InventoryReservedTrigger Trigger = "inventory_reserved"
	InventoryRejected        Trigger = "inventory_rejected"
	InvoiceGenerated         Trigger = "invoice_generated"
	PaymentSucceeded         Trigger = "payment_succeeded"
	PaymentFailed            Trigger = "payment_failed"
	OrderFulfilled           Trigger = "order_fulfilled"
	InventoryReleased        Trigger = "inventory_released"
)

var transitions = map[State]map[Trigger]State{
	Pending: {
		InventoryReservedTrigger: InventoryReserved,
		InventoryRejected:        Cancelled,
	},
	InventoryReserved: {
		InvoiceGenerated: Invoiced,
		PaymentSucceeded: Paid,
		PaymentFailed:    Compensating,
	},
	Invoiced: {
		PaymentSucceeded: Paid,
		PaymentFailed:    Compensating,
	},
	Paid: {
		OrderFulfilled: Completed,
	},
	Compensating: {
		InventoryReleased: Cancelled,
	},
}

var ErrInvalidTransition = errors.New("invalid saga transition")

type Item struct {
	SKU            string
	Quantity       int32
	UnitPriceCents int64
}

type Transition struct {
	From    State
	To      State
	Trigger Trigger
	At      time.Time
}

type Order struct {
	ID           string
	UserID       string
	State        State
	TotalCents   int64
	CancelReason string
	Items        []Item
	History      []Transition
	CreatedAt    time.Time

	persistedTransitions int
}

func NewOrder(id, userID string, items []Item, createdAt time.Time) *Order {
	return &Order{ID: id, UserID: userID, State: Pending, Items: items, CreatedAt: createdAt}
}

func (o *Order) Apply(trigger Trigger, reason string, at time.Time) error {
	nextState, ok := transitions[o.State][trigger]
	if !ok {
		return fmt.Errorf("%w: %s in state %s", ErrInvalidTransition, trigger, o.State)
	}
	o.History = append(o.History, Transition{From: o.State, To: nextState, Trigger: trigger, At: at})
	o.State = nextState
	if reason != "" && o.CancelReason == "" && (nextState == Compensating || nextState == Cancelled) {
		o.CancelReason = reason
	}
	return nil
}

func (o *Order) Terminal() bool { return o.State == Completed || o.State == Cancelled }

func (o *Order) Price(items []Item) {
	o.Items = items
	o.TotalCents = 0
	for _, item := range items {
		o.TotalCents += int64(item.Quantity) * item.UnitPriceCents
	}
}

func (o *Order) NewTransitions() []Transition { return o.History[o.persistedTransitions:] }

func (o *Order) MarkPersisted() { o.persistedTransitions = len(o.History) }
