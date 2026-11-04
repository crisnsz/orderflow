# Walkthrough: one order through the code

This is a guided tour. It follows one order, placed by Alice, from the browser
to the last timeline entry, and tells you which file to open at each step.
Open the files in the order given here.

Other docs cover other angles, so this one does not repeat them:

- What the system is and how to run it: [README.md](../README.md).
- Why each choice was made (ADRs): [DECISIONS.md](../DECISIONS.md).
- Demo scenarios and the RabbitMQ setup: [demo-guide.md](demo-guide.md).
- The live timeline: [sse.md](sse.md).
- Naming and layering rules: [conventions.md](conventions.md).
- Logs and traces: [observability.md](observability.md).

All paths below are relative to the repository root. Money is integer EUR
cents. The code has no comments, so function names carry the meaning.

## 1. Map

| Service | Owns | Is called by | Calls | Database |
|---|---|---|---|---|
| `gateway` | Browser-facing HTTP API, demo page, login cookie, SSE timeline | the browser (HTTP) | orders and inventory (gRPC), payments (HTTP); consumes every event (RabbitMQ) | none |
| `orders` | Orders and the saga state machine | gateway (gRPC), RabbitMQ | inventory (gRPC); publishes through its outbox | `orders` |
| `inventory` | Products, stock, reservations, prices | orders and gateway (gRPC) | none | `inventory` |
| `billing` | Invoices | RabbitMQ (`order.confirmed`) | publishes through its outbox | `billing` |
| `payments` | Cards, autopay decision, the fake bank, the failure switch | RabbitMQ (`invoice.generated`), gateway (HTTP) | the fake bank (in process); publishes through its outbox | `payments` |
| `notifications` | Per-user channel preferences, delivery log | RabbitMQ | the log-only channels (in process) | `notifications` |

Three transports, each with one job:

- HTTP: browser to gateway, and gateway to payments for cards and the failure switch.
- gRPC: calls that need an answer now (place an order, reserve stock, list products).
- RabbitMQ: facts that already happened (`order.confirmed`, `payment.succeeded`, ...).

The contracts are in `proto/orders/v1/orders.proto`,
`proto/inventory/v1/inventory.proto` (generated code in `gen/`) and
`internal/platform/events/events.go` (event names and the JSON payload).

The gateway has no database. Its only state is the in-memory timeline.

## 2. The happy path

Alice (`u-alice`) has a Visa with autopay consent. She orders one keyboard
(`KB-01`, 8900 cents). The seed data is in the `001_init.sql` migration of
each service.

Events you will see, in order: `order.placed`, `order.confirmed`,
`invoice.generated`, `payment.succeeded`, `order.completed`.

### Step 0: she is logged in

`services/gateway/internal/adapters/http/session_handlers.go`, `login`.

`POST /login` with a seeded user id sets an HMAC-signed cookie
(`domain.Signer` in `services/gateway/internal/domain/session.go`). There is
no password. Every other route is wrapped by `authenticated` in
`services/gateway/internal/adapters/http/auth.go`, which verifies the cookie
and looks up the user.

Reason: authentication is not what this project demonstrates (ADR 11).

### Step 1: the browser posts the order

`services/gateway/internal/adapters/http/static/app.js`, the
`byId("place").onclick` handler.

It sends `POST /api/orders` with `{ items: [{sku, quantity}] }`. No prices.
Then it calls `followOrder(order)`, which opens an `EventSource` on
`/api/orders/{id}/events`. That happens after the POST returns.

### Step 2: gateway HTTP handler

`services/gateway/internal/adapters/http/server.go` (`NewHandler` registers
`POST /api/orders`) and `order_handlers.go` (`placeOrder`).

The handler checks `Content-Type: application/json` (`decodeJSONBody` in
`respond.go`), requires 1 to 20 items, and passes the user id from the cookie,
not from the body. The handler depends on the `storefront.Orders` interface
(`services/gateway/internal/storefront/storefront.go`), not on gRPC.

Reason: the client can never choose its user or its price.

### Step 3: gateway gRPC client

`services/gateway/internal/adapters/grpc/clients.go`, `Orders.PlaceOrder`.

Translates the HTTP-side types into protobuf, applies a 15 s timeout
(`placeOrderTimeout`), and maps gRPC `NotFound` / `InvalidArgument` back to
`storefront.ErrNotFound` / `ErrInvalid`. The `otelgrpc` stats handler puts the
trace context on the call.

### Step 4: orders gRPC server

`services/orders/internal/adapters/grpc/server.go`, `Server.PlaceOrder`.

A thin adapter: protobuf in, `domain.Item` out, calls
`placing.Service.PlaceOrder`, converts the result with `toProtoOrder`. Error
mapping is in `toGRPCError`; unknown errors become a generic `Internal` so
details do not leak.

### Step 5: the `placing` use case, first transaction

`services/orders/internal/placing/place_order.go`, `PlaceOrder` then
`recordPlacedOrder`.

`PlaceOrder` validates, builds `domain.NewOrder` (state `PENDING`), and runs
this in one database transaction:

```go
return s.repository.InTx(ctx, func(tx domain.Tx) error {
	if err := tx.Insert(ctx, order); err != nil {
		return err
	}
	return domain.QueueEvent(ctx, tx, events.OrderPlaced, events.Payload{
		OrderID: order.ID, UserID: order.UserID, Items: toEventItems(order.Items)})
})
```

`tx.Insert` writes the order. `QueueEvent` (`services/orders/internal/domain/ports.go`)
builds an `events.Message` and calls `tx.Outbox`, which is
`outbox.Insert` into the `outbox` table of the same database. This is the
transactional outbox: the state change and the "please publish this" row
commit or roll back together. See `orderTx` in
`services/orders/internal/adapters/postgres/store.go`.

Reason: "update the database and publish to the broker" cannot be made atomic
across two systems. Writing the event to your own database can (ADR 5).

The order is saved as `PENDING` before inventory is called. If the process
dies in between, the order is visible and cancellable; nothing is reserved
without a trace (ADR 4).

### Step 6: synchronous reservation in inventory

Caller: `services/orders/internal/adapters/inventory/client.go`, `Client.Reserve`
(5 s timeout). Server: `services/inventory/internal/adapters/grpc/server.go`,
`Server.Reserve`, then `services/inventory/internal/reserving/reserve_stock.go`.

Inside one inventory transaction, `Reserve`:

1. looks up an existing reservation for the order id and, if there is one,
   returns it unchanged (this makes the call idempotent);
2. otherwise locks the product rows (`LockProducts`, `SELECT ... FOR UPDATE`,
   sorted by SKU), runs the pure function `domain.Plan`
   (`services/inventory/internal/domain/stock.go`) to check stock and price
   the lines, decrements stock with `AdjustStock`, and saves the reservation.

The priced lines come back to orders. Prices therefore come from inventory
only (ADR 19).

Reason for gRPC here: the customer needs to know now whether the order is
accepted, and the row locks make overselling impossible
(`TestConcurrentReservationsNeverOversell`). The cost is that orders depends
on inventory being up to place an order (ADR 4).

### Step 7: the `placing` use case, second transaction

Same file, `confirmReservedOrder`.

In a new transaction: `tx.Load` (which locks the row with `FOR UPDATE`),
`order.Price(pricedItems)`, `order.Apply(domain.InventoryReservedTrigger, ...)`
which moves `PENDING` to `INVENTORY_RESERVED`, `tx.Save` (updates the order and
appends the transition to `saga_log`), and `QueueEvent` for `order.confirmed`
with the amount and the priced items.

The state machine itself is a map in
`services/orders/internal/domain/order.go`:

```go
var transitions = map[State]map[Trigger]State{
```

`Order.Apply` looks the trigger up and returns `ErrInvalidTransition` if it is
not allowed. No I/O in the domain package.

`PlaceOrder` then reloads the order and returns it. The gateway answers the
browser with `201` and state `INVENTORY_RESERVED`. Everything after this
happens asynchronously.

### Step 8: the outbox relay publishes

`internal/platform/outbox/outbox.go`, `Relay.Run` and `Relay.RunOnce`.
Started in `services/orders/cmd/orders/main.go` as an `errgroup` goroutine.

Every 500 ms (immediately again if it just sent something), the relay opens a
transaction and runs:

```sql
SELECT id, message_id::text, routing_key, order_id, body, traceparent, occurred_at
FROM outbox WHERE sent_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED
```

For each row it calls `Publisher.Publish`, then sets `sent_at`, and commits.
`SKIP LOCKED` lets two relays run without publishing the same row twice.

`Publisher.Publish` is in `internal/platform/rabbitmq/rabbitmq.go`. It publishes
to the topic exchange `orderflow.events` with the event type as routing key,
persistent delivery, and waits for the broker's publisher confirm
(`publishAndAwaitConfirm`). The envelope (message id, order id as correlation
id, type, timestamp, `traceparent`) is in AMQP properties and headers; the
body is the JSON payload (ADR 9).

Reason: if the broker or the relay dies after the publish but before the
commit, the row is sent again. That is why delivery is at-least-once and why
step 9 has an inbox.

### Step 9: billing issues the invoice

Files, in order:

- `services/billing/internal/adapters/rabbitmq/consumer.go`: `Spec` binds
  `billing.q` to `order.confirmed` only.
- `internal/platform/rabbitmq/rabbitmq.go`: `Consume` and `processDelivery` turn
  an AMQP delivery into an `events.Message` and call the handler.
- `services/billing/internal/invoicing/generate_invoice.go`: `HandleEvent`.
- `services/billing/internal/domain/invoice.go`: `NewInvoice`.
- `services/billing/internal/adapters/postgres/store.go`: `Store.Once`,
  `billingTx.SaveInvoice`.

`HandleEvent` decodes the payload, builds the invoice, then calls
`store.Once(ctx, Consumer, message.ID, ...)`. `Once` is
`internal/platform/inbox/inbox.go`:

```go
insertResult, err := tx.Exec(ctx,
	`INSERT INTO inbox(consumer, message_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, consumer, messageID)
```

If zero rows were inserted, the message was already processed and the
handler returns without side effects. If the handler fails, the transaction
rolls back, inbox row included, so the retry runs normally. Inside the same
transaction `issueInvoice` saves the invoice (unique per order id, number
`INV-<sequence>`) and queues `invoice.generated` in billing's own outbox.

For Alice: 8900 is tax-inclusive, VAT is 21 %, so net is 7355 and tax is 1545
(`NewInvoice` extracts tax, rounding half up).

Billing's relay then publishes `invoice.generated`.

### Step 10: payments charges the card

Files: `services/payments/internal/adapters/rabbitmq/consumer.go` (`payments.q`
binds `invoice.generated`), `services/payments/internal/charging/handle_invoice.go`
(`HandleEvent`, `settleInvoice`, `chargeSavedCard`),
`charge.go` (`charge`), `services/payments/internal/domain/payment.go`
(`Decide`), `services/payments/internal/adapters/bank/fake.go`.

1. `CardFor` loads Alice's card.
2. `domain.Decide` is a pure function: no card, no consent or an expired card
   gives `AwaitManual`; otherwise `ChargeNow`.
3. Inside `store.Once` (inbox again), `settleInvoice` calls `charge`:
   `bank.Authorize`, then `bank.Sale`. If the sale fails after a successful
   authorization, `charge` calls `bank.Void` so no funds stay held.
4. The payment row is saved and `payment.succeeded` is queued in payments'
   outbox, in the same transaction.

The idempotency key passed to the bank is the order id, so a retried call
gets the same references from the fake bank. The `Bank` interface is declared
in `charging/ports.go`, next to the code that uses it; the fake implements it.

Note that the bank call happens inside the database transaction. If the
commit fails after the bank call, the retry calls the bank again with the same
key.

### Step 11: orders advances the saga

Files: `services/orders/internal/adapters/rabbitmq/consumer.go` (`orders.q`
binds `invoice.generated`, `payment.succeeded`, `payment.failed`,
`payment.pending_manual`), `services/orders/internal/advancing/handle_saga_event.go`.

`HandleEvent` runs everything inside `repository.Once` (inbox), loads the
order with a row lock, and `react` switches on the event type:

- `invoice.generated`: `applyTrigger(InvoiceGenerated)`, `INVENTORY_RESERVED`
  to `INVOICED`.
- `payment.succeeded`: `completeAfterPayment`. Moves to `PAID`, then
  immediately to `COMPLETED` through `OrderFulfilled`, saves, and queues
  `order.completed`, all in one transaction.

`applyTrigger` treats `ErrInvalidTransition` as "log a warning and ignore", so
a late or duplicate event cannot corrupt the state. Orders never publishes a
state change without a matching `saga_log` row.

Reason: orders is the only component that knows the whole flow. Billing and
payments just react to one event and emit one event (ADR 3).

### Step 12: notifications

Files: `services/notifications/internal/adapters/rabbitmq/consumer.go`
(`Spec` lists seven event types), `services/notifications/internal/notifying/notify_customer.go`,
`services/notifications/internal/domain/notification.go` (`Compose`, `Plan`),
`services/notifications/internal/adapters/channels/log.go`.

`HandleEvent` loads the user's preferences, `domain.Plan` builds one
notification per enabled channel (Alice: email only), and inside `Once` each
`Sender.Send` is called and recorded. The senders only write a log line
(ADR 14). Notifications has no outbox because it publishes nothing.

### Step 13: gateway SSE hub and the browser timeline

Files, in order:

- `services/gateway/internal/adapters/rabbitmq/subscriber.go`: `Run` consumes
  queue `gateway.sse` bound with `#`, so it receives every event, and calls
  `hub.Publish`.
- `services/gateway/internal/timeline/hub.go`: `Hub.Publish`, `Hub.Subscribe`.
- `services/gateway/internal/adapters/http/stream_events.go`: `streamEvents`.
- `services/gateway/internal/adapters/http/static/app.js`: `followOrder`.

`streamEvents` first checks the order belongs to the logged-in user
(`ownedOrder` calls orders over gRPC; another user's order gives 404). It then
calls `Hub.Subscribe`, which returns the events already seen for that order
plus a channel for new ones, both taken under one lock. It writes the replay,
then loops on live events and a 15 s keep-alive comment.

Reason for the replay: the browser opens the stream after the POST returned,
by when `order.placed` and `order.confirmed` may already be published
(ADR 18). The hub also de-duplicates by message id, since delivery is
at-least-once.

The browser's `EventSource` listeners append each event to the timeline and
reload the order list when it sees `order.completed` or `order.cancelled`.
Alice's order is `COMPLETED`.

Full detail on the stream: [sse.md](sse.md).

## 3. The other paths

All of these use the same code. Only the data differs.

### Bob: no autopay consent

`domain.Decide` returns `AwaitManual` ("customer has not enabled autopay").
`awaitManualPayment` in `handle_invoice.go` records a `PENDING_MANUAL` payment
and queues `payment.pending_manual`. The bank is never called. Orders'
`react` only logs it, so the order stays `INVOICED`. Notifications tells Bob.
Nothing ever moves the order again; this is a known dead end (ADR 24).

### Payment failure and compensation

The failure switch (`bank.Switch` in `fake.go`, set by `PUT /admin/failure` in
`services/payments/internal/adapters/http/admin.go`) makes the fake bank fail
at `auth` or `sale`.

- `auth`: `Authorize` returns an error wrapping `charging.ErrDeclined`. No void.
- `sale`: `Sale` fails, `charge` calls `Void`, returns an error wrapping
  `ErrDeclined`.

In both cases `chargeSavedCard` catches `ErrDeclined`, records `FAILED`, and
queues `payment.failed` with the reason. If the sale is declined and the void
also fails, the decline still wins: `payment.failed` is published and the failed
void is logged at error level with the authorization reference (ADR 29). A
technical error (anything that is not `ErrDeclined`) is returned as is, the
transaction rolls back, and the message is retried (see below).

Orders receives `payment.failed` and calls `Compensator.Compensate`
(`services/orders/internal/compensating/compensate.go`), inside the same
inbox transaction:

1. `Apply(PaymentFailed)`: to `COMPENSATING`, cancel reason recorded.
2. `inventory.Release` over gRPC. Inventory puts the stock back and marks the
   reservation `RELEASED`; it is a no-op if already released or unknown.
3. `Apply(InventoryReleased)`: to `CANCELLED`.
4. Save, and queue `order.cancelled`.

If `Release` fails, the whole transaction rolls back and the message goes to
the retry queues. A side effect: `COMPENSATING` is only ever visible in
`saga_log`, never as a committed `orders.state` (ADR 20).

`advancing` does not import `compensating`. It declares the small
`Compensator` interface it needs (`advancing/service.go`) and
`services/orders/cmd/orders/main.go` passes the real service in.

### Out of stock at reservation

Inventory returns `FailedPrecondition` (insufficient stock) or `NotFound`
(unknown SKU). `Client.Reserve` in the orders inventory adapter maps those to
`domain.ErrRejected`. In `placing.cancelAfterReservationFailure` the order
goes `PENDING` to `CANCELLED` through the `InventoryRejected` trigger, with
`order.cancelled` queued, and the cancelled order is returned. The gateway
still answers `201`, with state `CANCELLED` and the reason. Nothing needs
releasing.

If the failure is technical (timeout, inventory down), the code calls
`Release` up to 3 times, 100 ms apart, and logs an error with the order id if
all attempts fail (ADR 30). It cancels the order the same way and returns an
error, which the gateway turns into `502`.

### A consumer fails: retry queues and the DLQ

`internal/platform/rabbitmq/rabbitmq.go`, `processDelivery`,
`chooseFailureQueue`, `republishFailedDelivery`, `Declare`.

For a queue `orders.q`, `Declare` creates `orders.q.retry.1`, `.retry.2`,
`.retry.3` (message TTL 2 s, 10 s, 30 s, `DefaultRetryDelays`) and
`orders.q.dlq`. A retry queue has no consumer: when the TTL expires,
RabbitMQ dead-letters the message through the default exchange back into
`orders.q`.

When the handler returns an error:

1. If the error wraps `rabbitmq.ErrPermanent`, or the message already had 3
   retries, it goes to `.dlq` with an `x-last-error` header.
2. Otherwise it is republished (with publisher confirm) to
   `.retry.<attempt+1>`; `x-retry-count` tracks the attempt.
3. Only after the republish is confirmed is the original acked. If the
   republish itself fails, the message is nacked and requeued.

Services mark an error permanent with their own `ErrPermanent`, converted in
each `adapters/rabbitmq/consumer.go`. Today that means an undecodable body
(and, in billing, an invoice that cannot be built). A panic in a handler is
recovered and treated as a normal failure (`handleRecoveringPanic`).

`gateway.sse` has no retry queues, so an error there goes straight to its DLQ.
Its handler returns `nil` on bad input, so in practice it does not fail.

Draining a DLQ is manual (RabbitMQ UI). Hands-on steps are in
[demo-guide.md](demo-guide.md).

## 4. Building blocks in `internal/platform/`

Shared infrastructure only. No business rules.

- `outbox`: the `Insert` function used inside a business transaction and the
  `Relay` that publishes unsent rows. Solves the dual write. Each service
  creates its own `outbox` table in its `001_init.sql`.
- `inbox`: `Once`, one function. Records `(consumer, message_id)` and runs
  the side effects in the same transaction, so duplicates are skipped and
  failures roll back together. Solves duplicate delivery.
- `rabbitmq`: a `Broker` that reconnects with backoff, the topic exchange, the publisher with
  confirms, queue declaration with retry queues and DLQ, and the consume loop
  with tracing, panic recovery and failure routing. Solves "every service
  would otherwise write this differently".
- `events`: event names, the single flat `Payload` struct and `Message`.
  The contract between services, and a compile-time one inside the monorepo.
- `telemetry`: JSON logging with `order_id`, `trace_id` and `span_id` added
  from the context, and OpenTelemetry setup. The outbox stores the
  `traceparent`, so the asynchronous publish continues the original trace.
- `config`: three helpers (`String`, `Bool`, `Duration`) that read an
  environment variable with a default.
- `database`: `Connect` (pool plus ping with retries, so a service can start
  before Postgres is ready) and `Migrate` (embedded SQL files, applied in name
  order, one transaction each, behind an advisory lock).
- `health`: `/healthz` server with pluggable checks, and the `healthcheck`
  sub-command used by Docker (`ProbeIfRequested`).

## 5. How a service is wired

Open `services/orders/cmd/orders/main.go`. It is the only place that creates
concrete types and connects them:

```go
store := postgres.NewStore(pool)
placingOrders := placing.NewService(store, inventoryClient)
advancingSaga := advancing.NewService(store, compensating.NewService(inventoryClient))
fetchingOrders := fetching.NewService(store)
```

`run` does, in this order: logger, tracing, Postgres (`database.Connect`,
`postgres.Migrate`), RabbitMQ (`NewBroker`, `NewResilientPublisher`, which do
not wait for the broker), the inventory gRPC client, the use cases, the health
endpoint, the gRPC server. Then an `errgroup` runs three things together: the
outbox relay, the RabbitMQ consumer, and the gRPC server. `broker.Consume`
reconnects on its own and only returns when the service shuts down, so a broker
outage does not stop the process. A real error elsewhere (for example the gRPC
server failing) cancels the context and the process exits; Docker restarts it.

The layers, from the inside:

| Layer | Path | Rule |
|---|---|---|
| Domain | `services/<x>/internal/domain` | Pure rules and types. No I/O, no imports of the other layers. Orders: the saga state machine. Inventory: `Plan`. Payments: `Decide`. Billing: `NewInvoice`. |
| Use cases | `services/<x>/internal/<gerund>` (`placing`, `advancing`, `compensating`, `fetching`, `reserving`, `releasing`, `listing`, `invoicing`, `charging`, `notifying`) | One package per use case. Orchestrate domain code and ports. Never import adapters or each other. |
| Adapters | `services/<x>/internal/adapters/{grpc,http,postgres,rabbitmq,...}` | Translate between the outside world and the use cases. Implement the ports. |
| Composition | `services/<x>/cmd/<x>/main.go` | Builds adapters, injects them into use cases, starts everything. |

Ports are interfaces declared by the code that uses them. `charging.Bank`,
`notifying.Sender` and `invoicing.Store` live in their own use-case packages,
because only that use case needs them. When several use cases share a port, it
goes in `domain`: in orders, `domain.Repository`, `domain.Tx` and
`domain.Inventory` (`services/orders/internal/domain/ports.go`). Adapters have
lines such as `var _ domain.Repository = (*Store)(nil)` so the compiler checks
the match.

Why it pays off: `services/orders/internal/orderstest` has an in-memory
repository and a fake inventory, so the saga and its failure paths are tested
without Docker (for example `advancing/handle_saga_event_test.go`). The
integration tests in `test/integration` cover what needs a real Postgres and
RabbitMQ. Reasoning and rules: ADR 2, ADR 26 and [conventions.md](conventions.md).

Two services differ a little. Notifications has no outbox and no relay (it
publishes nothing). The gateway has no database and no use-case package for
orders; `storefront` holds the types and the ports to other services, and
`timeline` holds the SSE hub.

## 6. Questions an interviewer may ask

**Why a transactional outbox?**
Because saving to Postgres and publishing to RabbitMQ are two systems; no
transaction spans both. If you publish after the commit and crash, the event
is lost. If you publish before and the commit fails, you announced something
that did not happen. With the outbox the event is a row in the same
transaction, and a relay publishes it afterwards. See `placing.recordPlacedOrder`
and `outbox.Relay.RunOnce`.

**Does that give exactly-once?**
No. Delivery is at-least-once. The relay can publish a row and crash before
marking it sent. RabbitMQ can redeliver after a consumer crash. A retry can
duplicate a message. What each consumer gets is exactly-once effects inside
its own database, because the inbox insert and the side effects share one
transaction. External effects (the bank, the notification channels) are not
covered by that: the fake bank is idempotent by key, notifications are
at-least-once.

**Why an orchestrated saga and not choreography?**
Orders holds the state machine in one table in one file, and every failure
path is unit-tested there. In choreography, each service would react to the
others' events and "why is this order stuck" becomes a question across
services. The price: orders must know the event vocabulary of billing and
payments (ADR 3).

**What does compensation do here?**
There is one compensating action: release the reserved stock when payment
fails. It runs in the `payment.failed` handler (`compensating.Compensate`).
Voiding a bank authorization is not a saga step; payments does it itself,
inside `charge`.

**Why is the stock reservation a synchronous gRPC call?**
The customer wants an immediate accept or reject, and prices come from
inventory. Row locks in inventory prevent overselling. The trade-off is
availability: if inventory is down, placing orders fails. The reservation and
the order commit are not atomic (two databases), which is why the order is
saved as `PENDING` first. Reserve and Release are idempotent per order id.

**What happens if RabbitMQ is down?**
Messaging pauses and everything else keeps working (ADR 28). Events are not
lost: they stay in each `outbox` table and the relay retries every 500 ms.
`Broker.Consume` reconnects with backoff (0.5 s up to 10 s), so the process
stays up and the orders gRPC server keeps answering. `/healthz` returns `503`
while the broker is down. An order placed during the outage is reserved,
waits at `INVENTORY_RESERVED`, and completes on its own once RabbitMQ is back.
`TestBrokerOutagePausesMessagingButKeepsOrdersServing` covers this by stopping
and restarting a real RabbitMQ container.

**What breaks with two gateway instances?**
Two things. The SSE hub is in memory, so each instance only knows what it
received. And both instances would consume from the same durable queue,
`gateway.sse`, so RabbitMQ would split the events between them: a browser
connected to instance A would miss events delivered to B. Fixing it needs one
queue per instance and sticky routing, or a shared pub/sub layer
([sse.md](sse.md), ADR 18). Also, the hub keeps only the last 500 orders and
is lost on restart.

**What stops a user from reading someone else's order?**
`ownedOrder` in `order_handlers.go` compares the order's user with the cookie's
user and answers 404 otherwise. The SSE endpoint uses the same check before
subscribing. Authentication itself is simulated.

**How do you handle duplicates and out-of-order events?**
Duplicates: inbox per consumer, plus natural keys (one invoice per order,
reservation per order id, payment per order id), plus message-id dedup in the
SSE hub. Out of order: `orders` and `payments` both consume `invoice.generated`, so
`payment.*` can reach orders before the invoice does. The transition table
accepts payment events from `INVENTORY_RESERVED` too, and a late
`invoice.generated` on a finished order is ignored (ADR 27).
`TestSagaReachesTheSameOutcomeForEveryArrivalOrderOfInvoiceAndPayment` covers
every ordering. Any other transition that is not in the table is logged and
ignored.

**Where can the saga get stuck?** (known limitations, from reading the code)

- `payment.pending_manual` leaves the order in `INVOICED` forever. No expiry,
  no pay-now flow (ADR 24).
- Compensation holds a database transaction open during a gRPC call (ADR 20).
- The payment failure switch is exposed as `PUT /api/admin/failure` to any
  logged-in user. Fine for a demo.

**What else would you change for production?**
A retention job for the `inbox`
and `outbox` tables (both grow without bound). Real authentication. A
relay woken by `LISTEN/NOTIFY` or CDC instead of polling. Exponential backoff
per message instead of three fixed tiers. A recovery sweeper for stuck sagas.
Roles per service in Postgres instead of one shared user.

**Why Go-specific choices: `internal/`, interfaces where used?**
`internal/` makes the compiler refuse imports of one service's code by
another, which matters in a monorepo. Interfaces are declared by the consumer
(Go's usual rule), so the use case states what it needs and any adapter that
fits can be injected.

## 7. Suggested reading order

A first pass in about 15 files. Each one is short.

1. `README.md`: the diagrams and the state machine.
2. `internal/platform/events/events.go`: the vocabulary.
3. `services/gateway/internal/adapters/http/order_handlers.go`: where an order enters.
4. `services/orders/internal/domain/order.go`: the saga as a table.
5. `services/orders/internal/placing/place_order.go`: two transactions and the reservation call.
6. `services/orders/internal/adapters/postgres/store.go`: how `InTx`, `Once` and `Outbox` are implemented.
7. `internal/platform/outbox/outbox.go`: insert and relay.
8. `internal/platform/inbox/inbox.go`: 36 lines, read all of it.
9. `internal/platform/rabbitmq/rabbitmq.go`: publisher, queue topology, retry and DLQ.
10. `services/inventory/internal/reserving/reserve_stock.go` and `internal/domain/stock.go`.
11. `services/billing/internal/invoicing/generate_invoice.go`.
12. `services/payments/internal/charging/handle_invoice.go` and `charge.go`.
13. `services/orders/internal/advancing/handle_saga_event.go` and `compensating/compensate.go`.
14. `services/gateway/internal/timeline/hub.go` and `adapters/http/stream_events.go`.
15. `services/orders/cmd/orders/main.go`: how it all connects.

Then run `make up`, place an order as Alice, and follow it in Jaeger and in the
RabbitMQ UI ([demo-guide.md](demo-guide.md), [observability.md](observability.md)).
