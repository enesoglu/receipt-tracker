-- name: PageOwnedStores :many
SELECT s.*, (SELECT count(*)::integer FROM receipts r WHERE r.user_id = @user_id AND r.store_id = s.id) AS receipt_count
FROM stores s WHERE s.user_id = @user_id
 AND (NOT @has_after::boolean OR (lower(s.name),s.id) > (lower(@after_name::text),@after_id::uuid))
ORDER BY lower(s.name),s.id LIMIT @page_limit;

-- name: LockOwnedStore :one
SELECT * FROM stores WHERE user_id = @user_id AND id = @store_id FOR UPDATE;

-- name: RenameOwnedStore :one
UPDATE stores SET name = @name,normalized_name = @normalized_name,updated_at = @now
WHERE user_id = @user_id AND id = @store_id RETURNING *;

-- name: CountStoreReceipts :one
SELECT count(*)::integer FROM receipts WHERE user_id = @user_id AND store_id = @store_id;

-- name: GetOwnedStoreByVKN :one
SELECT * FROM stores WHERE user_id = @user_id AND vkn = sqlc.arg(vkn)::text;

-- name: GetOwnedStoreByKey :one
SELECT * FROM stores WHERE user_id = @user_id AND normalized_name = @normalized_name;

-- ListNearestOwnedStoreNames returns one row per store name and per alias
-- name of the user's, nearest to name by trigram distance — the <->
-- ordering walks the GiST index on (user_id, normalized_name) — each
-- carrying its own similarity to it. A store's score is the maximum among
-- its rows, which the caller aggregates.
-- name: ListNearestOwnedStoreNames :many
SELECT n.store_id, n.normalized_name, similarity(n.normalized_name, sqlc.arg(name)::text)::float8 AS score
FROM (
    SELECT s.id AS store_id, s.normalized_name FROM stores s WHERE s.user_id = @user_id
    UNION ALL
    SELECT a.store_id, a.normalized_name FROM store_aliases a WHERE a.user_id = @user_id
) n
ORDER BY n.normalized_name <-> sqlc.arg(name)::text, n.store_id, n.normalized_name
LIMIT @max_rows;

-- name: CreateOwnedStore :one
INSERT INTO stores (user_id, name, normalized_name, vkn, created_at, updated_at)
VALUES (@user_id, @name, @normalized_name, sqlc.narg(vkn), @now, @now)
RETURNING *;

-- name: AdoptOwnedStoreVKN :one
UPDATE stores SET vkn = sqlc.arg(vkn)::text, updated_at = @now
WHERE user_id = @user_id AND id = @store_id AND vkn IS NULL
RETURNING *;

-- RememberStoreAlias records raw_name as a name the store printed. A
-- second sighting of the same reading bumps seen_count, and last_seen_at
-- follows the latest receipt; raw_name stays the first form seen.
-- name: RememberStoreAlias :exec
INSERT INTO store_aliases (user_id, store_id, raw_name, normalized_name, last_seen_at, created_at)
VALUES (@user_id, @store_id, @raw_name, @normalized_name, @now, @now)
ON CONFLICT (store_id, normalized_name) DO UPDATE
SET seen_count = store_aliases.seen_count + 1, last_seen_at = @now;
