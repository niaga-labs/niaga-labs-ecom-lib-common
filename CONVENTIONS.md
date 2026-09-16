# Niaga API conventions: the response envelope

The response standard every Niaga Go service follows, and exactly what the API Conformance gate checks of it.
infra-platform's `api-conformance.yml` links here.

Restored 2026-09-16 (NIAGA-272). The first version (`c88b3d0`, "the v1 API standards source of truth") was
deleted in `f5a7d70` with every other `.md` file, and the gate kept linking to it. This version is rewritten
from `response/response.go` as it is today. Every JSON sample below is real output of those helpers, not a
sketch.

## 1. Scope

This file covers the response envelope (§3), pagination `meta` (§4) and what the gate checks (§5).

The old file also had rules on URL prefixes, JSON casing, path params, the middleware chain, health endpoints,
OpenAPI, pre-commit guards and NATS events. They were **not** restored, because nobody has re-checked them against
the code since the deletion. `git show f5a7d70^:CONVENTIONS.md` has the old text. For NATS subjects, the audited
catalog is the table in `README.md` (NIAGA-117).

## 2. The code is the standard

`response/response.go` is the source of truth; this file describes it. If the two disagree, the code wins and
this file is the bug.

## 3. Response envelope

Every public API response goes through a helper in `lib-common/response`. A handler that writes
`c.JSON(status, gin.H{...})` itself is off the standard. **Nothing enforces this**: the gate (§5) checks
responses, not source.

Top-level fields, all `omitempty` except `success`:

| Field | Type | Present when |
|---|---|---|
| `success` | bool | always |
| `message` | string | the helper was given a non-empty message |
| `data` | any | success with non-nil data; see the nil rules below |
| `error` | object | failure only |
| `meta` | object | paginated success only (§4) |

Success, from `response.OK(c, "", []string(nil))`:

```json
{"success":true,"data":[]}
```

Failure, from `response.ValidationError(c, "bad", map[string]string{"email":"required"})`:

```json
{"success":false,"error":{"code":"VALIDATION_ERROR","message":"bad","details":{"email":"required"}}}
```

- **A success carries no `error` key, and a failure carries no `data` key.** They are omitted, not `null`.
- `error.details` is omitted when the helper passes nil.

**Nil `data`:**

- A nil slice or map becomes `[]` / `{}` (`emptyCollection`), so a list endpoint never answers `null` for "none".
- An untyped `nil` is omitted entirely. `response.Deleted(c, "gone")` answers `{"success":true,"message":"gone"}`,
  with no `data` key.
- A **typed** nil pointer is kept and answers `"data":null` (`response.OK(c, "", (*Order)(nil))` gives
  `{"success":true,"data":null}`). Inside an `interface{}` it is not nil, so `omitempty` keeps it. That is
  deliberate: "the object you asked for does not exist" is genuinely null and must not become `[]`.

**Helpers**, with the status and `error.code` each one sends:

| Helper | HTTP | `error.code` |
|---|---|---|
| `OK(c, msg, data)` · `List` · `Updated` | 200 | — |
| `Created(c, msg, data)` | 201 | — |
| `Deleted(c, msg)` | 200 (no `data`) | — |
| `NoContent(c)` | 204, no body | — |
| `Paginated(c, data, page, limit, total)` · `SuccessWithPagination` | 200 | — |
| `Success` / `SuccessWithMeta(c, status, …)` | as given | — |
| `BadRequest(c, msg, details)` | 400 | `BAD_REQUEST` |
| `Unauthorized(c, msg)` | 401 | `UNAUTHORIZED` |
| `Forbidden(c, msg)` | 403 | `FORBIDDEN` |
| `NotFound(c, msg)` | 404 | `NOT_FOUND` |
| `Conflict(c, msg)` | 409 | `CONFLICT` |
| `ValidationError(c, msg, details)` | 422 | `VALIDATION_ERROR` |
| `TooManyRequests(c, msg, details)` | 429 | `RATE_LIMIT_EXCEEDED` |
| `InternalServerError(c, msg)` | 500 | `INTERNAL_SERVER_ERROR` |
| `ServiceUnavailable(c, msg)` | 503 | `SERVICE_UNAVAILABLE` |
| `Error(c, status, code, msg, details)` | as given | as given |

`ErrorTranslator` (with `DefaultOrderErrorTranslator`) maps domain error text to one of these, so an internal
error message does not reach the client.

## 4. Pagination `meta`

`response.Paginated(c, items, page, limit, total)` computes `total_pages` as `ceil(total / limit)`, at least 1.

Page 2 of 87 at 20 a page:

```json
{"success":true,"message":"Data retrieved successfully","data":["a"],"meta":{"page":2,"limit":20,"total_pages":5,"total_count":87,"total":87}}
```

- `total` is the same number as `total_count`, kept for the frontends.

**An empty result has no count in `meta`.** Every `meta` field is `omitempty`, so a zero count is dropped:

```json
{"success":true,"message":"Data retrieved successfully","data":[],"meta":{"page":1,"limit":20,"total_pages":1}}
```

A client must read a missing `total_count` / `total` as 0. `meta.total` on an empty list is `undefined`, not
`0`.

## 5. What the API Conformance gate checks

infra-platform `.github/workflows/api-conformance.yml`, job "Bruno smoke + envelope assertion", runs `_auth` and
then the nine `bruno-tests/<service>/smoke` folders against the compose stack. Counted on bruno-tests `main`
on 2026-09-16, after NIAGA-321:

- **16 smoke probes** (`_auth` login plus the nine folders). Every one asserts `res.status eq 200` and
  `res.body.success eq true` — except the one deliberate failure probe below.
- **Paginated list probes assert the `meta` shape** (§4). Five probes hit a `Paginated` endpoint and now check
  `meta.page`, `meta.limit` and `meta.total_pages` are numbers:
  - service-agent `01-list-agents` and `02-list-commissions`;
  - service-catalog `01-list-products`;
  - service-inventory `01-list-warehouses`;
  - service-support `02-list-tickets`.
  The three whose seed guarantees rows (products 5, warehouses 3, tickets 19 — measured live) also assert
  `meta.total_count` and `meta.total` are numbers. **An empty list omits those two** (§4), so a probe whose
  list can be empty must not assert them — that is why the agent probes, and the non-paginated list probes
  (customer addresses, order orders, support categories, marketplace connections: plain `data`, no `meta`),
  do not.
- **A failure probe checks the error envelope**: service-inventory `03-get-warehouse-not-found` GETs the
  all-zero UUID and asserts `res.status eq 404`, `res.body.success eq false` and `res.body.error.code eq
  NOT_FOUND`. Before NIAGA-321 nothing exercised a non-2xx response, so the whole error envelope was
  unchecked by the gate.
- Six `data` assertions remain as before (service-customer `01`/`02`, service-catalog `02-get-product`,
  service-inventory `02-get-warehouse`, service-auth `01-get-me` ×2).
- **Not checked by the gate:**
  - whether a handler used a helper at all;
  - `meta.total` / `total_count` on a list that can be empty (deliberately, per §4).

Proved live on 2026-09-16 (dev-infra stack + nine services from source): `bru run _auth <nine smoke folders> -r
--env Local` → **17 requests, 63/63 assertions, PASS** (was 16 requests, 39 assertions before NIAGA-321). The
new `meta` assertion was mutation-checked: pointing the warehouses probe at a no-`meta` endpoint
(`/api/v1/support/categories`) fails it, 8/9.

So a green gate means: every smoke endpoint answered its expected status — 200 with `success: true`, or the one
documented 404 with `success: false` — and every paginated list carried a numeric `meta`. §3 and §4 are the
standard; §5 is now most of the part that is enforced.
