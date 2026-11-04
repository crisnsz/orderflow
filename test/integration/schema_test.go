//go:build integration

package integration_test

const outboxSchema = `CREATE TABLE IF NOT EXISTS outbox (
	id          bigserial   PRIMARY KEY,
	message_id  uuid        NOT NULL UNIQUE,
	routing_key text        NOT NULL,
	order_id    text        NOT NULL,
	body        jsonb       NOT NULL,
	traceparent text        NOT NULL DEFAULT '',
	occurred_at timestamptz NOT NULL,
	created_at  timestamptz NOT NULL DEFAULT now(),
	sent_at     timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_unsent_idx ON outbox(id) WHERE sent_at IS NULL;`

const inboxSchema = `CREATE TABLE IF NOT EXISTS inbox (
	consumer     text        NOT NULL,
	message_id   uuid        NOT NULL,
	processed_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (consumer, message_id)
);`
