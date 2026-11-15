-- name: PageOwnedCategories :many
SELECT c.*, (SELECT count(*)::integer FROM products p WHERE p.user_id = @user_id AND p.category_id = c.id) AS product_count
FROM categories c WHERE c.user_id = @user_id
 AND (NOT @has_after::boolean OR (lower(c.name),c.id) > (lower(@after_name::text),@after_id::uuid))
ORDER BY lower(c.name),c.id LIMIT @page_limit;

-- name: CreateOwnedCategory :one
INSERT INTO categories (user_id,name,created_at,updated_at) VALUES (@user_id,@name,@now,@now) RETURNING *;

-- name: LockOwnedCategory :one
SELECT * FROM categories WHERE user_id = @user_id AND id = @category_id FOR UPDATE;

-- name: RenameOwnedCategory :one
UPDATE categories SET name = @name,updated_at = @now
WHERE user_id = @user_id AND id = @category_id RETURNING *;

-- name: DeleteOwnedCategory :exec
DELETE FROM categories WHERE user_id = @user_id AND id = @category_id;

-- name: CountCategoryProducts :one
SELECT count(*)::integer FROM products WHERE user_id = @user_id AND category_id = @category_id;
