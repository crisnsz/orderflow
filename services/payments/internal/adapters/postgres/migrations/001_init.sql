CREATE TABLE cards (
    user_id          text PRIMARY KEY,
    brand            text    NOT NULL,
    last4            text    NOT NULL,
    token            text    NOT NULL,
    autopay_consent  boolean NOT NULL DEFAULT false,
    exp_month        integer NOT NULL,
    exp_year         integer NOT NULL
);

CREATE TABLE payments (
    order_id     text PRIMARY KEY,
    user_id      text   NOT NULL,
    invoice_id   text   NOT NULL,
    amount_cents bigint NOT NULL,
    status       text   NOT NULL,
    auth_ref     text   NOT NULL DEFAULT '',
    sale_ref     text   NOT NULL DEFAULT '',
    reason       text   NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
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

INSERT INTO cards (user_id, brand, last4, token, autopay_consent, exp_month, exp_year) VALUES
    ('u-alice', 'Visa',       '4242', 'tok_demo_alice', true,  12, 2032),
    ('u-bob',   'Mastercard', '4444', 'tok_demo_bob',   false, 11, 2031),
    ('u-carol', 'Visa',       '1111', 'tok_demo_carol', true,  8,  2033);
