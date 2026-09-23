# Niaga — lib-common

Shared Go library for every Niaga service: config loading, database and NATS helpers, auth middleware, logging, the transactional outbox and event sourcing.
Jira project **NIAGA** · GitHub `niaga-labs/niaga-labs-ecom-lib-common` · Go 1.25.0 · module `github.com/niaga-labs/niaga-labs-ecom-lib-common`.
Library, not a service: no `cmd/`, no port, no database of its own.
Owns `events`, `outbox` in `niaga_db` (3 tables referenced by `TableName()`).

Global rules live in `~/.claude/`; this file only adds what is specific here.

## Orient here first

- `.claude/memory/project_state.md` — **resume here** (`/continue` reads it, `/recap` rewrites it).
- `CHANGELOG.md` — what changed.
- `README.md` — package index and the **event subject catalog** (subject > publisher > consumers), NIAGA-117.
- `CONVENTIONS.md` — the response envelope and pagination `meta` as `response/` emits them, and exactly what
  infra-platform's API Conformance gate checks (NIAGA-272). The gate's summary links it.
- `../CLAUDE.md` — the workspace repo map and the cross-repo change order.
- `../infra-platform/docs/LOCAL_DEV.md` — bringing the whole platform up locally.

## Commands

| Task | Command |
|---|---|
| install | `go mod download` |
| build | `go build ./...` |
| test | **In the container** (HQ-22) — `cd ~/Documents/niaga-labs/dev-infra && ./dev.ps1 gotest <ABSOLUTE-PATH-TO-THIS-REPO> "test" "./..." "-v" "-count=1"`. Native `go test ./...` still runs, but Smart App Control blocks a freshly linked test binary at random and reports it as a FAIL (NIAGA-76), so a red from it is not evidence about the code — **quote container counts, never native ones**. Quote every go argument: an unquoted `-v` is swallowed by `dev.ps1`, never reaches `go test`, and a green run prints `PASS: 0`. |
| lint | `gofmt -l . && go vet ./...` |


## Conventions that differ from the global rules

- **CI covers the integration-tagged tests (NIAGA-338).** The `Integration` job in `ci.yml` calls lib-common's `go-integration.yml` and runs `go test -tags integration ./...` against a throwaway Postgres with the canonical schema and the E2E seeds, one database per repo, so tests that need a warehouse or a category run instead of skipping. It needs the org secret `WORKSPACE_PAT` (NIAGA-199).
- **`infra-database/schemas/` owns the schema, not this repo.** A schema change is a migration there plus a
  re-export, never an `AutoMigrate` and never a hand edit of a dumped schema file.
- Default branch is `main`.
- Protected paths (never edited in place, see `.claude/protected-paths.txt`): `migrations/*.sql`.

## Where things are

- Entry point: no `cmd/` — this is a library
- Packages: `auth`, `config`, `database`, `domain`, `eventsourcing`, `lock`, `logger`, `middleware`, `monitoring`, `nats`
- Config: `config/`
- Tests: **16** `*_test.go` files across `auth`, `bizday`, `domain`, `eventsourcing`, `monitoring`, `nats`, `outbox` and
  `response` (counted with `find` 2026-09-24). Measured 2026-09-24 in the container with `-json`: **118 runs pass, 0 fail,
  5 skip** by default (was 89 top-level + 22 subtests on 2026-09-17). Plus **6** `-tags integration` claim tests in
  `eventsourcing/idempotency_claim_integration_test.go` (NIAGA-357), which need `DB_HOST` on the dev network.
  The 5 skips are `outbox/processor_pg_test.go`, which needs real `SKIP LOCKED`, runs only with
  `OUTBOX_TEST_DSN` pointing at a scratch Postgres database, and refuses to run against `niaga_db` (NIAGA-207).
  It was not re-run with the DSN on 2026-09-16.

## Open units

| Ticket | State | Blocked on | Note |
|---|---|---|---|
| NIAGA-69 | **Done** | — | CI could not resolve this module from a service repo. Settled by the owner ruling of 2026-09-05: **this repo is public**, so a workflow checks it out with no token. The eight services with CI workflows now do exactly that |
| NIAGA-117 | **Done** | — | the subject catalog is audited and documented in `README.md` — 24 subjects, each with a publisher and consumers or a stated reason it has neither. service-marketplace needed no change: it already published on the canonical constants |
| NIAGA-76 | To Do | owner | Windows blocks the `eventsourcing` test binary after the rename |
| NIAGA-151 | To Do | — | deep review of this repo and `service-auth` |
