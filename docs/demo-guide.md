# Demo guide

How to run orderflow, what every control on the demo page does, and five
scenarios that each prove one part of the design. Every scenario says **what to
do**, **what you should see** and **why it matters**.

- [1. Start the stack](#1-start-the-stack)
- [2. The demo page, control by control](#2-the-demo-page-control-by-control)
- [3. Scenarios](#3-scenarios)
- [4. RabbitMQ: how the broker is set up](#4-rabbitmq-how-the-broker-is-set-up)
- [5. Jaeger and logs](#5-jaeger-and-logs)
- [6. HTTP API reference](#6-http-api-reference)

## 1. Start the stack

Requirements: Docker with Compose v2.

```sh
make up      # = docker compose up --build -d --wait
make down    # stop everything and delete the volumes (fresh data next time)
```

`--wait` returns only when every service reports healthy. The first build takes
a few minutes.

| What | URL | Login |
|---|---|---|
| Demo page | http://localhost:8080 | pick a user, no password |
| Jaeger (traces) | http://localhost:16686 | none |
| RabbitMQ management | http://localhost:15672 | `orderflow` / `orderflow-demo-password` |

From another machine on the LAN, replace `localhost` with the host's IP. The
ports can be changed with `GATEWAY_PORT`, `JAEGER_UI_PORT` and
`RABBITMQ_UI_PORT`.

Only the gateway, Jaeger and the RabbitMQ UI are published. The other services,
Postgres and the payments admin API are reachable only inside the compose
network.

## 2. The demo page, control by control

### Pick a demo user

There is no real login. Clicking a user calls `POST /login`, and the gateway
answers with a **signed session cookie** (HMAC). Every later request is
identified by that cookie, and you can only see your own orders.

Each user exists to show a different branch of the flow:

| User | Card | Autopay consent | Notifications | Demonstrates |
|---|---|---|---|---|
| **Alice** | Visa •4242 | yes | email | the happy path |
| **Bob** | Mastercard •4444 | **no** | SMS | the no-consent branch |
| **Carol** | Visa •1111 | yes | email + SMS + push | per-customer channels |

### Your account

Read only: the saved card and whether the customer allowed automatic charges.

### Place an order

Set a quantity per product and press **Place order**. Prices are shown in EUR.

| SKU | Product | Price | Initial stock |
|---|---|---|---|
| KB-01 | Mechanical keyboard | 89.00 | 10 |
| MS-01 | Wireless mouse | 29.00 | 25 |
| HUB-01 | USB-C hub | 45.00 | 3 |
| MON-01 | Ultrawide monitor | 399.00 | 2 |

The small stock on HUB-01 and MON-01 is deliberate: it lets you hit the
out-of-stock branch quickly. `make down` resets the stock.

### Payment failure switch

Tells the **fake bank** inside the payments service to fail. Choose a value and
press **Apply**: the gateway forwards it to the payments admin API
(`PUT /api/admin/failure`).

| Value | What the bank does | Result |
|---|---|---|
| `off` | approves everything | payment succeeds |
| `auth` | declines the **authorization** | nothing was ever held; `payment.failed` |
| `sale` | authorizes, then the **capture** fails, so payments **voids** the authorization | no money stays held; `payment.failed` |

Two things to keep in mind:

- The switch is **global**: it affects every user's orders, not only yours.
- It simulates a **business** failure (the bank said no). That is not the same
  as a **technical** failure (a service is down). Business failures go through
  the saga's compensation; technical failures go through retries. Scenario 5
  shows the difference.

### Live timeline

The events of the order you just placed, pushed by the server as they happen
(Server-Sent Events, see [sse.md](sse.md)). The chip next to it shows the
saga state.

| Timeline line | Event | Published by |
|---|---|---|
| Order placed | `order.placed` | orders |
| Stock reserved, order confirmed | `order.confirmed` | orders |
| Invoice generated | `invoice.generated` | billing |
| Payment succeeded | `payment.succeeded` | payments |
| Payment failed | `payment.failed` | payments |
| Waiting for manual payment | `payment.pending_manual` | payments |
| Order cancelled (stock released) | `order.cancelled` | orders |
| Order completed | `order.completed` | orders |

### Your orders

Your order history with the final state. It refreshes when an order completes
or is cancelled.

## 3. Scenarios

Before each one, set the failure switch to `off` unless the scenario says
otherwise.

### Scenario 1: happy path

**Do:** log in as Alice, order 1 keyboard.

**See:** order placed → stock reserved → invoice generated → payment succeeded →
order completed. The chip ends at `COMPLETED`, and the keyboard stock drops by 1.

**Why it matters:** this is the whole saga moving forward. Each step runs in a
different service, and they talk only through events on RabbitMQ.

### Scenario 2: no autopay consent

**Do:** log out, log in as Bob, order anything.

**See:** the timeline stops at "Waiting for manual payment". The chip stays
at `INVOICED`.

**Why it matters:** payments never charges a card without consent. It publishes
`payment.pending_manual` instead.

**Known gap:** there is no "pay now" button and no expiry, so the order waits
forever ([ADR 24](../DECISIONS.md)).

### Scenario 3: payment fails, the saga compensates

This is the most important one.

**Do:** log in as Carol, set the switch to `sale`, press Apply. Note the HUB-01
stock, then order 1 hub. Set the switch back to `off` afterwards.

**See:** invoice generated → payment failed → order cancelled. The chip ends
at `CANCELLED`, and the hub stock goes back to the number you noted.

**What happened behind the scenes:**

1. payments: Auth succeeds → Sale fails → **Void** the authorization.
2. payments publishes `payment.failed`, with the reason
   `sale: declined by bank: capture_failed`.
3. orders moves the saga to `COMPENSATING` and calls inventory `Release` over
   gRPC. That call is the compensation: it undoes the earlier reservation.
4. orders moves to `CANCELLED` and publishes `order.cancelled`.
5. notifications sends email + SMS + push, because Carol has all three.

The Void is not an event, so it does not show in the timeline. You see it in the
payments logs:

```text
fake bank: authorized
fake bank: sale failed (forced failure)
fake bank: authorization voided
```

Try `auth` too: the authorization is declined, so there is nothing to void.

**Why it matters:** in a distributed system you cannot roll back a database
transaction that spans services. Instead, each completed step has a
**compensating action** that undoes it.

### Scenario 4: out of stock

**Do:** order 5 monitors (there are 2).

**See:** the order is created and immediately `CANCELLED`, with the reason
`insufficient stock: MON-01 requested 5, available 2`.

**Why it matters:** the stock check is a **synchronous** gRPC call (`Reserve`)
made while the order is placed, before any money is involved. Nothing was
reserved, so there is nothing to compensate.

### Scenario 5: a service is down, the message is retried

This one is run from a terminal, because you need to stop a container at the
right moment.

**Do:**

```sh
# with the failure switch on "sale" and logged in on the page as anyone:
#   1. place an order on the page
#   2. right away:
docker compose stop inventory
```

Then open RabbitMQ → **Queues** and watch `orders.q.retry.1`, `.retry.2` and
`.retry.3`.

**See:** orders tries to compensate, but `Release` cannot reach inventory, so
the handler fails. The message moves to `orders.q.retry.1`, then `.retry.2`,
then `.retry.3`. Meanwhile the order stays at `INVOICED`. Then:

```sh
docker compose start inventory
```

The next retry succeeds, and the order ends `CANCELLED`. Measured on this stack:
three retries, then success about 35 s after inventory came back.

If you leave inventory stopped for longer than all the retries (about 45 s plus
the gRPC timeouts), the message lands in `orders.q.dlq` and the order stays
stuck. That is what a dead-letter queue is for: a human inspects it.

The orders logs show each attempt:

```text
WARN message handling failed; retry scheduled  attempt=1  orders.q.retry.1
WARN message handling failed; retry scheduled  attempt=2  orders.q.retry.2
WARN message handling failed; retry scheduled  attempt=3  orders.q.retry.3
WARN order cancelled after compensation
```

**Why the state stays `INVOICED` and not `COMPENSATING`:** the state change and
the `Release` call run in the same database transaction. When `Release` fails,
the transaction rolls back. This is the trade-off documented in
[ADR 20](../DECISIONS.md).

**Why it matters:** a temporary outage does not lose the event, and it does not
need manual work. The broker keeps the message and tries again later.

### All of it, unattended

`make smoke` runs scenarios 1 to 4 against the running stack and prints
PASS/FAIL.

## 4. RabbitMQ: how the broker is set up

### Exchange

There is one **topic exchange**, `orderflow.events`, and every service publishes
to it. The routing key is the event type, for example `payment.failed`. Each
service binds its queue to the event types it cares about.

### Queues and bindings

The live bindings, as shown in Exchanges → `orderflow.events` → Bindings:

| Queue | Consumer | Listens to |
|---|---|---|
| `orders.q` | orders | `invoice.generated`, `payment.succeeded`, `payment.failed`, `payment.pending_manual` |
| `billing.q` | billing | `order.confirmed` |
| `payments.q` | payments | `invoice.generated` |
| `notifications.q` | notifications | every event except `order.placed` |
| `gateway.sse` | gateway | `#`, which means **everything** (it feeds the timeline) |

Each consumer queue has a set of companions:

| Queue | Purpose | Message TTL | When the TTL expires |
|---|---|---|---|
| `<name>.retry.1` | first retry | 2 s | goes back to `<name>` |
| `<name>.retry.2` | second retry | 10 s | goes back to `<name>` |
| `<name>.retry.3` | third retry | 30 s | goes back to `<name>` |
| `<name>.dlq` | dead letters, kept for a human | none | stays |

`gateway.sse` has a DLQ but no retry queues. Its handler only writes to memory,
so a failure there is not worth retrying.

### How a retry works

Nobody consumes from the retry queues. They are waiting rooms:

```text
orders.q ──handler fails──▶ orders.q.retry.1 (sits 2 s)
                                   │ TTL expires: dead-lettered
                                   ▼
orders.q ◀── default exchange, routing key "orders.q"
```

1. The handler returns an error. The consumer publishes a copy to the next retry
   queue, with headers `x-retry-count` and `x-last-error`, and then acks the
   original.
2. The retry queue has `x-message-ttl`. When it expires, RabbitMQ
   **dead-letters** the message to `x-dead-letter-exchange: ""` (the default
   exchange) with `x-dead-letter-routing-key: orders.q`, which delivers it
   straight back to the main queue.
3. After the third retry fails, or right away when the error is marked
   **permanent** (`ErrPermanent`), the message goes to `<name>.dlq`.

You can check these arguments in Queues → click `orders.q.retry.1` → Features.

### What to look at in the UI

- **Queues**: the message count per queue. During scenario 5 you see the message
  move through the retry queues.
- **Queues → `<name>.dlq` → Get messages**: read a dead letter, including
  `x-last-error` (why it failed) and `x-retry-count`.
- **Exchanges → `orderflow.events`**: message rates and the bindings table above.

### Why each message is safe to process twice

RabbitMQ delivers **at least once**, so a message can arrive twice. Every
consumer records the message id in an **inbox** table, in the same transaction
as its work. A duplicate finds the id already there and is ignored. Publishing
goes through a **transactional outbox**: the event row is committed together
with the state change, and a relay publishes it afterwards. That way an event is
never lost, and never sent for a change that rolled back.

## 5. Jaeger and logs

**Jaeger** (http://localhost:16686): pick service `gateway`, operation
`POST /api/orders`, and press **Find Traces**. One trace covers HTTP, gRPC
(`Reserve`, `Release`), each outbox relay, each publish and each consume,
retries included. The fake bank calls have no spans of their own: use the logs
for those.

**Logs**: every line is JSON with `service`, `order_id` and `trace_id`. To see
one order across all services:

```sh
docker compose logs --no-log-prefix \
  | jq -R -c --arg o "<order id>" 'fromjson? | select(.order_id == $o)' \
  | jq -s -r 'sort_by(.time)[] | "\(.time[11:23]) \(.service) \(.level) \(.msg)"'
```

More recipes: [observability.md](observability.md).

## 6. HTTP API reference

All `/api/*` routes except `/api/users` need the session cookie from `/login`.

| Method and path | Does |
|---|---|
| `GET /` | the demo page |
| `GET /api/users` | the demo users to choose from |
| `POST /login` | body `{"user_id":"u-alice"}`; sets the session cookie |
| `POST /logout` | clears the cookie |
| `GET /api/me` | current user and saved cards |
| `GET /api/products` | catalog with live stock |
| `POST /api/orders` | body `{"items":[{"sku":"KB-01","quantity":1}]}`; places an order |
| `GET /api/orders` | your orders |
| `GET /api/orders/{id}` | one order, with its saga history |
| `GET /api/orders/{id}/events` | live timeline as Server-Sent Events |
| `GET /api/admin/failure` | current failure switch value |
| `PUT /api/admin/failure` | body `{"stage":""}`, `"auth"` or `"sale"` |

Example from a terminal:

```sh
curl -c jar -H 'Content-Type: application/json' -d '{"user_id":"u-alice"}' localhost:8080/login
curl -b jar -H 'Content-Type: application/json' \
  -d '{"items":[{"sku":"KB-01","quantity":1}]}' localhost:8080/api/orders
curl -N -b jar localhost:8080/api/orders/<id>/events    # -N: print events as they arrive
```
