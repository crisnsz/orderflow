# Start here

This is the entry point. It tells you what to install, what to run, where to
click, what each tool is for, and why the system is built this way. It does not
walk through the Go code. When you are ready for that, the last step is
[walkthrough.md](walkthrough.md).

Everything below was run against the real stack. Where a time or a count is
given, it was measured, not guessed.

- [1. What this project is](#1-what-this-project-is)
- [2. The programs and what each is for](#2-the-programs-and-what-each-is-for)
- [3. First run](#3-first-run)
- [4. A guided session](#4-a-guided-session)
- [5. Logs, traces and metrics](#5-logs-traces-and-metrics)
- [6. Project structure](#6-project-structure)
- [7. Architecture and the reasons behind it](#7-architecture-and-the-reasons-behind-it)
- [8. How quality is checked](#8-how-quality-is-checked)
- [9. Suggested path to learn it](#9-suggested-path-to-learn-it)

## 1. What this project is

- A customer orders products in an online shop. The page shows the order moving
  live: stock reserved, invoice generated, card charged, customer notified.
- Each of those steps belongs to its own service: `orders`, `inventory`,
  `billing`, `payments`, `notifications`. A `gateway` serves the web page.
- Services do not call each other directly, except one call: `orders` asks
  `inventory` to reserve stock over gRPC. Everything else is a message through
  RabbitMQ.
- If a later step fails (the card is declined), the system undoes the earlier
  ones. This is called a **saga with compensation**: the stock is released and
  the order ends `CANCELLED`.
- The bank, the login and the email/SMS/push senders are simulated. The
  database transactions, the broker, retries and tracing are real. Prices are in
  EUR cents internally (8900 means 89.00 EUR).

Why split it at all? A single program would be simpler. The project exists to
show how a distributed system handles the hard parts: a step fails halfway, a
message arrives twice, a service is down, a broker is down. Each of these has
a concrete scenario in [section 4](#4-a-guided-session).

## 2. The programs and what each is for

| Program | What it is | Why this project uses it | Configured in | How to open or see it |
|---|---|---|---|---|
| Go 1.26 | The programming language | All six services and the tests are Go | `go.mod` | `go version`. Only needed to run tests and lint, not to run the stack |
| Docker and Docker Compose | Runs programs in isolated containers; Compose starts many together | One command starts nine containers: six services plus Postgres, RabbitMQ and Jaeger | `docker-compose.yml`, `Dockerfile` | `docker compose ps`, `docker compose logs <service>` |
| PostgreSQL 17 | Relational database | One server, one database per service, so services do not share tables | `docker-compose.yml`, `docker/postgres/init.sh` | Not published to the host. Inside the network only |
| RabbitMQ 4.3 | Message broker: services publish events, others receive them | Lets services work without calling each other. Holds messages while a service is down | `docker-compose.yml`, plugins in `docker/rabbitmq/enabled_plugins` (shovel, for **Move messages**), topology declared in `internal/platform/rabbitmq` | UI at http://localhost:15672, user `orderflow`, password `orderflow-demo-password` (a demo value from the repo docs) |
| gRPC and protobuf | Typed remote calls between services; `.proto` files define them | The one synchronous call: `orders` to `inventory` (`Reserve`, `Release`), plus gateway to orders and inventory | `proto/`, generated code in `gen/` | No UI. Calls show as spans in Jaeger |
| buf and protoc plugins | `buf` lints `.proto` files and runs the Go code generators `protoc-gen-go` and `protoc-gen-go-grpc` | Turns `.proto` files into the Go code in `gen/`, and CI checks that `gen/` is up to date | `buf.yaml`, `buf.gen.yaml` | `make proto` |
| OpenTelemetry | A standard for emitting traces from code | Each service records spans and passes a trace id along HTTP, gRPC and RabbitMQ messages | `internal/platform/telemetry/trace.go` | Nothing to open. It sends data to Jaeger |
| Jaeger 1.76 | A web app that stores and draws traces | Shows one order's whole path across six services in one view | `docker-compose.yml` (service `jaeger`) | http://localhost:16686 |
| slog JSON logs | Go's standard logger, writing one JSON object per line | Every line carries `service`, `order_id`, `trace_id` so one order can be filtered out of all services | `internal/platform/telemetry/log.go` | `docker compose logs <service>` |
| golangci-lint | Runs many Go code checkers at once | Keeps the code consistent. Includes `sloglint`, which flags log calls without a context | `.golangci.yml` | `make lint` |
| testcontainers | A Go library that starts real Postgres and RabbitMQ in Docker during tests | Integration tests run against the real databases and broker, not mocks | `test/containers/` | `make test-integration` |
| GitHub Actions | CI: runs checks on every push and pull request | Runs lint, tests, integration tests, proto check, and a full compose smoke test | `.github/workflows/ci.yml` | The Actions tab on GitHub |
| Make | A task runner. A `Makefile` is a list of named commands | One short name per task | `Makefile` | `make up`, `make test`, and so on |

Also in the repo: `scripts/smoke.sh`, an end-to-end test written with `curl`
and `jq`. It needs `jq` installed.

## 3. First run

**Install**

- Docker with Compose v2 (`docker compose version` must work).
- `jq`, only for the log recipes and `make smoke`.
- Go 1.26 and `make tools` (installs golangci-lint), only if you want to run
  tests and lint on your machine. `buf` and the protoc plugins only if you
  change `.proto` files.

**Start**

```sh
make up
```

This is `docker compose up --build -d --wait`. The first run builds six images
and can take a few minutes. The command returns only when every service reports
healthy. If it returns without an error, the stack is ready.

**Check**

```sh
docker compose ps
```

Every service should say `healthy`. Jaeger has no health check and says only
`Up`.

**Open these three tabs**

| What | URL | Login |
|---|---|---|
| The shop page | http://localhost:8080 | pick a user, no password |
| Jaeger (traces) | http://localhost:16686 | none |
| RabbitMQ UI | http://localhost:15672 | `orderflow` / `orderflow-demo-password` |

**Stop**

```sh
make down
```

This removes the containers and the data volume, so the next `make up` starts
from the original stock (KB-01: 10, MS-01: 25, HUB-01: 3, MON-01: 2).
`docker compose stop` would keep the data.

## 4. A guided session

Open the three tabs from section 3. Keep a terminal in the repo folder.

Three small things to know before you start:

- The **stock table** refreshes when you place an order and again when the
  order you are following completes or is cancelled.
- The **state chip** next to "4. Live timeline" follows the saga:
  `INVENTORY_RESERVED`, `INVOICED`, `PAID`, then `COMPLETED`, or
  `COMPENSATING` then `CANCELLED`. It only moves forward, so a late event never
  takes it back.
- The whole happy path takes about three seconds. That is not slowness in the
  code. The outbox relay (section 7) polls every 500 ms and each hop waits for
  one poll.

### How to find an order in the three tools

Every scenario below ends with the same three lookups. Learn them once.

**Jaeger.** In the search form pick Service `gateway`, Operation
`POST /api/orders`, press **Find Traces**. The newest trace is first. Click it.
You see a list of bars, one per span, indented by who called whom. The service
name is on the left of each bar. A normal order trace has 28 spans across six
services.

The span names are:

| Span name | Meaning |
|---|---|
| `POST /api/orders` | The HTTP request in the gateway (the root) |
| `orders.v1.OrdersService/PlaceOrder` | gRPC call, a client span in gateway and a server span in orders |
| `inventory.v1.InventoryService/Reserve` and `/Release` | gRPC calls to inventory |
| `outbox.relay <event>` | The relay picking an event out of the outbox table |
| `publish <event>` | Sending the event to RabbitMQ |
| `consume <event>` | A service handling the event it received |

**RabbitMQ UI.** Click the **Queues** tab. Each row has Ready (waiting),
Unacked (being handled) and Total, plus rates. For a finished order every queue
is back at 0, so the useful number is the message rate or, better, the
**Exchanges** tab, then `orderflow.events`, then **Bindings**, which shows which
queue listens to which event. Messages pass through too fast to see on a
healthy system. The retry scenarios below make them visible.

**Logs.** Copy the full order id (the page shows 8 characters in "Your orders";
the line under "4. Live timeline" shows the full id once you click an order).
Then:

```sh
ORDER=<full order id>
docker compose logs --no-log-prefix \
  | jq -R -c --arg o "$ORDER" 'fromjson? | select(.order_id == $o)' \
  | jq -s -r 'sort_by(.time)[] | "\(.time[11:23]) \(.service) \(.level) \(.msg)"'
```

For one service only: `docker compose logs payments`, and add the same filter.
The `trace_id` field in any of those lines can be pasted into
`http://localhost:16686/trace/<trace_id>` to open that trace directly.

### Scenario 1: Alice, happy path

**Clicks**

1. In "1. Pick a demo user", press **Log in as Alice**.
2. In "3. Place an order", type `1` in the Qty box of Mechanical keyboard (KB-01).
3. Press **Place order**. The line next to the button reads
   `Order <8 chars> is INVENTORY_RESERVED`.

**Timeline** (each entry has a label, the event name in small text and a time):
Order placed, Stock reserved, order confirmed, Invoice generated, Payment
succeeded, Order completed. The chip turns `COMPLETED`. The keyboard stock is
one lower.

**Jaeger.** One trace, 28 spans, six services (gateway, orders, inventory,
billing, payments, notifications). Read it top to bottom: the HTTP request, the
two gRPC calls, then a repeating pattern of `outbox.relay`, `publish`, and
several `consume` spans in parallel for each event. There are no spans for the
database or the fake bank.

**RabbitMQ.** Nothing stays in any queue. In the **Queues** tab the totals of
`orders.q` and `gateway.sse` count up over time; `gateway.sse` receives every
event, so it counts the most.

**Logs.** About 25 lines for the order, in this order: `orders order placed`,
`orders inventory reserved; order confirmed`, `billing invoice generated`,
`payments fake bank: authorized`, `payments fake bank: sale captured`,
`payments payment succeeded`, `orders saga advanced`, `orders order completed`.
You also see `gateway event forwarded to sse hub` and `outbox event published`
repeatedly, and one `notification sent (simulated)` per event for Alice's email.

### Scenario 2: Bob, no autopay

**Clicks.** Press **log out** at the top. Press **Log in as Bob**. Order one
Wireless mouse (MS-01). Press **Place order**.

**Timeline.** Order placed, Stock reserved, Invoice generated, then
**Waiting for manual payment (no autopay consent)**. It stops. There is no
Order completed. In "2. Your account", Bob's card shows `autopay OFF`.

The chip stops at `INVOICED`. The API says the same (`state: INVOICED`).

**Jaeger.** Same shape as scenario 1 but the trace ends with
`consume payment.pending_manual` in orders and notifications. No
`payment.succeeded`, no `order.completed`.

**RabbitMQ.** Nothing stuck. The order is not in a queue. It is waiting in the
database, with nobody going to move it: this is a known gap
([ADR 24](../DECISIONS.md)).

**Logs.** `payments payment awaits manual action`, then
`orders payment requires manual action; order stays invoiced`. Notifications
log `sms` for Bob.

### Scenario 3: payment fails, the saga compensates

This is the one to rehearse for an interview.

**Clicks**

1. Log in as Carol.
2. In "Payment failure switch", choose **sale (capture fails, authorization
   voided)** and press **Apply**. The text next to the button reads `Applied.`
3. Order one USB-C hub (HUB-01). Note the stock before.
4. After the timeline ends, set the switch back to **off (payments succeed)**
   and press **Apply**. The switch is global, so it affects every user until
   you do. The hub stock is back to what it was.

**Timeline.** Order placed, Stock reserved, Invoice generated, **Payment
failed** (detail: `reason: sale: declined by bank: capture_failed (simulated
failure)`), **Order cancelled**. The chip goes
`COMPENSATING`, then `CANCELLED`.

**Jaeger.** About 30 spans. After `consume invoice.generated` in payments you
see `outbox.relay payment.failed`, `publish payment.failed`, and
`consume payment.failed` in orders. Under it, one more gRPC call:
`inventory.v1.InventoryService/Release`. That is the compensation. Then
`order.cancelled` is published and consumed by notifications and the gateway.

**RabbitMQ.** Nothing stays in a queue. The binding table shows `orders.q`
listens to `payment.failed`, which is how the failure reaches the orchestrator.

**Logs.** Look for the three WARN lines in payments: `fake bank: sale failed
(forced failure)`, `fake bank: authorization voided`, `payment failed`. Then
in orders: `payment failed; compensating`, and `order cancelled after
compensation`. Inventory logs `stock released`. The void has no event or span;
logs are the only place it appears. Carol gets email, SMS and push for every
event.

With the switch on **auth**, the bank declines at authorization. The reason is
`authorize: declined by bank: do_not_honor (simulated failure)`. There is no
`authorization voided` line, because nothing was held.

### Scenario 4: out of stock

**Clicks.** As anyone, type `5` for Ultrawide monitor (MON-01), which has
2 in stock. Press **Place order**. The line next to the button reads
`Order <8 chars> is CANCELLED: inventory rejected the reservation:
insufficient stock: MON-01 requested 5, available 2`.

**Timeline.** Order placed, then **Order cancelled**. Nothing had been
reserved, so nothing is released. The chip is `CANCELLED`.

The HTTP answer is `201 Created` even though the order is cancelled
([ADR 31](../DECISIONS.md)): the order exists, and its outcome is the state.

**Jaeger.** A short trace: the HTTP request, `PlaceOrder`, one
`inventory.v1.InventoryService/Reserve`, then the publish and consume of
`order.placed` and `order.cancelled`. No billing, no payments, no `Release`.

**RabbitMQ.** `billing.q` and `payments.q` receive nothing for this order.

**Logs.** `orders order placed`, then `orders order cancelled at reservation`.
Billing and payments have no lines for this order id.

### Scenario 5: a service is stopped, the message is retried

The goal is to make the compensation fail for a technical reason: payments
declines the card, orders tries to release the stock, and inventory is down.
The order must be placed while inventory is still up, because placing needs it.
Pausing payments first gives you all the time you need.

1. In the page, log in as Carol, set the switch to **sale** and press
   **Apply** (scenario 3 steps 1 and 2).
2. In the terminal: `docker compose pause payments`
3. In the page, order one Wireless mouse (MS-01). The timeline stops at
   **Invoice generated**: the invoice event is waiting in `payments.q`.
4. In the terminal:

   ```sh
   docker compose stop inventory
   docker compose unpause payments
   ```

What happens next:

- payments declines the card and publishes `payment.failed`.
- orders tries to release the stock, the gRPC call to inventory fails, and the
  database transaction rolls back.
- RabbitMQ keeps the message inside `orders.q` and hands it back after a delay
  that doubles on each failure: 1 s, 2 s, 4 s, then 8 s twice. The handler runs
  up to 6 times. Each run also waits for the gRPC timeout while inventory is
  down.
- The order stays `INVOICED` the whole time.

**RabbitMQ.** There are no retry queues. In **Queues**, watch `orders.q` (one
message, which flips between Ready and Unacked) and `orders.q.dlq` (0 until the
retries run out). Tip: set the table to refresh every 5 seconds (top right of
the page). Measured on this stack: the first failure came about 5 s after
unpausing payments, the retries were scheduled 1, 2, 4, 8 and 8 s apart, and
the message reached `orders.q.dlq` 33 s after the first failure.

**Logs.** `docker compose logs orders` shows, per attempt, `payment failed;
compensating` followed by `message handling failed; retry scheduled` with the
fields `attempt`, `retry_in` and `error`.

Now choose:

- **Recover.** Run `docker compose start inventory` within about 30 seconds of
  stopping it. The next retry succeeds, the log shows `order cancelled after
  compensation`, and the order ends `CANCELLED`. The delay is the backoff
  delay, not slowness.
- **Let it die.** Leave inventory down for about 40 seconds. After the sixth
  failure orders logs `message moved to DLQ` at ERROR level and the message
  sits in `orders.q.dlq`. In the RabbitMQ UI open **Queues**, click
  `orders.q.dlq`, open **Get messages**, press **Get Message(s)**. You see the
  payload and the headers `x-last-error` (why it failed, for example
  `release stock: rpc error: code = Unavailable ...`), `x-delivery-count` (6),
  and `x-first-death-reason: delivery_limit`, which says RabbitMQ gave up after
  the retries.
  The order stays `INVOICED` until a person sends the message back. Start
  inventory, then use the **Move messages** section of the `orders.q.dlq` page
  with destination `orders.q`. The order ends `CANCELLED` a few seconds later.

Finally set the switch back to **off** in the page.

**Jaeger.** Take the `trace_id` from the orders log lines and open it. The
retries keep the trace context, so they appear in the same trace: in a run with
six failed attempts and one replay, the trace had nine `consume
payment.failed` spans (seven in orders, one each in notifications and the
gateway) and eight `inventory.v1.InventoryService/Release` calls.

One detail: after inventory restarts, orders may need one or two more failed
attempts because its gRPC connection still points at the old container
address, and those attempts log `no route to host`.

Business failure (bank said no) goes through compensation. Technical failure
(service down) goes through retries. Same order, two mechanisms.

### Scenario 6: RabbitMQ is stopped

```sh
docker compose stop rabbitmq
```

Now place an order in the page.

**What you see.** The order is accepted: the button line reads
`Order <8 chars> is INVENTORY_RESERVED`. The stock drops. The timeline stays
**empty**, because the timeline is fed by RabbitMQ and the broker is away. The
order stays `INVENTORY_RESERVED`. The gateway and orders keep answering HTTP
and gRPC. They do not crash.

**Logs.** `docker compose logs orders` shows `ERROR outbox relay failed`
(about every 500 ms) and `WARN consumer stopped; reconnecting`. The event is
safe: it is a row in the `outbox` table, marked unsent.

**RabbitMQ and Jaeger.** The UI at port 15672 does not answer, since it is
the broker. Jaeger shows the HTTP and gRPC part of the order, but no
`publish` or `consume` spans yet.

Now start it again:

```sh
docker compose start rabbitmq
```

**Within about 12 seconds** the order moved on its own: `INVOICED`, then
`COMPLETED`. The unsent outbox rows were published, billing and payments
processed them, and the order finished. To see the timeline, click the order
in "Your orders": the gateway replays the events so far.

Docker marks a service `unhealthy` only after twelve failed health checks in a
row (about one minute), so a short outage may never flip it. A restart does not
happen in either case ([ADR 28](../DECISIONS.md)).

This is the reason for the transactional outbox: the order and its event are
written together, so a broker outage delays events but does not lose them.

### Run all of it unattended

`make smoke` runs scenarios 1 to 4 against the running stack and prints
PASS or FAIL. Scenarios 5 and 6 are not scripted.

## 5. Logs, traces and metrics

They answer different questions. A way to remember them:

| | Question it answers | Shape | Here |
|---|---|---|---|
| **Log** | What did this one service say happened? | A line of text with a time | `docker compose logs` |
| **Trace** | Where did this one request travel, and how long did each hop take? | A tree of timed steps across services | Jaeger |
| **Metric** | How is the system doing overall? | A number over time (rate, count, duration) | Not in this project |

**A log line here** looks like this (shortened):

```json
{"time":"2026-11-04T03:27:39.986Z","level":"WARN","msg":"payment failed",
 "service":"payments","order_id":"7e8b4124-...","trace_id":"8206ac6b...","span_id":"..."}
```

It is one service's statement about one moment. Good for the exact error text
and for facts that are not spans, such as `fake bank: authorization voided`.
Weak at showing how six services' lines connect, unless you filter by
`order_id`, which is why every line carries it.

**A trace here** is what Jaeger shows when you open the order from scenario 1:
28 bars in a tree. Nobody wrote that tree by hand. Each service records its own
spans and passes a trace id along with every call and every RabbitMQ message,
so Jaeger can join the pieces. Good for "which service did this order visit,
in what order, and where did the time go". Weak at the exact error text, and
not every step has a span (the database and the fake bank do not).

The page you got lost on, "the platform where you see the tracking", is Jaeger.
It is not a log viewer. It draws the path of one request.

**Metrics** are things like "orders per minute", "queue depth", "p95 latency".
They need a metrics store and dashboards (Prometheus and Grafana, for example).
This project has none. The only counters you can see are RabbitMQ's own queue
numbers in its UI. [observability.md](observability.md) lists what you would
add for production.

They link together: a log line has a `trace_id`, and that id opens the trace.
Details and more recipes in [observability.md](observability.md).

## 6. Project structure

```text
orderflow/
├── services/            one folder per service
│   ├── gateway/         HTTP API, demo page, live timeline (SSE)
│   ├── orders/          owns orders and the saga
│   ├── inventory/       products, stock, Reserve and Release
│   ├── billing/         creates invoices
│   ├── payments/        autopay decision, fake bank, failure switch
│   └── notifications/   email, SMS and push (log lines only)
├── internal/platform/   code shared by services, no business rules
├── proto/               .proto files (gRPC contracts)
├── gen/                 Go code generated from proto/, committed
├── test/                testcontainers helpers and integration tests
├── scripts/smoke.sh     end-to-end test with curl
├── docker/              Postgres init script (creates the databases)
├── docs/                these guides
├── DECISIONS.md         the ADRs: why each choice was made
├── docker-compose.yml   the whole stack
├── Dockerfile           one file builds every service (build arg SERVICE)
├── Makefile             short names for common commands
├── .golangci.yml        lint rules
├── buf.yaml, buf.gen.yaml   protobuf lint and code generation
└── .github/workflows/ci.yml   CI
```

`internal/platform/` holds the reusable parts: `outbox`, `inbox`, `rabbitmq`
(publisher, consumer, queue and DLQ topology, retry policy), `telemetry` (logs and traces),
`events` (the event vocabulary), `database`, `config`, `health`.

One service expanded, `services/orders/`:

```text
services/orders/
├── cmd/orders/main.go        wires everything together and starts it
└── internal/
    ├── domain/               the saga state machine and the ports (interfaces)
    ├── placing/              use case: place an order
    ├── advancing/            use case: move the saga on an incoming event
    ├── compensating/         use case: undo the reservation
    ├── fetching/             use case: read orders
    ├── orderstest/           in-memory fakes used by unit tests
    └── adapters/
        ├── grpc/             the gRPC server the gateway calls
        ├── inventory/        gRPC client to inventory
        ├── postgres/         storage, migrations
        └── rabbitmq/         the event consumer
```

The pattern repeats in every service. Business rules sit in the middle and know
nothing about databases or the network. Adapters sit around them.

## 7. Architecture and the reasons behind it

The numbers link to the ADR in [DECISIONS.md](../DECISIONS.md), which has the
alternatives that were rejected.

**Hexagonal layout per service (ADR 2, 26).** The domain and the use cases
define what they need as interfaces (ports). Postgres, RabbitMQ and gRPC
implement them as adapters. Reason: the business logic can be unit-tested with
in-memory fakes, and a technology can change without touching the rules. Each
use case has its own package named after what it does (`placing`, `charging`).

**Orchestrated saga (ADR 3).** Placing an order touches several services and
there is no distributed transaction. `orders` keeps the state machine
(`PENDING`, `INVENTORY_RESERVED`, `INVOICED`, `PAID`, `COMPLETED`, with
`COMPENSATING` and `CANCELLED` on failure) and decides what happens next.
Reason: the whole flow and all failure paths live in one place and one table of
transitions, instead of being spread over six services reacting to each other.
The cost is that `orders` knows the other services' events.

**Synchronous gRPC for stock, async events for the rest (ADR 4).** Reserving
stock is a request that needs an answer now: the customer must not get a
confirmed order for something out of stock. Invoicing, charging and notifying
can happen a moment later, so they are events. Reason: use the simplest tool
that fits each step. The cost: placing an order depends on inventory being up.

**Transactional outbox (ADR 5).** A service that changes its database and then
publishes to RabbitMQ can crash between the two. Instead, the event is written
to an `outbox` table in the same database transaction as the change, and a
relay publishes it afterwards. Reason: no lost events and no events for changes
that rolled back. The cost: delivery is at least once, and a polling relay adds
up to 500 ms delay per hop (this is why the happy path takes seconds).

**Inbox idempotency (ADR 6).** Because delivery is at least once, a service can
receive the same message twice. Each consumer records the message id in an
`inbox` table in the same transaction as its work. A duplicate finds the id and
is skipped. Reason: processing twice would invoice or charge twice.

**Delayed retry and DLQ (ADR 8, 33).** When a handler fails, the consumer
returns the message to its own queue with a delay that doubles each time (1, 2,
4, 8 s). After the sixth failure, or at once on an error marked permanent, it
goes to a dead-letter queue (`.dlq`) for a human to inspect. Reason: temporary
failures heal themselves without a hot retry loop, a poison message cannot block
the queue forever, and each consumer needs only one queue plus its DLQ. You watched this in scenario 5.

**SSE for the live timeline (ADR 10, 18).** The browser opens
`GET /api/orders/{id}/events` and the server pushes each event as it arrives.
Reason: the traffic is one-way, SSE needs no library and reconnects by itself,
and it is easy to test with `curl -N`. WebSocket would add two-way capability
nobody uses. The gateway keeps the last 500 orders in memory, so the timeline
is lost if the gateway restarts. See [sse.md](sse.md).

**Broker-outage resilience (ADR 28).** If RabbitMQ goes away, services keep
running: consumers reconnect with backoff, the outbox relay keeps trying, and
HTTP and gRPC keep answering. Reason: a messaging problem should delay work,
not take the shop down. You saw it in scenario 6.

**Limits worth saying out loud in an interview.** Compensation calls inventory
inside a database transaction (ADR 20), which is fine here and wrong at high
volume. A failed void after a declined sale is only logged (ADR 29). Manual
payment is a dead end (ADR 24). Tracing samples everything and logs live only
in container output. Full list in the README under "Trade-offs and known gaps".

## 8. How quality is checked

| Check | Command | What it proves | Needs |
|---|---|---|---|
| Unit tests | `make test` (runs `go vet` and `go test -race`) | Business rules work: saga transitions including compensation and invalid events, reservation rules, autopay decision, tax maths, notification planning, sessions, the SSE hub. The race detector catches unsafe concurrent access | Go only |
| Integration tests | `make test-integration` | The parts that mocks cannot prove, against real Postgres and RabbitMQ started by testcontainers: the outbox publishes each row once even with two relays, the inbox drops duplicates, retries and the DLQ work, the saga persists, concurrent reservations never oversell, a broker outage pauses and resumes | Docker. Slow (minutes) |
| Smoke test | `make up` then `make smoke` | The built images work together: login, order completes, no-consent order waits, failure switch cancels and releases stock, oversized order is cancelled | Running stack, `jq` |
| Lint | `make lint` | Style and common mistakes, including that log calls carry a context so they get `order_id` and `trace_id` | `make tools` once |
| Protobuf | `make proto` | `.proto` files pass lint and `gen/` matches them | `buf` and the plugins |

CI (`.github/workflows/ci.yml`) runs five jobs on every push to `main` and on
every pull request:

| Job | Runs |
|---|---|
| `lint` | `golangci-lint run ./...` |
| `test` | `go vet` and `go test -race ./...` |
| `integration` | the testcontainers tests |
| `proto` | `buf lint`, `buf generate`, then fails if `gen/` changed |
| `compose` | builds all images, starts the stack, runs the smoke test |

Not covered by any automated test: the browser page itself, a broker outage
across the whole compose stack (the integration test covers `orders` only),
and load behaviour.

## 9. Suggested path to learn it

**Sitting 1: see it work (about an hour).** Read section 1 and 2 here. Run
`make up` and do scenarios 1 to 4 in section 4, with Jaeger and the logs open.
Goal: be able to say what happens when you press Place order.

**Sitting 2: see it break (about an hour).** Do scenarios 5 and 6. Read
section 5 and 7 here, then [observability.md](observability.md) and
[sse.md](sse.md). Goal: explain why a stopped service does not lose an order.

**Sitting 3: the reasons.** Read the README diagrams (architecture, event flow,
saga state machine), then [DECISIONS.md](../DECISIONS.md) ADRs 2 to 8, 20 and
28. Goal: for each pattern, state the problem it solves and its cost.

**Sitting 4: the demo detail.** Read [demo-guide.md](demo-guide.md), especially
the RabbitMQ part (section 4), and run `make test` and `make test-integration`.

**Sitting 5 and after: the code.** Read [conventions.md](conventions.md), then
[walkthrough.md](walkthrough.md). It follows one order file by file and ends
with a list of questions an interviewer may ask, with answers.

For the interview, the strongest single story is scenario 3 plus scenario 5:
the bank refuses, the saga undoes the reservation, and when the undo itself
fails, retries and the DLQ keep the work from being lost.
