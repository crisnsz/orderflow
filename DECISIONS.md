# Decisions

A lightweight ADR log. Each entry: context, decision, consequences, alternatives.
Entries are never rewritten; a changed decision gets a new entry that supersedes the old one.

| # | Decision |
|---|----------|
| 1 | Monorepo, single Go module |
| 2 | Hexagonal layout per service |
| 3 | Orchestrated saga, owned by `orders` |
| 4 | Synchronous stock reservation over gRPC |
| 5 | Transactional outbox for publishing |
| 6 | Inbox table for consumer idempotency |
| 7 | Retry with backoff through TTL queues, then a DLQ |
| 8 | One topic exchange, one durable queue per consumer |
| 9 | Event envelope in AMQP properties, flat JSON payload |
| 10 | SSE (not WebSocket) for the live timeline |
| 11 | Simulated authentication (signed cookie, seeded users) |
| 12 | Own minimal migration runner with embedded SQL |
| 13 | One Postgres instance, one database per service |
| 14 | Notification channels only write logs |
| 15 | Fake bank with Auth, Sale and Void semantics and a failure switch |
| 16 | Generated protobuf code is committed |
| 17 | Logging and tracing design |
| 18 | Gateway keeps an in-memory timeline per order |
| 19 | Pricing authority is inventory; invoice extracts tax from inclusive prices |
| 20 | Compensation calls inventory inside the consumer transaction |
| 21 | Integration tests behind a build tag, testcontainers-go |
| 22 | Pinning policy and tooling without Homebrew |
| 23 | Consumers do not reconnect; the container restarts them |
| 24 | Manual payment is not implemented (`payment.pending_manual` is terminal for the demo) |
| 25 | Payments owns cards, notifications owns channel preferences |

---

## 1. Monorepo, single Go module

**Context.** Six services share a message contract, small infrastructure helpers and generated gRPC code. The project is a portfolio piece that one person builds and reviews.

**Decision.** One repository, one `go.mod` (`github.com/crisnsz/orderflow`). Layout: `proto/`, `gen/`, `services/<name>/`, `internal/platform/` (shared infrastructure only), `test/`.

**Consequences.** Atomic refactors across services, one dependency set, one CI. Services can import each other's code by accident; this is prevented by Go's `internal/` rule (each service keeps everything under `services/<name>/internal`) and by keeping `internal/platform/` small (and, being under the root `internal/`, it cannot be imported from outside this module either). Services cannot be versioned or deployed with different dependency versions; acceptable here.

**Alternatives.** One module per service with a `go.work` (more ceremony, little gain for six services); one repo per service (hard to review and demo).

## 2. Hexagonal layout per service

**Context.** The author wants testable business rules and replaceable infrastructure.

**Decision.** Each service has `internal/domain` (pure, no I/O, no imports from the other layers), use-case packages such as `placing` or `charging` (use cases and the *ports* they need, as interfaces; see decision 26), `internal/adapters/*` (gRPC, Postgres, RabbitMQ, HTTP, bank, channels) and `cmd/<name>/main.go` as the composition root.

**Consequences.** Domain and use-case tests run in microseconds with in-memory fakes (see `services/orders/internal/advancing/handle_saga_event_test.go`). Adapters are thin and covered by integration tests. Some boilerplate (port interface plus adapter plus fake) for small services such as billing.

**Alternatives.** A flat package per service (faster to write, but I/O ends up mixed with rules); a shared `domain` package (couples services).

## 3. Orchestrated saga, owned by `orders`

**Context.** An order spans stock, invoice and payment. Failure after stock is reserved must release it. Two styles exist: choreography (each service reacts to events and emits new ones) and orchestration (one component holds the state machine and decides the next step).

**Decision.** `orders` orchestrates. Its state machine (`PENDING → INVENTORY_RESERVED → INVOICED → PAID → COMPLETED`, with `COMPENSATING → CANCELLED` on failure) is a pure function in `services/orders/internal/domain`, persisted as `orders.state` plus an append-only `saga_log`. Billing and payments stay simple event reactors; they do not know the saga exists.

**Consequences.** The whole flow and every failure path are in one table that is unit-tested exhaustively. `orders` is a coordinator and knows the event vocabulary of the others. Choreography would spread that knowledge across services and make "why is this order stuck" a distributed puzzle.

**Alternatives.** Choreography (less coupling to a coordinator, harder to reason about compensation); a workflow engine such as Temporal (the right tool in production, but hides exactly the mechanics this project wants to show).

## 4. Synchronous stock reservation over gRPC

**Context.** An order must not be confirmed if stock is unavailable, and the customer wants an immediate answer.

**Decision.** `PlaceOrder` calls `inventory.Reserve` synchronously (gRPC, 5 s timeout) before the order is confirmed. Reserve and Release are idempotent by order id, so retries are safe. A business rejection (insufficient stock, unknown SKU) cancels the order and returns it as `CANCELLED`; a technical failure also cancels it and returns an error.

**Consequences.** Instant feedback and no oversell (row locks, see integration test `TestConcurrentReservationsNeverOversell`). `orders` depends on inventory availability for placing orders. The reservation and the order commit are not atomic (two databases); the order is persisted first as `PENDING` so a crash leaves a visible, cancellable order rather than a lost reservation.

**Alternatives.** Fully asynchronous reservation through events (more resilient, but the customer waits and the API needs polling).

## 5. Transactional outbox for publishing

**Context.** "Update the database and publish an event" is a dual write: either can fail alone, leaving the system inconsistent.

**Decision.** Services insert events into an `outbox` table in the **same transaction** as the state change (`internal/platform/outbox`). A relay goroutine polls unsent rows (`FOR UPDATE SKIP LOCKED`, ordered by id), publishes with RabbitMQ publisher confirms, and sets `sent_at` after the confirm. Orders, billing and payments each have their own outbox.

**Consequences.** No lost events and no phantom events. Delivery is **at-least-once**: a crash between the confirm and the commit republishes the row, which consumers absorb with the inbox (ADR 6). Polling adds up to 500 ms latency and one query per interval per service. The integration test proves each row is published exactly once, including with two relays running concurrently, and that a broker failure resumes without gaps or reordering.

**Alternatives.** `LISTEN/NOTIFY` to wake the relay (lower latency, added later without changing the table); CDC with Debezium (production-grade, heavy infrastructure for a demo); publish after commit without an outbox (loses events on crash).

## 6. Inbox table for consumer idempotency

**Context.** With at-least-once delivery every consumer will see duplicates (redelivery, relay replays, retries).

**Decision.** Each consumer writes `(consumer, message_id)` into an `inbox` table **in the same transaction** as its side effects (`internal/platform/inbox.Once`). A duplicate insert affects zero rows, so the handler is skipped. If the handler fails, the inbox row rolls back with everything else and the retry runs normally.

**Consequences.** Effects are exactly-once per message id inside the database. External side effects are still at-least-once: the fake bank is idempotent by key (the order id) and the notification channels are documented as at-least-once. The inbox grows without bound; production needs a retention job.

**Alternatives.** Natural idempotency only (unique keys such as one invoice per order, kept as a second line of defence); Redis-based dedup (not transactional with the database).

## 7. Retry with backoff through TTL queues, then a DLQ

**Context.** Handlers fail transiently (database blip, downstream outage). Immediate requeue creates a hot loop; dropping the message loses work. Poison messages must not block the queue forever.

**Decision.** Per consumer queue `X`: queues `X.retry.1..N`, each with a message TTL (2 s, 10 s, 30 s) and a dead-letter route back to `X` through the default exchange, plus `X.dlq`. On failure the consumer republishes the message (with confirms) to `X.retry.<attempt+1>` and acks the original; after N retries, or on an `ErrPermanent` error (for example a malformed body), it goes to `X.dlq` with `x-last-error`. The attempt counter lives in the `x-retry-count` header. Trace context and ids are preserved.

**Consequences.** Backoff and DLQ with plain RabbitMQ, no plugin. Delays are fixed per tier (not exponential per message) and a crash between republish and ack duplicates the message, which the inbox absorbs. The DLQ is manual to drain: inspect in the RabbitMQ UI and republish with the shovel or management UI.

**Alternatives.** The `rabbitmq-delayed-message-exchange` plugin (arbitrary delays, an extra plugin to install and operate); a single retry queue with per-message TTL (head-of-line blocking: RabbitMQ only expires messages at the head); quorum-queue `x-delivery-limit` (counts redeliveries but gives no backoff).

## 8. One topic exchange, one durable queue per consumer

**Decision.** A single durable topic exchange `orderflow.events`. Routing keys: `order.placed`, `order.confirmed`, `invoice.generated`, `payment.succeeded`, `payment.failed`, `payment.pending_manual`, `order.cancelled`, `order.completed`. Every consumer owns a durable queue (`orders.q`, `billing.q`, `payments.q`, `notifications.q`, `gateway.sse`) with its own bindings; the gateway binds `#`.

**Consequences.** Adding a consumer never touches a producer. A slow or dead consumer only affects its own queue. Messages are persistent and published with confirms. The topology is declared idempotently by whoever starts first.

**Alternatives.** One queue shared by competing consumers of different kinds (breaks isolation); direct or fanout exchanges (less flexible routing).

## 9. Event envelope in AMQP properties, flat JSON payload

**Decision.** Envelope fields use native AMQP properties: `message_id` (UUID), `correlation_id` (= order id), `type` (= routing key), `timestamp` (occurred at), and a `traceparent` header. The body is JSON. All event types share one flat `Payload` struct in `internal/platform/events` with omitted-when-empty fields.

**Consequences.** Brokers and tools (RabbitMQ UI) show the envelope without parsing the body. One struct is easy to read and evolve additively; the cost is weaker typing (a reader must know which fields an event fills). The contract is a Go package, so schema changes are compile-time visible inside this monorepo; across repositories you would add a schema registry or per-event versioned types.

**Alternatives.** Protobuf payloads (stronger contract, harder to read in the UI); CloudEvents envelope in the body (standard, more verbose).

## 10. SSE (not WebSocket) for the live timeline

**Context.** The original .NET system used SignalR (WebSocket with fallbacks) for server-to-browser push.

**Decision.** Server-Sent Events: `GET /api/orders/{id}/events`. Traffic is one-directional (server to browser), SSE works over plain HTTP, reconnects automatically in the browser (`EventSource`), needs no library and passes through proxies. The endpoint checks session and ownership, replays the order's events so far, then streams live events and sends a comment ping every 15 s.

**Consequences.** Simple code and simple debugging with `curl -N`. One direction only (the page uses ordinary POSTs for actions). Under HTTP/1.1 browsers limit connections per host (6); fine for one timeline.

**Alternatives.** WebSocket (needed only for bidirectional or binary traffic); long polling (wasteful).

## 11. Simulated authentication (signed cookie, seeded users)

**Context.** The demo needs "a logged-in user" to pick whose cards, autopay flag and notifications apply. A real identity provider would dominate the project and is not what it demonstrates.

**Decision.** `POST /login` with a seeded demo user id (`u-alice`, `u-bob`, `u-carol`) issues an HMAC-SHA256-signed token in an `HttpOnly`, `SameSite=Strict` cookie (`Secure` when `COOKIE_SECURE=true`). The token carries the user id and an expiry and is verified in constant time. **There is no password, no IdP and no real authentication**; anyone who can reach the gateway can pick any demo user. State-changing endpoints require `Content-Type: application/json`, which together with `SameSite=Strict` blocks simple cross-site form posts. The payments admin API is unauthenticated and not published to the host.

**Consequences.** Enough to demonstrate ownership checks (a user cannot read another user's order or stream; they get 404) and session handling. Must not be exposed to the internet. The session secret has a demo default in `docker-compose.yml`; the gateway refuses to start with a secret shorter than 16 bytes.

**Alternatives.** OIDC with Keycloak or Dex (the production answer, a large side quest); opaque server-side sessions (needs a store).

## 12. Own minimal migration runner with embedded SQL

**Context.** Each service needs schema changes applied at start-up, and the Docker images must be self-contained.

**Decision.** `internal/platform/database.Migrate`: SQL files embedded with `go:embed` (`internal/adapters/postgres/migrations/NNN_name.sql`), applied in lexical order, each in a transaction, recorded in `schema_migrations`, serialized by a Postgres advisory lock. Forward-only. About 60 lines of code and no extra dependency.

**Consequences.** No down migrations, no checksums, no out-of-order detection. Seed data lives in migrations for the demo. Services migrate on start, which is acceptable for a demo and wrong for zero-downtime production deploys with several replicas and incompatible changes.

**Alternatives.** `goose` or `golang-migrate` (feature-rich and well tested; I would adopt one the moment the schema needs down migrations or Go-coded migrations); Atlas (declarative, heavier).

## 13. One Postgres instance, one database per service

**Decision.** A single Postgres container; `docker/postgres/init.sh` creates `orders`, `inventory`, `billing`, `payments`, `notifications`. Each service connects only to its own database. All use the same demo user.

**Consequences.** Data ownership is enforced by convention and by the absence of cross-database queries (Postgres cannot join across databases), at the cost of one container instead of five. Using one superuser for all databases is a demo shortcut; production would use a role per service with access to its database only.

**Alternatives.** Schemas in one database (easier to cross-join by mistake); one container per service (realistic, heavy on memory for a laptop demo).

## 14. Notification channels only write logs

**Decision.** `notifying.Sender` is a port; the email, SMS and push adapters (`channels.Log`) write one structured log line each and send nothing. The README says so. Preferences (which channels, which destination) are real and stored; the delivery log table records what would have been sent.

**Consequences.** No credentials, no cost, deterministic tests. A real provider (SES, Twilio, FCM) is a new adapter implementing `Sender`, wired in `main.go`. Failure behaviour of real providers (timeouts, rate limits) is not exercised.

## 15. Fake bank with Auth, Sale and Void semantics and a failure switch

**Decision.** The payments port mirrors card-gateway semantics: `Authorize` reserves funds, `Sale` captures them, `Void` releases an authorization. If the sale fails after a successful authorization the service voids it, so no funds stay held. The fake derives references from the order id (idempotent by key). A switch forces failures at the `auth` or `sale` stage: initial value from `PAYMENTS_FAIL_STAGE`, changeable at runtime through `PUT /admin/failure` (the demo page calls it through the gateway). A bank *decline* publishes `payment.failed`; a *technical* error rolls back and is retried.

**Consequences.** Both failure shapes are demonstrable live: a plain decline, and a capture failure that needs a Void. The autopay decision (card exists, consent given, not expired) is a pure function with table tests.

## 16. Generated protobuf code is committed

**Decision.** `proto/` is the source of truth, `buf generate` writes `gen/`, and `gen/` is committed so `go build` and `go test` work without buf or protoc. CI runs `buf lint`, regenerates and fails on `git diff` in `gen/`.

**Consequences.** Clone-and-build works with only Go installed; generated diffs appear in reviews; the plugin versions are pinned (`protoc-gen-go` v1.36.12, `protoc-gen-go-grpc` v1.6.2, `buf` v1.74.0).

## 17. Logging and tracing design

**Decision.** `log/slog` JSON to stdout everywhere. A context-aware handler adds `service`, `order_id`, `trace_id`, `span_id`. OpenTelemetry traces are exported over OTLP/gRPC to Jaeger all-in-one; `otelgrpc` and `otelhttp` instrument the transports, and `internal/platform/rabbitmq` creates producer and consumer spans and propagates `traceparent`. The outbox stores the `traceparent` so the asynchronous relay continues the original trace. Details and recipes: `docs/observability.md`.

**Consequences.** One order is one trace across HTTP, gRPC and the broker, and its logs are filterable by `order_id`. Always-on sampling and stdout logs are demo settings.

## 18. Gateway keeps an in-memory timeline per order

**Context.** The browser opens the SSE stream *after* `POST /orders` returns, by which time the first events have already been published.

**Decision.** The gateway's `timeline.Hub` keeps the events of the last 500 orders in memory, de-duplicates by message id, and gives each new subscriber a replay followed by live events atomically (under one lock).

**Consequences.** No event is missed or duplicated on connect. State is lost on gateway restart, and a second gateway instance would need its own queue (not a shared one) and its own timeline, so a browser must keep talking to the instance that holds its session. The orders service remains the source of truth through `GET /api/orders/{id}` and its saga history.

**Alternatives.** Rebuild the timeline from the persisted `saga_log` (accurate, but it lacks events such as `invoice.generated` details); store events in Redis.

## 19. Pricing authority is inventory; invoice extracts tax from inclusive prices

**Decision.** Clients send SKUs and quantities only. `inventory.Reserve` returns the priced lines and `orders` stores them, so a client cannot choose its price. Prices are tax-inclusive; billing derives net and VAT (21 %, rounded half up, `net + tax = total`), so the invoice total always equals what the order and the payment use.

**Consequences.** Simple and consistent money handling (integer cents, one currency, EUR). No price history or promotions.

## 20. Compensation calls inventory inside the consumer transaction

**Decision.** When `payment.failed` arrives, `orders` moves to `COMPENSATING`, calls `inventory.Release` and moves to `CANCELLED` in **one** database transaction together with the inbox row and the `order.cancelled` outbox row. If the Release call fails, the whole transaction rolls back and the message is retried with backoff (and ends in the DLQ if inventory stays down).

**Consequences.** `COMPENSATING` is never visible in the database in a committed state, only in `saga_log` as part of the same commit; the failure window is covered by retries instead of a persisted intermediate state. Holding a transaction open during a gRPC call is acceptable at this scale (short call, 5 s timeout) and wrong for high throughput. Release is idempotent, so a retry after a lost response is safe.

**Alternatives.** Commit `COMPENSATING` first, call Release, commit `CANCELLED` (visible intermediate state, needs a recovery sweeper for orders stuck in `COMPENSATING`).

## 21. Integration tests behind a build tag, testcontainers-go

**Decision.** Files that need Docker carry `//go:build integration`. `go test ./...` stays fast and needs no Docker; `make test-integration` (and CI) run `go test -tags=integration ./...`, starting Postgres 17.6 and RabbitMQ 4.1.4 once per test binary. Each test gets its own database or uses unique queue names.

**Consequences.** The default loop is under a few seconds; the integration loop costs about a minute of container start-up. Covered: outbox exactly-once with concurrent relays and resume after publish failure, inbox duplicates and rollback, retry then DLQ, permanent failure straight to DLQ, saga persistence over real Postgres, and inventory under concurrent reservations.

## 22. Pinning policy and tooling without Homebrew

**Decision.** Go modules are pinned by `go.mod`/`go.sum`. Tools are installed with `go install <pkg>@<exact version>` (golangci-lint v2.15.0, buf v1.74.0, protoc plugins). Docker images use exact tags (`postgres:17.6-alpine`, `rabbitmq:4.1.4-management-alpine`, `jaegertracing/all-in-one:1.76.0`, `golang:1.26.8-alpine`) and the runtime base is pinned by digest. GitHub Actions are pinned by full commit SHA with a version comment.

**Consequences.** Reproducible builds. Updates are deliberate, manual work (a Dependabot or Renovate config would automate them).

## 23. Consumers do not reconnect; the container restarts them

**Decision.** If the broker connection or a consumer channel drops, `Consume` returns an error, the process exits and Docker (`restart: unless-stopped`) starts it again. Start-up waits for dependencies with retries.

**Consequences.** Much less code than in-process reconnection logic, and no half-alive states. Restart costs a second or two and the in-flight message is redelivered (the inbox makes that safe). A production client library would reconnect in process.

## 24. Manual payment is not implemented

**Decision.** For a customer without autopay consent, `payments` publishes `payment.pending_manual`, `notifications` tells the customer, and the order stays `INVOICED`. There is no "pay now" endpoint and no timeout that cancels the order.

**Consequences.** The branch is visible and tested, but it is a dead end in the demo. A real system needs a payment page or link, an expiry and a compensation path for unpaid orders. Listed under known gaps in the README.

## 25. Payments owns cards, notifications owns channel preferences

**Context.** The customer's card and autopay consent, and their notification channels, are customer data that no listed service would otherwise own.

**Decision.** `payments` stores cards and the autopay consent (the only service that needs them; the gateway reads them through a small HTTP endpoint to render the demo page). `notifications` stores channel preferences and destinations. Neither stores card numbers: only brand, last four digits and a fake vault token. There is no separate customer service.

**Consequences.** No extra service and no shared customer database. Customer data is split by the consumer that needs it, so changing both (for example a "profile" page) would need a customer service to own them.

## 26. Use-case packages and `internal/platform`

**Context.** The first layout put every service's use cases in one `internal/app` package and shared code in `pkg/`. Reviewing it, `app` told a reader nothing about what a service does, files grew to several use cases each, and `pkg/` is a convention Go itself does not have: [go.dev/doc/modules/layout](https://go.dev/doc/modules/layout) puts server code under `internal/` and keeps the module root for what is meant to be imported.

**Decision.**

- Shared infrastructure moves from `pkg/` to `internal/platform/` (`config`, `database`, `events`, `health`, `inbox`, `outbox`, `rabbitmq`, `telemetry`). `envx` became `config`.
- Inside a service, `internal/app` is replaced by **use-case packages named by what they do** (gerunds): orders has `placing`, `advancing`, `compensating`, `fetching`; inventory has `reserving`, `releasing`, `listing`; billing `invoicing`; payments `charging`; notifications `notifying`. The gateway has `timeline` (the SSE hub) and `storefront` (what it shows and the ports to other services), which are nouns because they are components, not use cases. The folder tree now says what the system does.
- `internal/domain` and `internal/adapters/*` stay, and `cmd/<name>/main.go` is still the only place that wires concrete types. Dependencies point inward: adapters import use cases and domain, never the reverse.
- A port used by one use case lives in that use-case package (`charging.Bank`, `notifying.Sender`). A port shared by several use cases lives in `domain` (orders: `domain.Repository`, `domain.Tx`, `domain.Inventory`; inventory: `domain.Repository`, `domain.Tx`). Use cases never import each other: `advancing` declares the small `Compensator` interface it needs and `main.go` passes it the `compensating` service.
- Names follow the Go Code Review Comments on [Variable Names](https://go.dev/wiki/CodeReviewComments#variable-names), [Receiver Names](https://go.dev/wiki/CodeReviewComments#receiver-names) and [Interfaces](https://go.dev/wiki/CodeReviewComments#interfaces): descriptive names everywhere, keeping only the idioms a Go reader expects (`ctx`, `err`, `ok`, `t`, `i`, `w`/`r`, `tx`, short receivers). Details in `docs/conventions.md`.

**Alternatives considered.**

- *Layer-first folders* (`domain/`, `app/`, `adapters/` at the repository root): spreads one feature over three trees and makes the services share packages by accident.
- *One file per use case inside `app/`*: better than one big file (tried first) but the package is still called `app`, still one namespace for unrelated use cases, and tests and fakes for different use cases share one package.
- *Keeping `pkg/`*: harmless, but it signals "importable library" for code that is private to this system.

**Consequences.** More, smaller packages, and a little wiring in each `main.go`. In exchange the package name documents the use case (`placing.NewService`), each package can be read and tested alone, and the cycle-free rule is enforced by the compiler. A service with a single use case (billing, payments, notifications) has one package; splitting it further would add nothing. Prior art: [CodelyTV/go-hexagonal_http_api-course](https://github.com/CodelyTV/go-hexagonal_http_api-course) (packages named `creating`, `increasing`) and [ThreeDotsLabs/wild-workouts-go-ddd-example](https://github.com/ThreeDotsLabs/wild-workouts-go-ddd-example) (domain, app and adapters per service, with `internal/` for private code).
