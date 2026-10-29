package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const Exchange = "orderflow.events"

const (
	OrderPlaced          = "order.placed"
	OrderConfirmed       = "order.confirmed"
	InvoiceGenerated     = "invoice.generated"
	PaymentSucceeded     = "payment.succeeded"
	PaymentFailed        = "payment.failed"
	PaymentPendingManual = "payment.pending_manual"
	OrderCancelled       = "order.cancelled"
	OrderCompleted       = "order.completed"
)

type Item struct {
	SKU            string `json:"sku"`
	Quantity       int32  `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents,omitempty"`
}

type Payload struct {
	OrderID     string `json:"order_id"`
	UserID      string `json:"user_id"`
	AmountCents int64  `json:"amount_cents,omitempty"`
	Currency    string `json:"currency,omitempty"`
	InvoiceID   string `json:"invoice_id,omitempty"`
	Reference   string `json:"reference,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Items       []Item `json:"items,omitempty"`
}

type Message struct {
	ID          string
	Type        string
	OrderID     string
	OccurredAt  time.Time
	Body        []byte
	Traceparent string
}

func New(eventType string, payload Payload) (Message, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}
	return Message{
		ID:         uuid.NewString(),
		Type:       eventType,
		OrderID:    payload.OrderID,
		OccurredAt: time.Now().UTC(),
		Body:       body,
	}, nil
}

func (m Message) Decode() (Payload, error) {
	var payload Payload
	err := json.Unmarshal(m.Body, &payload)
	return payload, err
}
