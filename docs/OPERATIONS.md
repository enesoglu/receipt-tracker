# Storage operations

## Connect Google Drive

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
rather than choosing a potentially incomplete recovery tree. Keep the same
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
