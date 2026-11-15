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
