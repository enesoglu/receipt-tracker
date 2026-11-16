# Release hardening review

Reviewed on 2026-11-16 against the completed application. This is a focused
code and integration review, supported by synthetic fixtures and real
PostgreSQL. It does not certify every provider, image variant or traffic pattern.

## Findings and fixes

Every security finding was reproduced before its fix and delivered separately.

| Finding | Severity | Before-fix evidence | Fix and regression |
| --- | --- | --- | --- |
| H1: missing global browser security headers | Low, defense in depth | SPA, API and generic errors lacked CSP, frame denial, no-referrer and nosniff | [PR #36](https://github.com/enesoglu/receipt-tracker/pull/36); `TestHandler_SetsSecurityHeadersOnEveryResponse` covers every route family, preserving stricter OAuth consent headers |
| H2: administrator rotation left existing sessions privileged | Medium | Former admin's existing session could disconnect Drive after `ADMIN_EMAIL` changed; HTTP regression returned 204 | [PR #37](https://github.com/enesoglu/receipt-tracker/pull/37); `TestStorage_AdminRotationRechecksExistingSessions` requires former admin 404, preserved connection, and new admin access through an existing session |
| H3: 16-bit PNG exhausted OCR resize memory | Medium, authenticated availability | Valid 49 MP PNG just above 4 MiB OOM-killed the public extractor in a 512 MiB/no-swap container, exit 137 | [PR #39](https://github.com/enesoglu/receipt-tracker/pull/39); `TestExtract_RefusesOversizedDecodedImageBeforeSubmission` rejects before decoding, provider submission or quota use |

H3's fixed-code capped probe refused the 7000×7000 image with zero provider
submissions and a 13,856,768-byte peak. A 6000×4000 16-bit PNG still completed
extraction against an in-container test provider, with one submission and a
291,532,800-byte peak. Neither fixed-code probe was OOM-killed. The 26M pixel
cap preserves 24 MP support; stored images are retained after processing fails.

The browser policy permits same-origin scripts, inline styles needed by charts,
and blob/data images needed by previews. It denies framing, objects, external
base URLs and cross-origin form actions. OAuth consent keeps its stricter CSP.
Authenticated JSON and errors already used `no-store`; HSTS remains at Caddy.
Chrome rendered the receipts filter and spending chart under the new policy.

## Review coverage

| Boundary | Cases checked | Representative tests |
| --- | --- | --- |
| Google sign-in and sessions | State, nonce, PKCE, verified allow-list, expiry, replay, session renewal, admission removal, production cookies, logout isolation and secret-safe errors | `TestLogin_RejectsInvalidCallbacksWithoutUserOrSession`, `TestLogin_ConcurrentCallbackIsExchangedOnlyOnce`, `TestSession_ProductionCookiesAndDevRoute`, `TestLogout_PreservesAnotherUsersSession` |
| MCP OAuth server | Redirect/resource/client/PKCE binding, consent, one-use codes, concurrent redemption, refresh rotation/replay revocation, expiry, registration limits and user-scoped revocation | `TestOAuth_RejectsInvalidCodeBindingsWithoutBurningCode`, `TestOAuth_ConcurrentRedemptionsHaveOneWinnerAndRefreshReplayRevokes`, `TestOAuth_CliRevocationPreservesOtherUsersGrant` |
| MCP transport/tools | Bearer-only admission, no REST authority, read-only inventory, all seven tools isolated, bound cursors, result bounds and reconnect behavior | `TestMCP_EveryToolIsolatesUsers`, `TestMCP_BearerOnlyMetadataChallengeAndNoRESTAuthority`, `TestMCP_EveryToolBoundsItsSerializedOutput`, `TestMCP_IdleKeepaliveReconnectAndForgottenSession` |
| Storage | ID-derived keys, local traversal/symlink confinement, atomic failed writes, encrypted/authenticated Drive tokens, admin-only consent, key rotation and refresh failures | `TestKey_UsesOnlyIdentifiersAndTrustedExtension`, `TestLocal_RefusesTraversalForEveryOperation`, `TestLocal_SymlinkCannotEscapeRoot`, `TestTokenCipher_AuthenticatedAndKeyed`, `TestConnection_RefreshFailuresAndKeyRotation` |
| Uploads and images | Session requirement, streaming parts, 20-file and 15 MiB bounds, duplicates scoped to user, decoder limits, failure handling, original preservation | `TestUploadReceipts_UserBGetsOwnReceiptForUserAsPhoto`, `TestUploadReceipts_RefusesMoreThanTwentyFiles`, ingest conversion tests, H3 regression |
| HTTP perimeter | Same-origin mutation protection, bounded bodies, generic server errors, query/credential-safe logging and response headers | HTTP middleware/problem tests and H1 regression |

The [isolation audit](ISOLATION-AUDIT.md) maps all 35 REST operations, seven
OAuth protocol operations and seven MCP tools to tests. Its inventory test
fails when an operation is added without an audit entry or its named test
disappears. A query-source guard discovers `user_id` tables from the migrated
schema and requires bound ownership in named sqlc queries. This heuristic is
complemented by real two-user behavior tests; it is not a formal SQL proof.

## Consistency and contract checks

[PR #38](https://github.com/enesoglu/receipt-tracker/pull/38) corrected five
specific repository-rule violations:

- PostgreSQL timestamps now decode in UTC on all pool connections, including
  arrays and both wire formats. Purchase dates and wall-clock times are unchanged.
- Missing service wiring returns classified 503 problems. No mounted operation
  retains a 501 fallback; the generator's unused `Unimplemented` helper is not mounted.
- Fixed Go lookups use local patterns or switches; a source guard excludes
  mutable production globals while permitting compiler-owned embeds.
- Null bytes in stored text, search and decoded cursor names return 400 before
  PostgreSQL. HTTP regressions verify receipt/product data remains unchanged.
- Ten embedded React components moved into their own files. All 34 existing
  function bodies compared unchanged; screen interaction tests and a policy
  guard verify the result.

`task e2e` starts the compiled server with real PostgreSQL, fake extraction and
local image storage. Through HTTP it signs in two users, uploads all synthetic
extraction cases, waits for processing, resolves flags, edits data, merges and
splits products, reads histories/reports and checks the other user's empty/404
responses. Real responses from every REST operation are validated against
OpenAPI. Drive operations use legitimate unconfigured-error responses here;
their configured successes are covered by separate fake-provider integration tests.

## Dependency scans and verification

Recorded after the final application fix with Go 1.26.11:

| Command | Result |
| --- | --- |
| `task check` | Generated drift, lint, Go/web tests and builds pass; commits also checked independently |
| `task e2e` | Compiled-server workflow and every REST response contract pass locally and in CI |
| `task backup:test` | Real dump/restore and fresh-connection fake-Drive recovery pass; disposable resources removed |
| `task audit` | Zero vulnerabilities affecting Go code or imported packages; zero production npm vulnerabilities |
| Matching coverage | 100.0% of statements, above the 90% requirement |
| Bounded HEIC fuzz smoke | 30 baseline seeds, 33 executions, two workers, 30 seconds; no saved failing input |

The verbose Go scan reports one module-only advisory,
[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932), concerning unmaintained,
unsafe OpenPGP code in `golang.org/x/crypto`. The application does not import
the affected package; the scan reports zero vulnerable imported packages and
zero affected code. Other crypto packages are required. This unused-package
advisory is justified rather than changing unrelated pinned dependencies.

## Limits of the evidence

External integrations use test providers in automated tests. The owner recorded
both real Drive reauthorization cases on 2026-11-15; see
[Operations](OPERATIONS.md#recovery-onto-a-fresh-host) for the separate deployment
recheck and the unverified moved-root exercise. Google Picker recovery remains
unbuilt. The accepted personal-use Drive-backup policy and shared-account risks
remain in [Architecture §8](ARCHITECTURE.md#8-risks-and-unverified-points).

The short fuzz run does not establish coverage of every HEIF variant. Resource
measurements cover bounded synthetic workloads, not arbitrary concurrent
traffic or future database growth. See [the resource report](RESOURCES.md).
