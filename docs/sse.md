# Live timeline with Server-Sent Events

How the demo page shows an order's events as they happen, and why it is built
this way.

## What SSE is

Server-Sent Events is a plain HTTP response that **stays open**. The server
keeps writing messages into it, and the browser handles each one as it arrives.

- **One direction only**: server to browser.
- **Built into browsers**: `new EventSource(url)`, with no library.
- **Plain HTTP**: it goes through proxies and uses cookies like any request.

### Why not WebSocket or polling

| Option | Verdict |
|---|---|
| **Polling** (`GET /api/orders/{id}` every second) | simple, but it adds latency and sends many empty requests |
| **WebSocket** | two-way, but the browser never needs to send anything here, and it needs its own protocol and handling |
| **SSE** | exactly what a timeline needs: the server pushes, the browser listens |

The original .NET system used SignalR, which can use SSE as one of its
transports.

## The path of one event

```text
orders / billing / payments
        │  publish (via the outbox)
        ▼
RabbitMQ exchange orderflow.events
        │  binding "#" (all events)
        ▼
queue gateway.sse
        │  rabbitmq/subscriber.go
        ▼
Hub (in memory, per order)
        │  timeline/hub.go
        ▼
GET /api/orders/{id}/events
        │  http/stream_events.go: streamEvents
        ▼
browser EventSource
           static/app.js: followOrder()
```

### 1. RabbitMQ to the Hub

`services/gateway/internal/adapters/rabbitmq/subscriber.go`

The gateway has its own durable queue, `gateway.sse`, bound with `#`, so it
receives **every** event from every service. Each message is decoded and
handed to the Hub. It has no retry policy: writing to memory cannot
fail, and an undecodable message is logged and dropped.

### 2. The Hub

`services/gateway/internal/timeline/hub.go`

It does three things:

| Job | How | Why |
|---|---|---|
| **Keep a history per order** | a map from order id to its events, capped at 500 orders; the oldest order is evicted | so that a browser that connects late can be caught up |
| **Drop duplicates** | it remembers each message id per order | RabbitMQ delivers *at least once*, so the same event can arrive twice |
| **Fan out to subscribers** | each open connection has a channel with a buffer of 64 | many tabs can follow the same order |

When a subscriber's buffer is full, that one event is dropped for that
subscriber and a warning is logged. The other subscribers are not slowed down.

### 3. The HTTP endpoint

`GET /api/orders/{id}/events`, in
`services/gateway/internal/adapters/http/stream_events.go` (`streamEvents`).

1. **Authorization**: the order must belong to the user in the session cookie.
   Otherwise there is no stream.
2. **Headers**:
   - `Content-Type: text/event-stream` marks the response as SSE.
   - `Cache-Control: no-cache` stops caches from storing it.
   - `X-Accel-Buffering: no` stops nginx-style proxies from buffering it.
3. **Replay**: it sends every event the Hub already has for this order.
4. **Live**: it sends each new event as it arrives, and **flushes** after each
   one so it leaves the server right away instead of waiting in a buffer.
5. **Keep-alive**: every 15 seconds it writes `: ping`. Lines that start with
   `:` are comments that the browser ignores. They keep proxies from closing a
   connection that looks idle.
6. **End**: when the browser disconnects, the request context is cancelled, the
   loop exits and the subscription is removed.

The HTTP server has no `WriteTimeout` (`services/gateway/cmd/gateway/main.go`),
because a write timeout would cut off long-lived streams.

### 4. The wire format

Each event is a small text block, ended by a blank line:

```text
id: 2d512310-2a64-4dfb-b23a-046218d16908
event: payment.failed
data: {"id":"2d512310-...","type":"payment.failed","order_id":"2710532d-...","data":{"reason":"sale: declined by bank: capture_failed (simulated failure)"}}

```

- `id`: the message id.
- `event`: the event type. The browser registers one listener per type.
- `data`: the JSON payload.

You can watch it raw:

```sh
curl -N -b jar localhost:8080/api/orders/<id>/events
```

### 5. The browser

`services/gateway/internal/adapters/http/static/app.js`, function `followOrder()`:

```js
const shownEventIds = new Set();
orderEventSource = new EventSource(`/api/orders/${order.id}/events`);
for (const eventType of Object.keys(EVENT_LABELS)) {
  orderEventSource.addEventListener(eventType, (messageEvent) => {
    const event = JSON.parse(messageEvent.data);
    if (shownEventIds.has(event.id)) return;
    shownEventIds.add(event.id);
    addEvent(event);
  });
}
```

`EventSource` sends the cookie automatically, and if the connection drops it
**reconnects by itself**. A reconnect gets the full replay again, so the page
keeps the ids it has already shown and skips repeats.

## The key design point: replay without gaps

**The problem:** pressing "Place order" starts the saga at once. The first
events (`order.placed`, `order.confirmed`) can be published **before** the
browser has opened the `EventSource`. If the server only sent new events, the
timeline would miss them.

**The fix:** `Subscribe` copies the history **and** registers the new channel
while holding the **same lock**. `Publish` takes that lock too, so an event is
either:

- already in the history, and sent as replay, or
- published after the subscription, and sent live.

It can never fall between the two, so nothing is lost and nothing is sent
twice. The test `TestLateSubscriberGetsReplayThenLive` in `timeline/hub_test.go` covers
exactly this.

## Limitations

| Limitation | Effect | What a production version would do |
|---|---|---|
| **The Hub is in memory** | restarting the gateway loses the timelines. The real saga history is safe in the orders database; this is only a live view. | rebuild the replay from the orders service |
| **A single gateway instance** | with two gateways, both consume from the same `gateway.sse` queue, so each event reaches only one of them | one queue per instance (exclusive, auto-delete), or a pub/sub layer such as Redis |
| **No `Last-Event-ID` resume** | on reconnect, the server sends the whole replay again. The page skips ids it has already shown, so the timeline stays correct, but the bytes are re-sent. | send only the events after `Last-Event-ID` |
| **Slow subscribers lose events** | a full buffer drops events for that tab | close the stream, so the browser reconnects and gets a full replay |
| **Unbounded wait** | a tab left open keeps one connection and one goroutine | an idle timeout once the order reaches a final state |

## Files

| File | Role |
|---|---|
| `services/gateway/internal/adapters/rabbitmq/subscriber.go` | broker to Hub |
| `services/gateway/internal/timeline/hub.go` | history, de-duplication, fan-out |
| `services/gateway/internal/timeline/hub_test.go` | replay, isolation and eviction tests |
| `services/gateway/internal/adapters/http/stream_events.go` | `streamEvents`, the SSE endpoint |
| `services/gateway/internal/adapters/http/static/app.js` | `EventSource` and rendering |
