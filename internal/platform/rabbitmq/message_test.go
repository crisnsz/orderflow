package rabbitmq

import (
	"errors"
	"testing"

	"github.com/Azure/go-amqp"
	"github.com/crisnsz/orderflow/internal/platform/events"
)

func TestEncodedMessageDecodesToTheSameEnvelope(t *testing.T) {
	original, _ := events.New(events.OrderConfirmed, events.Payload{OrderID: "order-1", UserID: "u"})
	decoded, err := decodeMessage(encodeMessage(original, "00-trace-span-01"))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != original.ID || decoded.Type != original.Type || decoded.OrderID != "order-1" ||
		!decoded.OccurredAt.Equal(original.OccurredAt) || string(decoded.Body) != string(original.Body) || decoded.Traceparent != "00-trace-span-01" {
		t.Errorf("decoded = %+v, original = %+v", decoded, original)
	}
}

func TestMessageWithoutTypeIsPermanentlyUndecodable(t *testing.T) {
	_, err := decodeMessage(amqp.NewMessage([]byte("{}")))
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("err = %v, want ErrPermanent", err)
	}
}

func TestTypeSurvivesAnAMQP091RoundTripThroughApplicationProperties(t *testing.T) {
	encoded := amqp.NewMessage([]byte("{}"))
	encoded.ApplicationProperties = map[string]any{propertyType: events.PaymentFailed}
	decoded, err := decodeMessage(encoded)
	if err != nil || decoded.Type != events.PaymentFailed {
		t.Errorf("decoded = %+v, err = %v", decoded, err)
	}
}
