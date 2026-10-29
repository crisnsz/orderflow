package domain

import (
	"fmt"
	"sort"

	"github.com/crisnsz/orderflow/internal/platform/events"
)

type Channel string

const (
	Email Channel = "email"
	SMS   Channel = "sms"
	Push  Channel = "push"
)

type Preferences struct {
	UserID   string
	Channels map[Channel]string
}

type Event struct {
	Type        string
	OrderID     string
	AmountCents int64
	InvoiceID   string
	Reason      string
}

type Notification struct {
	Channel     Channel
	Destination string
	OrderID     string
	Subject     string
	Body        string
}

const (
	centsPerEuro       = 100
	shortOrderIDLength = 8
)

func formatEuros(cents int64) string {
	return fmt.Sprintf("%d.%02d EUR", cents/centsPerEuro, cents%centsPerEuro)
}

func Compose(event Event) (subject, body string, ok bool) {
	shortOrderID := event.OrderID
	if len(shortOrderID) > shortOrderIDLength {
		shortOrderID = shortOrderID[:shortOrderIDLength]
	}
	amount := formatEuros(event.AmountCents)
	switch event.Type {
	case events.OrderConfirmed:
		return "Order confirmed", fmt.Sprintf("Your order %s is confirmed (%s).", shortOrderID, amount), true
	case events.InvoiceGenerated:
		return "Invoice ready", fmt.Sprintf("Invoice %s for order %s: %s.", event.InvoiceID, shortOrderID, amount), true
	case events.PaymentSucceeded:
		return "Payment received", fmt.Sprintf("We charged your saved card %s for order %s.", amount, shortOrderID), true
	case events.PaymentPendingManual:
		return "Payment needed", fmt.Sprintf("Order %s needs a manual payment of %s (%s).", shortOrderID, amount, event.Reason), true
	case events.PaymentFailed:
		return "Payment failed", fmt.Sprintf("We could not charge your card for order %s: %s.", shortOrderID, event.Reason), true
	case events.OrderCancelled:
		return "Order cancelled", fmt.Sprintf("Your order %s was cancelled: %s.", shortOrderID, event.Reason), true
	case events.OrderCompleted:
		return "Order completed", fmt.Sprintf("Your order %s is complete. Thank you!", shortOrderID), true
	}
	return "", "", false
}

func Plan(preferences Preferences, event Event) []Notification {
	subject, body, ok := Compose(event)
	if !ok {
		return nil
	}
	channelNames := make([]string, 0, len(preferences.Channels))
	for channel := range preferences.Channels {
		channelNames = append(channelNames, string(channel))
	}
	sort.Strings(channelNames)
	notifications := make([]Notification, 0, len(channelNames))
	for _, channelName := range channelNames {
		channel := Channel(channelName)
		notifications = append(notifications, Notification{
			Channel: channel, Destination: preferences.Channels[channel], OrderID: event.OrderID, Subject: subject, Body: body})
	}
	return notifications
}
