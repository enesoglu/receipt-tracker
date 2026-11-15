-- name: PageProducts :many
-- PageProducts returns one page of the user's products in name order. A
-- product matches the search when one of its aliases contains every search
-- token, or when its canonical name, folded to plain lower-case letters and
-- digits like a comparison key, contains every token. The search tokens are
-- those of matching.Normalize, so "SÜT" finds "sut".
SELECT sqlc.embed(p), c.name AS category_name
FROM products p
LEFT JOIN categories c ON c.user_id = @user_id AND c.id = p.category_id
WHERE p.user_id = @user_id
  AND (sqlc.narg(category_id)::uuid IS NULL OR p.category_id = sqlc.narg(category_id))
  AND (cardinality(@tokens::text[]) = 0
       OR EXISTS (
           SELECT 1 FROM product_aliases a
           WHERE a.user_id = @user_id AND a.product_id = p.id
             AND NOT EXISTS (SELECT 1 FROM unnest(@tokens::text[]) t WHERE strpos(a.normalized_name, t) = 0)
       )
       OR NOT EXISTS (
           SELECT 1 FROM unnest(@tokens::text[]) t
           WHERE strpos(regexp_replace(translate(lower(translate(p.canonical_name,
                        'İIÇĞÖŞÜÂÎÛÉÈÊ', 'iıçğöşüâîûéèê')),
                        'çğıöşüâîûéèê', 'cgiosuaiueee'), '[^a-z0-9]+', ' ', 'g'), t) = 0
       ))
  AND (NOT @has_after::boolean OR (p.canonical_name, p.id) > (@after_name::text, @after_id::uuid))
ORDER BY p.canonical_name, p.id
LIMIT @page_limit;

-- name: GetProduct :one
SELECT sqlc.embed(p), c.name AS category_name
FROM products p
LEFT JOIN categories c ON c.user_id = @user_id AND c.id = p.category_id
WHERE p.user_id = @user_id AND p.id = @product_id;

-- name: ListProductAliases :many
-- ListProductAliases returns the names a product was seen under, most often
-- seen first, with the store each was seen at.
SELECT sqlc.embed(a), s.name AS store_name
FROM product_aliases a
LEFT JOIN stores s ON s.user_id = @user_id AND s.id = a.store_id
WHERE a.user_id = @user_id AND a.product_id = @product_id
ORDER BY a.seen_count DESC, a.last_seen_at DESC, a.id;

-- name: ListProductCodes :many
SELECT sqlc.embed(pc), s.name AS store_name
FROM product_codes pc
LEFT JOIN stores s ON s.user_id = @user_id AND s.id = pc.store_id
WHERE pc.user_id = @user_id AND pc.product_id = @product_id
ORDER BY pc.seen_count DESC, pc.code, pc.id;

-- name: ListProductPurchaseStats :many
-- ListProductPurchaseStats returns, for each of the given products that has
-- any, the number of purchases that count for the price history and the
-- most recent of them. A purchase counts when its receipt is ready or needs
-- review and has a date, the line is not a discount, and a unit price is
-- known or follows from the line total. The same rules select the points of
-- ListProductPricePoints.
WITH purchases AS (
    SELECT l.product_id, r.purchased_on, r.purchased_time, r.created_at, r.id AS receipt_id, l.position,
           r.currency, r.store_id,
           COALESCE(l.unit_price, round(l.line_total / NULLIF(l.quantity, 0), 2), l.line_total)::numeric AS unit_price
    FROM line_items l
    JOIN receipts r ON r.user_id = l.user_id AND r.id = l.receipt_id
    WHERE l.user_id = @user_id
      AND r.user_id = @user_id
      AND l.product_id = ANY(@product_ids::uuid[])
      AND r.status IN ('ready', 'needs_review')
      AND r.purchased_on IS NOT NULL
      AND (l.line_total IS NULL OR l.line_total >= 0)
), counted AS (
    SELECT *, count(*) OVER (PARTITION BY product_id) AS purchase_count
    FROM purchases
    WHERE unit_price IS NOT NULL AND unit_price >= 0
)
SELECT DISTINCT ON (c.product_id)
       c.product_id, c.purchase_count, c.purchased_on::date AS purchased_on, c.unit_price, c.currency,
       c.store_id, s.name AS store_name
FROM counted c
LEFT JOIN stores s ON s.user_id = @user_id AND s.id = c.store_id
ORDER BY c.product_id, c.purchased_on DESC, c.purchased_time DESC NULLS LAST, c.created_at DESC,
         c.receipt_id DESC, c.position DESC;

-- name: LockProduct :one
-- LockProduct reads a product and holds it until the transaction ends, so
-- that concurrent edits, merges and splits of it run one after another.
SELECT * FROM products WHERE user_id = @user_id AND id = @product_id FOR UPDATE;

-- name: ProductCategoryExists :one
SELECT EXISTS (SELECT 1 FROM categories WHERE user_id = @user_id AND id = @category_id);

-- name: UpdateProduct :exec
UPDATE products
SET canonical_name = @canonical_name, category_id = sqlc.narg(category_id),
    size_value = sqlc.narg(size_value), size_unit = sqlc.narg(size_unit), updated_at = @now
WHERE user_id = @user_id AND id = @product_id;

-- name: WriteProductAudit :exec
INSERT INTO audit_events (user_id, entity, entity_id, action, detail, created_at)
VALUES (@user_id, 'product', @product_id, @action, @detail, @now);

-- The product catalogue: list and search, detail with aliases and codes,
-- edits, merge, split and price history.
--
-- Tables: products, product_aliases, product_codes, match_rejections,
-- line_items.
--
-- Rules: every query on a user-owned table filters by user_id; query names
-- are unique across all files; run `task generate` after editing.
--
-- Add queries above this description: sqlc copies the comment that precedes
-- a file's first query into that query's Go documentation.
