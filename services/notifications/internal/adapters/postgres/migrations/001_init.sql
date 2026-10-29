CREATE TABLE preferences (
    user_id    text PRIMARY KEY,
    email_addr text,
    phone      text,
    push_token text,
    email_on   boolean NOT NULL DEFAULT false,
    sms_on     boolean NOT NULL DEFAULT false,
    push_on    boolean NOT NULL DEFAULT false
);

CREATE TABLE notification_log (
    id         bigserial PRIMARY KEY,
    message_id uuid NOT NULL,
    order_id   text NOT NULL,
    channel    text NOT NULL,
    subject    text NOT NULL,
    sent_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (message_id, channel)
);

CREATE TABLE inbox (
    consumer     text        NOT NULL,
    message_id   uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, message_id)
);

INSERT INTO preferences (user_id, email_addr, phone, push_token, email_on, sms_on, push_on) VALUES
    ('u-alice', 'alice@example.test', NULL,            NULL,            true,  false, false),
    ('u-bob',   NULL,                 '+34600000002',  NULL,            false, true,  false),
    ('u-carol', 'carol@example.test', '+34600000003',  'device-carol-1', true,  true,  true);
