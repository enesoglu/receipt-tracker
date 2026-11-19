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
 AND (sqlc.narg(category_id)::uuid IS NULL OR EXISTS (
   SELECT 1 FROM line_items li
   JOIN products p ON p.user_id = li.user_id AND p.id = li.product_id
   JOIN categories c ON c.user_id = p.user_id AND c.id = p.category_id
   WHERE li.user_id = @user_id AND li.receipt_id = r.id
     AND li.line_total >= 0 AND c.id = sqlc.narg(category_id)::uuid))
 AND (
   sqlc.narg(breakdown_group)::text IS NULL OR CASE sqlc.narg(breakdown_group)::text
     WHEN 'unallocated' THEN r.total IS NOT NULL AND r.total <> COALESCE((
       SELECT sum(li.line_total) FROM line_items li
       WHERE li.user_id = r.user_id AND li.receipt_id = r.id AND li.line_total IS NOT NULL), 0)
     WHEN 'discounts' THEN EXISTS (
       SELECT 1 FROM line_items li
       WHERE li.user_id = r.user_id AND li.receipt_id = r.id AND li.line_total < 0)
     WHEN 'uncategorised' THEN EXISTS (
       SELECT 1 FROM line_items li
       WHERE li.user_id = r.user_id AND li.receipt_id = r.id AND li.line_total >= 0
         AND NOT EXISTS (
           SELECT 1 FROM products p
           JOIN categories c ON c.user_id = p.user_id AND c.id = p.category_id
           WHERE p.user_id = li.user_id AND p.id = li.product_id))
     WHEN 'no-store' THEN r.store_id IS NULL
     ELSE FALSE
   END)
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

-- FillReceiptIdentity fills the identity columns that are still unset and
-- touches nothing else on the receipt: a user-corrected value survives, and
-- a receipt that gains no value matches no rows, so the caller can count it
-- as skipped.
-- name: FillReceiptIdentity :execrows
UPDATE receipts SET
    invoice_number = COALESCE(invoice_number, sqlc.narg(invoice_number)::text),
    ettn = COALESCE(ettn, sqlc.narg(ettn)::uuid),
    seller_tax_number = COALESCE(seller_tax_number, sqlc.narg(seller_tax_number)::text)
WHERE user_id = @user_id AND id = @receipt_id
    AND ((invoice_number IS NULL AND sqlc.narg(invoice_number)::text IS NOT NULL)
        OR (ettn IS NULL AND sqlc.narg(ettn)::uuid IS NOT NULL)
        OR (seller_tax_number IS NULL AND sqlc.narg(seller_tax_number)::text IS NOT NULL));

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
UPDATE user_settings SET default_currency = @default_currency,default_receipt_title = @default_receipt_title,llm_suggestions_enabled = @llm_suggestions_enabled,updated_at = @now
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

-- The suggestions of one receipt in line order, without paging: review
-- shows them beside their lines.
-- name: ListReceiptSuggestions :many
SELECT s.* FROM line_suggestions s
JOIN line_items l ON l.user_id = s.user_id AND l.id = s.line_item_id
WHERE s.user_id = @user_id AND s.receipt_id = @receipt_id
ORDER BY l.position;

-- name: GetOwnedSuggestion :one
SELECT * FROM line_suggestions WHERE user_id = @user_id AND receipt_id = @receipt_id AND id = @suggestion_id;

-- The line a suggestion is for, to check its match state before deciding.
-- name: GetSuggestionLine :one
SELECT * FROM line_items WHERE user_id = @user_id AND id = @line_item_id;

-- The open match question of a needs_review line, which accepting a
-- suggestion resolves exactly like a different_product answer.
-- name: GetOpenLineFlag :one
SELECT * FROM review_flags
WHERE user_id = @user_id AND line_item_id = @line_item_id AND resolved_at IS NULL
  AND kind IN ('uncertain_match', 'code_conflict')
ORDER BY created_at, id LIMIT 1;

-- Decides a pending suggestion; the guard keeps a second decision from
-- overwriting the first, and no row means the decision came too late.
-- name: SetSuggestionDecision :one
UPDATE line_suggestions SET status = @status, decided_at = @now
WHERE user_id = @user_id AND id = @suggestion_id AND status = 'pending'
RETURNING *;
