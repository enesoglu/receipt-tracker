-- name: CreateAuthUser :one
INSERT INTO users (google_sub, email, name, is_admin)
VALUES ($1, $2, $3, $4)
ON CONFLICT (google_sub) DO NOTHING
RETURNING *;

-- name: UpdateAuthUser :one
UPDATE users SET email = $2, name = $3, is_admin = $4, updated_at = $5
WHERE google_sub = $1
RETURNING *;

-- name: GetAuthUser :one
SELECT * FROM users WHERE id = $1;

-- name: CreateAuthSettings :exec
INSERT INTO user_settings (user_id) VALUES ($1);

-- name: SeedAuthCategories :exec
INSERT INTO categories (user_id, name)
SELECT sqlc.arg(user_id)::uuid, unnest(sqlc.arg(names)::text[]);
