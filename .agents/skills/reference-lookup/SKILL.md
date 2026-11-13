---
name: reference-lookup
description: How to consult the Receipt Wrangler reference codebase. Use before designing or implementing a receipt-tracker subsystem that the reference also has (file handling, auth, MCP and OAuth, reporting, money, receipt history, tests).
---

# Reference codebase lookup

Receipt Wrangler is a mature receipt manager with a Go API. It is cloned next
to the main checkout at `../receipt-wrangler-ref` (clone it there from
`https://github.com/Receipt-Wrangler/receipt-wrangler` if missing).

**Licence boundary.** The reference is AGPL-3.0; this project is MIT. Read it
to learn which cases exist and which bugs they hit. Then close it and write
our code from our own design in `docs/ARCHITECTURE.md`. Carry over ideas and
edge cases, never code, file structure, identifiers or comments.

## Where to look

`api/CLAUDE.md` is the richest source: it records the bugs and the reasons.
Search it by heading before reading code.

| Topic | Read |
| --- | --- |
| File handling, path safety, image types | `api/CLAUDE.md` "Filesystem Access & Path-Traversal Safety"; `api/internal/utils/data_files.go`; `api/internal/repositories/files.go` |
| MCP server and its OAuth server | `api/CLAUDE.md` "MCP Server & OAuth 2.1"; `api/internal/mcp/`; `api/internal/oauth/` (and their tests) |
| Sign-in via OIDC, sessions, cookies | `api/CLAUDE.md` "OIDC (OpenID Connect) login", "Session lifetime"; `api/internal/oidc/` |
| API keys and scopes | `api/CLAUDE.md` "API key scopes"; `api/internal/middleware/api_key_scope.go` |
| Money and decimals | `api/internal/models/receipt.go`, `item.go`; `api/internal/reporting/render/currency.go` |
| Reporting and aggregation | `api/internal/reporting/README.md`; `api/CLAUDE.md` "Reporting Engine" |
| Receipt history and update snapshots | `api/CLAUDE.md` "Receipt history", "Receipt update snapshots" |
| Background jobs, retries, temp files | `api/internal/wranglerasynq/`; `api/CLAUDE.md` "Temporary file retention & cleanup" |
| Handler error logging | `api/CLAUDE.md` "Handler errors are logged with a stack trace" |
| Test layout and authorization tests | `api/internal/handlers/*_test.go`, `api/internal/repositories/main_test.go` |

The reference has no cross-receipt product matching. That design is ours:
`docs/ARCHITECTURE.md` §5.2.

## How to use what you find

Record in your progress file, under "Decisions", each edge case you took from
the reference and how our design handles it. Where the reference and our
architecture differ (it uses GORM, Redis/Asynq, JWT; we use sqlc, a Postgres
queue, opaque tokens), our architecture wins.
