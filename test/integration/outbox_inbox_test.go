//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/inbox"
	"github.com/crisnsz/orderflow/internal/platform/outbox"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/test/containers"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedOutbox(t *testing.T, pool *pgxpool.Pool, routingKey string, count int) []string {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, outbox.Schema); err != nil {
		t.Fatal(err)
	}
	var messageIDs []string
	for i := 0; i < count; i++ {
		message, err := events.New(routingKey, events.Payload{OrderID: fmt.Sprintf("order-%d", i), UserID: "u"})
		if err != nil {
			t.Fatal(err)
		}
		tx, _ := pool.Begin(ctx)
		if err := outbox.Insert(ctx, tx, message); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		messageIDs = append(messageIDs, message.ID)
	}
	return messageIDs
}

func TestOutboxRelayPublishesEachRowExactlyOnce(t *testing.T) {
	ctx := context.Background()
	pool := containers.Postgres(t)
	connection, err := rabbitmq.Dial(ctx, containers.RabbitMQ(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()

	const routingKey, total = "test.outbox.once", 25
	channel, _ := connection.Channel()
	if err := rabbitmq.Declare(channel, rabbitmq.QueueSpec{Name: "test.outbox.once.q", Keys: []string{routingKey}}); err != nil {
		t.Fatal(err)
	}
	wantMessageIDs := seedOutbox(t, pool, routingKey, total)

	var relays []*outbox.Relay
	for i := 0; i < 2; i++ {
		publisher, err := rabbitmq.NewPublisher(connection)
		if err != nil {
			t.Fatal(err)
		}
		relays = append(relays, &outbox.Relay{Pool: pool, Publisher: publisher, Batch: 4})
	}
	var sent atomic.Int64
	var wg sync.WaitGroup
	for _, relay := range relays {
		wg.Add(1)
		go func(relay *outbox.Relay) {
			defer wg.Done()
			for {
				sentCount, err := relay.RunOnce(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if sentCount == 0 {
					return
				}
				sent.Add(int64(sentCount))
			}
		}(relay)
	}
	wg.Wait()
	if sent.Load() != total {
		t.Fatalf("relays reported %d sends, want %d", sent.Load(), total)
	}
	if sentCount, err := relays[0].RunOnce(ctx); err != nil || sentCount != 0 {
		t.Fatalf("second pass sent %d (err %v), want 0", sentCount, err)
	}

	deliveriesByMessageID := map[string]int{}
	for {
		delivery, ok, err := channel.Get("test.outbox.once.q", true)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		deliveriesByMessageID[delivery.MessageId]++
	}
	if len(deliveriesByMessageID) != total {
		t.Fatalf("broker received %d distinct messages, want %d", len(deliveriesByMessageID), total)
	}
	for _, messageID := range wantMessageIDs {
		if deliveriesByMessageID[messageID] != 1 {
			t.Errorf("message %s delivered %d times, want 1", messageID, deliveriesByMessageID[messageID])
		}
	}
	var unsent int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE sent_at IS NULL`).Scan(&unsent)
	if unsent != 0 {
		t.Errorf("%d rows still unsent", unsent)
	}
}

type flakyPublisher struct {
	failAtCall          int
	calls               int
	publishedMessageIDs []string
}

func (p *flakyPublisher) Publish(_ context.Context, message events.Message) error {
	p.calls++
	if p.failAtCall > 0 && p.calls == p.failAtCall {
		return errors.New("broker unavailable")
	}
	p.publishedMessageIDs = append(p.publishedMessageIDs, message.ID)
	return nil
}

func TestOutboxRelayResumesAfterPublishFailure(t *testing.T) {
	ctx := context.Background()
	pool := containers.Postgres(t)
	messageIDs := seedOutbox(t, pool, "test.outbox.flaky", 5)

	publisher := &flakyPublisher{failAtCall: 3}
	relay := &outbox.Relay{Pool: pool, Publisher: publisher}
	sentCount, err := relay.RunOnce(ctx)
	if err == nil || sentCount != 2 {
		t.Fatalf("first pass: sent=%d err=%v, want 2 sent and an error", sentCount, err)
	}
	sentCount, err = relay.RunOnce(ctx)
	if err != nil || sentCount != 3 {
		t.Fatalf("second pass: sent=%d err=%v, want 3 sent", sentCount, err)
	}
	if len(publisher.publishedMessageIDs) != 5 {
		t.Fatalf("published %d messages, want 5", len(publisher.publishedMessageIDs))
	}
	for i, messageID := range messageIDs {
		if publisher.publishedMessageIDs[i] != messageID {
			t.Errorf("position %d: got %s, want %s (order must be preserved)", i, publisher.publishedMessageIDs[i], messageID)
		}
	}
}

func TestInboxDropsDuplicates(t *testing.T) {
	ctx := context.Background()
	pool := containers.Postgres(t)
	if _, err := pool.Exec(ctx, inbox.Schema+`; CREATE TABLE effects(n int)`); err != nil {
		t.Fatal(err)
	}
	effect := func(tx pgx.Tx) error { _, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`); return err }
	const id = "6f0f4f64-8d57-4a52-9d3f-2f5e1e0a0001"

	if ok, err := inbox.Once(ctx, pool, "c", id, effect); err != nil || !ok {
		t.Fatalf("first delivery: processed=%v err=%v", ok, err)
	}
	if ok, err := inbox.Once(ctx, pool, "c", id, effect); err != nil || ok {
		t.Fatalf("duplicate: processed=%v err=%v, want skipped", ok, err)
	}
	if ok, _ := inbox.Once(ctx, pool, "other", id, effect); !ok {
		t.Error("another consumer must process the same message")
	}
	var effectCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM effects`).Scan(&effectCount)
	if effectCount != 2 {
		t.Fatalf("side effects = %d, want 2 (one per consumer)", effectCount)
	}
}

func TestInboxRollsBackWhenTheHandlerFails(t *testing.T) {
	ctx := context.Background()
	pool := containers.Postgres(t)
	if _, err := pool.Exec(ctx, inbox.Schema+`; CREATE TABLE effects(n int)`); err != nil {
		t.Fatal(err)
	}
	const id = "6f0f4f64-8d57-4a52-9d3f-2f5e1e0a0002"
	_, err := inbox.Once(ctx, pool, "c", id, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want handler error")
	}
	ok, err := inbox.Once(ctx, pool, "c", id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO effects VALUES (1)`)
		return err
	})
	if err != nil || !ok {
		t.Fatalf("retry: processed=%v err=%v", ok, err)
	}
	var effectCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM effects`).Scan(&effectCount)
	if effectCount != 1 {
		t.Fatalf("effects = %d, want 1 (failed attempt rolled back)", effectCount)
	}
}

func TestInboxConcurrentDuplicatesRunOnce(t *testing.T) {
	ctx := context.Background()
	pool := containers.Postgres(t)
	if _, err := pool.Exec(ctx, inbox.Schema); err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = inbox.Once(ctx, pool, "c", "6f0f4f64-8d57-4a52-9d3f-2f5e1e0a0003", func(pgx.Tx) error {
				runs.Add(1)
				return nil
			})
		}()
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", runs.Load())
	}
}
