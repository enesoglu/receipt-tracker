# receipt-tracker

A self-hosted app that turns grocery receipt photos into structured data,
product price history, and spending reports. One Go binary serves the React
application; PostgreSQL is the only companion service. The project is MIT
licensed.

## Status

The bootstrap provides the server, embedded placeholder page, design tokens,
development database, reproducible tooling, containers, and CI. Receipt
uploading, authentication, extraction, matching, reports, and backups are
planned in later work packages. No application schema or API contract exists
yet; `task generate` starts running generators when their sources are added.

`/livez` and `/healthz` currently report process health. Database and Drive
readiness monitoring arrives with those integrations. The app does not yet
read or store receipts.

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
variable with the same name overrides it for CI. Bootstrap tests need no
application tables. The database volume persists until explicitly removed.

## Commands

| Command         | Purpose                                                                           |
| --------------- | --------------------------------------------------------------------------------- |
| `task setup`    | Download pinned Go modules, install web packages, and install the verified linter |
| `task db:up`    | Start Postgres and wait for both development and test databases                   |
| `task dev`      | Run Go and Vite with reload                                                       |
| `task generate` | Generate sqlc and API bindings when source configurations exist                   |
| `task lint`     | Check Go formatting/lint, strict TypeScript, ESLint, Prettier, and UI policy      |
| `task test`     | Build embedded assets, then run Go and web tests                                  |
| `task build`    | Produce `bin/server.exe` on Windows or `bin/server` on Linux                      |
| `task check`    | Check generated drift, lint, test, and build                                      |

Run `task build`, then `./bin/server.exe` in PowerShell or `./bin/server` in
bash to serve the built application and health endpoints on port 8080 without
Vite. The binary embeds all frontend assets and needs no external web files.

The skeleton reads `APP_ENV` (`dev` by default) and `APP_LISTEN_ADDR` (`:8080`
by default). It rejects `DEV_LOGIN_EMAIL` outside development. The complete
future environment-variable inventory is in `.env.example`, with names only;
secrets and `.env` files must stay outside Git. See
[the architecture](docs/ARCHITECTURE.md) for the binding design and future
configuration requirements.

## Container

```bash
docker build -f deploy/Dockerfile -t receipt-tracker:dev .
docker run --rm -p 127.0.0.1:8080:8080 receipt-tracker:dev
```

The image uses Node 24.22.3 and Go 1.26.11 build stages, then Alpine 3.23.4
with `postgresql18-client`. It runs as the unprivileged `app` user. CI checks
that the image is below 60 MB and serves the embedded page and both health
endpoints. Development Postgres is pinned to `postgres:18.4-alpine3.23`.

## Checks and contribution workflow

`task check` must pass before pushing. CI runs checks on Ubuntu 24.04 and
Windows PowerShell, validates the development Compose file, checks PR titles
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
