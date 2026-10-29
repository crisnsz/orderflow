package domain

import "time"

type Card struct {
	UserID         string
	Brand          string
	Last4          string
	Token          string
	AutopayConsent bool
	ExpiryMonth    int
	ExpiryYear     int
}

func (c Card) Expired(now time.Time) bool {
	return now.Year() > c.ExpiryYear || (now.Year() == c.ExpiryYear && int(now.Month()) > c.ExpiryMonth)
}

type Action string

const (
	ChargeNow   Action = "CHARGE_NOW"
	AwaitManual Action = "AWAIT_MANUAL"
)

type Decision struct {
	Action Action
	Reason string
}

func Decide(card *Card, now time.Time) Decision {
	switch {
	case card == nil:
		return Decision{AwaitManual, "no card on file"}
	case !card.AutopayConsent:
		return Decision{AwaitManual, "customer has not enabled autopay"}
	case card.Expired(now):
		return Decision{AwaitManual, "card on file is expired"}
	default:
		return Decision{ChargeNow, "autopay enabled"}
	}
}

type Status string

const (
	Succeeded     Status = "SUCCEEDED"
	Failed        Status = "FAILED"
	PendingManual Status = "PENDING_MANUAL"
)

type Payment struct {
	OrderID          string
	UserID           string
	InvoiceID        string
	AmountCents      int64
	Status           Status
	AuthorizationRef string
	SaleRef          string
	Reason           string
}
