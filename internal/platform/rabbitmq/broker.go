package rabbitmq

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

const (
	brokerConnectTimeout   = 5 * time.Second
	reconnectInitialDelay  = 500 * time.Millisecond
	reconnectMaxDelay      = 10 * time.Second
	reconnectBackoffFactor = 2
)

var ErrBrokerUnavailable = errors.New("rabbitmq connection is not established")

type Broker struct {
	address     string
	idleTimeout time.Duration

	mu         sync.Mutex
	connection *rabbitmqamqp.AmqpConnection
}

type BrokerOption func(*Broker)

func WithIdleTimeout(idleTimeout time.Duration) BrokerOption {
	return func(b *Broker) { b.idleTimeout = idleTimeout }
}

func NewBroker(address string, options ...BrokerOption) *Broker {
	broker := &Broker{address: address}
	for _, option := range options {
		option(broker)
	}
	return broker
}

func (b *Broker) Connect(ctx context.Context) (*rabbitmqamqp.AmqpConnection, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connection != nil && isOpen(b.connection) {
		return b.connection, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, brokerConnectTimeout)
	defer cancel()
	options, err := b.connectionOptions()
	if err != nil {
		return nil, err
	}
	connection, err := rabbitmqamqp.Dial(dialCtx, b.address, options)
	if err != nil {
		return nil, err
	}
	b.connection = connection
	slog.InfoContext(ctx, "rabbitmq connected")
	return connection, nil
}

func (b *Broker) connectionOptions() (*rabbitmqamqp.AmqpConnOptions, error) {
	parsed, err := url.Parse(b.address)
	if err != nil {
		return nil, err
	}
	options := &rabbitmqamqp.AmqpConnOptions{
		IdleTimeout:           b.idleTimeout,
		RecoveryConfiguration: &rabbitmqamqp.RecoveryConfiguration{ActiveRecovery: false},
	}
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		options.SASLType = amqp.SASLTypePlain(parsed.User.Username(), password)
	}
	return options, nil
}

func isOpen(connection *rabbitmqamqp.AmqpConnection) bool {
	_, open := connection.State().(*rabbitmqamqp.StateOpen)
	return open
}

func (b *Broker) Check(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connection == nil || !isOpen(b.connection) {
		return ErrBrokerUnavailable
	}
	return nil
}

func (b *Broker) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), brokerConnectTimeout)
	defer cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connection == nil {
		return nil
	}
	return b.connection.Close(ctx)
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
		if p.publisher, err = NewPublisher(ctx, connection); err != nil {
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
