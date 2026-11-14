-- name: FindReceiptByImageHash :one
SELECT receipt_id FROM receipt_images
WHERE user_id = @user_id AND sha256 = @sha256;

-- name: GetUploadDefaults :one
SELECT default_receipt_title, default_currency FROM user_settings
WHERE user_id = @user_id;

-- name: CreateQueuedReceipt :exec
INSERT INTO receipts (id, user_id, title, currency, status, source, created_at, updated_at)
VALUES (@id, @user_id, @title, @currency, 'queued', @source, @now, @now);

-- name: CreateReceiptImage :exec
INSERT INTO receipt_images (
    id, user_id, receipt_id, position, sha256, content_type, size_bytes,
    storage_backend, storage_ref, original_ref, original_content_type,
    original_size_bytes, created_at
) VALUES (
    @id, @user_id, @receipt_id, 0, @sha256, @content_type, @size_bytes,
    @storage_backend, @storage_ref, sqlc.narg(original_ref), sqlc.narg(original_content_type),
    sqlc.narg(original_size_bytes), @now
);

-- name: GetProviderUsage :one
SELECT pages FROM provider_usage
WHERE provider = @provider AND period = @period;

-- name: AddProviderUsage :exec
INSERT INTO provider_usage (provider, period, pages, updated_at)
VALUES (@provider, @period, @pages, @now)
ON CONFLICT (provider, period) DO UPDATE
SET pages = provider_usage.pages + EXCLUDED.pages, updated_at = EXCLUDED.updated_at;

-- name: GetExtractionTarget :one
SELECT r.status, i.storage_ref, i.content_type
FROM receipts r
JOIN receipt_images i ON i.user_id = r.user_id AND i.receipt_id = r.id AND i.position = 0
WHERE r.user_id = @user_id AND r.id = @receipt_id;

-- name: SetExtractionStatus :exec
UPDATE receipts SET status = @status, failure_reason = NULL, updated_at = @now
WHERE user_id = @user_id AND id = @receipt_id;

-- name: FailExtraction :exec
UPDATE receipts SET status = 'failed', failure_reason = @failure_reason, updated_at = @now
WHERE user_id = @user_id AND id = @receipt_id;

-- name: GetLatestExtraction :one
SELECT provider, raw FROM extractions
WHERE user_id = @user_id AND receipt_id = @receipt_id
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: CreateExtraction :exec
INSERT INTO extractions (user_id, receipt_id, provider, model, raw, created_at)
VALUES (@user_id, @receipt_id, @provider, @model, @raw, @now);

-- name: UpsertExtractedStore :one
INSERT INTO stores (user_id, name, normalized_name, created_at, updated_at)
VALUES (@user_id, @name, @normalized_name, @now, @now)
ON CONFLICT (user_id, normalized_name) DO UPDATE SET updated_at = EXCLUDED.updated_at
RETURNING id;

-- name: ApplyExtractedReceipt :exec
UPDATE receipts SET
    store_id = sqlc.narg(store_id),
    purchased_on = sqlc.narg(purchased_on),
    purchased_time = sqlc.narg(purchased_time),
    currency = COALESCE(sqlc.narg(currency), currency),
    subtotal = sqlc.narg(subtotal),
    tax_total = sqlc.narg(tax_total),
    total = sqlc.narg(total),
    status = 'needs_review',
    failure_reason = NULL,
    updated_at = @now
WHERE user_id = @user_id AND id = @receipt_id;

-- name: DeleteExtractedLineItems :exec
DELETE FROM line_items WHERE user_id = @user_id AND receipt_id = @receipt_id;

-- name: CreateExtractedLineItem :exec
INSERT INTO line_items (
    user_id, receipt_id, position, raw_name, name, quantity, quantity_unit,
    unit_price, line_total, product_code, confidence, created_at, updated_at
) VALUES (
    @user_id, @receipt_id, @position, @raw_name, @name, sqlc.narg(quantity), sqlc.narg(quantity_unit),
    sqlc.narg(unit_price), sqlc.narg(line_total), sqlc.narg(product_code), @confidence, @now, @now
);

-- Receipt submission and extraction: duplicate check by image hash, the
-- queued receipt with its image, raw extraction results and the monthly
-- provider page counter.
--
-- Tables: receipts, receipt_images, extractions, provider_usage.
--
-- Rules: every query on a user-owned table filters by user_id; query names
-- are unique across all files; run `task generate` after editing.
--
-- Add queries above this description: sqlc copies the comment that precedes
-- a file's first query into that query's Go documentation.
