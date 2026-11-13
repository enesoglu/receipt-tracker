---
name: add-endpoint
description: Contract-first recipe for receipt-tracker API work. Use when adding or changing a REST operation, its SQL, its handler, or the web code that calls it.
---

# Adding or changing an API operation

The contract comes first so backend and web can be built independently.

1. **Contract.** Edit `api/openapi.yaml`. Give the operation an `operationId`
   in `camelCase` (`listReceipts`), one tag, request and response schemas
   under `components/schemas`, and the shared `Problem` response for errors.
   Money is a decimal string (`"12.50"`), dates are `date`, ids are `uuid`.
   List operations take `limit` and an opaque `cursor`.
2. **Generate.** `task generate`. Commit the contract and generated files
   together.
3. **SQL.** Add queries to `internal/db/queries/<domain>.sql` with sqlc
   annotations. Every query on a user-owned table filters by `user_id`. Run
   `task generate` again.
4. **Service.** Put the behaviour in the domain package (`internal/receipts`,
   …) as a method taking `ctx` and `userID` first. Return `apperr` kinds.
5. **Handler.** Implement the generated method in the handler file for the
   operation's tag in `internal/httpapi/`. A handler reads the user id from
   the request context, converts types, calls one service method and returns.
   It contains no business rules and no SQL.
6. **Tests.** Service tests against real Postgres; one handler test for the
   happy path, one per error kind the operation can return, and the isolation
   test (user B gets 404 for user A's resource).
7. **Web.** Add or update the query hook in `web/src/api/` using the generated
   types. Components use the hook.

Done when `task check` passes and the operation's examples in the contract
match what the handler returns.

A change to an operation another work package already implemented is a
contract change: describe it under "Contract or schema changes" in the PR.
