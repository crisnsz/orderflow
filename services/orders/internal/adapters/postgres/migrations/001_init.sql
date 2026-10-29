CREATE TABLE orders (
    id            text PRIMARY KEY,
    user_id       text   NOT NULL,
    state         text   NOT NULL,
    total_cents   bigint NOT NULL DEFAULT 0,
    cancel_reason text   NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX orders_user_idx ON orders(user_id, created_at DESC);

CREATE TABLE order_items (
    order_id         text    NOT NULL REFERENCES orders(id),
    sku              text    NOT NULL,
    quantity         integer NOT NULL,
    unit_price_cents bigint  NOT NULL DEFAULT 0,
    PRIMARY KEY (order_id, sku)
);

CREATE TABLE saga_log (
    id         bigserial PRIMARY KEY,
    order_id   text NOT NULL REFERENCES orders(id),
    from_state text NOT NULL,
    to_state   text NOT NULL,
    trigger    text NOT NULL,
    at         timestamptz NOT NULL
);
CREATE INDEX saga_log_order_idx ON saga_log(order_id, id);

CREATE TABLE outbox (
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
CREATE INDEX outbox_unsent_idx ON outbox(id) WHERE sent_at IS NULL;

CREATE TABLE inbox (
    consumer     text        NOT NULL,
    message_id   uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, message_id)
);
