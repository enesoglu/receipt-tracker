# AGENTS.md

Rules for every coding agent working in this repository. This file is the
single source of rules; `docs/ARCHITECTURE.md` is the single source of design.
When the two seem to disagree, or anything is ambiguous, **stop and ask the
owner**. Write the question in your progress file and continue only with work
the answer cannot affect. Do not guess.

Everything is written in English: code, comments, UI text, docs, commits, PRs.

## Read first

- `docs/ARCHITECTURE.md`: stack, layout, data model, flows. Read it before
  designing anything.
- `.agents/skills/work-package/SKILL.md`: how to start, resume, open the PR
  and merge. Follow it for every work package.
- `.agents/skills/add-endpoint/SKILL.md`: the contract-first recipe. Follow it
  whenever you add or change an API operation.
- `.agents/skills/reference-lookup/SKILL.md`: how to consult the reference
  codebase. Follow it before designing a subsystem the reference also has.

## Commands

All commands run through `task` and behave the same in PowerShell and bash.

| Command | Does |
| --- | --- |
| `task setup` | Install tools and frontend dependencies |
| `task db:up` | Start the development Postgres |
| `task dev` | Run the server and the web dev server with reload |
| `task generate` | Regenerate sqlc, OpenAPI server and TypeScript client |
| `task lint` | Lint and format-check Go and web |
| `task test` | All Go and web tests (needs `task db:up`) |
| `task build` | Build the web app and the server binary |
| `task check` | `generate` drift check, `lint`, `test`, `build`: what CI runs |

`task check` must pass before every push.

## Scope and ownership

- Work only on your work package. Edit only the paths its prompt lists as
  owned. If you need a change elsewhere, ask.
- Add a dependency only if the stack table in `docs/ARCHITECTURE.md` names
  it. Anything else needs the owner's approval first.
- Shared files may be edited for your own lines only: `go.mod`, `go.sum`,
  `cmd/server/main.go`, `api/openapi.yaml`, the generated files that follow
  from your sources, the wiring in `internal/httpapi` that registers your
  handlers, `web/package.json` with its lock file, tests beside files you
  own, and `.env.example`, `README.md` and `docs/` where your change alters
  documented behaviour. Resolve conflicts by rebasing and regenerating.
- Log out-of-scope ideas in your progress file instead of building them.

## Go

- Layout and package boundaries follow `docs/ARCHITECTURE.md` §3. Package
  names are short, lower-case, one word. Name packages for what they provide
  (`receipts`, `matching`), never `util`, `common` or `helpers`.
- `cmd/server/main.go` only wires dependencies. Dependencies are passed
  through constructors named `New`. State lives in structs: write no `init`
  functions and no mutable package-level variables.
- `context.Context` is the first parameter of anything that does I/O.
- Define an interface in the package that consumes it, with only the methods
  that consumer needs. Return concrete types.
- The authenticated user id is an explicit `userID uuid.UUID` parameter on
  every service method and query. It is read from the session or token in
  middleware and nowhere else.
- Money and quantities are `decimal.Decimal`. Use `float64` only for scores
  and confidences.
- Time comes from an injected clock in code that tests need to control.
  Store and compare in UTC.
- Exported identifiers have a doc comment that starts with the name. Comments
  explain why, not what.

### Errors

- Return errors; wrap with context in lower case and no trailing punctuation:
  `fmt.Errorf("load receipt: %w", err)`.
- Classify with `internal/apperr` kinds (`NotFound`, `Invalid`, `Conflict`,
  `Forbidden`, `Unavailable`). Test with `errors.Is` / `errors.As`.
- Only `internal/httpapi` turns errors into HTTP responses, as RFC 9457
  problem details, in one mapping function. Unknown errors become 500 with a
  generic message.
- A row owned by another user is `NotFound`, never `Forbidden`.
- Log an error once, where it is handled. `panic` only in `main` for startup
  failures.

### Logging

- `log/slog` with the injected logger; JSON in production.
- Message is a short lower-case phrase; data goes in attributes with
  `snake_case` keys: `logger.Info("receipt extracted", "receipt_id", id)`.
- Identify users by `user_id`. Never log tokens, secrets, cookies, emails,
  query strings or receipt contents.

### Database

- SQL lives in `internal/db/queries/<domain>.sql` and is called through
  sqlc-generated code. Build no SQL strings in Go.
- Every query on a user-owned table has `user_id = $n` in its `WHERE`.
- Migrations are goose SQL files in `internal/db/migrations`, named
  `NNNNN_description.sql` where `NNNNN` is your work-package number times 10,
  plus a counter (package 08 uses `00080`, `00081`, …). A merged migration is
  never edited; fix forward with a new one. Every migration has a working
  `-- +goose Down`.
- Multi-step writes run in one transaction.

### Generated code

`internal/db/sqlc/`, `internal/httpapi/api.gen.go` and
`web/src/api/schema.gen.ts` are generated and committed. Change the source
(`*.sql`, `api/openapi.yaml`), run `task generate`, commit both. After a
rebase conflict in a generated file, regenerate instead of merging by hand.

### Tests

- Standard `testing`, table-driven, compared with `go-cmp`. Test names
  describe behaviour: `TestMatch_SizeConflictAsksUser`.
- Database code is tested against real Postgres (`TEST_DATABASE_URL`, set by
  `task test`). External services are replaced by hand-written fakes behind
  the consumer's interface or by `httptest` servers. No mock generators.
- `TEST_SKIP_DATABASE=1` exists only for the Windows CI job, which checks
  tooling; never set it anywhere else.
- Every endpoint and MCP tool that touches user-owned data has an isolation
  test: user B requesting user A's resource gets 404 and no data.
- Fixtures in `testdata/` are synthetic. Real receipts, names, emails and
  tokens never enter the repository.
- New behaviour comes with tests in the same PR. `internal/matching` keeps
  statement coverage at or above 90%.

## Web

- TypeScript strict; no `any`, no `@ts-ignore`. Function components and hooks.
- Structure: `web/src/api/` (generated schema, client, query hooks),
  `web/src/components/ui/` (shared primitives), `web/src/features/<feature>/`
  (screens and their components), `web/src/styles/tokens.css`.
- Components are `PascalCase.tsx`, one component per file; other files are
  `kebab-case.ts`. Tests sit next to the file as `*.test.tsx`.
- All server access goes through the generated client and TanStack Query
  hooks in `web/src/api/`. Components never call `fetch`.
- Colours, spacing, radii and type sizes come from the tokens in
  `web/src/styles/tokens.css` via Tailwind theme utilities. Components use
  token utilities only: no hex values, no arbitrary values such as `w-[13px]`.
- The UI is static: no animations, no transitions, charts render with
  animation disabled.
- Money arrives as decimal strings and is formatted with one shared
  formatter. The client does no money arithmetic.
- Every screen handles loading, empty and error states.

## Security

- Secrets come from the environment. `.env` files, keys, tokens and real
  receipt data are never committed. `.env.example` holds names only.
- Validate all input at the API boundary; the OpenAPI schema is the first
  line, service-level checks the second.
- File names from clients are never used as paths. Storage keys are built
  from ids.
- Auth, session, OAuth and token code changes need tests for the failure
  paths, not only the happy path.

## Git and pull requests

History on `main` must read like real, incremental development. Pull requests
are **rebase-merged**: every commit on your branch lands on `main` unchanged,
so every commit is held to the standard below.

- `main` is protected and linear: changes arrive only through pull requests
  with green CI. Never force-push `main`; never push to it directly.
- A work package is delivered as one or more small pull requests, split at
  the seams its prompt names, each merged before the next starts. Branch
  names: `<type>/<nn>-<slug>` for work-package PRs (`feat/05-matching-core`),
  `<type>/<slug>` for anything else. Types: `feat`, `fix`, `chore`, `docs`,
  `refactor`, `test`, `ci`.
- Each commit is **atomic**: one logical change, with its tests, that builds
  and passes `task check` on its own.
- Commit messages follow Conventional Commits: `<type>(<scope>): <subject>`,
  imperative mood, at most 72 characters, scope is the package or area
  (`matching`, `web`, `ci`). The body explains what changed and why.
  Work-package numbers and words like "step" or "phase" stay out of messages.
- Every commit ends with a trailer crediting the agent that wrote it:
  - Claude Code: `Co-Authored-By: Claude <model name> <noreply@anthropic.com>`
  - Codex: `Co-Authored-By: Codex <model name> <noreply@openai.com>`
  - Antigravity / Gemini: `Co-Authored-By: Gemini <model name> <noreply@google.com>`
- Correct a mistake in the commit that made it: record the correction with
  `git commit --fixup=<sha>` and fold it in before the PR. A branch offered
  for review contains no `wip`, `fixup!`, "fix lint", "address review" or
  merge commits. The `work-package` skill gives the exact, non-interactive
  commands.
- Rebase your branch on `origin/main` before opening the PR and whenever it
  falls behind. Rewriting and force-pushing your own unmerged branch with
  `--force-with-lease` is expected.
- The PR title is a short Conventional Commit style summary of the series;
  the description explains the series and how it was verified.
- The agent opens the PR, waits for CI, fixes failures by amending the
  responsible commit, and rebase-merges it itself once every check is green.

## Definition of done

A work package is done when all of these hold:

1. Every acceptance criterion in its prompt is met and demonstrated by a test
   or a recorded command output.
2. `task check` passes locally and CI is green on the PR.
3. New endpoints exist in `api/openapi.yaml`, are implemented, and have
   isolation tests.
4. Docs changed where behaviour changed (`docs/`, `.env.example`, `README.md`).
5. No secrets, no real personal data, no unrelated changes in the diff.
6. Every commit on `main` from this package builds, passes tests, and carries
   the trailer.
7. Every PR is rebase-merged, its branch deleted, and the progress file says
   so.
