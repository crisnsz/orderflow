package rabbitmq

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	brokerConnectTimeout   = 5 * time.Second
	reconnectInitialDelay  = 500 * time.Millisecond
	reconnectMaxDelay      = 10 * time.Second
	reconnectBackoffFactor = 2
)

var ErrBrokerUnavailable = errors.New("rabbitmq connection is not established")

type Broker struct {
	url string

	mu         sync.Mutex
	connection *amqp.Connection
}

func NewBroker(url string) *Broker { return &Broker{url: url} }

func (b *Broker) Connect(ctx context.Context) (*amqp.Connection, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connection != nil && !b.connection.IsClosed() {
		return b.connection, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection, err := amqp.DialConfig(b.url, amqp.Config{Dial: amqp.DefaultDial(brokerConnectTimeout)})
	if err != nil {
		return nil, err
	}
	b.connection = connection
	slog.InfoContext(ctx, "rabbitmq connected")
	return connection, nil
}

func (b *Broker) Check(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connection == nil || b.connection.IsClosed() {
		return ErrBrokerUnavailable
	}
	return nil
}

func (b *Broker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connection == nil {
		return nil
	}
	return b.connection.Close()
}

func (b *Broker) Consume(ctx context.Context, spec QueueSpec, handle Handler) error {
	delay := reconnectInitialDelay
	for ctx.Err() == nil {
		connection, err := b.Connect(ctx)
		if err == nil {
			delay = reconnectInitialDelay
			err = Consume(ctx, connection, spec, handle)
		}
		if ctx.Err() != nil {
			return nil
		}
		slog.WarnContext(ctx, "consumer stopped; reconnecting", "queue", spec.Name, "error", err, "retry_in", delay)
		waitOrDone(ctx, delay)
		delay = nextReconnectDelay(delay)
	}
	return nil
}

func nextReconnectDelay(delay time.Duration) time.Duration {
	return min(delay*reconnectBackoffFactor, reconnectMaxDelay)
}

func waitOrDone(ctx context.Context, delay time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(delay):
	}
}

type ResilientPublisher struct {
	broker *Broker

	mu        sync.Mutex
	publisher *Publisher
}

func NewResilientPublisher(broker *Broker) *ResilientPublisher {
	return &ResilientPublisher{broker: broker}
}

func (p *ResilientPublisher) Publish(ctx context.Context, message events.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.publisher == nil {
		connection, err := p.broker.Connect(ctx)
		if err != nil {
			return err
		}
		if p.publisher, err = NewPublisher(connection); err != nil {
			return err
		}
	}
	if err := p.publisher.Publish(ctx, message); err != nil {
		p.discardPublisher()
		return err
	}
	return nil
}

func (p *ResilientPublisher) discardPublisher() {
	p.publisher.Close()
	p.publisher = nil
}
