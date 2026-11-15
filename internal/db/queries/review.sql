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
