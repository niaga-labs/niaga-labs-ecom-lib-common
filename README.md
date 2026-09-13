# lib-common

Shared Go code for the Niaga platform: configuration, database and NATS plumbing, auth middleware, the
transactional outbox, and the **canonical event subject catalog** every service publishes and consumes
through.

Module path `github.com/niaga-labs/niaga-labs-ecom-lib-common`. Public since 2026-09-05, so CI in the other repos checks it
out with no token.

Consumed by **ten** Go services — every `service-*` repo — via a `replace` directive in their `go.mod` during
local development, and by a sibling checkout in CI. Counted with
`grep -rl "replace github.com/niaga-labs/niaga-labs-ecom-lib-common" --include=go.mod`.

**Ten consume it; only eight have CI.** `service-marketplace` and `service-support` have no
`.github/workflows` at all, so the familiar "the eight Go services" from the workspace's `ci-known-red.txt`
is a count of *CI workflows*, not of lib-common's consumers. An earlier draft of this line conflated the two
— caught in review, and worth naming because the two numbers describe different sets.

## Packages

| Package | What it holds |
|---|---|
| `auth` | JWT parsing, API-key and internal-token middleware, `IsDevEnv` |
| `config` | env loading and the shared config shape |
| `database` | Postgres connection helpers |
| `domain` | shared value types — money, quantity, typed errors |
| `eventsourcing` | **the subject catalog**, event envelope, payload types, Postgres event store, DLQ, idempotency |
| `lock` | advisory locking |
| `logger` · `monitoring` · `telemetry` | zap setup, metrics, tracing |
| `middleware` | HTTP middleware, health endpoints |
| `nats` | JetStream connection and stream helpers |
| `outbox` | the transactional outbox processor |
| `resilience` | retry and circuit-breaker helpers |
| `response` | the shared HTTP response envelope |
| `saga` | saga coordination primitives |
| `storage` | object-storage helpers |
| `validator` | request validation |

## Event subjects

`eventsourcing/catalog.go` declares every canonical subject. **Every one has a publisher and a consumer, or
says here why it does not.**

Measured on **2026-09-06** (NIAGA-117) rather than assumed, by two independent methods: a search for
`eventsourcing.<Const>` across every Go file in the workspace, and a literal search for each subject *string*
in every tracked file of every repo, frontends included. Where they disagreed, the literal search won.

Publisher and consumer are read from where the constant is used — `internal/events/publisher.go` versus
`internal/events/subscriber.go` in each service.

| Subject | Publisher | Consumers |
|---|---|---|
| `events.user.registered` | service-auth | service-notification |
| `events.user.password_reset_requested` | service-auth | service-notification |
| `events.order.created` | service-order | service-inventory, service-notification |
| `events.order.confirmed` | service-order | service-inventory |
| `events.order.cancelled` | service-order | service-inventory |
| `events.order.status_changed` | service-order | service-inventory, service-notification |
| `events.order.payment.received` | service-order | service-notification |
| `events.order.payment.verified` | service-order | service-notification |
| `events.order.payment.rejected` | service-order | service-notification |
| `events.order.payment.refunded` | service-order | service-notification |
| `events.inventory.stock.updated` | service-inventory **and** service-order | service-marketplace |
| `events.inventory.product.restocked` | service-inventory | service-customer |
| `events.catalog.product.created` | service-catalog | service-inventory, service-marketplace |
| `events.catalog.product.updated` | service-catalog | service-inventory, service-marketplace |
| `events.catalog.product.deleted` | service-catalog | service-inventory, service-marketplace |
| `events.catalog.flash_sale.activated` | service-catalog | service-notification |
| `events.catalog.flash_sale.deactivated` | service-catalog | service-notification |
| `events.support.ticket.created` | service-support | service-notification |
| `events.support.ticket.replied` | service-support | service-notification |
| `events.support.ticket.resolved` | service-support | service-notification |
| `events.customer.back_in_stock` | service-customer | service-notification *(NIAGA-123, landing)* |
| `events.marketplace.sync.completed` | service-marketplace | **none** |
| `events.marketplace.sync.failed` | service-marketplace | **none** |
| `events.customer.created` | **none — Reserved** | none |
| `events.agent.commission.paid` | **none — Reserved** | none |

### `events.customer.back_in_stock` is declared ahead of both its ends

Added by **NIAGA-123**. At the moment this row appeared, nothing published it and
nothing consumed it — the publisher (service-customer) and the consumer (service-notification) land in the
same ticket, in the two repos that follow this one in the merge order.

It is **not Reserved**: Reserved means nobody intends to build it. This one is mid-build, and the row says
which ticket to blame if it is still marked *landing* long after NIAGA-123 closed. **If you are reading this
and NIAGA-123 is Done, check both ends and delete the parentheses** — a subject stuck between "declared" and
"published" is the exact state that produced the four orphaned `events.cart.*` entries below.

It is downstream of `events.inventory.product.restocked`, not a duplicate: inventory says a product came
back, this says a *named customer asked to be told*. The subscription lookup between the two is
service-customer's job, so notification never has to know what a subscription is.

### The three rows that are not a simple pair

**`events.inventory.stock.updated` has TWO publishers.** service-inventory publishes it from its own
`publisher.go`; service-order also enqueues it through the outbox in `internal/events/nats_publisher.go`.
Both go through the outbox and **both are correct today**.

That second publisher is where **NIAGA-178** used to live: service-order published a *bare*
`inventory.stock.updated` that no consumer subscribed to, so the stock push to the marketplace never
happened while the code read as though it did. **That is fixed** — the ticket is Done, no bare publish
remains anywhere in service-order, and the code carries a comment saying what it used to do. Recorded
because "service-order publishes stock.updated" is true both before and after the fix, and only the subject
told them apart.

**The two `events.marketplace.sync.*` subjects have a publisher and no consumer.** They are real events on
the wire that nothing subscribes to yet. That is not a defect and not the same as Reserved — it is worth
knowing before someone spends time working out why a handler never fires.

**`events.customer.created` and `events.agent.commission.paid` are Reserved.** Declared, never published,
never consumed — they appear nowhere in the workspace but `catalog.go`. Kept rather than deleted because
they name planned work and the constant is the only surviving record of the intent; nothing imports them, so
removing them later stays cheap.

### Routed by service-notification but absent from this catalog

Four subjects have a handler and a template in `service-notification/internal/events/template_router.go`,
but **no entry here, no publisher, and no durable consumer**:

- `events.user.email_verification_requested`
- `events.cart.abandoned`
- `events.cart.abandoned_reminder`
- `events.cart.abandoned_final`

A reader of service-notification would reasonably conclude that cart-abandonment email works. It cannot:
nothing publishes those events and nothing consumes them. They are listed here so the gap is visible from
the catalog as well as from that service's own README, which already records it under *Known gaps*.

### Adding a subject

0. **Never rename a subject constant or its string once published.** A rename is a silent break: the old
   publisher keeps emitting and the new consumer keeps listening, each perfectly healthy, and nothing
   connects them. Add a new subject and retire the old one deliberately instead.
1. Declare the constant in `eventsourcing/catalog.go` and add it to `SubjectDomains`.
2. Add its row to the table above — including who consumes it. **A subject with no consumer is a message
   into a void**; if there is no consumer yet, say so in the row rather than leaving the column blank.
3. Publish through the **outbox**, never `nats.Conn.Publish` directly. A bare publish to a subject nobody
   consumes returns `nil` and looks exactly like success (NIAGA-178, NIAGA-166).
4. A JetStream consumer whose `FilterSubject` matches nothing is **not an error** — it is a healthy consumer
   that never fires. Prove the wiring with a message, not by reading the code (NIAGA-116).
