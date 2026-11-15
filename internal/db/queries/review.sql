-- name: InsertReviewFlag :exec
INSERT INTO review_flags (user_id, receipt_id, line_item_id, kind, detail, created_at)
VALUES (@user_id, @receipt_id, sqlc.narg(line_item_id), @kind, @detail, @now);

-- name: DeleteReceiptFlags :exec
DELETE FROM review_flags WHERE user_id = @user_id AND receipt_id = @receipt_id;

-- name: GetReceiptFlags :many
SELECT * FROM review_flags WHERE user_id = @user_id AND receipt_id = @receipt_id ORDER BY created_at, id;
