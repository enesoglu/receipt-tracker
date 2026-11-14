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
