-- name: InsertReviewFlag :exec
INSERT INTO review_flags (user_id, receipt_id, line_item_id, kind, detail, created_at)
VALUES (@user_id, @receipt_id, sqlc.narg(line_item_id), @kind, @detail, @now);

-- name: DeleteReceiptFlags :exec
DELETE FROM review_flags WHERE user_id = @user_id AND receipt_id = @receipt_id;

-- name: GetReceiptFlags :many
SELECT * FROM review_flags WHERE user_id = @user_id AND receipt_id = @receipt_id ORDER BY created_at, id;

-- name: CloseReviewFlag :exec
UPDATE review_flags SET resolved_at = @now, resolution = @resolution
WHERE user_id = @user_id AND id = @flag_id AND resolved_at IS NULL;

-- name: RestoreReviewFlag :exec
INSERT INTO review_flags (id,user_id,receipt_id,line_item_id,kind,detail,created_at,resolved_at,resolution)
VALUES (@id,@user_id,@receipt_id,sqlc.narg(line_item_id),@kind,@detail,@created_at,sqlc.narg(resolved_at),sqlc.narg(resolution));

-- name: GetOwnedReviewFlag :one
SELECT * FROM review_flags WHERE user_id = @user_id AND id = @flag_id;

-- name: PageReviewFlagIDs :many
SELECT f.id,r.created_at AS receipt_created_at,f.created_at AS flag_created_at
FROM review_flags f JOIN receipts r ON r.user_id = @user_id AND r.id = f.receipt_id
WHERE f.user_id = @user_id AND f.resolved_at IS NULL
 AND (NOT @has_after::boolean OR (r.created_at,f.created_at,f.id) > (@after_receipt::timestamptz,@after_flag::timestamptz,@after_id::uuid))
ORDER BY r.created_at,f.created_at,f.id LIMIT @page_limit;

-- name: CountOpenReviewFlags :one
SELECT count(*)::integer FROM review_flags WHERE user_id = @user_id AND resolved_at IS NULL;

-- name: RememberReviewRejection :exec
INSERT INTO match_rejections (user_id,product_id,normalized_name,store_id,created_at)
SELECT @user_id,p.id,@normalized_name,sqlc.narg(store_id)::uuid,@now
FROM products p WHERE p.user_id = @user_id AND p.id = @product_id
ON CONFLICT (product_id,normalized_name,store_id) DO NOTHING;
