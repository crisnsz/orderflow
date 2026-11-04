# Code conventions

How the code is organised and named, so any file can be read and defended on
its own. The reasoning behind the structure is in `DECISIONS.md` (decision 26).

## 1. Naming

Names say what a thing is. No abbreviations that a reader has to decode.

| Instead of | Write | Where |
|---|---|---|
| `sw`, `fl` | `failureSwitch`, `flusher` | HTTP handlers, bank fake |
| `h` (headers) | `headers` | RabbitMQ, HTTP |
| `m`, `p`, `d` | `message`, `payload`, `delivery` | consumers, use cases |
| `o`, `inv` | `order`, `invoice` or `inventory` | use cases, tests |
| `ch`, `pub`, `conn` | `channel`, `publisher`, `connection` | RabbitMQ code |
| `rerr`, `verr`, `cerr` | `republishErr`, `voidErr`, `chargeErr` | error variables named after what failed |
| `cctx`, `pctx` | `consumerCtx`, `relayCtx` | derived contexts |
| `tp`, `typ`, `evt` | `traceparent`, `eventType`, `outgoingEvent` | |
| `svc` | `service` or the use case name (`placingOrders`) | `main.go`, adapters |

Rules of thumb:

- A variable that lives more than two or three lines gets a full word.
- Booleans and errors say what they mean: `alreadyApplied`, `deadLettered`, `chargeErr`.
- Constants replace magic numbers and carry the unit: `publisherConfirmTimeout`, `consumerPrefetchCount`, `maxItemsPerOrder`.
- Unexported struct fields follow the same rule (`persistedTransitions`, not `persisted`).
- Avoid stutter: `placing.Service`, not `placing.PlacingService`; `timeline.Hub`, not `timeline.TimelineHub`.
- Package names are short, lowercase, one word. Use-case packages are gerunds (`placing`, `charging`).

### Go idioms kept on purpose

These are what a Go reader expects, and Go's own guidance asks for them
([Code Review Comments: Variable Names](https://go.dev/wiki/CodeReviewComments#variable-names),
[Receiver Names](https://go.dev/wiki/CodeReviewComments#receiver-names)):

| Name | Used for | Why it stays |
|---|---|---|
| `ctx` | `context.Context`, always the first parameter | universal, even in the standard library |
| `err`, `ok` | the error and the comma-ok result | universal |
| `t`, `b` | `*testing.T`, `*testing.B` | the testing package's convention |
| `tt` | the loop variable of a table-driven test | the Go wiki's table-driven test pattern |
| `i`, `j` | loop indices over a few lines | "short names for short scopes" |
| `w`, `r` | `http.ResponseWriter`, `*http.Request` | every `net/http` handler |
| `tx` | a database transaction | name used by `database/sql` and `pgx` |
| 1-3 letter receivers | `s *Service`, `h *Hub`, `o *Order` | consistent per type, a short form of the type name, never `this` or `self` |

Not renamed: anything in `gen/` (generated), and the exported API of generated code.

## 2. How a service is layered

```
services/<name>/
  cmd/<name>/main.go        composition root: builds adapters, use cases, wires them
  internal/
    domain/                 entities, rules, errors, ports shared by several use cases
    <use-case>/             one package per use case (placing, charging, ...)
    adapters/<technology>/  postgres, grpc, rabbitmq, http, bank, channels ...
```

Dependencies point inward:

```
cmd  ->  adapters  ->  use cases  ->  domain
          (never the reverse)
```

- `domain` has no I/O and imports nothing from the service. It holds the entities (`Order`, `Invoice`), their rules and the ports that several use cases share (orders: `domain.Repository`, `domain.Tx`, `domain.Inventory`).
- A **use-case package** is named by what it does and holds the service type, the steps of the use case as small private functions, and the ports only that use case needs (`charging.Bank`, `notifying.Sender`). It imports `domain` and `internal/platform`, never an adapter and never another use case. When one use case needs another, it declares the small interface it requires and `main.go` passes the implementation (`advancing.Compensator` is satisfied by `compensating.Service`).
- **Adapters** implement ports (Postgres store, gRPC client, fake bank) or drive use cases (gRPC server, RabbitMQ consumer, HTTP). They are thin: translate, call, translate back.
- `main.go` is the only place that names concrete types together.
- Shared infrastructure lives in `internal/platform/` (`config`, `database`, `events`, `health`, `inbox`, `outbox`, `rabbitmq`, `telemetry`). It never imports a service.

| Service | Packages |
|---|---|
| orders | `domain`, `placing`, `advancing`, `compensating`, `fetching`, `orderstest` (test doubles) |
| inventory | `domain`, `reserving`, `releasing`, `listing` |
| billing | `domain`, `invoicing` |
| payments | `domain`, `charging` |
| notifications | `domain`, `notifying` |
| gateway | `domain` (session, demo users), `storefront` (view types and ports), `timeline` (SSE hub) |

Services with a single use case have a single use-case package; splitting
further would add nothing.

## 3. Files are named by use case

Inside a package, a file holds one use case or one concern, named after it:

| File | Holds |
|---|---|
| `service.go` | the `Service` type, its constructor, package doc |
| `ports.go` | interfaces and sentinel errors, when there are several |
| `place_order.go`, `handle_saga_event.go`, `compensate.go` | one use case with its private steps |
| `*_handlers.go`, `stream_events.go`, `auth.go`, `respond.go` | HTTP handlers grouped by feature, middleware, helpers |
| `store.go`, `consumer.go`, `client.go`, `server.go` | the adapter for one technology |

Functions do one thing and read as a sequence of named steps. For example
`PlaceOrder` validates, records the order, reserves stock, then confirms or
cancels, each a helper with its own name.

Tests live next to the code they test and use the external package
(`placing_test`). Fakes shared by several packages of one service go in a
`<service>test` package (`orderstest`).

## 4. Error handling

- Errors are values: use cases return them and the edge (consumer, handler) decides what to do and logs once.
- Wrap with context using `%w`: `fmt.Errorf("release stock: %w", err)`. Callers test with `errors.Is`.
- Sentinel errors name business outcomes and are declared where they are produced: `domain.ErrNotFound`, `domain.ErrRejected`, `placing.ErrInvalid`, `domain.ErrInvalidTransition`.
- **Business outcome vs technical failure.** A rejected reservation or a bank decline is a normal result (the order is cancelled, `payment.failed` is published). Anything else returns an error so the message is retried.
- **Permanent failures** (malformed body, an order with no lines) wrap the use case's `ErrPermanent` with `%w: %w`; the RabbitMQ adapter maps it to `rabbitmq.ErrPermanent`, so the message goes straight to the DLQ instead of being retried.
- A trigger that is no longer valid for the current state (late or duplicate event) is logged at warn and swallowed, because retrying cannot fix it.
- Adapters translate errors at the edge: gRPC maps domain errors to status codes, the gateway maps `storefront.ErrNotFound` to 404 and unknown failures to 502, never leaking internals.
- Deferred cleanup that cannot be handled is dropped explicitly: `defer func() { _ = tx.Rollback(ctx) }()`.

## 5. Logging

- `log/slog`, JSON on stdout, configured once in `telemetry.NewLogger(serviceName)`.
- Always the context variants (`slog.InfoContext(ctx, ...)`) so `order_id`, `trace_id` and `span_id` are attached automatically. Put the order id in the context with `telemetry.WithOrderID`.
- Messages are lower case, short, and describe the event: `"order placed"`, `"payment failed; compensating"`.
- Attributes use snake_case keys (`order_id`, `message_id`, `amount_cents`, `retry_in`) and the error goes under `"error"`.
- Levels: `Info` for state changes in the happy path, `Warn` for handled anomalies (duplicates, retries scheduled, ignored late events), `Error` for failures that need attention (message moved to the DLQ, failed request), `Debug` for "nothing to do".
- Never log secrets, tokens or full card data.

## 6. Comments

The code has no comments: names and small functions carry the meaning, and design reasons live in `DECISIONS.md`. The exceptions are compiler directives (`//go:embed`, `//go:build`), generated code and the version next to each pinned action SHA in CI.

## 7. Configuration and constants

Environment variables are read in `main.go` through `internal/platform/config`
(`config.String`, `config.Bool`, `config.Duration`) with a default. Timeouts,
buffer sizes, retry counts and limits are named constants next to the code
that uses them.
