# Observability

How to find out why *one* order failed when six services are writing logs.

## The problem

In a monolith a failing request is one stack trace. Here a single order touches
the gateway, orders, inventory, billing, payments and notifications, across HTTP,
gRPC and RabbitMQ, and most of the work happens **asynchronously after the HTTP
response has been sent**. When something goes wrong:

- the error is logged in one service (say `payments`) but the symptom shows up
  in another (the order is stuck, or cancelled, in `orders`);
- every service logs thousands of lines for other orders at the same time;
- logs from different containers are interleaved with no shared ordering;
- by the time you look, the lines you need are buried in volume.

Plain `grep` on timestamps does not scale. What scales is making every line
**filterable by the thing you are investigating** (the order) and **linkable to
a causal trace**.

## What this project does

### 1. Structured logs with a fixed set of correlation fields

All services log JSON through `log/slog` (`internal/platform/telemetry`). There is no
`fmt.Println`. A context-aware handler adds the same fields to every line that
is written with a request or message context (`slog.InfoContext(ctx, ...)`):

| field      | meaning |
|------------|---------|
| `service`  | which service wrote the line |
| `order_id` | the correlation id: the order being processed |
| `trace_id` | the OpenTelemetry trace the work belongs to |
| `span_id`  | the span that wrote the line |
| `level`, `time`, `msg` | as usual |

Example line:

```json
{"time":"2026-10-28T09:03:19.486Z","level":"WARN","msg":"payment failed","service":"payments","reason":"capture_failed (simulated failure)","order_id":"a4d9b8c3-...","trace_id":"dd958f03a40632e308b1c25c4d430377","span_id":"5b0e..."}
```

### 2. The correlation id travels with the work

- Over HTTP/gRPC: `order_id` is put in the context as soon as the order exists
  (`telemetry.WithOrderID`), so every later log line in that call has it.
- Over RabbitMQ: the AMQP `correlation_id` is the order id. The consumer wrapper
  reads it and calls `WithOrderID` before the handler runs, so a consumer
  needs no extra code to log with the right `order_id`.
- Trace context: the producer injects a W3C `traceparent` header; the consumer
  extracts it. The transactional outbox stores the `traceparent` next to the
  event, so the relay (which publishes later, from another goroutine) continues
  the **original** trace instead of starting a new one. Result: one trace spans
  `POST /api/orders` through gateway, orders, inventory, outbox relay, broker,
  billing, payments, the compensation and notifications.

### 3. Logs link to traces

Every line carries `trace_id`. Copy it into the Jaeger search box
(`http://localhost:16686`) to see the timeline, or go the other way from a slow
span to the logs of that trace id.

## Recipes

`docker compose logs` prefixes lines with the container name, which breaks JSON
parsing; use `--no-log-prefix`. Lines that are not JSON (Postgres, RabbitMQ) are
skipped by `fromjson?`.

Everything that happened to one order, across services, in time order:

```sh
ORDER=<order id>
docker compose logs --no-log-prefix \
  | jq -R -c --arg o "$ORDER" 'fromjson? | select(.order_id == $o)' \
  | jq -s -r 'sort_by(.time)[] | "\(.time[11:23]) \(.service) \(.level) \(.msg)"'
```

One service only (the spec-style one-liner):

```sh
docker compose logs --no-log-prefix payments | jq -R -c --arg o "$ORDER" 'fromjson? | select(.order_id == $o)'
```

By trace id (also catches lines that have a trace but no order id yet):

```sh
docker compose logs --no-log-prefix | jq -R -c --arg t "$TRACE" 'fromjson? | select(.trace_id == $t)'
```

Which orders had problems recently (warnings and errors, grouped by order):

```sh
docker compose logs --no-log-prefix \
  | jq -R -r 'fromjson? | select(.level == "WARN" or .level == "ERROR") | .order_id // empty' \
  | sort | uniq -c | sort -rn | head
```

Messages that exhausted their retries (also visible in the RabbitMQ UI under
queues named `*.dlq`):

```sh
docker compose logs --no-log-prefix | jq -R -c 'fromjson? | select(.msg == "message moved to DLQ")'
```

Finding the trace of a failed order without reading logs: take any `trace_id`
from its lines (recipe above), then open `http://localhost:16686/trace/<trace_id>`.

## Reading a failed order end to end (worked example)

1. Turn the failure switch to `sale` in the demo page and place an order.
2. The order ends `CANCELLED` with reason `sale: declined by bank: capture_failed (simulated failure)`.
3. The log recipe shows the story in order: `payments` authorizes, the sale fails,
   the authorization is voided, `payment.failed` is published; `orders` receives it,
   logs `payment failed; compensating`, `inventory` logs `stock released`, and
   `orders` logs `order cancelled after compensation`.
4. The Jaeger trace for the same `trace_id` shows the same hops with timings,
   including the `Release` gRPC call made during compensation.

## Limits of this setup

- Logs live in the containers' stdout (Docker's json-file driver). They are
  lost with the container and are not indexed. `jq` over `docker compose logs`
  is fine for a demo, not for production.
- Traces are exported with a batch span processor and **always sampled**
  (the SDK default is parent-based always-on). Fine for a demo.
- Only the instrumented boundaries produce spans: HTTP server/client, gRPC,
  publish and consume. Database calls are not traced.
- Log lines written without a context (`slog.Info` instead of `InfoContext`)
  lack `order_id` and `trace_id`. The linter (`sloglint`, scope mode) flags such
  calls where a context is available.

## What I would add in production

- **Centralized logs**: ship stdout to Loki (Grafana), Seq, Elasticsearch/OpenSearch
  or Application Insights through an agent (Promtail/Alloy, Fluent Bit, OTel
  Collector). Index `service`, `level`, `order_id` and `trace_id` as fields so the
  recipes above become one query, and so a log line can deep-link to its trace.
- **OTel Collector** between services and the backend: batching, retries, tail
  sampling (keep 100% of traces that contain an error or a slow span, a small
  percentage of the rest) and redaction in one place.
- **Retention and cost**: short retention for debug/info (days), longer for
  warn/error and for audit events; cap log volume per service; never log
  payment data or personal data (this project logs only ids and amounts).
- **Metrics and alerts**: RED metrics per service, outbox lag (`count(*)` of
  unsent rows and the age of the oldest), queue depth and DLQ depth per queue,
  saga duration histograms and a count of orders stuck in a non-terminal state
  for longer than a threshold. Alert on DLQ depth > 0 and on stuck sagas, not
  on individual error lines.
- **Exemplars**: link metrics to traces, so a latency spike on a dashboard opens
  a representative trace.
