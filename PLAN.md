# Job Application Tracker — Implementation Plan

> Working plan. Steps are ordered by dependency and ticked off only after their
> verification command passes. During implementation this file is updated after
> every completed step, with a short entry in the Progress Log.

## Status

- Current phase: **M4 complete — next 5.1 (settings service and page)**
- Plan revision: **2026-11-21 — migrations switched from a custom runner to goose v3.30.1**
- Repo: `/home/ryan/Projects/job-application-tracker` (git initialized, branch `main`)
- Plan mode restriction: lifted. This file is the live checklist.

## Locked decisions

| Area             | Decision                                                                                                         |
| ---------------- | ---------------------------------------------------------------------------------------------------------------- |
| Frontend         | Go `html/template` + htmx + vendored Pico.css. No Node/build step.                                               |
| Backend          | Go stdlib `net/http` (1.22+ method routing), thin handlers over a service layer.                                 |
| Repository layer | **sqlc** (`v1.31.1`, installed) generating from `internal/store/migrations` + `internal/store/queries`.          |
| DB               | SQLite via `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`), WAL, FK on, single writer.                           |
| Migrations       | **goose v3.30.1** as a library; embedded Up-only SQL files; `goose_db_version`.                                  |
| Timezone         | **`Africa/Harare`** (UTC+2, no DST). Overridable via `TZ`.                                                       |
| Files            | `DATA_DIR/files/<random-key>`; metadata in `documents`. `DATA_DIR` is the backup unit.                           |
| Auth             | Single admin password (argon2id), signed session cookie. `AUTH_DISABLED` opt-out, guarded by `TRUST_PROXY_AUTH`. |
| Stale windows    | applied 14, screening 7, interviewing 7, offer 3 days; per-app `follow_up_on` override.                          |
| Job ad           | Upload or paste only. URL fetching is future work (documented, not built).                                       |
| Deploy           | `compose.yaml` with app + `./data` volume, bound for an existing TLS reverse proxy.                              |
| Image            | `gcr.io/distroless/static-debian12:nonroot`, static binary, ~10–12 MB compressed.                                |
| Module path      | `github.com/rgmaz/job-application-tracker`                                                                       |
| User model       | Single user. No `users` table.                                                                                   |

### Verified in planning

- `sqlc v1.31.1` on this machine supports `engine: sqlite`, `sqlc.embed`,
  named args (`@name`), `RETURNING *`, `ON CONFLICT ... DO UPDATE`, and
  `?1` positional reuse. Selecting `a.*` produces flat row structs.
- `goose v3.30.1` (requires Go 1.26) is the current release. A scratch probe
  confirmed sqlc v1.31.1 parses goose-style `-- +goose Up` migration files, so
  Up-only migrations can also serve as the sqlc schema source.
- Go 1.27.4, Node 26, sqlc, sqlite3, make, git, curl available.
- Host is Arch Linux; user `ryan` is in `wheel`. Docker 29.10.0 was installed
  during M0.1 (see checklist).

## Architecture

```
cmd/server/                 main; subcommands serve|migrate|backup|healthcheck
internal/config/            env parsing, defaults, validation
internal/timeutil/          Harare clock, date helpers, embedded tzdata
internal/store/             Open(), pragmas, goose runner
internal/store/migrations/  goose Up-only SQL (also the sqlc schema source)
internal/store/queries/     sqlc query files
internal/store/db/          sqlc-generated code (committed)
internal/model/             status/source enums, labels, terminality
internal/service/           applications, documents, settings, auth
internal/web/               router, handlers, middleware, render helpers
internal/files/             upload validation, hashing, storage
web/templates/              layout + pages + htmx partials
web/static/                 htmx.min.js, pico.min.css, app.css, app.js
Dockerfile, compose.yaml, Makefile, README.md, .dockerignore, .gitignore
```

## Data model (goose migration 00001)

Dates that represent calendar days are stored as `YYYY-MM-DD` **in Harare local
time**; instants are stored as Unix epoch seconds. This keeps stale arithmetic
timezone-free (`date(x, '+N days')`) and display simple.

```sql
applications(
  id INTEGER PRIMARY KEY,
  company TEXT NOT NULL,
  role TEXT NOT NULL,
  location TEXT,
  job_url TEXT,
  source TEXT NOT NULL DEFAULT 'other',
  applied_on TEXT NOT NULL,                -- YYYY-MM-DD, Harare
  status TEXT NOT NULL DEFAULT 'applied'
    CHECK (status IN ('applied','screening','interviewing','offer',
                      'accepted','rejected','withdrawn','ghosted')),
  status_changed_on TEXT NOT NULL,         -- YYYY-MM-DD, Harare
  status_changed_at INTEGER NOT NULL,      -- unix seconds
  follow_up_on TEXT,                       -- optional YYYY-MM-DD override
  contact_name TEXT,
  contact_email TEXT,
  email_subject TEXT,
  email_link TEXT,
  notes TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
)

documents(
  id INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('job_ad','cv','cover_letter','email','other')),
  original_name TEXT NOT NULL,
  mime TEXT NOT NULL,
  size INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  storage_key TEXT NOT NULL,
  uploaded_at INTEGER NOT NULL
)

events(
  id INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  type TEXT NOT NULL CHECK (type IN ('status_change','note','email_sent',
                                     'email_received','document_added')),
  body TEXT,
  occurred_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
)

status_thresholds(status TEXT PRIMARY KEY, days INTEGER NOT NULL CHECK (days > 0))
settings(key TEXT PRIMARY KEY, value TEXT NOT NULL)
```

goose creates and owns its `goose_db_version` table; there is no hand-written
migration bookkeeping table.

Staleness is derived, never stored:

```sql
due_on = COALESCE(a.follow_up_on, date(a.status_changed_on, '+' || t.days || ' days'))
is_stale = t.days IS NOT NULL AND due_on <= @today
```

Terminal statuses have no `status_thresholds` row, so `t.days IS NULL` makes
them never stale. `@today` is computed once per request in `Africa/Harare`.

## Migration workflow

- Migrations live in `internal/store/migrations/` and follow goose's sequential
  naming: `00001_init.sql`, `00002_seed_thresholds.sql`, and so on.
- Each file starts with `-- +goose Up` and contains **no Down section**. sqlc
  parses the same files as its schema, so a Down section would drop the tables
  sqlc is trying to read. Rollback is restore-from-backup, matching the
  additive-only stance below.
- `jat serve` applies pending migrations before listening; `jat migrate` runs
  them explicitly (useful in CI or before a backup).
- Never edit an applied migration; add a new numbered file.
- goose is embedded as a library (`goose.SetBaseFS` + `goose.UpContext`), so the
  container needs no extra binary. The goose CLI can still be installed locally
  for `status`/`down` debugging against a copy of the database.

## Configuration surface

```
BIND_ADDR=0.0.0.0          # 127.0.0.1 when a local proxy fronts the app
PORT=8080
DATA_DIR=./data            # compose mounts /data
BASE_URL=https://jobs.example.com
TZ=Africa/Harare
AUTH_DISABLED=false
TRUST_PROXY_AUTH=false
COOKIE_SECURE=             # unset: derived from BASE_URL scheme
MAX_UPLOAD_MB=25
SESSION_TTL_HOURS=720
```

Env values are startup defaults; `status_thresholds` and the password hash live
in the DB and are editable in Settings. Validation refuses
`AUTH_DISABLED=true` unless `TRUST_PROXY_AUTH=true` or the bind address is
loopback.

## Milestones

### M0 — Host tooling (first action after leaving Plan mode)

- [x] **0.1 Install and configure Docker**
    - Targets: host system only (no repo files)
    - Changes: `sudo pacman -Syu --needed docker docker-compose docker-buildx`
      (~56 MiB download); `sudo systemctl enable --now docker`;
      `sudo usermod -aG docker ryan`; apply group with a re-login or `newgrp docker`.
    - Verify: `docker version` (client+server) and
      `docker run --rm hello-world` without sudo.
    - Risk: host-level change. Rollback: `sudo pacman -Rns docker docker-compose docker-buildx`,
      remove the docker group membership.
    - **Blocked:** the agent shell is non-interactive and sudo requires a password.
      Run the four commands manually in a terminal, then tell the agent to verify.
    - **Done:** Docker 29.10.0 and Compose 5.8.0 installed; `docker` and
      `containerd` active; `hello-world` passed through `newgrp docker`.
- [x] **0.2 Confirm toolchain**
    - Verify: `sqlc version` = v1.31.1, `sqlite3 --version`, `go version`.
- [x] **0.3 Initialize repository**
    - Targets: `.git`, `.gitignore`, `.editorconfig`
    - Changes: `git init -b main`; ignore `data/`, `*.db`, `*.db-wal`, `*.db-shm`, `.env`, `/tmp`.
    - Verify: `git status --short` shows only intended files.

### M1 — Scaffold, config, timezone, container

- [x] **1.1 Go module and dependencies**
    - Targets: `go.mod`, `go.sum`
    - Changes: `go mod init github.com/rgmaz/job-application-tracker`; add
      `modernc.org/sqlite v1.62.0`, `golang.org/x/crypto v0.59.0`; `go 1.27.4`.
    - Verify: `go build ./...` (empty tree compiles).
- [x] **1.2 Config package**
    - Targets: `internal/config/config.go`, `internal/config/config_test.go`
    - Changes: typed `Config`, env parsing with defaults, `time.LoadLocation`,
      validation (port, dirs, BASE_URL, auth/proxy guard, upload cap).
      Added `BIND_ADDR` (default `0.0.0.0`) and derived `COOKIE_SECURE`.
    - Verify: `go test ./internal/config -run TestLoad`.
- [x] **1.3 Time utilities**
    - Targets: `internal/timeutil/timeutil.go`, `internal/timeutil/timeutil_test.go`
    - Changes: `_ "time/tzdata"`; `Clock` with `Now()`, `Today()` (Harare date
      string), `FormatDate`, `FormatDateTime`; override location for tests.
      Added `DateLayout`/`DateTimeLayout`; nil location falls back to UTC.
    - Verify: `go test ./internal/timeutil` including a UTC-boundary case
      (e.g. 23:30 UTC on 2026-01-31 is 2026-02-01 in Harare).
- [x] **1.4 Entrypoint and subcommands**
    - Targets: `cmd/server/main.go`, `cmd/server/serve.go`,
      `cmd/server/healthcheck.go`, `internal/web/router.go`
    - Changes: `serve` (bind, explicit timeouts, graceful shutdown, `slog`),
      `healthcheck` (GET `http://127.0.0.1:$PORT/healthz`, probing loopback
      when bound to `0.0.0.0`/`::`); `migrate`/`backup` stubs that fail loudly
      until M2/M6; `GET /healthz` returning `{"status":"ok"}`.
    - Verify: `go run ./cmd/server serve` then `curl -fsS localhost:8080/healthz`;
      `healthcheck` exits 0; SIGTERM shuts down cleanly.
- [x] **1.5 Container assets**
    - Targets: `Dockerfile`, `.dockerignore`, `compose.yaml`, `.env.example`, `Makefile`
    - Changes: multi-stage `golang:1.27-alpine` → distroless static nonroot;
      `CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"`; `VOLUME /data`;
      `HEALTHCHECK CMD ["/app/app", "healthcheck"]`; compose sets
      `TZ=Africa/Harare`, `./data:/data`, `127.0.0.1:8080:8080`,
      `restart: unless-stopped`; `Makefile` targets for `build`, `test`,
      `generate`, `migrate`, `run`, `docker`, `sqlc-check`, `lint`.
    - Verify: `docker build -t jat:dev .` → 3.9 MB compressed; `docker compose
up -d` → `curl /healthz` returns `{"status":"ok"}`, health status becomes
      `(healthy)`; `docker compose down -v` cleans up.

### M2 — Schema, goose, sqlc, store

- [x] **2.1 goose migrations**
    - Targets: `internal/store/migrations/00001_init.sql`,
      `internal/store/migrations/00002_seed_thresholds.sql`
    - Changes: goose Up-only files (`-- +goose Up`); tables/indexes above; seed
      applied/screening/interviewing/offer thresholds with `INSERT OR IGNORE`.
      No Down section: sqlc parses the same files and a Down section would drop
      the tables it is trying to read.
    - Verify: `sqlite3 /tmp/jat.db < internal/store/migrations/00001_init.sql`
      then `.schema`; applying 00002 twice is idempotent. Both pass: five tables
      plus six indexes, four seeded thresholds, and the `days > 0` CHECK
      rejects a zero-day row.
- [x] **2.2 sqlc query files**
    - Targets: `internal/store/queries/applications.sql`, `events.sql`,
      `documents.sql`, `settings.sql`
    - Changes:
        - Applications: `CreateApplication :one RETURNING *`, `GetApplication`,
          `ListApplications :many` (named filters `@today`, `@status_filter`,
          `@source_filter`, `@q`, `@stale_only`, `@limit`, `@offset`, computed
          `due_on`/`is_stale`), `UpdateApplication`, `UpdateStatus`,
          `DeleteApplication`, `DashboardCounts :one` with `COALESCE(SUM(...),0)`.
        - Events: `CreateEvent`, `ListEvents`, `DeleteEvent`.
        - Documents: `CreateDocument`, `ListDocuments`, `GetDocument`, `DeleteDocument`.
        - Settings: `GetSetting`, `ListSettings`, `UpsertSetting`, `ListThresholds`,
          `UpsertThreshold`.
        - Optional filters use `""`/`0` sentinels plus explicit `CAST(...)`;
          `sqlc.narg` types as `interface{}` under the SQLite engine. Aggregate
          sums are cast to `INTEGER` for the same reason. Deletes are
          `:execrows` so services can distinguish "not found".
    - Verify: `sqlc generate` exits 0 (checked in 2.3); generated SQL was also
      run against a seeded temp DB: stale/terminal/follow-up-override rows,
      status/source/q filters, pagination, dashboard counts, and cascade delete
      all behaved as specified.
- [x] **2.3 sqlc configuration and generation**
    - Targets: `sqlc.yaml`, `internal/store/db/*.go` (generated)
    - Changes: `version: "2"`, `engine: sqlite`, schema
      `internal/store/migrations`, queries `internal/store/queries`, out
      `internal/store/db`, package `db`, `emit_json_tags`,
      `emit_pointers_for_null_types`, `emit_empty_slices`.
    - Verify: `sqlc generate && git diff --exit-code internal/store/db` is clean
      after committing generated output. A planning probe already confirmed sqlc
      v1.31.1 parses goose-style Up-only files (markers are comments).
- [x] **2.4 Store bootstrap and goose runner**
    - Targets: `internal/store/store.go`, `internal/store/migrate.go`,
      `cmd/server/migrate.go`, `cmd/server/serve.go`, `go.mod`
    - Changes: `go mod tidy` promotes goose and modernc to direct requirements;
      `Open(ctx, path)` creates the parent directory and uses a `file:` DSN with
      `_pragma` (WAL, `busy_timeout=5000`, `foreign_keys=1`,
      `synchronous=NORMAL`) plus `SetMaxOpenConns(1)`; `Path(dataDir)` resolves
      `<DATA_DIR>/app.db`; `//go:embed migrations/*.sql` in `migrate.go`;
      `goose.SetBaseFS`, `goose.SetDialect("sqlite3")`,
      `goose.UpContext(ctx, db, "migrations")`. goose owns `goose_db_version`;
      no custom `schema_migrations` table. goose's base FS, dialect, and logger
      are initialized once via `sync.Once` so parallel store tests stay
      race-free. `jat migrate` opens and migrates; `jat serve` migrates before
      binding.
    - Verify: `go test ./internal/store -run 'TestMigrate|TestOpen'` covers
      pragmas, parent-dir creation, idempotency, and parallel databases;
      `jat migrate` twice leaves `max(version_id)` = 2; `jat serve` serves
      `/healthz` with version 2 already applied; `go test -race ./...` and
      `go vet ./...` are clean.
- [x] **2.5 Store integration tests**
    - Targets: `internal/store/store_test.go`
    - Changes: `newTestStore` helper opens a temp-file DB, runs goose, and binds
      `db.New`; `createApplication` / `listApplications` helpers keep fixtures
      terse. Coverage: application create/get/update/status/delete including
      missing-row `sql.ErrNoRows` and `:execrows` counts; list status, source,
      case-insensitive text, stale-only, limit, and offset filters; derived
      `due_on` / `is_stale` with a fixed `2026-11-22` reference date covering the
      exact-threshold boundary, `follow_up_on` overrides in both directions,
      terminal-status immunity, and the 3-day offer threshold; cascade delete of
      events and documents plus an explicit foreign-key violation; dashboard
      counts including the empty-database zero case; settings and threshold
      upserts; event and document ordering and deletes. The existing migrate
      tests already assert `goose_db_version` reaches version 2.
    - Verify: `go test -race ./internal/store` and `go test -race ./...` pass;
      `go vet ./...`, `gofmt -l .`, and `sqlc generate` are clean.
- [x] **2.6 Makefile**
    - Targets: `Makefile`
    - Changes: `generate` (sqlc), `build` (static `CGO_ENABLED=0`), `test`
      (`go test -race ./...`), `lint` (`go vet` plus a `gofmt -l` gate that
      actually fails), `run`, `migrate`, `docker`, and `sqlc-check`. `BIN` is
      used verbatim so `make build BIN=bin/jat` works; `clean` removes it.
    - Verify: `make test`, `make generate`, `make lint`, `make sqlc-check`,
      `make build`, and `make clean` pass with no unexpected diff; a deliberately
      unformatted file makes `make lint` exit non-zero.

### M3 — Applications and timeline

- [x] **3.1 Domain helpers**
    - Targets: `internal/model/status.go`, `internal/model/status_test.go`
    - Changes: `Status` and `Source` string types; ordered `Statuses` /
      `Sources`; label lookup with raw-value fallback; `IsTerminal` covering the
      four final statuses; `Valid` and `ParseStatus` / `ParseSource` returning
      `ErrInvalidStatus` / `ErrInvalidSource` for handler-to-422 mapping.
      Sources: `linkedin`, `referral`, `company_site`, `job_board`, `recruiter`,
      `other`.
    - Verify: `go test ./internal/model` covers ordering, uniqueness, labels,
      terminality, and strict (case-sensitive) parsing.
- [x] **3.2 Application service**
    - Targets: `internal/service/applications.go`, `internal/service/applications_test.go`
    - Changes: `ApplicationService` wraps the generated queries and a small
      `Clock` interface (`Now`/`Today`) so tests pin Harare dates. `Create`
      defaults `applied_on` to today, defaults status/source, validates required
      fields, strict `YYYY-MM-DD` dates, status, and source, then inserts the
      application and first `status_change` event in one transaction. `Update`
      edits non-status fields and clears empty optionals. `ChangeStatus`
      validates, sets `status_changed_on/at`, appends the `old → new` event in
      one transaction, and treats the current status as a no-op so the follow-up
      clock is not reset. `Delete` collects document storage keys before the
      cascade and returns them for post-commit file cleanup. `List` validates
      filters, trims the query, maps `StaleOnly`, defaults/clamps the page size
      (50/200), and uses today's Harare date. `Dashboard` uses the same date.
      `ValidationError.Fields` carries per-field messages for the web layer.
    - Verify: `go test ./internal/service -run TestApplication` covers defaults,
      optional fields, multi-field validation with no partial writes, get,
      update, status transitions/no-op/invalid, delete keys and cascade,
      list filters/pagination, and dashboard counts; race/vet/gofmt/sqlc clean.
- [x] **3.3 Layout, assets, render helper**
    - Targets: `web/templates/layout.html`, `web/templates/_flash.html`,
      `web/static/{htmx.min.js,pico.min.css,app.css,app.js,*.LICENSE}`,
      `web/embed.go`, `internal/web/render.go`, `internal/web/render_test.go`
    - Changes: a `webassets` package embeds `templates/` and `static/` because
      `go:embed` cannot reach parent directories. Vendored htmx 2.0.11 and Pico
      2.1.1 (with their licenses) keep the image offline; `app.css` adds
      flash/badge/stale/timeline styles and `app.js` handles `data-confirm`.
      The layout has nav with active state, a flash partial, and a default
      `content` block. `Renderer` parses the layout plus partials once per page
      so each page can define `content` without colliding. Template funcs:
      `fmtDate`, `fmtDateTime` (string/unix/time, Harare display), `statusLabel`,
      `isTerminal`. Rendering goes through a buffer so execution errors never
      emit a partial page. Added `timeutil.Clock.Location()` for display
      formatting.
    - Verify: `go test ./internal/web -run TestTemplatesParse` renders the layout
      with nav, flash, asset links, and default content; `TestTemplateFuncs`
      covers dates, labels, and terminality; `TestStaticAssetsEmbedded` confirms
      all four assets are non-empty. Full race/vet/gofmt/sqlc checks clean.
- [x] **3.4 Application handlers**
    - Targets: `internal/web/router.go`, `internal/web/applications.go`,
      `internal/web/applications_test.go`,
      `web/templates/applications_{list,form,detail}.html`,
      `web/templates/_application_rows.html`,
      `web/templates/_application_form_fields.html`,
      `web/templates/_timeline.html`, `internal/web/render.go`
    - Changes: routes for list, new/create, detail, edit/update, status change,
      timeline events, delete, plus an exact-root redirect and `/static/`.
      Filters (`status`, `source`, `q`, `stale`) drive the list and an htmx
      rows partial; the timeline form swaps a `timeline_block` partial and keeps
      errors in place because htmx does not swap 4xx. Plain form posts still
      redirect with a flash code, so the UI works without JavaScript. Validation
      errors re-render with 422 and per-field messages. Missing/invalid IDs map
      to 404. Added `Renderer.RenderPartial`, `sourceLabel`, and `eventLabel`.
      `ApplicationService` gained `AddEvent`/`ListEvents` with user event types
      (`note`, `email_sent`, `email_received`); status/document events stay
      service-owned. `NewRouter` now takes the service and renderer (nil for
      health-only use), and `serve` builds them from the DB and configured
      timezone.
    - Verify: `go test ./internal/web -run TestApplications` plus detail, edit,
      status, events, delete, root, and static tests cover rendering, filters,
      htmx fragments, redirects, 422 re-renders, and 404s against a temp store;
      race/vet/gofmt/sqlc clean.
- [x] **3.5 Dashboard**
    - Targets: `internal/web/dashboard.go`, `internal/web/dashboard_test.go`,
      `web/templates/dashboard.html`, `web/templates/_compact_rows.html`,
      `internal/web/router.go`
    - Changes: `GET /dashboard` shows total/active/interviewing/offers/stale
      counts, a stale list capped at 10, and the 5 most recently added
      applications. The stale count links to the filtered list, and the root
      now redirects to the dashboard.
    - Verify: `go test ./internal/web -run TestDashboard` covers seeded counts,
      stale-only filtering, the recent list, the empty state, active nav, and
      the root redirect. A manual `serve` smoke test created an application,
      found it through the list filter, and rendered its initial timeline
      event.

### M4 — Documents

- [x] **4.1 File storage package**
    - Targets: `internal/files/files.go`, `internal/files/files_test.go`
    - Changes: random 16-byte key, `files/<key>` path (never user-controlled),
      streaming copy with size cap, sha256, first-512-byte MIME sniff, reject
      HTML/XHTML/SVG, delete by key.
    - Verify: `go test ./internal/files` (oversize, traversal name, rejected MIME).
- [x] **4.2 Document service and queries**
    - Targets: `internal/service/documents.go`, `..._test.go`
    - Changes: upload writes file then inserts row + `document_added` event in a
      tx (file removed on tx failure); list/get/delete; delete removes file after
      successful DB delete, logging orphans.
    - Verify: `go test ./internal/service -run TestDocument`.
- [x] **4.3 Document handlers and UI**
    - Targets: `internal/web/documents.go`, `web/templates/_documents.html`
    - Changes: multipart upload with `MAX_UPLOAD_MB`, authenticated download with
      `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff`,
      cache headers; delete action; kind selector.
    - Verify: handler test uploads a PDF, downloads it byte-identical, rejects a
      `.html` upload; container restart retains the file via the volume.
    - Notes: the download route carries attachment/nosniff/no-store headers and
      will sit behind the M5 auth middleware. Upload errors re-render the detail
      page with 422 (413 when the multipart body itself exceeds the limit).
      Application delete now removes the cascade's files via `DeleteStored`.
      Verified with web tests, a live serve restart, and a Docker volume restart.

### M5 — Settings, staleness UI, auth

- [ ] **5.1 Settings service and page**
    - Targets: `internal/service/settings.go`, `internal/web/settings.go`,
      `web/templates/settings.html`
    - Changes: edit thresholds (validated 1–365), display effective timezone,
      change password, session-secret rotation ("log out everywhere").
    - Verify: threshold edit changes `due_on`/stale for an existing backdated row.
- [ ] **5.2 Authentication**
    - Targets: `internal/service/auth.go`, `internal/web/auth.go`,
      `web/templates/{login,setup}.html`
    - Changes: argon2id hash/verify (constant time), first-run setup, login,
      logout, HMAC-signed `HttpOnly`/`SameSite=Lax` cookie, 30-day expiry,
      in-memory login rate limit.
    - Verify: `go test ./internal/service -run TestPassword`; wrong password is
      rejected; cookie tampering is rejected.
- [ ] **5.3 Middleware and hardening**
    - Targets: `internal/web/middleware.go`
    - Changes: auth gate, CSRF origin check on non-GET, CSP
      (`default-src 'self'`), `X-Frame-Options: DENY`, referrer policy,
      request logging, panic recovery.
    - Verify: unauthenticated GET redirects to `/login`; POST with foreign
      `Origin` gets 403; `AUTH_DISABLED` guard test.
- [ ] **5.4 Auth/handler tests**
    - Targets: `internal/web/auth_test.go`
    - Changes: setup → login → authenticated request → logout; rate-limit burst.
    - Verify: `go test ./internal/web`.

### M6 — Export, backup, docs, final gate

- [ ] **6.1 Export**
    - Targets: `internal/web/export.go`, `internal/service/export.go`
    - Changes: `GET /export.json` (applications, events, documents with base64
      payloads) and `GET /export.csv` (applications only).
    - Verify: round-trip test parses the JSON and counts match.
- [ ] **6.2 Backup command**
    - Targets: `cmd/server/backup.go`
    - Changes: `VACUUM INTO <DATA_DIR>/backups/jat-<timestamp>.db`, copy `files/`,
      write a manifest with schema version; refuse to overwrite.
    - Verify: `go run ./cmd/server backup`; restore into a temp `DATA_DIR` and
      `go test ./internal/store` against the restored DB.
- [ ] **6.3 README and operations docs**
    - Targets: `README.md`
    - Changes: quickstart, env table, reverse-proxy example, backup/restore,
      timezone note, single-replica warning, roadmap (URL fetch, kanban, IMAP,
      Litestream).
    - Verify: fresh-clone walkthrough on a temp dir works as written.
- [ ] **6.4 Final verification gate**
    - Verify: `gofmt -l .` empty; `go vet ./...`; `go test ./...`;
      `sqlc generate` clean; `docker build`; compose smoke test; manual checklist
      (create app, upload doc, restart container, backdate status → stale badge,
      mark ghosted, export, backup/restore).
- [ ] **6.5 Copy plan into repo (optional)**
    - Targets: `PLAN.md`
    - Changes: copy this document into the repo and keep ticking it there.
    - Verify: `git status` shows the intended file only.

### Backlog (explicitly out of v1)

- [ ] Fetch job ad text from URL (needs SSRF guard + outbound TLS certs).
- [ ] Kanban board with drag-and-drop (htmx + SortableJS).
- [ ] IMAP sync for email threads.
- [ ] Litestream sidecar for off-box SQLite replication.
- [ ] Reminders/notifications (ntfy/email).
- [ ] Multi-user support.

## Risks and rollback

| Risk                                                   | Mitigation / rollback                                                                                                             |
| ------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| Docker install touches the host (sudo, systemd, group) | Step 0.1 is isolated and reversible via `pacman -Rns`; do it first so image builds are verified early.                            |
| SQLite allows a single replica                         | Compose must not scale `app`; README states it.                                                                                   |
| No down-migrations (goose Up-only)                     | Restore the `DATA_DIR` backup to roll back; migrations are additive only. A Down section would also break the shared sqlc schema. |
| goose/sqlc schema coupling                             | Keep migrations Up-only with marker comments; avoid `StatementBegin`/`Down` until verified against sqlc.                          |
| File/DB inconsistency on delete                        | Delete DB row first, then file; log orphaned keys; `backup` can later add a GC pass.                                              |
| sqlc drift                                             | Generated code committed; `make sqlc-check` runs `sqlc generate` + `git diff --exit-code`.                                        |
| Timezone bugs                                          | All date logic uses `Africa/Harare`; `time/tzdata` embedded; tests cover UTC-boundary.                                            |
| Self-hosted exposure                                   | Auth on by default; `AUTH_DISABLED` requires `TRUST_PROXY_AUTH`; security headers + origin checks.                                |

## Verification commands (summary)

```sh
gofmt -l .
go vet ./...
go test ./...
sqlc generate && git diff --exit-code internal/store/db
go run ./cmd/server migrate
sqlite3 ./data/app.db 'select max(version_id) from goose_db_version'
docker build -t jat:dev .
docker compose up -d && curl -fsS http://127.0.0.1:8080/healthz
```

## Progress Log

| Date       | Step      | Note                                                                                                                                               |
| ---------- | --------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-11-21 | Plan      | Approved decisions recorded; sqlc SQLite capabilities verified with a scratch probe.                                                               |
| 2026-11-21 | M0.2–M0.3 | Toolchain confirmed (Go 1.27.4, sqlc v1.31.1, sqlite3). `PLAN.md` and `.gitignore` added.                                                          |
| 2026-11-21 | M0.1      | Docker 29.10.0 + Compose 5.8.0 installed; daemon active; `hello-world` verified.                                                                   |
| 2026-11-21 | M1.1      | Module initialized as `github.com/rgmaz/job-application-tracker`; deps pinned and verified.                                                        |
| 2026-11-21 | M1.2      | Typed config with Harare default, auth/proxy guard, and 18 table-driven tests.                                                                     |
| 2026-11-21 | M1.3      | Harare-aware `Clock`, embedded tzdata, UTC-boundary tests.                                                                                         |
| 2026-11-21 | M1.4      | CLI (`serve`, `healthcheck`, stubs), `/healthz`, graceful shutdown; smoke-tested.                                                                  |
| 2026-11-21 | Plan rev. | Migrations switched to goose v3.30.1; migrations/queries moved under `internal/store`.                                                             |
| 2026-11-21 | M1.5      | Dockerfile, compose, .env.example, Makefile; image 3.9 MB compressed; compose smoke test passed.                                                   |
| 2026-11-22 | M2.1      | goose migrations verified; added missing `settings` table and dropped zero-day terminal seeds.                                                     |
| 2026-11-22 | M2.2      | sqlc queries for applications/events/documents/settings; filters and staleness checked against seeded SQLite.                                      |
| 2026-11-22 | M2.3      | sqlc.yaml + generated `internal/store/db`; compile/build/vet clean and regeneration is byte-stable.                                                |
| 2026-11-22 | M2.4      | `store.Open` pragmas + embedded goose runner; `jat migrate` and `serve` wired; race/vet clean.                                                     |
| 2026-11-22 | M2.5      | Store integration tests cover CRUD, filters, derived staleness, cascade delete, dashboard, settings, events, documents; race/vet/gofmt/sqlc clean. |
| 2026-11-23 | M2.6      | Makefile targets verified; `lint` now fails on unformatted files, `test` runs with `-race`, and `BIN` overrides work.                              |
| 2026-11-23 | M3.1      | `model.Status` / `model.Source` with ordered lists, labels, terminality, and strict parsing; race suite clean.                                     |
| 2026-11-23 | M3.2      | Application service with transactional create/status/delete, shared validation, filtered list, and dashboard counts; race/vet clean.               |
| 2026-11-23 | M3.3      | Embedded layout + vendored htmx/Pico assets, per-page renderer, and date/status template funcs; render and asset tests pass.                       |
| 2026-11-24 | M3.4      | Application list/form/detail routes with filters, htmx rows and timeline fragments, 422 validation, flash redirects, and event service support.   |
| 2026-11-24 | M3.5      | Dashboard counts, stale list, and recent applications; root redirects to the dashboard; seeded tests and a live serve smoke test pass.            |
| 2026-11-25 | M4.1      | Random-key file store with size cap, sha256, MIME sniffing, HTML/XHTML/SVG rejection, name sanitisation, and idempotent delete; race/vet clean.    |
| 2026-11-25 | M4.2      | Document upload/list/get/open/delete service; upload commits row + event with file cleanup on failure; delete logs orphans; tests cover validation. |
| 2026-11-25 | M4.3      | Document upload/download/delete routes, kind selector, detail-page list, attachment/nosniff headers; live and Docker restart smoke tests pass.      |
