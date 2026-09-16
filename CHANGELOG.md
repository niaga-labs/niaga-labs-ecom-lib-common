# Changelog

All notable changes to `lib-common` are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Docs — `CONVENTIONS.md` §5 now records what the gate actually checks (NIAGA-321)

- The section said the gate checked 15 smoke probes on status and `success` only, and listed the `meta`
  shape and the error envelope under "not checked". Both are now checked: five paginated list probes
  assert `meta.page`/`limit`/`total_pages` are numbers (three of them, whose seed guarantees rows, also
  `total`/`total_count`), and a new service-inventory probe asserts a 404 with `success: false` and
  `error.code eq NOT_FOUND`. Counts and the live run are recorded there.
- No code in this library changed — the standard was already right; §5 is the record of what enforces it.

### Fixed — a malformed `SENTRY_DSN` no longer crashes nine services at boot (NIAGA-309)

- **Before:** `NewSentryMonitor` returned `nil, err` when `sentry.Init` rejected the DSN. Every service logs
  that error and carries on, then calls `GinMiddleware()` and `RecoveryMiddleware()` while building its router,
  with a deferred `Flush`. Nine services do this: agent, auth, catalog, customer, inventory, marketplace,
  order, reporting and support.
  - **Reproduced by a boot**, not only read: service-support on `origin/main` with `SENTRY_DSN=not-a-dsn`
    logged `Failed to initialize Sentry … DsnParseError: invalid scheme`, then panicked on a nil pointer in
    `GinMiddleware` (and again in the deferred `Flush`), exit code 2.
- **Now:** the constructor never returns nil. A rejected DSN gives the same no-op monitor as an empty DSN,
  **together with** the error, so each caller's Warn line still fires (and service-order's `enabled` boot line
  stays right). Every method is also safe on a nil `*SentryMonitor` and on a nil config. An empty or valid DSN
  behaves as before, and the caller's config is not modified.
- Tests: `monitoring/sentry_test.go`, 5 tests. Each drives both middlewares through a gin engine (200, and a
  panicking handler → 500) and calls `CaptureError`, `CaptureMessage` and `Flush`; the cases are a malformed
  DSN, an empty DSN, a nil config, a nil receiver and a valid-shaped DSN. **Against the old `sentry.go`**:
  `MalformedDSN` fails (`monitor = nil`) and `NilConfig` panics. `go test ./...`: 103 pass, 0 fail, 5 skip
  (was 98).
- **Boot proof after the fix**, the same service-support build with `SENTRY_DSN=not-a-dsn`: the same Warn, no
  panic, `/health` 200. With an empty DSN, `/health` 200 as before.

### Docs — `CONVENTIONS.md` is back, rewritten from the response code as it is today (NIAGA-272)

- infra-platform's API Conformance summary links `lib-common/CONVENTIONS.md` "for the full standard the gate
  enforces". The file was deleted in `f5a7d70`, so the link answered 404.
- It is restored with only what was checked. It covers:
  - §3 the envelope, the nil-`data` rules, and every helper with its status and `error.code`;
  - §4 pagination `meta`;
  - §5 exactly what the gate checks.
  The JSON samples are real output of the helpers, printed by a scratch program. The old sections on URLs,
  casing, middleware, OpenAPI and NATS were not restored unchecked; the file says where the old text is.
- **Two things the old text got wrong:**
  - A success has no `"error": null`, because the key is omitted.
  - An empty page's `meta` has no `total_count` and no `total`, because every `meta` field is `omitempty`:
    `{"page":1,"limit":20,"total_pages":1}`. A client reading `meta.total` gets `undefined`, not 0.
- **The gate asserts less than its summary says.** Its 15 smoke probes check `status 200` and
  `success: true`, and five of them add six `data` assertions between them. None checks `meta` or the error
  shape. §5 says so, and
  infra-platform's summary is corrected in the same ticket.
- `response.go`: one comment corrected. It said `Deleted` answers `data: null`; it sends no `data` key.

### Docs — `events.user.registered` has a second consumer, service-customer (NIAGA-235)

- The README's subject table now lists service-customer next to service-notification. Its new durable consumer
  `customer-events-user-registered` creates the customer's `customers.customers` row, keyed by the user id.
- Until NIAGA-235, service-auth defined the publisher and never called it, so the table's "service-auth" row
  described a constant, not traffic. No code in this repo changes.

### Fixed — an outbox row is published once, whichever services are running (NIAGA-207)

- **Two processors published the same row.** Every service that starts a `Processor` drains the one shared
  `outbox.events` table, and `GetUnprocessedEvents` took no lock. In the new test, two concurrent processors
  over 300 rows published **295 of them twice** on the old code.
- **`Outbox.ProcessNext`** claims ONE row per transaction with `SELECT ... FOR UPDATE SKIP LOCKED`, publishes
  it, marks it processed or failed, and commits. Another processor skips a row in flight. The shared drain
  (any processor may publish any service's row) is kept on purpose and commented; no owner column.
- **One retry policy, and the cap is real.** `processBatch` also had a second pass, `retryFailedEvents`, that
  re-selected failed rows in the same tick, so a failing row was attempted twice per tick. Meanwhile the
  main query had no cap, so a row retried forever. Now a row is tried at most once per tick, only while
  `retry_count < MaxRetries` (default 5). After that it stays in the table with its error, and an ERROR log
  line says so. The retry pass is deleted.
- **Crash semantics are at-least-once**, written down at `ProcessNext`. The mark commits after the publish,
  so a crash between them republishes; JetStream dedupes that by the `Nats-Msg-Id` header.
- `NewProcessor` falls back to the defaults for a zero `BatchSize`, `MaxRetries` or `Interval`. A zero cap
  would have claimed nothing and published silently never. All seven services pass
  `DefaultProcessorConfig()` today, so this changes nothing for them.
- `GetUnprocessedEvents` and `GetFailedEvents` stay, marked `Deprecated`; no service calls either.
- **Tests:** `outbox/processor_pg_test.go`, 5 tests against a real Postgres (`OUTBOX_TEST_DSN`, a scratch
  database; they refuse `niaga_db`):
  - the two-processor race;
  - one attempt per tick for a failing row;
  - a failing head row does not block the rows behind it;
  - a row at the cap is not attempted;
  - a zero config still publishes.
  All 5 fail on `main` and pass here. The race test passed 5 of 5 repeats.
- Whole suite: 103 pass with the DSN, 98 pass and 5 skip without it. gofmt clean on every changed file;
  vet and build clean. service-order, service-support and service-marketplace build against it.

### Changed — repo name and Go module path follow the org naming ruling (HQ-89)

- **The module path is `github.com/niaga-labs/niaga-labs-ecom-lib-common`** (was
  `github.com/niaga-labs/lib-common`); the repo was renamed the same way on 2026-09-13, and GitHub redirects
  the old URL. Every importer must change its import paths and its `require`/`replace` lines: the 10 ecom
  services do in the same change set (HQ-89, one PR each, merged after this one). A service still on the old
  path fails with "module declares its path as … but was required as …" until its own PR lands.

### Fixed — a failed event handler can be retried again (NIAGA-263)

- `IdempotencyChecker.Release(ctx, eventID, consumerName)` gives back the claim `CheckAndMark`
  took, and `CheckAndMark`'s doc now says what it actually is: **the row is written BEFORE the
  handler runs, so it is a CLAIM on the event, not proof the work was done.**

**What was broken, and why nobody saw it.** A handler returned an error, the consumer NAKed,
JetStream redelivered — and `CheckAndMark` reported "already processed", so the message was
**acked without the handler ever running again**. Retry was dead. So was the DLQ, for a second
reason worth stating separately: `NumDelivered` never climbed to the max-deliver threshold,
because the redelivery never reached the handler-error branch that routes there. Meanwhile the
consumer logged `"Handler error, will retry"` — a promise the code could not keep, which is
exactly what kept this invisible in operation.

**Why release rather than mark-after-success.** Marking only on success reopens the window that
mark-first exists to close: two concurrent deliveries of one event would both pass the check and
both process. Keeping the claim and handing it back on failure preserves that guarantee and
restores retry.

**The holes that remain — plural.** A first draft of this entry said "the remaining hole" as
though there were one. (1) A process dying between claiming and releasing strands that one event,
which is strictly better than the previous behaviour where **every** handler error stranded one.
(2) `AckWait` expiring while the handler still runs: the redelivery finds the claim, acks, and
terminates a message the first delivery is still working on — the event is lost and now leaves no
row behind either. (3) `RouteToDLQ` itself failing, where the caller returns without acking or
releasing and nothing redelivers — note it writes the `events.failed` row **before** terminating
the message, so on that error the row may well exist and the event be recoverable. **(2) and (3) predate this change and are not fixed by it**;
they are written down here because the code now invites the reader to think the claim lifecycle is
complete.

**Replay from the DLQ needs the claim deleted first**, or a republished event is acked and skipped
silently. No replay tooling exists in this workspace today — checked — but whoever writes it needs
to know.

**Not released on the DLQ path.** A dead-lettered event is finished, and its claim is what stops
it being picked up again.

#### Proved on the live stack, with the failure created and then removed

`service-inventory` on 8003 against the dev stack, events published through the **real outbox**
(a row in `outbox.events`; the processor publishes it and uses the row id as `Nats-Msg-Id`), so
no test-only publishing path was involved. The failure was a temporary `CHECK` constraint on
`inventory.stock_items` naming one probe product id — a genuine handler error, not a stub.

| what was done | what happened |
|---|---|
| publish, constraint in place | **deliveries 1, 2, 3, 4** each re-ran the handler and failed |
| delivery 5 | **routed to the DLQ**, carrying the real `SQLSTATE 23514` error |
| publish a second event, then **drop the constraint mid-retry** | it had failed 3 times; the next delivery **succeeded** — `Provisioned stock for new product`, row created |
| pre-insert a claim for a fresh event, then publish it | handler **did not run**: provisioned count unchanged, no row, no retry lines |

The first row is the whole fix: before it, delivery 1 was the only time the handler ever ran.
The second row matters as much — the DLQ was **unreachable**, because `NumDelivered` could not
climb while every redelivery was acked as a duplicate. The last row is the control: dedup still
works, which is the property the mark-first design exists to provide.

Probe rows and the temporary constraint were removed afterwards; verified 0 left.

**What the proof actually exercised, stated precisely.** The dedup control was a claim **pre-inserted**
into `events.processed` before publishing, not a success followed by a forced redelivery. That is the
identical database state — same `(event_id, consumer_name)` primary key, same `CheckAndMark`
`RowsAffected == 0` path — so the property is genuinely proved, but it is worth saying which route was
taken. The other half is proved by the count: **7 deletes for 7 handler failures** means `Release` did
**not** fire on the successful delivery, so the claim survived it.

**Scope of the proof, at the right granularity.** Four consumer *services* use `IdempotencyChecker`
(`service-customer`, `service-inventory`, `service-marketplace`, `service-notification`) and all four
were changed — but in this codebase a "consumer" is the durable name written to
`events.processed.consumer_name`, and there are **26** of those: inventory 7, notification 14,
marketplace 4, customer 1. They all share one `dispatch`, so the fix covers all 26; the live run
exercised **one** (`handleProductCreated`). So: **4 services / 26 durable consumers / 1 proved live.**

### Security — golang-jwt bumped: unauthenticated memory exhaustion via the Authorization header (NIAGA-173)

- What changed in **this** repo's `go.mod`, read off the diff:
  - `golang-jwt/jwt/v5` v5.2.1 → v5.2.2
- **GHSA: "jwt-go allows excessive memory allocation during header parsing" — HIGH, CVSS 7.5**, affecting
  `>= 5.0.0-rc.1, < 5.2.2`. `parser.ParseUnverified` splits untrusted input on periods, so an
  `Authorization: Bearer` value made of many periods allocates without bound.
- **This is worse than the pgx advisory fixed alongside it.** pgx needed a database path. This one is
  triggered by *any* unauthenticated HTTP request that reaches the auth middleware, because the middleware
  must parse the header before it can know who is calling — there is no credential to check first.
- **Reachable here directly**: `.go` files in this repo import `golang-jwt/jwt/v5`.
- No code changed — the diff is `go.mod` and `go.sum`. `go build ./...` and `go vet ./...` exit 0.
  `go test ./...` passes **98 tests, 0 failures**. This repo has no `.github/workflows`, so its own CI settles nothing; the seven services with a
  workflow verify the same bump.

### Security — pgx bumped to v5.9.2: two memory-safety criticals and a SQL injection (NIAGA-173)

- `github.com/jackc/pgx/v5` **v5.5.5 → v5.9.2**. It is marked `// indirect` — it comes in under
  `gorm.io/driver/postgres v1.5.9` — but it is the driver that actually talks to Postgres, so it is on the
  data path of every service that uses this library.
- **Three advisories, not the two the ticket named**, and the third moved the target version:
  two memory-safety criticals fixed in **5.9.0**, and *SQL injection via placeholder confusion with dollar
  quoting* fixed in **5.9.2**. Stopping at 5.9.0 would have cleared both criticals and left the injection
  open. Its GitHub severity is *low*; that label decided nothing here.
- **The bump is wider than pgx, and every part of it is `go mod tidy`'s own resolution — nothing hand-edited.**
  What actually changed in **this** repo's `go.mod`, read off the diff rather than assumed:
  - `jackc/pgservicefile` v0.0.0-20221227161230-091c0ba34f0a → v0.0.0-20240606120523-5a60cdf6a761
  - `jackc/pgx/v5` v5.5.5 → v5.9.2
  - `jackc/puddle/v2` v2.2.1 → v2.2.2
  - `x/sync` v0.12.0 → v0.17.0
  - `x/text` v0.23.0 → v0.29.0
  - `go` directive **1.24.0 → 1.25.0**, because pgx v5.9.2 declares `go 1.25.0`.
- **The versions above are this repo's, not a house number.** pgx v5.9.2's own `go.mod` sets a *floor*
  (`testify v1.11.1`, `pgservicefile`, `puddle/v2 v2.2.2`, `x/sync v0.17.0`, `x/text v0.29.0`); what each
  module actually ends up with is whatever MVS resolves across its whole graph, and the repos here differ.
  *An earlier draft of this entry restated pgx's floor as if it were what happened everywhere. It was wrong in
  ten of the eleven repos — claiming dependencies this module does not have, versions that did not move, and
  numbers that did not match `go.mod`. Review caught it; these are now generated from each repo's own diff.*
- **The `go` directive is the part with teeth.** This repo has **no CI of its own** — no `.github/workflows` at all, which `ci-known-red.txt`
  already records — so the toolchain bump is settled by the nine services whose `Test` job passes on the
  matching branch, and locally here by `go build`, `go vet` and the test run below.
- No code changed. `go build ./...` and `go vet ./...` exit 0; `go test ./...` passes **98 tests, 0 failures**
  (76 top-level functions) across **6 of 17** packages — `auth`, `domain`, `eventsourcing`, `nats`, `outbox`,
  `response`. *An earlier draft said "5 of 15" and omitted `auth`. Both numbers were read off a `tail -15` of
  the test output, which had cut the first two lines — a count taken from truncated output is not a count.
  Review caught it.*
- `gofmt -l .` reports 7 files, **all pre-existing** — the identical 7 on `origin/main`, checked by restoring
  that content and re-running, not assumed. NIAGA-34's debt, deliberately untouched.
- **What this does NOT prove.** Nothing here opens a real Postgres connection through
  `gorm.Open(postgres.Open(...))`, and no `.go` file in any of the eleven repos imports `jackc/pgx` directly
  (checked by grep). The gorm → pgx → Postgres path is verified by compilation, not a live round-trip.
- **This repo's own Dependabot feed lists no pgx advisory at all**, unlike the other ten, though alerts are
  enabled here (`vulnerability-alerts` → 204). The 5.9.2 target comes from those ten, not from this repo's
  own feed — said plainly rather than implying it was confirmed here.

### Fixed — the internal-token guard knew about one published placeholder and accepted the other (NIAGA-216)

- **The value the documented setup actually produces was accepted.** `ResolveInternalToken` refused exactly
  two things outside development: an empty token, and `dev-internal-token` — with error text saying why,
  "that value is published in every .env.example". But `infra-platform/.env.example` line 57 does not ship
  that value. It ships `INTERNAL_API_TOKEN=CHANGE_ME_GENERATE_WITH_openssl_rand_base64_32`, which is what an
  operator gets by copying that file and running `docker compose up` without editing the line. Equally
  published, equally guessable, accepted.
- **Measured, not reasoned about.** With `APP_ENV=production`: empty → refused, `dev-internal-token` →
  refused, the CHANGE_ME value → **booted, `/health` 200, no panic**. The failure mode was the exact one the
  guard exists to prevent, with every check reporting healthy, across all five token-consuming services
  (catalog, inventory, order, agent, marketplace).
- **The root cause is two placeholder conventions and one guard that knew about one of them.** The service
  `.env.example` files use `dev-internal-token`; the compose `.env.example` uses `CHANGE_ME_…`. So the fix is
  not another literal: `PlaceholderPrefix` (`CHANGE_ME`, matched case-insensitively) covers that whole
  convention, and generalises to `POSTGRES_PASSWORD`, `JWT_SECRET`, `MARKETPLACE_ENCRYPTION_KEY` and
  `MINIO_ROOT_PASSWORD` if they ever grow guards of their own.
- **A length floor as well, because a list only catches what somebody was already bitten by.**
  `MinInternalTokenLength` is 24. `openssl rand -base64 32` — what every `.env.example` tells the operator to
  run — produces 44 characters, so nothing generated for this purpose is near the floor, and anything under it
  is a placeholder somebody typed. Both new refusals name the problem and the fix, the way the existing two do.
- **The existing two refusals are byte-for-byte unchanged**, and development still accepts every placeholder —
  treating an unset `APP_ENV` as development is deliberate so a fresh clone runs with no setup (NIAGA-210).

### Fixed — EVENTS_CUSTOMER, without which the subject declared last commit had nowhere to land (NIAGA-123)

- **`events.customer.back_in_stock` was declared with no stream to carry it.** `DefaultStreams` had six
  entries — user, order, inventory, catalog, support, marketplace — and **nothing matched
  `events.customer.>`**. Confirmed against the *running* NATS, not just the source: `/jsz` listed exactly
  those six.
- **That is a loud failure, not a silent one, and the difference matters.** `publish()` uses
  `js.PublishMsg`, which waits for a stream to acknowledge. With no stream, service-customer's outbox row is
  written and then **never leaves the outbox**. No data is lost; nothing is delivered either.
- **On "retries forever" — review said that was wrong, and checking showed it right for a different
  reason.** The retry cap (`MaxRetries`, 5) lives on `GetFailedEvents`. The *main* loop uses
  `GetUnprocessedEvents`, whose filter is only `processed_at IS NULL` — **no cap at all** — so a failing row
  is re-attempted every tick indefinitely; the cap only ever limited the *supplementary* attempt. Worth
  getting right, because "stops after 5 and is silently stranded" and "hammers forever" call for opposite
  fixes. Both queries selecting the same row in one tick is now recorded on **NIAGA-207**.
- **It was a real gap between two merged commits.** The subject landed in `46dc5ce` and the publisher in
  service-customer's `14e426e`; from then until this entry, a restock would have produced an undeliverable
  outbox row. Found by checking the stream *before* writing the consumer, which is the order that catches it.
- The test's expected-stream map gains the entry, and it was **mutation-checked**: removing the stream makes
  it fail with `missing default stream EVENTS_CUSTOMER`.
- The comment on the new entry records both halves of the failure mode, because only one of them is loud: a
  **missing stream errors**, while a **consumer bound to a subject nothing publishes is a healthy consumer
  that never fires** (NIAGA-116).
- **`TestEverySubjectDomainHasAStream` derives the check from `eventsourcing.SubjectDomains`** instead of a
  second hand-written list. The map-based test is a fine guard for the entries it names, but it carries the
  flaw it exists to catch: a domain added next month and left out of *that list* is exactly as invisible as
  one left out of `DefaultStreams`. It keeps an explicit exception for `agent` — Reserved, never published
  (NIAGA-117) — so whoever un-reserves that subject must add the stream or justify the exception, rather
  than discovering it the way this ticket did.
- **The map-based test caught a real mistake within minutes of the new one being written.** Adding the
  `MaxAge` rationale comment, my edit **dropped the `Subjects:` line** — leaving a stream that captured
  nothing, which is the precise bug this whole entry is about. `EVENTS_CUSTOMER subjects = [], want
  [events.customer.>]`. Both tests are worth having: the derived one catches a *missing* stream, the map one
  catches a *malformed* one.
- Both were mutation-checked: removing the stream fails each by name.
- Tests: **88 pass, 0 fail** (was 87) — one new.


### Added — events.customer.back_in_stock (NIAGA-123)

- **The subject, its payload and its tests**, so the two repos that publish and consume it have something to
  build against. This is the `lib-common` third of NIAGA-123; service-customer publishes it and
  service-notification consumes it, in that order.
- **It is downstream of `events.inventory.product.restocked`, not a duplicate.** Inventory says a product came
  back; this says a *named customer asked to be told*. The subscription lookup between the two belongs to
  service-customer, so notification never has to know what a subscription is.
- **`CustomerBackInStockPayload` carries everything the email needs** — customer, product, variant, quantity —
  so the consumer never queries another service. A notification consumer that has to look things up fails
  when the other service is down, and produces an email nobody can explain afterwards. The publisher already
  has all of it in hand at the moment it matches the subscription.
- **The wire shape is pinned, not the Go struct** (`~/.claude/rules/go.md`). `TestBackInStockEmitsEveryRequiredKey`
  marshals a populated payload and checks the keys the template depends on; `omitempty` on a required field
  is the bug, because a consumer cannot tell *absent* from *empty*.
- **A zero `stock_quantity` survives.** Zero is meaningful here — the restock that triggered the email has
  already sold out again — and is different from no quantity at all. `omitempty` on that int would erase
  exactly that case.
- **The variant fields really are omitted when absent**, rather than emitted empty: an empty string renders as
  a blank line in a template instead of being skipped by it.
- **`ProductURL` carries the full call-to-action link**, built by the publisher — the convention every other
  templated email here follows (`reset_url`, `verification_url`, `cart_url`). Handing over only a slug would
  make service-notification own the storefront's base URL *and* its `/products/:slug` pattern, so a route
  change would have to be made in a service with nothing to do with the storefront. Raised in review, and
  decided **now** rather than later: service-customer has no storefront base URL configured yet, so adding
  the field after its publisher was built would mean reworking a pinned wire shape.
- **`TestEverySubjectIsRegisteredInSubjectDomains` parses `catalog.go`'s own source** rather than checking a
  hand-maintained list. A list carries the identical flaw it is meant to catch — a subject added next month
  and left out of the *list* is exactly as invisible as one left out of the map. Mutation-checked: removing
  the `SubjectDomains` entry makes it fail by name. It also refuses to pass on a parse that found nothing.
- The new subject sits **above** the `RESERVED` block under its own `IN PROGRESS — NIAGA-123` header. The
  first draft spliced it *inside* that block, so a reader skimming headers would have read it as Reserved —
  the opposite of the truth. Caught in review.
- The README table gains the row, marked **landing** rather than Reserved: Reserved means nobody intends to
  build it; this one is mid-build, and the note says to delete the marker once NIAGA-123 closes. A subject
  stuck between *declared* and *published* is exactly how the four orphaned `events.cart.*` entries happened.
- Tests: **87 pass, 0 fail** (was 81) — 6 new. `go build ./...` and `go vet ./...` clean.

### Added — README with the subject > publisher > consumers table (NIAGA-117)

- **`lib-common` had no README at all.** The ticket's done-when is "the README table is complete"; there was
  no README to complete. One now exists, with the event subject catalog as its centrepiece plus a package
  index, because a public module with no README is its own gap (public since 2026-09-05).
- **Every one of the 24 catalog subjects now has a publisher and a consumer, or says here why not.**
  Measured 2026-09-06 by **two independent methods** — a search for `eventsourcing.<Const>` across every Go
  file in the workspace, and a literal search for each subject *string* in every tracked file of every repo,
  frontends included. The two agree.
- **Three rows are not a simple pair, and each is now written down rather than left to be re-derived:**
  - `events.inventory.stock.updated` has **two** publishers — service-inventory *and* service-order, both
    correctly through the outbox. Not to be confused with NIAGA-178, a separate bare-subject publish in
    service-order that bypasses the outbox and reaches nobody.
  - The two `events.marketplace.sync.*` subjects have a publisher and **no consumer**. Real events on the
    wire that nothing subscribes to — not a defect, and not the same as Reserved.
  - `events.customer.created` and `events.agent.commission.paid` are **Reserved**: declared, never published,
    never consumed, appearing nowhere in the workspace but `catalog.go`.
- **The two Reserved subjects were KEPT, not removed** — the ticket allowed either. Nothing imports them so
  deletion would be safe, but they name planned work (service-customer exists; service-agent is
  legacy-hidden rather than deleted) and the constant is the only surviving record of that intent. Deleting
  costs that and saves nothing; keeping it cheap to remove later.
- **A fourth category the ticket did not name is documented too:** four subjects are routed and handled in
  `service-notification/internal/events/template_router.go` with **no catalog entry, no publisher and no
  consumer** — `events.user.email_verification_requested` and the three `events.cart.abandoned*`. A reader
  would reasonably conclude cart-abandonment email works. It cannot.
- `catalog.go` carries the same facts at the declarations, so someone reading the constants sees them
  without opening the README.
- **A count in the first draft was wrong and review caught it.** The README said lib-common is "consumed by
   the eight Go services". It is **ten** — every `service-*` repo carries the `replace` directive. The "eight"
   is the number of services with a **CI workflow** (`service-marketplace` and `service-support` have none),
   which is the phrasing the workspace's `ci-known-red.txt` uses. Two different sets, one number; both are
   now stated so the conflation does not recur. Fitting error for a document whose premise is "measured, not
   assumed" — it was the one line in it that wasn't.
- **A second stale fact of my own, found while checking the review's reasoning.** The first draft described
  **NIAGA-178** in the present tense — "a bare-subject publish in service-order that bypasses the outbox and
  reaches nobody". It is **fixed and Done**: no bare publish remains anywhere in service-order, and the code
  carries a comment saying what it used to do. Corrected to the past tense, and kept in the README rather
  than dropped, because "service-order publishes stock.updated" is true both before and after the fix — only
  the subject told them apart.
- **This repo's own `CLAUDE.md` was stale in three places** and is corrected here, since the parent epic is
  literally *Docs tell the truth*: it said `CHANGELOG.md` "does not exist yet" (it does), listed **NIAGA-69**
  as blocked on an owner decision (Done — this repo went public on 2026-09-05), and counted **6** test files
  (there are **10**).
- Tests: **81 pass, 0 fail** in this repo (unchanged — this is documentation and comments).
  `go build ./...` and `go vet ./...` clean.

### Added — auth.NewInternalHTTPClient, the calling half of the guard (NIAGA-114)

- The service clients that call `/internal/*` build requests in a dozen places each, mostly through
  `http.Client.Post`, with no shared helper to hang a header on. `InternalTokenTransport` sets
  `X-Internal-Token` in a `RoundTripper` instead, so it covers **every** request a client makes — including
  the ones someone adds later without reading the ticket.
- An empty token sends **no** header, so a misconfigured caller takes a clear 401 from the callee rather than
  presenting an empty credential.
- `RoundTrip` clones the request rather than mutating the caller's, as the `http.RoundTripper` contract
  requires.
- 4 tests, including one that runs the client against a server applying the same rule the middleware does —
  the two halves have to agree or the guard passes nothing.

### Added — auth.InternalToken, a guard for the service-to-service routes (NIAGA-114)

- The `/api/v1/internal/*` routes across four services reserve, deduct and restock inventory, reserve
  flash-sale allocations, approve agent commissions and create marketplace orders. **Not one of them checked
  anything.** `service-inventory`'s block carried the comment *"should be protected by internal
  network/service mesh in production"* and `service-order`'s said *"No auth required - called by marketplace
  service"*. There is no service mesh, and nginx proxies those paths.
- `auth.InternalToken(expected)` requires an `X-Internal-Token` header matching in **constant time**
  (`crypto/subtle`). `auth.ResolveInternalToken(appEnv)` reads `INTERNAL_API_TOKEN`, falls back to the
  published `dev-internal-token` placeholder **in development only**, and returns an error outside it — both
  when unset *and* when it still holds the placeholder, which is in every `.env.example` in the workspace.
  `MustResolveInternalToken` panics, for a `main()` that should not start without one.
- **An unconfigured service authenticates nobody.** An empty expected token refuses every request, including
  an empty header. If it accepted anything when unconfigured, a service that failed to read its environment
  would silently serve stock movements to the world — the shape of NIAGA-170.
- `IsDevEnv` treats an unrecognised `APP_ENV` as production, because guessing wrong in that direction is the
  safe way to be wrong.
- 9 tests: missing, wrong, correct and prefix tokens; the unconfigured case; the dev fallback; the placeholder
  refused outside development; whitespace trimmed.
- Deliberately **not** the database-backed `APIKeyMiddleware` already in this package, which no service uses.
  Callers here are our own services on a private network, and a shared token read from the environment is the
  smallest thing that closes the hole. Per-service keys with scopes remain a later question.

### Added — the order event payload contract (NIAGA-166)

- `eventsourcing/order_payloads.go` defines `OrderCreatedPayload`, `OrderStatusChangedPayload`,
  `OrderEventItem` and `OrderShippingAddress` — the contract for the two order subjects that carry
  customer-facing notifications. It lives here because it is an agreement between two services that never
  import each other, and until now there was nowhere for it to live, so the two sides drifted in **five**
  separate ways at once and every one of them was silent.
- The drift, for the record: the publisher sent `new_status` and the consumer read `status`; `customer_email`
  and `customer_name` were never published; the address was `address` as an object against `shipping_address`
  as a string; items were `{product_id, unit_price}` against `{name, price}`; and the SMS leg was handed an
  email address. `encoding/json` does not error on a field the sender omitted, so nothing failed, nothing
  retried, and nothing reached a dead-letter queue — the order moved to shipped and the customer heard
  nothing.
- `DeliverableTo()` on both payloads, so a consumer can skip and say so instead of calling an email service
  with an empty `To` — which is what `service-notification` did. `OrderShippingAddress.OneLine()` renders an
  address for a mail or SMS body, skipping empty parts.
- 6 tests pin the **wire format**, not the Go structs: every required JSON key must be emitted by a populated
  payload, `status` must not appear where `new_status` is meant, and a round trip must keep the recipient, the
  product names, the unit prices and the phone number. They fail on a rename, which is the change that breaks
  a consumer silently.

### Changed - the Claude layer says Niaga and lists only open units (NIAGA-105)

- `CLAUDE.md` and `.claude/memory/MEMORY.md` are titled **Niaga**, not Desa Murni Batik, and the Jira project
  is **NIAGA**. Ticket keys in both files moved from `DMB-n` to `NIAGA-n` (same issues, the old keys are
  aliases); git branch names keep the `DMB-` they were created with, because those are real refs.
- **The open-units table was wrong, not just stale**: every ticket it listed had already been closed. It now
  lists the tickets that actually name this repo, checked against Jira on 2026-09-03.
- `.claude/memory/project_state.md` gained a `2026-09-03` resume block with the current default branch and
  sha; the previous block was demoted to a checkpoint unedited, so it still reads as what was true that day.

### Changed — module path moved to github.com/niaga-labs (NIAGA-103)

- The GitHub org was renamed `KilangDesaMurniBatik` → **`niaga-labs`**, so the module is now
  **`github.com/niaga-labs/lib-common`**. `go.mod`, the three internal imports in `middleware/` and the
  `CLAUDE.md` header moved with it; the remote was repointed. Consumers change their `require` line and the
  local `replace` target name — the `=> ../lib-common` path itself is unchanged.
- History lines in this file keep the old path on purpose: they record what was true then.

### Fixed — InjectTracingHeaders passed a lock by value (DMB-93)

- `telemetry.InjectTracingHeaders` took `gin.Context` **by value**. `gin.Context` contains a `sync.RWMutex`,
  so every call would have copied a lock — `go vet`: *passes lock by value*. A copied mutex guards nothing,
  because the copy has its own.
- Now takes `*gin.Context`. Every gin handler already holds a pointer, so there was never a reason to take a
  value. Nil-guards `ctx`, `ctx.Request` and `req` while in there.
- **This is a signature change on an exported function**, but nothing calls it — checked every service and
  library in the workspace. It was an unused helper with a latent bug rather than a working one.
- With this, **`go vet ./...` is clean in all eleven Go repos**. It was not before.

### Fixed — empty collections serialised as null (DMB-74)

- **`"data": null` where a list was promised.** `Success`, `SuccessWithMeta` and `SuccessWithPagination` passed
  the handler's value straight to `c.JSON`. A Go repository with no rows returns a **nil slice**, and
  `encoding/json` writes a nil slice as `null` — so `GET /api/v1/inventory/movements` and
  `GET /api/v1/admin/payments` answered `{"success":true,"data":null,...}` on an empty result. Every client
  had to guard for null before calling `.map()`, and two bruno probes had to drop their `data: isArray`
  assertion because of it.
- A nil slice or nil map now becomes an empty one before it is written, so an empty list is `[]` and an empty
  map is `{}`. Fixed here rather than in each handler: all ten services reach `c.JSON` through this package,
  so one change covers every paginated and list endpoint instead of a sweep that would miss the next one.
- Deliberately untouched, each pinned by a test:
  - an **untyped nil** — `Deleted` and friends still answer with no data;
  - a **nil pointer** — "the object you asked for does not exist" is genuinely `null`, and turning it into
    `[]` would be a lie;
  - any non-nil collection, including one that is already empty.

### Added — tests (DMB-74)

- `response/response_test.go`, 6 passing — first tests for this package. They assert on the decoded JSON a
  client actually receives, not on Go values: nil slice through `Paginated` and through `OK`, nil map, a
  populated slice passing through unchanged, a nil pointer staying `null`, and `Deleted` unaffected.

### Changed

- **Module path moved to the surviving org.** `github.com/niaga-platform/lib-common` is now
  `github.com/KilangDesaMurniBatik/lib-common`, along with every import that named the old org — 3 Go
  files here. Two GitHub orgs held the same product; `niaga-platform` was last touched in
  December 2025 and is retired, so `KilangDesaMurniBatik` is the only one. Nothing outside `.go` referenced
  the old path, so this is a pure import rename: no behaviour change, no dependency change. The
  `replace` directive still points at `../lib-common`; how services depend on it is DMB-72. (DMB-71)

### Notes

- **`eventsourcing` could not be run on the Windows dev laptop after the rename.** `go build` and
  `go vet` are clean and the test binary compiles, but Windows Application Control refuses to launch
  it: *"An Application Control policy has blocked this file"*, and running the compiled `.exe`
  directly gives `Permission denied`. It is specific to that one binary and reproducible — the other
  three packages rebuild and pass, the same package passes on the pre-rename source, and renaming the
  output file does not help, so it is a content-keyed reputation false positive rather than anything
  about the test. Nothing but an import path string changed, and the block happens at `fork/exec`
  before the test starts, so no test logic is involved. Linux CI is unaffected. Tracked in DMB-76.
- Shared Go library consumed by every service as a module dependency; it has no `cmd/` and no port.
- 6 `*_test.go` files — the only repo besides `service-marketplace` with any tests at all.
- Owns `events` and `outbox` in `niaga_db` (the transactional outbox the services publish through).
