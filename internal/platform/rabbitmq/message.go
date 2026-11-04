package rabbitmq

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Azure/go-amqp"
	"github.com/crisnsz/orderflow/internal/platform/events"
)

const (
	propertyTraceparent = "traceparent"
	propertyType        = "type"
	annotationLastError = "x-last-error"
	contentTypeJSON     = "application/json"
)

var ErrPermanent = errors.New("permanent failure")

func encodeMessage(message events.Message, traceparent string) *amqp.Message {
	encoded := amqp.NewMessage(message.Body)
	messageID, eventType, orderID, contentType, occurredAt := message.ID, message.Type, message.OrderID, contentTypeJSON, message.OccurredAt
	encoded.Header = &amqp.MessageHeader{Durable: true}
	encoded.Properties = &amqp.MessageProperties{
		MessageID:     messageID,
		Subject:       &eventType,
		CorrelationID: orderID,
		ContentType:   &contentType,
		CreationTime:  &occurredAt,
	}
	encoded.ApplicationProperties = map[string]any{propertyTraceparent: traceparent, propertyType: eventType}
	return encoded
}

func decodeMessage(encoded *amqp.Message) (events.Message, error) {
	eventType := eventTypeOf(encoded)
	if eventType == "" {
		return events.Message{}, fmt.Errorf("%w: message has no type", ErrPermanent)
	}
	message := events.Message{Type: eventType, Body: bytes.Join(encoded.Data, nil)}
	if properties := encoded.Properties; properties != nil {
		message.ID, _ = properties.MessageID.(string)
		message.OrderID, _ = properties.CorrelationID.(string)
		if properties.CreationTime != nil {
			message.OccurredAt = *properties.CreationTime
		}
	}
	message.Traceparent, _ = encoded.ApplicationProperties[propertyTraceparent].(string)
	return message, nil
}

func eventTypeOf(encoded *amqp.Message) string {
	if encoded.Properties != nil && encoded.Properties.Subject != nil {
		return *encoded.Properties.Subject
	}
	eventType, _ := encoded.ApplicationProperties[propertyType].(string)
	return eventType
}
