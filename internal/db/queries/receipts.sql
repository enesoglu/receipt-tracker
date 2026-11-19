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
ON CONFLICT (product_id, normalized_name, store_id) DO UPDATE
SET seen_count = product_aliases.seen_count + 1, last_seen_at = @now;

-- name: RememberReviewCode :exec
INSERT INTO product_codes (user_id, product_id, code, kind, store_id, created_at)
VALUES (@user_id, @product_id, @code, @kind, sqlc.narg(store_id), @now)
ON CONFLICT (product_id, code, kind, store_id) DO UPDATE
SET seen_count = product_codes.seen_count + 1;

-- name: FindDuplicateReceipts :many
SELECT id FROM receipts WHERE user_id = @user_id AND id <> @receipt_id
 AND store_id = @store_id AND purchased_on = @purchased_on AND total = @total ORDER BY id LIMIT 1;

-- name: RefreshReviewStatus :exec
UPDATE receipts SET status = CASE WHEN EXISTS (
 SELECT 1 FROM review_flags f WHERE f.user_id = @user_id AND f.receipt_id = @receipt_id AND f.resolved_at IS NULL
) THEN 'needs_review' ELSE 'ready' END, failure_reason = NULL, updated_at = @now
WHERE user_id = @user_id AND id = @receipt_id;

-- name: PageReceiptIDs :many
SELECT r.id FROM receipts r
LEFT JOIN stores s ON s.user_id = @user_id AND s.id = r.store_id
WHERE r.user_id = @user_id
 AND (cardinality(@ids::uuid[]) = 0 OR r.id = ANY(@ids::uuid[]))
 AND (@status::text = '' OR r.status = @status)
 AND (sqlc.narg(store_id)::uuid IS NULL OR r.store_id = sqlc.narg(store_id))
 AND (sqlc.narg(month_start)::date IS NULL OR (r.purchased_on >= sqlc.narg(month_start) AND r.purchased_on < sqlc.narg(month_end)))
 AND (@search::text = '' OR strpos(lower(r.title),lower(@search)) > 0 OR strpos(lower(s.name),lower(@search)) > 0
  OR EXISTS (SELECT 1 FROM line_items l WHERE l.user_id = @user_id AND l.receipt_id = r.id AND strpos(lower(l.name),lower(@search)) > 0))
 AND (NOT @has_after::boolean OR (COALESCE(r.purchased_on,'infinity'::date),r.created_at,r.id)
  < (COALESCE(sqlc.narg(after_date)::date,'infinity'::date),@after_created::timestamptz,@after_id::uuid))
ORDER BY r.purchased_on DESC NULLS FIRST,r.created_at DESC,r.id DESC LIMIT @page_limit;

-- name: GetReceiptStore :one
SELECT * FROM stores WHERE user_id = @user_id AND id = @store_id;

-- name: ListReceiptImages :many
SELECT * FROM receipt_images WHERE user_id = @user_id AND receipt_id = @receipt_id ORDER BY position;

-- name: GetOwnedReceiptImage :one
SELECT * FROM receipt_images WHERE user_id = @user_id AND receipt_id = @receipt_id AND id = @image_id;

-- name: UpdateReviewReceipt :exec
UPDATE receipts SET title = @title, store_id = sqlc.narg(store_id), purchased_on = sqlc.narg(purchased_on),
 purchased_time = sqlc.narg(purchased_time), currency = @currency, subtotal = sqlc.narg(subtotal),
 tax_total = sqlc.narg(tax_total), total = sqlc.narg(total),
 invoice_number = sqlc.narg(invoice_number), ettn = sqlc.narg(ettn),
 seller_tax_number = sqlc.narg(seller_tax_number), updated_at = @now
WHERE user_id = @user_id AND id = @receipt_id;

-- name: UpdateReviewLine :one
UPDATE line_items SET name = @name, quantity = sqlc.narg(quantity), quantity_unit = sqlc.narg(quantity_unit),
 unit_price = sqlc.narg(unit_price), line_total = @line_total, product_code = sqlc.narg(product_code),
 confidence = NULL, updated_at = @now
WHERE user_id = @user_id AND receipt_id = @receipt_id AND id = @line_id RETURNING *;

-- name: DeleteReviewLine :exec
DELETE FROM line_items WHERE user_id = @user_id AND receipt_id = @receipt_id AND id = @line_id;

-- name: DeleteReceiptJobs :exec
DELETE FROM jobs WHERE kind = 'extract_receipt' AND payload->>'user_id' = @user_id::text AND payload->>'receipt_id' = @receipt_id::text;

-- name: DeleteReceiptData :exec
DELETE FROM receipts WHERE user_id = @user_id AND id = @receipt_id;

-- name: WriteReceiptAudit :exec
INSERT INTO audit_events (user_id,entity,entity_id,action,detail,created_at)
VALUES (@user_id,@entity,@entity_id,@action,@detail,@now);

-- name: GetOwnedSettings :one
SELECT * FROM user_settings WHERE user_id = @user_id;

-- name: LockOwnedSettings :one
SELECT * FROM user_settings WHERE user_id = @user_id FOR UPDATE;

-- name: UpdateOwnedSettings :one
UPDATE user_settings SET default_currency = @default_currency,default_receipt_title = @default_receipt_title,updated_at = @now
WHERE user_id = @user_id RETURNING *;

-- name: ListPipelineStages :many
-- One row per receipt whose stage was asked about, or — without ids — per
-- receipt still working through the pipeline. Every stage is derived here:
-- the status column, whether a raw result was already saved (processing
-- with a saved raw is matching, without one it is extracting), and how many
-- of this user's queued receipts are ahead. (created_at, id) is a total
-- order: a batch can share one timestamp.
SELECT r.id,
	r.status,
	r.failure_reason,
	EXISTS (SELECT 1 FROM extractions e
		WHERE e.user_id = r.user_id AND e.receipt_id = r.id) AS has_extraction,
	1 + (SELECT count(*) FROM receipts ahead
		WHERE ahead.user_id = r.user_id
			AND ahead.status = 'queued'
			AND (ahead.created_at, ahead.id) < (r.created_at, r.id)) AS queue_position
FROM receipts r
WHERE r.user_id = @user_id
	AND (cardinality(@ids::uuid[]) = 0 OR r.id = ANY(@ids::uuid[]))
	AND (cardinality(@ids::uuid[]) > 0 OR r.status IN ('queued', 'processing', 'waiting_quota'))
ORDER BY r.created_at, r.id
LIMIT 100;
