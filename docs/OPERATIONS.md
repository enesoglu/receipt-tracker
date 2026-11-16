# Operations

## Browser response policy

The application sends a Content Security Policy on every route, including
errors. Scripts, connections and forms are limited to the same origin;
objects and framing are denied. Images may also use blob/data URLs for local
previews, and inline styles support the chart library. OAuth consent uses a
stricter policy with `default-src 'none'`. `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY` and `Referrer-Policy: no-referrer` apply globally.
Authenticated REST responses use `Cache-Control: no-store`, except owned
image responses with an explicit private cache lifetime. Caddy supplies HSTS
at the HTTPS boundary; local HTTP development does not set it.

## Connect Google Drive

To rotate the instance administrator, change `ADMIN_EMAIL` to another
allow-listed address and restart the application. Existing sessions immediately
use the new configuration: the former administrator loses storage management
rights and the new administrator gains them without another sign-in. This
does not disconnect Drive or revoke its Google grant.

Use the existing Google OAuth web client, with Drive API enabled and these
redirect URIs registered for the origins you use:

- `http://localhost:8080/api/v1/storage/drive/callback`
- `https://receipt.enesoglu.com/api/v1/storage/drive/callback`

Set `STORAGE_BACKEND=drive` and provide `GOOGLE_CLIENT_ID`,
`GOOGLE_CLIENT_SECRET` and `APP_ENCRYPTION_KEY`. The default root folder is
`receipt-tracker`; set `DRIVE_ROOT_FOLDER=receipt-tracker-test` for the
owner-run reauthorization test. Keep test data separate from production.

Start `task db:up` and `task dev`, sign in as the configured admin, then open
`/api/v1/storage/drive/connect`. In development the configured
`/auth/dev/login` shortcut can establish the admin session. Consent requests
only `drive.file`, offline access and fresh consent, with PKCE and a
single-use state bound to the administrator. The callback redirects to
`/settings`; the settings UI is delivered separately.

Connecting again replaces the encrypted token and reuses an existing visible
root folder. Duplicate matching roots fail with `storage_duplicate_folder`
rather than choosing a potentially incomplete recovery tree.

## Keep the root folder inside another Drive folder

The app first creates its root at the top of My Drive. To keep it elsewhere:

1. Connect once, so the app creates the folder. Move that folder in the
   Drive web UI into the folder you want; renaming it is allowed. Do not
   create a folder by hand: under `drive.file` the app cannot see it.
2. Open the moved folder and copy its id, the part of the address after
   `/folders/`. Use the moved app folder's id, not the id of its new parent.
3. Set `DRIVE_ROOT_FOLDER_ID` to that id and restart. No reconnect is
   needed; the next check verifies the folder and records it as the root.
4. Run `storage probe --write`, then `storage probe --list` (through
   `task server --` locally): the root's subfolders are listed and the probe
   file is `readable`. Delete the probe file in the Drive web UI afterwards.
   Open an earlier receipt's image in the app to confirm older files.

Keep the variable in the host's secrets backup. A new connection without it,
such as recovery onto a fresh host, searches My Drive by name and creates a
new empty root there. With the id set, a missing, invisible, trashed,
non-folder or read-only root fails with `storage_root_not_found`,
`storage_root_trashed`, `storage_root_not_folder` or
`storage_root_not_writable`, and nothing is created; the storage status stays
`ok` because the grant is fine. Restore the folder from the trash or correct
the id, then restart. Images and backups use the same root. Keep the same
Google Cloud project and client ID for recovery; rotate only the client secret.

`GET /api/v1/storage` exposes operational state to signed-in users and
`canManage` for the current user. Other users receive 404 on connect,
callback and disconnect. `DELETE /api/v1/storage/connection` deletes the
local connection and refresh token only. It never revokes Google's grant or
deletes Drive files. Removing access in the Google account is a separate
manual action that may hide files from later grants. Do not do it with
production data until the owner-run recovery test has established the result.

Tokens use AES-256-GCM with a fresh random nonce and a version/key identifier
prefix. A changed encryption key or `invalid_grant` marks the connection
`reconnect_needed`. Network failures, rate limits and Google server failures
preserve the previous state. A background refresh runs at startup and every
15 minutes. `/healthz` checks the database and cached Drive state and returns
503 when required storage needs attention; `/livez` stays independent.

Backups can reuse `drive.Service.Access(ctx)` to obtain the authorized Drive
service and app root. Image operations still require caller ownership checks;
Drive file IDs are private storage references, never public image URLs.

## Recovery probe

`task server -- <args>` builds the server with embedded web assets, loads
`.env` using the same parser as `task dev`, and forwards arguments. Shell
variables win over `.env`; a missing file is allowed. The Go binary itself
reads environment variables only.

After connecting the test root, run:

```powershell
task server -- storage probe --write
task server -- storage probe --list
```

Save the marker reference, then repeat `--list` after reauthorization. A
successful recovery lists that same reference as `readable` and creates no
second root folder. `--list` reads every visible non-folder file in the root,
prints names and private references, and exits unsuccessfully if the listing
is empty or any file is unreadable. Subfolders are listed as `folder` and are
not treated as downloadable files. No HTTP session is needed by the command;
it uses the encrypted connection in the configured database.

The owner-run exercise must test both a fresh local connection without
revoking Google access and revocation in the Google account followed by fresh
consent. Use the same client ID and only `receipt-tracker-test`. Record both
results before declaring storage complete; run this exercise only after the
Drive implementation PR has merged. Neither production data nor real Google
credentials belong in test fixtures or command output.

## Production deployment

OCR resizing of JPEG/PNG images above 4 MiB admits at most 26 million decoded
pixels. Larger images fail processing before provider submission; their stored
images remain available. HEIC conversion has the same pixel cap. A 24 MP
16-bit PNG completes the capped-container probe; a 49 MP input is refused
before decoding. See the release hardening report for the recorded evidence.

The target is Debian 13, Docker Engine 29.9.3, Compose v5.7.0 and host Caddy
v2.11.4. Deploy manually with `sudo docker compose`; CI publishes images and
does not access the VPS. Check `timedatectl status` before deployment and
after reboot.

1. Restore or create `/etc/receipt-tracker/` (0700), with `app.env` and
   `db.env` (0600, root-owned). The host's existing encrypted configuration
   backup covers this directory. `.env.example` lists application variable
   names; never commit real values. In `db.env`, set `POSTGRES_USER`,
   `POSTGRES_PASSWORD` and `POSTGRES_DB`. Application `DATABASE_URL` uses
   those credentials, hostname `db`, port 5432 and that database name.
2. In `app.env`, set `APP_ENV=prod`, `APP_BASE_URL=https://receipt.enesoglu.com`,
   encryption key, allowed/admin accounts, Google client and extractor.
   Do not set `DEV_LOGIN_EMAIL`. For Drive backups use
   `BACKUP_DESTINATION=drive` and `BACKUP_SCHEDULE=02:00` (UTC), independently
   of the image backend. Preserve `DRIVE_ROOT_FOLDER_ID` when configured.
3. Copy `deploy/compose.prod.yaml` from the selected repository revision to
   `/opt/receipt-tracker/compose.yaml`. Keep only the non-secret `IMAGE_TAG`
   in `/opt/receipt-tracker/.env`. Prefer a full commit SHA. Every push to
   main publishes its full SHA and `latest`; `v*` Git tags publish that tag.
   Images are `ghcr.io/enesoglu/receipt-tracker`. Ensure the package is public
   or authenticate `sudo docker login ghcr.io` with a read-packages credential
   through stdin before pulling.
4. Start from `/opt/receipt-tracker`:

   ```sh
   sudo docker compose -f compose.yaml config --quiet
   sudo docker compose -f compose.yaml pull
   sudo docker compose -f compose.yaml up -d --wait
   curl --fail http://127.0.0.1:8087/livez
   ```

   Always use `config --quiet`; the plain command prints resolved secrets.
   App runs with a read-only root and a 64 MiB `/tmp` tmpfs. Only loopback
   port 8087 is published. Postgres has no published port and persists in
   `pgdata`. Final limits are 512 MiB app and 256 MiB db; Postgres uses
   `shared_buffers=64MB`, `max_connections=20`, and the app pool is capped at
   five. Container logging inherits the host daemon's rotation settings.
5. Append `deploy/Caddyfile.snippet` once to the existing host Caddyfile,
   leaving its four sites intact. It is a complete independent site block,
   with HSTS and Server-header removal only; content security headers come
   from the app. Nothing in Compose binds 80/443 or runs a Caddy container.
   Prepare the log directory, validate and reload on the host:

   ```sh
   sudo install -d -o caddy -g caddy -m 0750 /var/log/caddy
   sudo -u caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
   sudo systemctl reload caddy
   ```

   If a root-run validation previously created the site log, give it to user
   `caddy`. JSON access logs go to
   `/var/log/caddy/receipt.enesoglu.com.log`, roll at 10 MiB, and retain five
   rolled files. The filter deletes OAuth `code` and `state` query parameters.
   Changing rotation settings for an existing file requires a Caddy restart;
   coordinate that with the other sites. `/mcp` response buffering is disabled.
6. Sign in and connect Drive as admin using the earlier section. Before
   consent, health may correctly return 503 while liveness stays 200.
   Check health, open an image, run a backup and perform the recovery recheck
   below before go-live.

The snippet follows the official [query-filter syntax](https://caddyserver.com/docs/caddyfile/directives/log#query),
confirmed against the [Caddy v2.11.4 source](https://github.com/caddyserver/caddy/blob/v2.11.4/modules/logging/filters.go).
Its `flush_interval -1` follows the official [streaming documentation](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#streaming).
Validation versions are recorded in the deployment PR. The final
[resource report](RESOURCES.md) records the capped upload measurements and
their limits; the [hardening report](HARDENING.md) records the release checks.

## Health, alerts and logs

Both endpoints are unauthenticated. `/livez` answers 200 whenever HTTP serves
requests and is the container health check. `/healthz` returns exactly
`status`, `database` and `drive`, without tokens, account details or error text:

```json
{"status":"ok","database":"ok","drive":"ok"}
```

Health returns 200 only when the database answers and required Drive state is
usable; otherwise 503 with `status: unavailable`. `database` is `ok` or
`unavailable`. During database failure, HTTP retains its last known Drive state.

| `drive` | Meaning and action |
| --- | --- |
| `ok` | Saved grant is connected; see the moved-root section for folder errors that do not invalidate a grant. |
| `not_connected` | Images or backups require Drive, but no connection is saved. Connect as admin. |
| `reconnect_needed` | Google returned `invalid_grant`, or the key cannot decrypt the saved token. Check the key and reconnect. |
| `not_required` | Neither image storage nor backups uses Drive. |

Health requests do not call Google. The background refresh runs at most every
15 minutes. Only definitive Google `invalid_grant` changes grant state;
network errors, timeouts, malformed replies, 429 and 5xx preserve the last
state, log sanitized warnings and retry next interval. Reconnecting updates
saved state immediately. Stopping db yields 503 and starting it restores 200
once it answers and required Drive state is usable. Liveness is independent.

Expect one alert from the host health script when app is intentionally
stopped, including during maintenance. `BACKUP_HEARTBEAT_URL`, when set,
receives one GET after success or one GET to its `/fail` path after failure,
with a 5-second timeout and no retries. A heartbeat outage is a warning and
never fails a successful backup. The app sends no email: host outbound SMTP
is blocked.

```sh
cd /opt/receipt-tracker
sudo docker compose -f compose.yaml logs --tail 100 app
sudo docker compose -f compose.yaml logs --tail 100 db
sudo tail -n 100 /var/log/caddy/receipt.enesoglu.com.log
sudo journalctl -u caddy --since today
sudo docker stats --no-stream
```

For a full disk, check space and inodes (`df -h`, `df -i`), `docker system df`,
unused release images, daemon log rotation, the Caddy log directory and
Postgres/WAL growth. Remove only identified unused files/images; keep
`pgdata` and a verified dump. Dumps stream directly to their destination,
without a database-sized spool file. Caddy logs are disposable and are not
covered by the host backup.

## Update and rollback

Before updating, run a backup and verify its listing, record the current
immutable image tag, check clock synchronization and the host secrets backup.
Edit only `IMAGE_TAG` in `/opt/receipt-tracker/.env`, then:

```sh
cd /opt/receipt-tracker
sudo docker compose -f compose.yaml config --quiet
sudo docker compose -f compose.yaml pull app
sudo docker compose -f compose.yaml up -d --wait app
curl --fail http://127.0.0.1:8087/livez
curl --fail https://receipt.enesoglu.com/healthz
```

Migrations run at startup. For a code rollback, put the previous SHA back and
repeat pull/up. Confirm the old image supports the current schema: image
rollback does not undo migrations. Otherwise restore a pre-update dump into a
separate database, verify it, switch `DATABASE_URL` and recreate app. Retain
the old database until recovery checks pass. Do not delete a volume to roll back.

## Backup and restore

From `/opt/receipt-tracker`:

```sh
sudo docker compose -f compose.yaml exec app /app/server backup run
sudo docker compose -f compose.yaml exec app /app/server backup list
```

A run holds a Postgres advisory lock, streams `pg_dump --format=custom`,
verifies stored size and records the result. Failed dumps remove partial
objects. Names are UTC timestamps ending in `.dump`. Retention keeps the
newest dump on seven distinct UTC days and in four distinct ISO weeks;
overlaps count once. Unknown names and future-dated dumps are untouched.
`BACKUP_DESTINATION=none` disables scheduling and logs a startup warning.

Drive backups use `backups/` beneath the root returned by the shared
connection, even with local image storage. Refer to
[Keep the root folder inside another Drive folder](#keep-the-root-folder-inside-another-drive-folder):
preserve the pinned id for a moved/renamed root. Backups never find a root
by name in My Drive independently.

Create an unused recovery database on the configured Postgres server. It
must differ from the database in `DATABASE_URL`; use another name if the
app already uses `receipt_recovery`. Passwords come from configuration:

```sh
sudo docker compose -f compose.yaml exec db sh -c 'createdb -U "$POSTGRES_USER" receipt_recovery'
sudo docker compose -f compose.yaml run --rm app backup restore <object>.dump --target-db receipt_recovery
sudo docker compose -f compose.yaml exec db sh -c 'psql -U "$POSTGRES_USER" -d receipt_recovery -c "SELECT count(*) FROM receipts;"'
```

Nonempty targets are refused unless `--force` is added. With force, archive
objects are cleaned/recreated in a single transaction; failure rolls back.
Verify counts, representative content and image references before switching
configuration. `task backup:test` automates real dump/restore and compares
counts/checksums of six key tables for two synthetic users, plus recovery
through a fresh token against fake Drive. It uses a disposable database.

To activate a recovered target, stop app and change the database name in the
root-only `app.env` `DATABASE_URL`. Set `POSTGRES_DB` in `db.env` to that same
name so container health checks and a future empty-host initialization agree.
This setting does not rename or remove existing databases. Recreate both
services with `sudo docker compose -f compose.yaml up -d --force-recreate --wait`;
`pgdata` is retained. A plain restart does not reload environment files. Check health, sign in,
inspect old receipts/images and reconnect if an older restored token needs
it. Keep the previous database until these checks pass.

### App down: local-file recovery

Download a dump by hand from the configured root's `backups/` folder in the
Drive web UI. Transfer it securely to a private host directory such as
`/root/receipt-recovery/`, with no public link. Start only db, create a separate
target as above and use a one-off container; app HTTP can remain stopped:

```sh
sudo docker compose -f compose.yaml up -d --wait db
sudo docker compose -f compose.yaml run --rm --user root --volume /root/receipt-recovery:/restore:ro app backup restore /restore/downloaded.dump --target-db receipt_recovery
```

The one-off root user reads the root-owned recovery file; the app service
keeps its unprivileged image user. No working Drive grant is needed for this
file path. Use `--force` only for an intentionally replaceable nonempty target.
Verify, switch `DATABASE_URL` and recreate app as above; remove the private
downloaded copy when no longer needed.

## Secrets and key rotation

`APP_ENCRYPTION_KEY` is not in the dump. Keep it, database credentials, Google
client secret, allowed/admin accounts and pinned root id in the encrypted
host configuration backup. Also retain a monthly dump and configuration copy
outside Google, as recorded in ARCHITECTURE §8. Never delete or recreate the
Google Cloud project or Drive OAuth client: keep the same `GOOGLE_CLIENT_ID`.
Only the client secret may be rotated; update app.env and recreate app.

For encryption-key rotation, first verify a dump, preserve the old key with
its dated configuration backup, and stop app. Generate a new random 32-byte
base64 key in the owner's secret workflow (for example host
`openssl rand -base64 32`), replace it in `app.env` and recreate app. The old
token cannot decrypt and reports `reconnect_needed`. Reconnect as admin to
save a fresh grant under the new key. Check an earlier image, list old dumps
and take a new dump. Keep the old key for older recovery sets; restoring an
old dump may require that key or another reconnect under the current key.
Disconnect/reconnect never revokes Google's grant.

## Recovery onto a fresh host

Use this order: **restore secrets -> empty database -> re-authorize Drive ->
restore the database -> restart/recreate app**.

1. Restore `/etc/receipt-tracker/`, including the encryption key, same Google
   client/project and `DRIVE_ROOT_FOLDER_ID` when configured. Restore Compose
   from the repository and the selected image tag under `/opt/receipt-tracker/`.
2. Bring up empty db and app as in first deployment. The app migrates its
   empty bootstrap database; admit the same owner/admin. Docker volumes and
   `/opt` are not recovered from the host backup.
3. Re-authorize Drive without revoking the previous grant. Preserve a moved
   root's id using the earlier moved-root section. Run `backup list` and
   confirm older dumps are visible before proceeding.
4. Create a different database and restore an earlier dump through the new
   connection. This separate target is required because CLI refuses its
   configured source database. Verify counts and content. If files are not
   visible, use the Drive web UI/local-file fallback.
5. Stop app, switch `DATABASE_URL` and `db.env` `POSTGRES_DB` to the restored
   target and recreate both services with `up -d --force-recreate --wait`.
   The existing volume is retained. Check health, sign in and inspect old
   images. Reconnect again if the restored dump contained an unusable old token.

The owner proved both required real-account cases on **2026-11-15**, recorded
in package 04's progress: (a) fresh database/new consent without revoking and
(b) manual Google revocation followed by consent. Both preserved the earlier
synthetic marker and reused the root. These results are specific to that
client/account/date. The moved-root exercise (guide 2.8) is separate and is
not claimed verified.

Before go-live, perform a short **non-revoking** recheck: create a synthetic
dump and record its name/counts, use an isolated empty bootstrap database with
the same client/root settings, authorize anew, list the earlier dump and
restore it into another scratch database with `--force`. Compare counts and
content and confirm one root and one `backups` folder. Leave production data
unchanged; record the result, remove only scratch databases/test dump, and
never revoke Google access as cleanup. This is the manual counterpart of
the fake-Drive recovery test.

The app never revokes Drive access. Manual Google-account revocation may hide
earlier files after reconnecting; the successful 2026-11-15 exercise is not a
guarantee of future provider behavior. The Picker-based re-grant flow in
ARCHITECTURE §5.7 is **planned and unbuilt**, intended for selecting a small
set of dumps; it is not an available recovery button or a proven solution
for thousands of images.

Drive policy restricts app-content backups without Google's written consent.
The owner accepted this risk for personal use (ARCHITECTURE §8); revisit
before public launch. The destination interface allows replacing Drive
without changing retention or scheduling. Images are not copied by this
backup service: Drive is their durable home.


## Connecting MCP clients

The connector URL is `APP_BASE_URL` followed by `/mcp` exactly. Discovery is
public at `/.well-known/oauth-protected-resource` (also at
`/.well-known/oauth-protected-resource/mcp`) and
`/.well-known/oauth-authorization-server`. The root-level OAuth protocol
contract is linked from `api/openapi.yaml` and lives in
`internal/oauthserver/openapi.yaml`. It uses standard OAuth errors, separately
from the session-only REST API and its problem documents.

Clients register as public clients and request an authorization code with
PKCE S256 and the exact MCP resource. The normal Google sign-in returns to a
server-rendered consent page naming the client and redirect hostname. Keeping
the request and approval token in the server session avoids another SPA round
trip carrying authorization state. Approval requires the app's exact Origin.
Loopback clients show a warning about local interception. Registration accepts
HTTPS callbacks and HTTP loopback callbacks. Only the port is ignored for
`localhost` and `127.0.0.1` registration matching; the issued code binds the
actual callback URI, including its port.

Codes are single use and expire in 60 seconds. Access tokens last at most one
hour. `offline_access` enables refresh tokens with a 30-day absolute grant
lifetime. Each refresh replaces the refresh token; reuse revokes the entire
grant, including all of its access tokens. Only SHA-256 hashes are stored.
Tokens contain an untrusted user-routing hint followed by 256 random bits.
The complete hash and bindings must match before the hint is trusted; clients
treat the whole value as opaque. Signing out revokes all MCP connections and
outstanding codes for that user. Removing an email from `ALLOWED_EMAILS` and
restarting rejects that user's tokens on every request, just like sessions.

Registration is limited to ten attempts per hour per IP, with a bounded
in-memory limiter and a database-wide cap of 1,000 clients. Never-used clients
expire in 24 hours and are removed at the next registration. Behind Caddy,
only loopback proxy peers may supply the final `X-Forwarded-For` address.
Keep the app port private as described in Deployment. Used registrations stay
available so existing connections continue to work.

To list or revoke connections, run the local CLI with the application's
environment and database. Obtain the user UUID from `/api/v1/me` while signed
in. The output contains client ids, names and callback addresses, never tokens:

```text
task server -- oauth list --user USER_UUID
task server -- oauth revoke --user USER_UUID --client CLIENT_UUID
```

Revocation affects only that user's grants and outstanding codes for that
client. Reconnect to grant access again. The command requires the server's
database credentials; it is not a public administrative endpoint.

Protocol sources checked:
[Claude authentication](https://claude.com/docs/connectors/building/authentication.md),
[Claude testing](https://claude.com/docs/connectors/building/testing),
[MCP June authorization](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization),
[MCP November authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization),
[RFC 9728](https://www.rfc-editor.org/rfc/rfc9728),
[RFC 8414](https://www.rfc-editor.org/rfc/rfc8414),
[RFC 7591](https://www.rfc-editor.org/rfc/rfc7591),
[RFC 7636](https://www.rfc-editor.org/rfc/rfc7636), and
[OAuth 2.1 draft](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13).


### Claude

1. Start the deployed application with its public HTTPS `APP_BASE_URL` and
   Google sign-in configured. Use that exact origin plus `/mcp` as the URL.
2. Open Claude's **Customize > Connectors**, choose **Add custom connector**,
   and enter a name and the MCP URL. An organization Owner adds connectors
   for Team or Enterprise accounts.
3. Connect, sign in with an allow-listed Google account, and review the client
   name and callback hostname before approving read access. Claude's hosted
   callback is `https://claude.ai/api/mcp/auth_callback`. Claude Code uses a
   loopback callback; the consent page warns about local interception.
4. Confirm the connector shows Connected and all seven tools appear in Tool
   permissions. Enable it in a chat and ask for spending in a named month or
   the price history of a product. Tool results keep currencies separate.
5. If the connection is revoked or the 30-day grant expires, connect again.
   A dropped network stream or application restart alone does not require
   another Google sign-in: an unknown MCP session gets HTTP 404, and the
   client initializes a new session with its existing access token.

### Gemini Apps

1. In the Gemini web app, open **Settings > Connected Apps** (on some accounts,
   **Personal Intelligence > Connected Apps**), then **Add a custom app**.
2. Enter the same public HTTPS MCP URL and continue. Dynamic registration is
   supported, so no manually created client secret is needed.
3. Complete Google sign-in and approve the receipt-tracker consent page.
4. In a chat, type `@` and select the custom app to request receipts, spending
   or prices. Check a known synthetic or non-sensitive example first.

Google currently documents custom apps as available only in the US, age 18+,
in English, with a personal Google account and Keep Activity enabled. A work
or school account is not eligible. If Add a custom app is absent, verify those
requirements before debugging the server. See
[Gemini custom apps](https://support.google.com/gemini/answer/17209137?hl=en).

### MCP Inspector

1. Run the development application with `task db:up` and `task dev`. Its
   `APP_BASE_URL` must agree with the server URL, normally
   `http://localhost:8080`. Google sign-in and its callback must use that
   origin. Development login, when configured, also supplies the normal app
   session; no app session cookie is accepted as MCP authorization.
2. Run `npx @modelcontextprotocol/inspector`, open its local UI, choose
   **Streamable HTTP**, and enter `http://localhost:8080/mcp`.
3. Use the OAuth authentication flow. Confirm discovery, public registration,
   the S256 request, consent naming the return host, and token exchange. Keep
   the generated credentials private.
4. Connect and call each tool: `list_receipts`, `get_receipt`,
   `search_products`, `get_price_history`, `get_spending_summary`,
   `list_categories`, `list_stores`. Use an owned receipt/product id and a
   YYYY-MM month range. Follow `next_cursor` for additional pages;
   price history also has `next_store_cursor` for store summaries.
5. Drop the stream or restart the app and reconnect. With a valid token the
   client can initialize again without a new sign-in. Sign out of the app
   and confirm subsequent MCP calls return the discovery-bearing 401.

The Inspector CLI is also supported for scripted local checks: use
`npx @modelcontextprotocol/inspector --cli URL --transport http --method tools/list`.
For reproducible automation, pin its version and use a private session config
for authorization rather than printing credentials. CLI documentation:
[MCP Inspector](https://github.com/modelcontextprotocol/inspector/tree/main/clients/cli).

All tool responses are capped below 64 KiB of serialized result JSON. Large
results ask for a smaller limit or narrower date range. The default page is
20 items and the maximum is 50. Receipt lines, monthly totals, price points,
and store summaries have explicit pagination; no stored image reference or
raw extraction is exposed. Pagination reflects current data, so changes made
in the app between pages can alter the results. Read-only is enforced by
registering only read tools and by interfaces without mutation methods;
`receipts:read` alone is not a future write-permission system.

The transport uses official Go SDK v1.8.0 session handling and ordinary JSON
POST responses. Idle standalone GET streams receive an SSE comment every
25 seconds (configurable in code with an injected ticker for tests). This
supplements the SDK's initial GET comment; its RPC ping option does not send
periodic SSE bytes. Stream writes clear the normal 30-second HTTP write
deadline and disable proxy buffering. Each new HTTP request verifies the
bearer token again, and SDK sessions are bound to that user. Origin checks
accept the app origin and official Claude/Gemini web origins; the Inspector's
local proxy and CLI do not require cross-origin browser access to `/mcp`.


The initial 401 challenge advertises the metadata URL without narrowing its
scopes. MCP clients choose challenge scopes ahead of metadata scopes; discovery
therefore offers both `receipts:read` and `offline_access` so connectors can
request refresh tokens. The bearer verifier still requires `receipts:read` on
every request. A client explicitly requesting only `receipts:read` receives
short-lived access without a refresh token.
