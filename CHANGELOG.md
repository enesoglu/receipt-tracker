# Changelog

## 1.0.0

### Added

- Multi-user Google sign-in, allow-listed admission and revocable server-side sessions.
- Batch JPEG, PNG and HEIC/HEIF uploads, with original HEIC preservation,
  bounded conversion, queued Azure extraction and a synthetic development extractor.
- Deterministic product matching with exact quantities, normalized sizes,
  store-specific codes, learned aliases and user-confirmed merge/split workflows.
- Receipt and line editing, review flags, categories, stores and receipt defaults.
- Product price history and monthly spending reports, keeping currencies separate.
- Google Drive image storage and administrator connection management.
- Seven read-only MCP tools with OAuth discovery, PKCE, consent, rotating tokens,
  user isolation and reconnect support.
- Scheduled PostgreSQL backups, retention, restore commands and recovery guidance.
- Compiled-server end-to-end tests, OpenAPI response validation, complete isolation
  audit, query ownership guards and repeatable container resource measurements.

### Fixed

- Apply browser security headers to successful and failing responses.
- Re-evaluate administrator rights for existing sessions after configuration rotation.
- Refuse oversized decoded OCR images before they exhaust container memory.
- Decode database timestamps in UTC and classify missing services as unavailable.
- Reject null bytes before text reaches PostgreSQL.
- Remove mutable Go lookup globals and split React components into their own files.

### Operations

- Measured ten-file 12 MP JPEG/HEIC batches with no OOM under 512 MiB app/256 MiB db;
  retain the app cap and reduce the database default from 384 MiB to 256 MiB.
- Document vulnerability-scan results, the unused OpenPGP advisory justification,
  security regressions, screenshots and remaining provider/recovery limitations.
- Preserve the owner's accepted personal-use Drive backup policy risk and
  the requirement to retain the Google OAuth client and encryption-key backups.
