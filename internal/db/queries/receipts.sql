-- name: LockReviewReceipt :one
SELECT * FROM receipts WHERE user_id = @user_id AND id = @receipt_id FOR UPDATE;

-- name: GetReviewReceipt :one
SELECT * FROM receipts WHERE user_id = @user_id AND id = @receipt_id;

-- name: ListReceiptLines :many
SELECT * FROM line_items WHERE user_id = @user_id AND receipt_id = @receipt_id ORDER BY position;

-- name: InsertReviewLine :one
INSERT INTO line_items (id, user_id, receipt_id, position, raw_name, name, quantity, quantity_unit,
 unit_price, line_total, product_code, confidence, created_at, updated_at)
VALUES (COALESCE(sqlc.narg(existing_id)::uuid, uuidv7()), @user_id, @receipt_id, @position, @raw_name, @name, sqlc.narg(quantity), sqlc.narg(quantity_unit),
 sqlc.narg(unit_price), sqlc.narg(line_total), sqlc.narg(product_code), sqlc.narg(confidence), @now, @now)
RETURNING *;

-- name: SetLineMatch :exec
UPDATE line_items SET product_id = sqlc.narg(product_id), match_status = @match_status,
 match_score = sqlc.narg(match_score), match_method = sqlc.narg(match_method), updated_at = @now
WHERE user_id = @user_id AND id = @line_id;

-- name: CreateReviewProduct :one
INSERT INTO products (user_id, canonical_name, size_value, size_unit, created_at, updated_at)
VALUES (@user_id, @canonical_name, sqlc.narg(size_value), sqlc.narg(size_unit), @now, @now) RETURNING *;

-- name: GetReviewProduct :one
SELECT * FROM products WHERE user_id = @user_id AND id = @product_id;

-- name: RememberReviewAlias :exec
INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name, store_id, last_seen_at, created_at)
VALUES (@user_id, @product_id, @raw_name, @normalized_name, sqlc.narg(store_id), @now, @now)
ON CONFLICT (product_id, normalized_name, store_id) DO NOTHING;

-- name: RememberReviewCode :exec
INSERT INTO product_codes (user_id, product_id, code, kind, store_id, created_at)
VALUES (@user_id, @product_id, @code, @kind, sqlc.narg(store_id), @now)
ON CONFLICT (product_id, code, kind, store_id) DO NOTHING;

-- name: FindDuplicateReceipts :many
SELECT id FROM receipts WHERE user_id = @user_id AND id <> @receipt_id
 AND store_id = @store_id AND purchased_on = @purchased_on AND total = @total ORDER BY id LIMIT 1;

-- name: RefreshReviewStatus :exec
UPDATE receipts SET status = CASE WHEN EXISTS (
 SELECT 1 FROM review_flags f WHERE f.user_id = @user_id AND f.receipt_id = @receipt_id AND f.resolved_at IS NULL
) THEN 'needs_review' ELSE 'ready' END, failure_reason = NULL, updated_at = @now
WHERE user_id = @user_id AND id = @receipt_id;
