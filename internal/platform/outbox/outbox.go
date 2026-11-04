package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Insert(ctx context.Context, tx pgx.Tx, message events.Message) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO outbox(message_id, routing_key, order_id, body, traceparent, occurred_at)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		message.ID, message.Type, message.OrderID, message.Body, telemetry.Traceparent(ctx), message.OccurredAt)
	return err
}

type Publisher interface {
	Publish(ctx context.Context, message events.Message) error
}

const (
	defaultPollInterval = 500 * time.Millisecond
	defaultBatchSize    = 50
)

type Relay struct {
	Pool      *pgxpool.Pool
	Publisher Publisher
	Interval  time.Duration
	Batch     int
}

type pendingEvent struct {
	rowID   int64
	message events.Message
}

func (r *Relay) Run(ctx context.Context) {
	if r.Interval == 0 {
		r.Interval = defaultPollInterval
	}
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		sentCount, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "outbox relay failed", "error", err)
		}
		if sentCount > 0 && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	batchSize := r.Batch
	if batchSize == 0 {
		batchSize = defaultBatchSize
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	pending, err := lockUnsentEvents(ctx, tx, batchSize)
	if err != nil {
		return 0, err
	}

	sentCount := 0
	var publishErr error
	for _, event := range pending {
		publishErr = r.publishEvent(ctx, event)
		if publishErr != nil {
			break
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE id = $1`, event.rowID); err != nil {
			return sentCount, err
		}
		sentCount++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return sentCount, publishErr
}

func lockUnsentEvents(ctx context.Context, tx pgx.Tx, batchSize int) ([]pendingEvent, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, message_id::text, routing_key, order_id, body, traceparent, occurred_at
		 FROM outbox WHERE sent_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, batchSize)
	if err != nil {
		return nil, err
	}
	var pending []pendingEvent
	for rows.Next() {
		var event pendingEvent
		message := &event.message
		if err := rows.Scan(&event.rowID, &message.ID, &message.Type, &message.OrderID, &message.Body, &message.Traceparent, &message.OccurredAt); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, event)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pending, nil
}

func (r *Relay) publishEvent(ctx context.Context, event pendingEvent) error {
	message := event.message
	relayCtx := telemetry.ContextWithTraceparent(ctx, message.Traceparent)
	relayCtx = telemetry.WithOrderID(relayCtx, message.OrderID)
	relayCtx, span := telemetry.Tracer().Start(relayCtx, "outbox.relay "+message.Type)
	err := r.Publisher.Publish(relayCtx, message)
	span.End()
	if err == nil {
		slog.InfoContext(relayCtx, "outbox event published", "type", message.Type, "message_id", message.ID)
	}
	return err
}
