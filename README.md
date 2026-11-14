# receipt-tracker

A self-hosted app that turns grocery receipt photos into structured data,
product price history, and spending reports. One Go binary serves the React
application; PostgreSQL is the only companion service. The project is MIT
licensed; the notices of the third-party components compiled into the server
are in [`THIRD_PARTY_LICENSES`](THIRD_PARTY_LICENSES).

## Status

The repository provides the server, embedded placeholder page, design tokens,
development database, reproducible tooling, containers, and CI, plus the
complete v1 database schema, validated configuration, Google sign-in and
server-side sessions. Receipt photos can be uploaded (JPEG, PNG, and HEIC/HEIF
converted to JPEG); each becomes a queued receipt with a job for the
background worker. Extraction, matching, reports and backups are planned in
later work packages. The v1 API is fully specified in `api/openapi.yaml`. API
requests require a session; authenticated operations answer with a `501`
problem document until their behaviour is built, except for the implemented
signed-in user, logout, storage and upload operations.

The server validates its configuration, connects to PostgreSQL and applies
the embedded migrations before it accepts requests. `/livez` and `/healthz`
currently report process health. Database and Drive readiness monitoring
arrives with those integrations.

## Prerequisites

- Go 1.26.11 (the Go toolchain directive also selects it automatically).
- Node 24.22.3, including npm. Other Node major versions are rejected.
- Docker with Linux containers and Compose; start the Docker engine first.
- Task 3.54.0. Install it with
  `go install github.com/go-task/task/v3/cmd/task@v3.54.0` and put Go's `bin`
  directory on your `PATH`.

Exact Go dependencies and generators are pinned in `go.mod`; exact frontend
versions are pinned in `web/package-lock.json`. `task setup` downloads
GolangCI-Lint 2.15.0 and checks its official SHA-256 checksum. It needs `tar`
(available on Windows and Ubuntu). Tool and build output lives in ignored
`bin/`; frontend dependencies live in ignored `web/node_modules/`.

## Quick start: Windows PowerShell

```powershell
git clone https://github.com/enesoglu/receipt-tracker.git
Set-Location receipt-tracker
go install github.com/go-task/task/v3/cmd/task@v3.54.0
$env:Path = "$(go env GOPATH)\bin;$env:Path"
task setup
task db:up
task check
Copy-Item .env.example .env   # then fill it in, see Configuration
task dev
```

## Quick start: Ubuntu / bash

```bash
git clone https://github.com/enesoglu/receipt-tracker.git
cd receipt-tracker
go install github.com/go-task/task/v3/cmd/task@v3.54.0
export PATH="$(go env GOPATH)/bin:$PATH"
task setup
task db:up
task check
cp .env.example .env   # then fill it in, see Configuration
task dev
```

Open [the Vite development app](http://127.0.0.1:5173). The Go server listens
on `:8080`; Vite proxies `/api`, `/auth`, `/oauth`, and `/mcp` there. Vite
reloads frontend changes and Node watches Go sources and rebuilds the server.
Stop both with Ctrl+C. Development ports 5173, 8080, and 15432 must be free.

The development Compose file binds Postgres to loopback port **15432** and
creates `receipt_tracker` and `receipt_tracker_test`. Its `receipt` user and
password are synthetic development values, never production credentials.
`task test` sets `TEST_DATABASE_URL` to the test database; an environment
variable with the same name overrides it for CI. Each database test creates
its own database on that server, applies the migrations and drops it again
(`internal/db/dbtest`), so the role must be allowed to create databases. A
database test fails, and never skips, when the server is missing. The one
exception is `TEST_SKIP_DATABASE=1`, set only by the Windows CI job, whose
runner cannot start the Linux database container. The database volume
persists until explicitly removed.

## Commands

| Command         | Purpose                                                                           |
| --------------- | --------------------------------------------------------------------------------- |
| `task setup`    | Download pinned Go modules, install web packages, and install the verified linter |
| `task db:up`    | Start Postgres and wait for both development and test databases                   |
| `task dev`      | Run Go and Vite with reload                                                       |
| `task server -- <args>` | Build and run the server with `.env` and optional arguments |
| `task generate` | Regenerate sqlc code and the Go and TypeScript API bindings from their sources     |
| `task lint`     | Check Go formatting/lint, strict TypeScript, ESLint, Prettier, and UI policy      |
| `task test`     | Build embedded assets, then run Go and web tests                                  |
| `task build`    | Produce `bin/server.exe` on Windows or `bin/server` on Linux                      |
| `task check`    | Check generated drift, lint, test, and build                                      |

Run `task build`, then `./bin/server.exe` in PowerShell or `./bin/server` in
bash to serve the built application and health endpoints on port 8080 without
Vite. The binary embeds all frontend assets and migrations; it needs no
external files, only its configuration and a reachable database.

## Configuration

The server reads environment variables only. `.env.example` lists every
name, without values; `.env` files and secrets must stay outside Git. At
startup the server checks all variables and, if anything is wrong, exits with
one error that lists every problem. Messages name the variable and the rule,
never the value.

| Variable                 | Required                         | Value                                                     |
| ------------------------ | -------------------------------- | --------------------------------------------------------- |
| `APP_ENV`                | yes                              | `dev` or `prod`                                           |
| `APP_LISTEN_ADDR`        | no, default `:8080`              | `host:port` or `:port`                                    |
| `APP_BASE_URL`           | yes                              | Public origin without a path; `https` when `APP_ENV=prod` |
| `DATABASE_URL`           | yes                              | `postgres://user:password@host:port/database`             |
| `APP_ENCRYPTION_KEY`     | yes                              | 32 random bytes in base64: `openssl rand -base64 32`      |
| `ALLOWED_EMAILS`         | yes                              | Comma-separated addresses that may sign in                |
| `ADMIN_EMAIL`            | yes                              | One of `ALLOWED_EMAILS`                                   |
| `GOOGLE_CLIENT_ID`       | yes, except as noted below       | OAuth client for sign-in and Drive                        |
| `GOOGLE_CLIENT_SECRET`   | yes, except as noted below       | Its secret                                                |
| `STORAGE_BACKEND`        | yes                              | `local` or `drive`                                        |
| `STORAGE_LOCAL_DIR`      | when `STORAGE_BACKEND=local`     | Directory for images                                      |
| `DRIVE_ROOT_FOLDER`      | no, default `receipt-tracker`    | Folder name in Drive                                      |
| `EXTRACTOR`              | yes                              | `azure` or `fake`                                         |
| `AZURE_DI_ENDPOINT`      | when `EXTRACTOR=azure`           | `https` endpoint of the Document Intelligence resource    |
| `AZURE_DI_KEY`           | when `EXTRACTOR=azure`           | Its key                                                   |
| `AZURE_DI_MONTHLY_PAGES` | no, default `500`                | Pages allowed per month                                   |
| `BACKUP_DESTINATION`     | yes                              | `drive`, `local` or `none`                                |
| `BACKUP_LOCAL_DIR`       | when `BACKUP_DESTINATION=local`  | Directory for dumps                                       |
| `BACKUP_SCHEDULE`        | unless `BACKUP_DESTINATION=none` | Daily start time in UTC as `HH:MM`                        |
| `BACKUP_HEARTBEAT_URL`   | no                               | Pinged after each backup run                              |
| `DEV_LOGIN_EMAIL`        | no; refused unless `APP_ENV=dev` | Enables the development sign-in for that address          |

The Google client may be left out only when `APP_ENV=dev`, `DEV_LOGIN_EMAIL`
is set and neither images nor backups use Drive.

For development, copy `.env.example` to `.env` and set at least these values.
They use no external service and match the database from `task db:up`;
replace the key with your own `openssl rand -base64 32` output and the
address with yours.

```
APP_ENV=dev
APP_BASE_URL=http://localhost:8080
DATABASE_URL=postgres://receipt:receipt@localhost:15432/receipt_tracker?sslmode=disable
APP_ENCRYPTION_KEY=<openssl rand -base64 32>
ALLOWED_EMAILS=you@example.com
ADMIN_EMAIL=you@example.com
STORAGE_BACKEND=local
STORAGE_LOCAL_DIR=data/images
EXTRACTOR=fake
BACKUP_DESTINATION=none
DEV_LOGIN_EMAIL=you@example.com
```

`task dev` and `task server` load `.env` from the repository root when it exists; the binary
itself never reads files. A variable that is already set in the shell takes
precedence over the file. Tests never see `.env`: only the explicit server
script loads it. See [the architecture](docs/ARCHITECTURE.md) for the binding
design.

## API contract

`api/openapi.yaml` is the source of truth for the REST API under `/api/v1`:
every operation, its schemas, its examples and the shared problem-details
error. `task generate` writes the Go server interface and types to
`internal/httpapi/api.gen.go` (oapi-codegen, configured in
`api/oapi-codegen.yaml`) and the TypeScript types to
`web/src/api/schema.gen.ts` (openapi-typescript). A Go test validates the
contract, including every example against its schema. To add or change an
operation, follow `.agents/skills/add-endpoint/SKILL.md`.

Handlers live in `internal/httpapi`, one file per API tag. They return a
response or an error; one function maps every error to an RFC 9457 problem
document (`application/problem+json`) that carries the id from the
`X-Request-Id` response header. Each request is logged as one JSON line,
without its query string. The API refuses state-changing requests from other
origins and request bodies over 1 MiB (uploads: 20 files of 15 MB).

The web app reaches the API only through `web/src/api/client.ts`: a typed
`openapi-fetch` client for the generated schema, and `unwrap`, which returns
the data of a call or throws an `ApiError` holding the problem document.

## Database schema

The schema is a series of goose migrations in `internal/db/migrations`,
embedded in the binary and applied at startup under a lock. Queries live in
`internal/db/queries/<domain>.sql`; `task generate` turns them into type-safe
Go in `internal/db/sqlc`. Generated code is committed, and `task check` fails
when it differs from its sources.

## Container

```bash
docker build -f deploy/Dockerfile -t receipt-tracker:dev .
docker run --rm -p 127.0.0.1:8080:8080 --env-file .env receipt-tracker:dev
```

The image sets `APP_ENV=prod` and `APP_LISTEN_ADDR=:8080`; everything else
comes from the environment, and `DATABASE_URL` must be reachable from inside
the container. Without configuration the container exits with the list of
missing variables.

The image uses Node 24.22.3 and Go 1.26.11 build stages, then Alpine 3.23.4
with `postgresql18-client`. It runs as the unprivileged `app` user. CI checks
that the image is below 60 MB, refuses to start without configuration, and,
next to a PostgreSQL container, applies the migrations and serves the embedded
page and both health endpoints. Development Postgres is pinned to `postgres:18.4-alpine3.23`.

## Checks and contribution workflow

`task check` must pass before pushing. CI runs checks on Ubuntu 24.04 and
Windows PowerShell (tooling only, without the database tests), validates the development Compose file, checks PR titles
and branch names, and builds/smoke-tests the runtime image. The `commits` job
checks each PR commit's subject, trailer, linear history, and independent
`task check` result in an isolated snapshot. Only the two owner-approved
pre-tooling bootstrap commits are exempt from executable checks.

Go formatting uses gofumpt and goimports. Application Go checks target
`cmd/` and `internal/` so Go examples shipped in frontend dependencies are
not treated as application packages. UI policy rejects component hex
colours, Tailwind arbitrary values/properties, and CSS motion; use the tokens
in `web/src/styles/tokens.css`. A pinned minimatch override supplies the
security fix required by the lint toolchain.

Pull requests are rebase-merged after every check is green. Configure branch
protection on `main` to require the final check named **ci**, require pull
requests, and enforce linear history. There is no deployment workflow.

## Sign-in and sessions

Register a Google web OAuth client with the redirect URI
`<APP_BASE_URL>/auth/google/callback`, and configure `GOOGLE_CLIENT_ID` and
`GOOGLE_CLIENT_SECRET`. Open `/auth/google/login` to sign in. Only verified
addresses in `ALLOWED_EMAILS` are admitted (case-insensitive); all others see
`access is closed`. The first sign-in creates the user, default settings and
eight grocery/household categories in one transaction. `ADMIN_EMAIL` determines
the admin flag on each sign-in. Admission removal also invalidates existing
sessions on their next API request.

Sign-in requests use `openid email profile`, state, nonce and PKCE S256. A
single-use pending attempt expires after ten minutes and is bound to a short-lived
`auth_binding` cookie. Attempts are held in memory, bounded to 4,096 entries;
restart the sign-in after a server restart. Google endpoints are fixed at startup,
with signing keys fetched on demand, so starting the server needs no Google
network request. Google access and refresh tokens are never stored.

Sessions use scs and PostgreSQL. The `session` cookie is host-only, `HttpOnly`,
`SameSite=Lax`, and `Secure` in production, with a 30-day maximum lifetime and
a 24-hour idle timeout. Tokens are hashed in the database and renewed on sign-in.
`GET /api/v1/me` returns the current user; `POST /api/v1/auth/logout` destroys the
session and expires its cookie. Missing, expired and revoked sessions receive
an RFC 9457 `401` problem document. Cross-origin state-changing API requests are
rejected by the existing CSRF middleware.

For local development, open `/auth/dev/login` when `APP_ENV=dev` and
`DEV_LOGIN_EMAIL` is set to an allowed address. This creates a separate development
identity; it does not impersonate an existing Google subject. The route is absent
in production and when the variable is unset. There is no authentication UI yet.

For later isolation tests, `internal/testutil` documents `NewUser` and `Request`.
Create a migrated database with `dbtest.New(t)`, build `auth.New` with a dev login
email, and call `testutil.NewUser(t, users)` to provision it. Build sessions with
`auth.NewSessions`, register its returned cleanup with `t.Cleanup`, and pass those
same services to `httpapi.New`. Use
`testutil.Request(t, sessions, user.ID, method, target, body)` for each user to send
a real persisted session through the production middleware. Provision a second
user with another allowed dev email for resource-isolation assertions. The helper
is for tests only and never enables a runtime authentication bypass.

Google Drive setup and connection recovery are documented in [Storage operations](docs/OPERATIONS.md).
