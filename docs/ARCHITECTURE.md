# Architecture

receipt-tracker turns photos of grocery receipts into structured data: line
items, per-product price history across stores, and monthly spending. It is
self-hosted, multi-user by design, and runs as one Go binary plus PostgreSQL.

## 1. System overview

```
Browser (React SPA) ──┐
Claude / Gemini (MCP) ─┼─> Caddy (host, TLS) ─> app container (127.0.0.1:8087) ─> Postgres container
                       │                         │
                       │                         ├─> Azure Document Intelligence (prebuilt-receipt)
                       │                         └─> Google OIDC (sign-in) / Google Drive (images, database backups)
```

One process serves the REST API (`/api/v1`), the embedded SPA, the MCP endpoint
(`/mcp`), the OAuth authorization server used by MCP clients (`/oauth/*`,
`/.well-known/*`), the job worker and the backup scheduler. Two containers
total: `app` and `db`. No Redis, no separate worker, no LLM.

## 2. Stack

Major/minor versions are fixed here. Exact patch versions are pinned by the
bootstrap package in `go.mod`, `package-lock.json` and image tags, and only
change through a dedicated `chore(deps)` PR.

| Area | Choice | Why |
| --- | --- | --- |
| Language | Go 1.26 | Required; current stable line |
| HTTP router | `go-chi/chi` v5 | Thin, stdlib `net/http` compatible, very widely used |
| API contract | OpenAPI 3.0 in `api/openapi.yaml`; `oapi-codegen` v2 (strict server, chi) | Contract-first; backend and frontend generate from one file |
| Database | PostgreSQL 18 (`postgres:18-alpine`) | Required; built-in `uuidv7()`; `pg_trgm` and `btree_gist` (both bundled) for candidate lookup |
| DB access | `jackc/pgx` v5 + `sqlc` | Plain SQL, generated type-safe Go, no ORM behaviour to learn around |
| Migrations | `pressly/goose` v3, SQL files, embedded, applied at startup | Simple, conventional, one binary |
| Money and quantities | `shopspring/decimal` + `NUMERIC` columns | Exact arithmetic; no floats for money |
| Sign-in | `coreos/go-oidc` v3 + `golang.org/x/oauth2` | Standard OIDC verification for Google |
| Sessions | `alexedwards/scs` v2 with its pgx store | Server-side, revocable sessions without extra services |
| CSRF | stdlib `http.CrossOriginProtection` + `SameSite=Lax` cookie | No dependency; fits a same-origin SPA |
| Job queue | `jobs` table, `FOR UPDATE SKIP LOCKED`, one worker goroutine | Azure free tier allows 1 request/s; a queue framework adds nothing |
| Extraction | Azure Document Intelligence REST `2024-11-30`, stdlib `net/http` client | Small surface (submit + poll); no SDK needed |
| Image storage | `storage.Store` interface; `local` and `drive` (`google.golang.org/api/drive/v3`) adapters | Required; Drive via official client |
| Backups | `pg_dump` custom format streamed to a `backup.Destination`; `drive` and `local` adapters | Reuses the Drive authorization; no extra account or service (policy risk in §8) |
| HEIC decoding | `gen2brain/h265` (MIT, pure Go, no cgo, no dependencies) | Works the same on Windows, Ubuntu and Alpine with no system packages and no LGPL component; `gen2brain/heic` moved to an AGPL decoder in v0.6.0 |
| MCP | `modelcontextprotocol/go-sdk` v1, Streamable HTTP | Official SDK; transport Claude and Gemini expect |
| Small libraries | `google/uuid`, `golang.org/x/text` (normalisation), `golang.org/x/image` (downscaling) | Standard choices; no cgo |
| Logging | stdlib `log/slog`, JSON | Standard library |
| Tests | stdlib `testing` + `google/go-cmp`; real Postgres for DB tests | Idiomatic; no mock frameworks |
| Lint/format | `golangci-lint` v2 (includes `gofumpt`, `goimports`) | One enforced config |
| Frontend | React 19, TypeScript (strict), Vite, Tailwind CSS v4 | Required; Tailwind v4 keeps tokens in CSS |
| Frontend data | `openapi-typescript` + `openapi-fetch`, TanStack Query v5, React Router v7 | Typed client generated from the contract |
| Charts | Recharts v3, animations disabled | Widely used, enough for bar/line charts |
| Frontend tests/lint | Vitest + Testing Library, ESLint, Prettier | Conventional |
| Tasks | `Taskfile.yml` (go-task v3) | Same commands on PowerShell and Ubuntu |
| Runtime | Node 24 LTS (build only), Alpine-based app image | Small image; Alpine supplies `pg_dump` |
| CI/CD | GitHub Actions; image published to GHCR | Free for public repos |

## 3. Repository layout

```
api/openapi.yaml            API contract (source of truth)
cmd/server/                 main: wiring only
internal/
  config/                   environment configuration
  apperr/                   error kinds shared by all packages
  db/migrations/            goose SQL migrations
  db/queries/<domain>.sql   sqlc queries, one file per domain
  db/sqlc/                  generated (committed)
  httpapi/                  generated server interface, one handler file per API tag,
                            middleware, problem-details mapping
  auth/                     Google sign-in, sessions, allow-list
  storage/ (local/, drive/) image storage interface and adapters
  ingest/                   channel-agnostic receipt submission
  jobs/                     queue and worker
  extract/ (azure/, fake/)  extractor interface and adapters
  receipts/                 receipts, line items, review flags
  matching/                 normalisation, scoring, match decisions
  products/                 products, aliases, codes, merge/split, price history
  reports/                  spending aggregation
  oauthserver/              OAuth 2.1 authorization server for MCP clients
  mcpserver/                read-only MCP tools
  backup/ (drive/, local/)  dump, destination interface, retention
  web/                      embeds the built SPA
web/                        React app (src/api, src/components/ui, src/features/*, src/styles/tokens.css)
deploy/                     Dockerfile, compose files, Caddy snippet
docs/                       ARCHITECTURE.md, OPERATIONS.md
.agents/skills/             agent skills (canonical); .claude/skills/ holds pointers
```

Packages are organised by domain. Dependencies point inward: `httpapi` and
`mcpserver` call domain services; domain services call `db/sqlc`; nothing
imports `httpapi`.

## 4. Data model

All ids are `uuid` (`uuidv7()`), all timestamps `timestamptz` in UTC, all money
`NUMERIC(12,2)`, quantities `NUMERIC(12,3)`. Every user-owned table carries
`user_id` and every query filters on it.

| Table | Purpose and key columns |
| --- | --- |
| `users` | `google_sub` (unique), `email`, `name`, `is_admin` |
| `user_settings` | `default_currency` (default `TRY`), `default_receipt_title` (default `Home shopping`) |
| `sessions` | scs session store |
| `stores` | `name`, `normalized_name` (unique per user) |
| `categories` | `name` (unique per user); seeded defaults per new user |
| `receipts` | `title`, `store_id`, `purchased_on` (date), `purchased_time`, `currency`, `subtotal`, `tax_total`, `total`, `status`, `source` (`web`, later others), `failure_reason` |
| `receipt_images` | `receipt_id`, `position`, `sha256` of the uploaded bytes (unique per user), `content_type`, `size_bytes`, `storage_backend`, `storage_ref` (the image the pipeline and UI use); `original_ref`, `original_content_type`, `original_size_bytes` (set only when the upload was converted) |
| `extractions` | `receipt_id`, `provider`, `model`, `raw` (jsonb), `created_at`; lets matching be re-run without spending quota |
| `line_items` | `receipt_id`, `position`, `raw_name`, `name`, `quantity`, `quantity_unit`, `unit_price`, `line_total`, `product_code`, `confidence`, `product_id`, `match_status`, `match_score`, `match_method` |
| `products` | `canonical_name`, `category_id`, `size_value`, `size_unit` |
| `product_aliases` | `product_id`, `raw_name`, `normalized_name`, `store_id` (nullable), `seen_count`, `last_seen_at`; trigram index on `normalized_name`, and a GiST trigram index on (`user_id`, `normalized_name`) for the nearest-name lookup of matching |
| `product_codes` | `product_id`, `code`, `kind` (`gtin`, `store`), `store_id` (set for `store` codes), `seen_count` |
| `match_rejections` | `product_id`, `normalized_name`, `store_id`: "the user said these are different" |
| `review_flags` | `receipt_id`, `line_item_id` (nullable), `kind`, `detail` (jsonb), `resolved_at`, `resolution` |
| `jobs` | `kind`, `payload`, `status`, `attempts`, `run_after`, `last_error`; processed in `created_at` order |
| `provider_usage` | `provider`, `period` (`YYYY-MM`), `pages`; monthly quota counter |
| `storage_connections` | `backend`, `connected_by`, `encrypted_refresh_token`, `root_folder_ref`; one active row per instance |
| `oauth_clients`, `oauth_codes`, `oauth_tokens` | MCP authorization server state; tokens stored hashed |
| `audit_events` | `user_id`, `entity`, `entity_id`, `action`, `detail` (jsonb) |

Receipt status: `queued → processing → needs_review | ready`, or `failed`,
or `waiting_quota` while the monthly Azure allowance is exhausted. A receipt is
`ready` when it has no open review flags.

One uploaded file creates one receipt. `receipt_images` allows several images
per receipt later without a schema change.

## 5. Key flows

### 5.1 Upload and extraction

1. `POST /api/v1/receipts` accepts several files (JPEG, PNG or HEIC/HEIF, at
   most 15 MB each). The handler calls
   `ingest.Service.Submit(ctx, userID, source, files)`; any future channel
   (Telegram) calls the same function, so conversion below applies to all.
2. Per file: reject duplicates by SHA-256 of the uploaded bytes, normalise
   (below), store through `storage.Store`, create the receipt (`queued`,
   default title) and one `extract_receipt` job. The response lists a result
   per file.

   **HEIC normalisation.** A HEIC/HEIF upload is decoded (primary image,
   orientation applied) and encoded once as JPEG (quality 85, longest side at
   most 3000 px, no metadata). Both files are stored: the JPEG as
   `storage_ref`, which extraction, the API and the UI use, and the untouched
   HEIC as `original_ref`. Everything after ingest sees only JPEG or PNG.
   Azure accepts HEIF directly (verified), but browsers do not display it and
   the downscale step needs decoded pixels, so one conversion at ingest keeps
   a single path. Keeping both costs roughly half as much again per HEIC
   receipt in Drive; that is the price of never discarding what the user
   uploaded. One conversion runs at a time to bound memory.
3. The single worker takes jobs oldest first. Before calling Azure it checks
   the monthly counter; if the allowance is used up the receipt becomes
   `waiting_quota` and the job is rescheduled for the first day of next month.
4. Azure limits on the free tier (verified): 1 analyze request/s, 1 poll
   request/s, 4 MB per document, 500 pages/month; accepted image formats are
   JPEG, PNG, BMP, TIFF and HEIF. The worker spaces submissions
   at least 1 s apart, polls no faster than every 2 s honouring `Retry-After`,
   and backs off exponentially on `429`. A file over 4 MB is downscaled to a
   temporary JPEG for Azure only; the stored original is untouched.
5. The raw result is saved in `extractions`, mapped to receipt fields and line
   items, then matching (5.2) and review rules (5.3) run in one transaction.
6. The UI polls `GET /api/v1/receipts?ids=…` every 2 s while any receipt is
   `queued` or `processing`. No websockets.

### 5.2 Product matching (no LLM)

`matching` is a deterministic package: same input, same decision.

**Normalise.** Unicode NFKC; Turkish-aware lower-casing; fold `çğıöşü` to
ASCII for the comparison key; drop punctuation and VAT markers (`%08`, `*`);
split letter/digit boundaries (`1LT` → `1 lt`); canonical units (`lt`→`l`,
`gr`→`g`, `adet`→`ad`). The result is a token list plus an optional size
signature. The signature keeps the printed value and unit (`1 l`, `500 g`)
for display. In the comparison key a size is converted to the base unit of
its dimension, volume to `ml` and mass to `g`, with exact decimal arithmetic
and a decimal comma accepted: `1 L`, `100 CL` and `1000 ML` all give
`1000 ml`, and `1,5 KG` gives `1500 g`. Two spellings of one amount are
therefore the same size for alias equality, for candidate lookup and for
rule 2.

**Candidates.** Up to 20 of the user's aliases by `pg_trgm` similarity, plus
every product that owns the line's product code. A product's score is the
best score among its aliases; the runner-up is the next-best other product
that rule 2 has not blocked. Candidates are ordered by score, then products
that are not blocked before blocked ones, then product id.
A product owns the line's code when it has that code as a store code of the
line's store, or as a valid GTIN from any store.

**Score (0–1).** `0.6 × token score + 0.4 × trigram Dice`, both on comparison
keys. The token score is `2m / (a + b)`, where `a` and `b` are the numbers of
tokens in the two keys and `m` is the largest number of matching token pairs
that can be formed when each token is used in at most one pair; it is 0 when
either key is empty. Two tokens match when they are equal.
Receipts abbreviate by cutting words short and by dropping vowels, so two
letter tokens also match when the shorter has at least three letters, both
start with the same letter, and the shorter can be produced from a prefix of
the longer by deleting vowels (`a e i o u`): `DOM` / `DOMATES`,
`AYRN` / `AYRAN`, `PNR` / `PINAR`, `CKL` / `CIKOLATA`, but not
`KOLA` / `KOLONYA`. Number tokens match only when equal. A token matches at
most one token on the other side. When both names print a size, the score is
computed on the keys without their size tokens, because rule 2 already
compares the sizes and shared size tokens would make unrelated products look
alike. When only one name prints a size, its tokens stay, so the missing size
lowers the score. If removing the sizes leaves either name empty, the full
keys are scored.

**Decide.** Rules 1 and 2 filter candidates; then the first rule that applies
decides:

| # | Condition | Decision |
| --- | --- | --- |
| 1 | A `match_rejections` row exists for (name, product) | Skip that candidate |
| 2 | Sizes present on both sides and different (amount or dimension) | The candidate is blocked: never auto-linked, but it can be offered in a question |
| 3 | Exact normalised alias, same store, alias maps to one product | Auto-link |
| 4 | More than one product owns the line's code | Ask: ambiguous code, showing the owners |
| 5 | One product A owns the code, and a product B that does not own it and is not blocked scores ≥ 0.90 and at least 0.08 above A | Ask: code conflict (A by code, B by name) |
| 6 | One product A owns the code, A scores ≥ 0.50 and is not blocked | Auto-link A |
| 7 | One product A owns the code (every other case) | Ask: "same code, different name" |
| 8 | The best product that is not blocked scores ≥ 0.90 and at least 0.08 above the runner-up, and no blocked product scores higher than it | Auto-link |
| 9 | Best score ≥ 0.55 | Ask, showing the top three candidates |
| 10 | Otherwise | Create a new product with the cleaned name |

Codes are a strong signal, never proof: a store code only counts inside its
store, a GTIN (8/12/13 digits with a valid check digit) counts everywhere, and
rules 4, 5 and 7 route disagreements to the user. When code and name both
fit, the code breaks a tie between similar names (rule 6). The other sizes of
a product are blocked by rule 2 and do not compete with the size that fits;
a blocked product that fits the name better than every other candidate
suggests a new size, so the line is asked. Thresholds live in one file.

**Learning.** "Same product" adds the alias (and code) to the product. "Different"
writes a `match_rejections` row and creates a new product. **Merge** moves
aliases, codes and line items from the source product to the target and deletes
the source. **Split** moves chosen aliases, with their line items, to a new
product and records rejections so they do not re-merge.

### 5.3 Review

Flags are created only for suspicious data:

| Kind | Trigger |
| --- | --- |
| `low_confidence` | Azure confidence below 0.80 on store, date, total or a line |
| `missing_field` | No date or no total |
| `line_math` | `quantity × unit_price` differs from `line_total` by more than 0.01 |
| `sum_mismatch` | Sum of line totals differs from the receipt total by more than 0.02 |
| `uncertain_match` | Matching rules 4, 7 and 9 |
| `code_conflict` | Matching rule 5 |
| `possible_duplicate` | Same store, date and total as another receipt |

The review screen lists open flags; each resolution (edit, confirm, pick a
product, accept as is) closes the flag. Editing a line name re-runs matching
for that line.

### 5.4 Sign-in and isolation

Google sign-in requests only `openid email profile`. The callback verifies the
ID token, requires `email_verified`, and admits the user only if the email is
in `ALLOWED_EMAILS`; the user row is created on first admitted sign-in. There
is no sign-up endpoint. The user id comes from the session only, is passed
explicitly to every service call, and every query on a user-owned table filters
by it. Accessing another user's row returns 404. Each endpoint has a test that
proves this.

### 5.5 Image storage

`storage.Store` has `Put`, `Open` and `Delete`. Images are always served
through the API after an ownership check; storage links are never exposed.
The Drive adapter uses a separate OAuth consent ("Connect Google Drive",
admin only) with the `drive.file` scope and offline access. The refresh token
is encrypted with AES-256-GCM using `APP_ENCRYPTION_KEY`. Disconnecting in
the app only deletes the stored token; the app never revokes the grant at
Google, because revoking may hide files created earlier (§5.7). Revoking is a
manual step in the owner's Google account settings. Files go under an
app-created folder, `receipt-tracker/<user_id>/<receipt_id>/`.

### 5.6 MCP

`/mcp` speaks Streamable HTTP and exposes read-only tools, each annotated
`readOnlyHint`: `list_receipts`, `get_receipt`, `search_products`,
`get_price_history`, `get_spending_summary`, `list_categories`, `list_stores`.
Tools call the same domain services as the REST handlers, with the user id from
the access token.

The built-in authorization server implements what Claude's client requires
(verified): `401` with `WWW-Authenticate: Bearer resource_metadata=…`, RFC 9728
protected-resource metadata whose `resource` equals the connector URL exactly,
RFC 8414 server metadata, Dynamic Client Registration, authorization code with
PKCE S256, rotating refresh tokens, `invalid_grant` on dead refresh tokens,
form-encoded token requests. The user authenticates with the normal Google
session and sees a consent page naming the client and its redirect host.
Tokens are opaque, stored hashed, bound to the MCP resource, and rejected by
the REST API.

Connections are expected to drop: a reload of the host's Caddy closes
long-lived connections, and a proxy in front may cut idle ones. Open streams
therefore carry a keepalive every 25 seconds, tool calls never depend on a
stream staying open, and a client that reconnects, or presents a session the
server no longer knows (after a restart), is told to re-initialise and then
works without signing in again. Write tools later need real per-tool scopes.

### 5.7 Backups

A scheduler runs `pg_dump -Fc` daily, streams it to a `backup.Destination`
(`Put`, `List`, `Open`, `Delete`) and applies retention (7 daily, 4 weekly).
The `drive` destination uploads to `receipt-tracker/backups/` in the owner's
Drive through the same connection and `drive.file` grant as image storage; the
`local` destination writes to a directory and is used in development and
tests. Another destination can be added without touching the scheduler.
`docs/OPERATIONS.md` documents the restore, including downloading a dump from
the Drive web UI when the app is down; CI runs a dump-and-restore round trip
against a scratch database with the `local` destination. Without a Drive
connection the backup fails loudly and the storage status shows it. Images are
not backed up by the app: Drive is their durable home. `APP_ENCRYPTION_KEY`
is not in the dump and must be kept separately.

**Recovery onto a fresh host** follows a fixed order: restore the secrets
(the host's configuration backup holds `/etc/receipt-tracker/`), start the app
with an empty database, re-authorize Google Drive, then restore the database
from the newest dump. This only works if a new authorization can read files
created under the previous one. `drive.file` access belongs to the app and
the user rather than to one token, but that is not taken on trust: on
connect, the Drive adapter looks for the existing app-created
`receipt-tracker` folder and reuses it instead of creating a second one, and
the owner proves the behaviour against the real account before go-live.
`drive.file` access is tied to the OAuth client that created the files,
identified by its client id. This is supported by community evidence (an
rclone maintainer's statement, confirmed by a user's test), not by official
documentation. So the Google Cloud project and the OAuth client used for
Drive must never be deleted or recreated: a grant under a new client would
not see the existing dumps and images. Recovery always uses the same
`GOOGLE_CLIENT_ID`; rotating the client secret is safe.

A second, older report (2015, deprecated Android API) says files created
before the user **revoked** the app became invisible after re-authorizing.
The two cases are therefore tested separately, early, with the real client
and account against a throwaway folder (`DRIVE_ROOT_FOLDER`), as soon as the
storage package is merged: (a) a new token without revoking, which is what
recovery onto a fresh host does, and (b) access removed in the Google account
and then granted again. Independently of the result, the app never revokes:
reconnecting replaces the token and disconnecting deletes it, and neither
calls Google's revoke endpoint. Case (b) therefore only tells the owner how
dangerous a manual revocation in their Google account is; if it fails, they
must never remove the app's access there. If (a) fails,
recovery by re-authorization does not work and the mitigation below is built
before go-live.

**Planned mitigation (not built unless the test fails).** `drive.file` also
covers files the user picks with the Google Picker (verified in the Drive
scope documentation). A "re-grant access" step for the admin would open the
Picker with a short-lived access token, let the owner select the dump files
in `receipt-tracker/backups/`, and so make them readable for the restore.
Assessment: feasible for database dumps, which are at most eleven files in
one folder. It needs a browser API key and the project number as new
configuration, Google's Picker script on one admin page (a content security
policy exception), and passing an access token to the admin's browser. It is
not a practical way to re-grant thousands of images unless selecting a folder
also grants its contents, which is unverified; without that, images would be
recovered by downloading them from Drive by hand and re-importing. The
always-available fallback needs no code: download a dump from the Drive web
UI and restore from the local file. The
host does not back up `/opt` or Docker volumes; the compose file comes from
the repository and the data from the application backup.

The host's alert policy is "success is silent, failure notifies". When
`BACKUP_HEARTBEAT_URL` is set, the app sends a GET to it after a successful
run and to `<url>/fail` after a failed one (one attempt, short timeout, never
logged). A heartbeat service then alerts on a failure or a missed run. The
app itself sends no email: outbound SMTP is blocked on the host.

## 6. Configuration

Environment variables only; `.env.example` lists them. `APP_ENV` (`dev`,
`prod`), `APP_LISTEN_ADDR` (default `:8080`; `:8087` in production),
`APP_BASE_URL`, `DATABASE_URL`, `APP_ENCRYPTION_KEY`, `ALLOWED_EMAILS`,
`ADMIN_EMAIL`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `STORAGE_BACKEND`
(`local`, `drive`), `STORAGE_LOCAL_DIR`, `DRIVE_ROOT_FOLDER` (default `receipt-tracker`), `EXTRACTOR` (`azure`, `fake`),
`AZURE_DI_ENDPOINT`, `AZURE_DI_KEY`, `AZURE_DI_MONTHLY_PAGES` (default 500),
`BACKUP_DESTINATION` (`drive`, `local`, `none`), `BACKUP_LOCAL_DIR`,
`BACKUP_SCHEDULE`, `BACKUP_HEARTBEAT_URL` (optional), `DEV_LOGIN_EMAIL` (accepted only
when `APP_ENV=dev`; the server refuses to start otherwise).

Only `APP_LISTEN_ADDR`, `DRIVE_ROOT_FOLDER` and `AZURE_DI_MONTHLY_PAGES` have
defaults; every other variable not marked optional is required in `dev` and
`prod`, backend-specific variables only when that backend is selected, and the
Google client may be omitted only in `dev` with `DEV_LOGIN_EMAIL` and no Drive
use.

## 7. Deployment

Caddy runs directly on the VPS host, terminates TLS for `receipt.enesoglu.com` and
proxies to `127.0.0.1:8087`. The app listens on 8087 in production and is
published as `127.0.0.1:8087:8087`, loopback only; nothing but Caddy binds 80
or 443 on this shared host. Postgres is not published. Secrets live in
root-only files under `/etc/receipt-tracker/` (`app.env`, `db.env`), which the
host's existing encrypted config backup already covers; the compose file and
the non-secret image tag live in `/opt/receipt-tracker/`. Containers use
`restart: unless-stopped` and the Docker daemon's log settings. Memory limits
start at 512 MB for `app` and 384 MB for `db` and are revisited after the
measurements in the ingestion and hardening packages.

Two health endpoints. `/livez` answers 200 whenever the process serves
requests; the container health check uses it. `/healthz` is for monitoring:
it returns a small JSON body, `{"status", "database", "drive"}`, and 200 only
when the database answers and Google Drive is usable; otherwise 503. `drive`
is `ok`, `not_connected`, `reconnect_needed` or `not_required` (neither image
storage nor backups use Drive). The Drive state is not probed per request:
the app refreshes the token in the background at most every 15 minutes and
caches the result, so a revoked or expired grant becomes visible to the
uptime monitor and the host's health script within that time. Only a
definitive authorization failure from Google (`invalid_grant`: the grant was
revoked or has expired) sets `reconnect_needed`. Network errors, timeouts,
`429` and `5xx` responses are transient: the last known state is kept and the
check is retried, so a Google outage never raises a false alarm. The body holds
no tokens, emails or error text.

Deploys are run by hand by the admin with `sudo docker compose` (the admin is
not in the `docker` group); CI only builds and publishes the image.
Compose files are validated with `docker compose config --quiet`, because the
plain form prints secrets. The host runs Docker Engine 29 and Compose v5: the
compose file follows the Compose Specification with no top-level `version`
key. The host's Caddyfile has no global options, snippets or imports, so the
site block carries its own transport headers (HSTS) and its own access log,
with OAuth `code` and `state` query values removed; content security headers
are sent by the app. Postgres runs with small memory settings (`shared_buffers=64MB`,
`max_connections=20`); the app pool is capped at 5 connections. CI publishes
`ghcr.io/enesoglu/receipt-tracker`; deploying is `docker compose pull` and
`up -d`. Migrations run at startup.

## 8. Risks and unverified points

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Drive API policy lists "backup of user or app content from a developer's app or project to Drive" as not allowed without Google's written consent, and database dumps go to Drive | Google could disable the OAuth project or its Drive access, which would stop backups and image storage together | **Accepted by the owner while the app is personal-use only. Revisit before any public launch**: switch `BACKUP_DESTINATION` to another adapter or obtain consent. The destination is behind an interface for this reason. Receipt images as primary content are not named by the policy (page last updated July 2022) |
| Backups and images share one Drive grant | Losing the grant stops both | Restore is tested; a new grant must still see old files (tested before go-live, §5.7); dumps can be downloaded from the Drive web UI |
| The HEIC decoder is young (first release 2026) | Some iPhone files fail to decode, or a malformed file crashes the decoder | Behind a converter interface; grid, 10-bit and rotation fixtures from an independent encoder; panics become per-file errors; fuzz test; one conversion at a time; peak memory measured |
| Google OAuth app left in "Testing" | Drive refresh token expires every 7 days (sign-in scopes are exempt) | Publish the app to "In production"; `drive.file` is non-sensitive. Reconnect flow and a visible storage status exist anyway |
| Refresh token revoked or unused for 6 months | Image reads and uploads fail | Storage status endpoint, clear UI error, reconnect button |
| Azure free tier: 500 pages/month, 4 MB, 1 request/s | Uploads wait or fail | Quota counter, `waiting_quota` state, downscale for OCR, serial worker |
| Receipt quality on Turkish thermal receipts (Turkish is a supported language) is unknown until measured | More review work | Review flow; raw results stored so rules can be tuned and re-run |
| Gemini app custom connectors are documented as US-only, 18+, English, personal accounts, "Keep Activity" on | The owner may be unable to add the connector in Gemini | Server is spec-compliant with DCR, which is what Gemini uses by default; test from an eligible account |
| Hand-written OAuth server | Security bugs | Small scope, opaque hashed tokens, dedicated tests, independent security review in the final package |
| Small, shared VPS (3.4 GiB RAM available, other sites, a second app needing 0.6 to 1 GiB planned) | Memory pressure | Two containers with memory limits, capped pools, streaming uploads and dumps |
| Host clock drift (synchronised when checked on 2026-11-12) | ID-token validation, sessions and OAuth fail with a skewed clock | Checked before every deployment and after every reboot (owner's VPS guide) |
| **Accepted by the owner:** the host's configuration backup, the database dumps and the receipt images all depend on one Google account | Losing or being locked out of that account loses all three | Two-step verification and recovery codes; a monthly copy of a dump and of the configuration backup kept outside Google; revisit before any public launch |
| Docker and Caddy are not covered by unattended upgrades on the host | Security fixes arrive only when installed by hand | Monthly supervised update by the owner (decided) |
| The Google Cloud project or the OAuth client used for Drive is deleted or recreated | A grant under a new client does not see files the old client created: backups and images become unreadable by the app (community evidence, not official) | Rule: never delete or recreate them; rotate only the client secret; recovery reuses the same client id; files remain downloadable by hand from the Drive web UI |
| Revoking the app's Drive access in the Google account may hide files created before the revocation, even after re-authorizing (2015 report on a deprecated API; unverified today) | Images and dumps unreadable by the app | The app never revokes (disconnect only deletes the stored token); manual revocation is documented with a warning; the effect is tested early against a throwaway folder; Picker re-grant is the planned mitigation for dumps (§5.7) |
| Caddy access logs under `/var/log/caddy/` are not in the host backup | Logs are lost with the host | Accepted: logs are disposable |

Not verified from official sources: the decoder's coverage of every HEIF
variant phones produce (tested with fixtures, not with a corpus of real
photos); whether failed Azure analyses count toward
the monthly allowance (the counter counts every accepted submission); the exact
JSON shape of currency fields in the `2024-11-30` response (the extraction
package must confirm it against the REST reference); an official Google
statement that service accounts have no personal Drive quota (only community
and vendor sources found; this is why the Drive adapter uses the owner's OAuth
grant); whether "In production" with only non-sensitive scopes ever requires
verification; the transport Gemini's custom apps require; ChatGPT connector
requirements.

## 9. Licensing

The project's own code is MIT (`LICENSE`). All dependencies are under
MIT-compatible permissive licences, with no exception. `THIRD_PARTY_LICENSES`
at the repository root records the notices shipped in the Docker image, such
as the MIT notice of the HEIC decoder `gen2brain/h265`, and the file ships in
the image. Any non-permissive dependency needs the owner's approval and an
entry in the same file.
