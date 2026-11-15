-- name: LockOAuthRegistration :exec
SELECT pg_advisory_xact_lock(130000);

-- name: ExpireUnusedOAuthClients :exec
DELETE FROM oauth_clients WHERE last_used_at IS NULL AND created_at <= $1;

-- name: CountOAuthClients :one
SELECT count(*) FROM oauth_clients;

-- name: CreateOAuthClient :one
INSERT INTO oauth_clients (name, redirect_uris, created_at) VALUES ($1, $2, $3) RETURNING *;

-- name: GetOAuthClient :one
SELECT * FROM oauth_clients WHERE id = $1;

-- name: TouchOAuthClient :exec
UPDATE oauth_clients SET last_used_at = $2 WHERE id = $1;

-- name: LockOAuthUser :one
SELECT id FROM users WHERE id = sqlc.arg(user_id) FOR UPDATE;

-- name: CreateOAuthCode :exec
INSERT INTO oauth_codes (user_id, code_hash, client_id, redirect_uri, code_challenge, resource, scope, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetOAuthCode :one
SELECT * FROM oauth_codes WHERE user_id = $1 AND code_hash = $2 FOR UPDATE;

-- name: ConsumeOAuthCode :exec
UPDATE oauth_codes SET used_at = $3 WHERE user_id = $1 AND code_hash = $2;

-- name: CreateOAuthToken :exec
INSERT INTO oauth_tokens (user_id, grant_id, client_id, kind, token_hash, resource, scope, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetOAuthToken :one
SELECT * FROM oauth_tokens WHERE user_id = $1 AND token_hash = $2;

-- name: ConsumeOAuthRefresh :exec
UPDATE oauth_tokens SET used_at = $3 WHERE user_id = $1 AND token_hash = $2;

-- name: RevokeOAuthGrant :exec
UPDATE oauth_tokens SET revoked_at = $3 WHERE user_id = $1 AND grant_id = $2 AND revoked_at IS NULL;

-- name: RevokeOAuthUserTokens :exec
UPDATE oauth_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL;

-- name: RevokeOAuthUserCodes :exec
UPDATE oauth_codes SET used_at = $2 WHERE user_id = $1 AND used_at IS NULL;

-- name: RevokeOAuthClientTokens :execrows
UPDATE oauth_tokens SET revoked_at = $3 WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL;

-- name: RevokeOAuthClientCodes :exec
UPDATE oauth_codes SET used_at = $3 WHERE user_id = $1 AND client_id = $2 AND used_at IS NULL;

-- name: ListOAuthConnections :many
SELECT c.id, c.name, c.redirect_uris, max(t.created_at)::timestamptz AS last_issued_at
FROM oauth_clients c JOIN oauth_tokens t ON t.client_id = c.id
WHERE t.user_id = $1 AND t.revoked_at IS NULL AND t.expires_at > $2
GROUP BY c.id ORDER BY c.name, c.id;
