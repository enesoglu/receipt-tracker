# Isolation audit

Every REST operation and MCP tool is listed below. Tests use real PostgreSQL
and authenticated sessions or bearer tokens. Resource requests by B for A's id
return 404; collection reads contain only B's data. Settings and identity
operations always address the current user. Usage and storage status are
deliberately instance-wide and expose no receipt data or credentials.

`TestE2E_RealServerWorkflowAndContract` additionally runs the compiled server
with local storage and the fake extractor. It signs in two synthetic identities
over HTTP, uploads all three extraction samples in a batch, completes review,
merges and splits products, checks populated reports, and repeats data reads
as the other user. Every observed REST response is validated against OpenAPI;
an inventory assertion fails when an operation has no validated real response.
Drive consent's successful external exchange is covered by the fake-Drive
transport test; the E2E test validates its unconfigured and foreign-user errors.

## REST

| Operation              | Isolation test                                                 | Behavior                                    |
| ---------------------- | -------------------------------------------------------------- | ------------------------------------------- |
| getMe                  | TestAuth_TestSupportKeepsSessionUsersIsolated                  | Session identity only                       |
| logout                 | TestLogout_PreservesAnotherUsersSession                        | Current session only; B's session unchanged |
| uploadReceipts         | TestUploadReceipts_UserBGetsOwnReceiptForUserAsPhoto           | Same bytes create B's own receipt           |
| listReceipts           | TestReceipts_IsolatesEveryOperation                            | Empty foreign-id filter                     |
| getReceipt             | TestReceipts_IsolatesEveryOperation                            | 404                                         |
| updateReceipt          | TestReceipts_IsolatesEveryOperation                            | 404                                         |
| deleteReceipt          | TestReceipts_IsolatesEveryOperation                            | 404                                         |
| reprocessReceipt       | TestReceipts_IsolatesEveryOperation                            | 404                                         |
| getReceiptImage        | TestReceipts_IsolatesEveryOperation                            | 404 for both variants; no storage access    |
| createLineItem         | TestReceipts_IsolatesEveryOperation                            | 404                                         |
| updateLineItem         | TestReceipts_IsolatesEveryOperation                            | 404                                         |
| deleteLineItem         | TestReceipts_IsolatesEveryOperation                            | 404                                         |
| listReviewFlags        | TestReview_ListsOnlyOpenOwnedFlagsOldestReceiptFirst           | Own open flags only                         |
| resolveReviewFlag      | TestReview_RejectsInvalidActionsCorrectionsAndForeignResources | 404; foreign product choice rolls back      |
| listProducts           | TestProducts_IsolatesReadOperations                            | Empty collection                            |
| getProduct             | TestProducts_IsolatesReadOperations                            | 404                                         |
| updateProduct          | TestProducts_IsolatesUpdate                                    | 404                                         |
| getProductPriceHistory | TestProducts_IsolatesPriceHistory                              | 404                                         |
| mergeProducts          | TestProducts_IsolatesMerge                                     | 404 for either foreign participant          |
| splitProduct           | TestProducts_IsolatesSplit                                     | 404 for foreign product or alias            |
| listStores             | TestStores_ListRenameConflictAndIsolation                      | Own stores only                             |
| updateStore            | TestStores_ListRenameConflictAndIsolation                      | 404                                         |
| listCategories         | TestCategories_CRUDPagingCountsAndIsolation                    | Own seeded/custom categories only           |
| createCategory         | TestCategories_CRUDPagingCountsAndIsolation                    | Creates only for current user               |
| updateCategory         | TestCategories_CRUDPagingCountsAndIsolation                    | 404                                         |
| deleteCategory         | TestCategories_CRUDPagingCountsAndIsolation                    | 404                                         |
| getMonthlySpending     | TestGetMonthlySpending_IsolatesUsers                           | Empty foreign spending                      |
| getMonthBreakdown      | TestGetMonthBreakdown_IsolatesUsers                            | Empty foreign breakdown                     |
| getSettings            | TestSettings_DefaultsAreIsolatedAndDoNotAlterExistingReceipts  | Own defaults only                           |
| updateSettings         | TestSettings_DefaultsAreIsolatedAndDoNotAlterExistingReceipts  | Other user's defaults unchanged             |
| getUsage               | TestGetUsage_ReportsTheInstanceAllowance                       | Intentionally shared provider counter       |
| getStorageStatus       | TestStorage_AdminConsentIsolationAndHealth                     | Safe shared state; management right differs |
| connectDrive           | TestStorage_AdminConsentIsolationAndHealth                     | Non-admin gets 404                          |
| completeDrive          | TestStorage_AdminConsentIsolationAndHealth                     | Non-admin gets 404; state bound to admin    |
| disconnectStorage      | TestStorage_AdminConsentIsolationAndHealth                     | Non-admin gets 404                          |

## Browser, health and OAuth protocol routes

The root-level OAuth contract is separate from the session REST contract.
Discovery and registration are public and return no user-owned rows. Consent
and token operations enforce their browser or credential bindings.

| Operation                       | Isolation test                                         | Behavior                                              |
| ------------------------------- | ------------------------------------------------------ | ----------------------------------------------------- |
| getProtectedResourceMetadata    | TestOAuth_MetadataMatchesClaudeRequirements            | Public metadata only                                  |
| getMcpProtectedResourceMetadata | TestOAuth_MetadataMatchesClaudeRequirements            | Public metadata only                                  |
| getAuthorizationServerMetadata  | TestOAuth_MetadataMatchesClaudeRequirements            | Public metadata only                                  |
| registerOAuthClient             | TestOAuth_RegistrationValidationExpiryAndRateLimits    | Public client metadata; no user data                  |
| authorizeOAuthClient            | TestOAuth_AuthorizationValidationAndConsentBinding     | Current session; registered callback only             |
| approveOAuthClient              | TestOAuth_AuthorizationValidationAndConsentBinding     | One-use consent bound to session and exact request    |
| exchangeOAuthToken              | TestOAuth_RejectsInvalidCodeBindingsWithoutBurningCode | Credential, client, callback, PKCE and resource bound |

| Route                     | Boundary tests                                                                                                     |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| GET /auth/google/login    | TestLogin_AllowedEmailCreatesSessionAndMe                                                                          |
| GET /auth/google/callback | TestLogin_RejectsInvalidCallbacksWithoutUserOrSession; TestLogin_ConsumesCallbacksBeforeExchangeAndExpiresAttempts |
| GET /auth/dev/login       | TestSession_ProductionCookiesAndDevRoute; TestLogin_DevAdmissionAndSameSubjectProvisionOnlyOnce                    |
| GET /livez, GET /healthz  | TestHealth_CachedStatesMatchGoldenAndLivenessIsIndependent (public status; no credentials)                         |
| GET/POST/DELETE /mcp      | TestMCP_EveryToolIsolatesUsers; TestMCP_BearerOnlyMetadataChallengeAndNoRESTAuthority                              |

## MCP

All seven tools are exercised with two distinct users by
`TestMCP_EveryToolIsolatesUsers`. Cross-user SDK sessions are also refused.

| Tool                 | Isolation test                 | Behavior                 |
| -------------------- | ------------------------------ | ------------------------ |
| list_receipts        | TestMCP_EveryToolIsolatesUsers | Empty foreign collection |
| get_receipt          | TestMCP_EveryToolIsolatesUsers | 404 tool error           |
| search_products      | TestMCP_EveryToolIsolatesUsers | Empty foreign collection |
| get_price_history    | TestMCP_EveryToolIsolatesUsers | 404 tool error           |
| get_spending_summary | TestMCP_EveryToolIsolatesUsers | No foreign amounts       |
| list_categories      | TestMCP_EveryToolIsolatesUsers | Own categories only      |
| list_stores          | TestMCP_EveryToolIsolatesUsers | Own stores only          |

## Queries

`TestQueries_UserOwnedTablesRequireBoundOwnership` discovers tables carrying
`user_id` from the migrated schema and inspects every named sqlc source query.
Reads and updates/deletes need a bound ownership predicate; inserts bind an
owner column. A self-test proves missing predicates, projected owner columns,
comments and unscoped insert-select statements fail. This is a regression
guard, not a SQL proof: joins and transaction semantics also require the
two-user behavioral tests above.

Instance-owned tables (`jobs`, `provider_usage`, `storage_connections`,
`oauth_clients`, `sessions`) have separate worker, admin or session boundaries.
`users` is the identity table: OIDC provisioning resolves verified subjects;
session/token admission retrieves only the verified user id. OAuth codes and
tokens do carry `user_id` and are subject to the ownership guard.
