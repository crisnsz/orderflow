CREATE SEQUENCE invoice_number_seq START 1000;

CREATE TABLE invoices (
    number      text PRIMARY KEY,
    order_id    text   NOT NULL UNIQUE,
    user_id     text   NOT NULL,
    currency    text   NOT NULL,
    net_cents   bigint NOT NULL,
    tax_cents   bigint NOT NULL,
    total_cents bigint NOT NULL,
    issued_at   timestamptz NOT NULL
);

CREATE TABLE invoice_lines (
    invoice_number   text    NOT NULL REFERENCES invoices(number),
    sku              text    NOT NULL,
    quantity         integer NOT NULL,
    unit_price_cents bigint  NOT NULL,
    PRIMARY KEY (invoice_number, sku)
);

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
