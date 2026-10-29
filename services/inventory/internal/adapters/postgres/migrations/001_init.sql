CREATE TABLE products (
    sku         text PRIMARY KEY,
    name        text   NOT NULL,
    price_cents bigint NOT NULL CHECK (price_cents >= 0),
    stock       integer NOT NULL CHECK (stock >= 0)
);

CREATE TABLE reservations (
    order_id   text PRIMARY KEY,
    status     text NOT NULL CHECK (status IN ('RESERVED', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE reservation_lines (
    order_id         text    NOT NULL REFERENCES reservations(order_id),
    sku              text    NOT NULL REFERENCES products(sku),
    quantity         integer NOT NULL CHECK (quantity > 0),
    unit_price_cents bigint  NOT NULL,
    PRIMARY KEY (order_id, sku)
);

INSERT INTO products (sku, name, price_cents, stock) VALUES
    ('KB-01',  'Mechanical keyboard',   8900, 10),
    ('MS-01',  'Wireless mouse',        2900, 25),
    ('HUB-01', 'USB-C hub',             4500, 3),
    ('MON-01', 'Ultrawide monitor',    39900, 2);
