-- name: ListMatchAliasesBySimilarity :many
-- ListMatchAliasesBySimilarity returns the user's aliases nearest to a
-- comparison key by trigram distance, which is 1 minus pg_trgm similarity,
-- so the nearest are the most similar. The <-> operator walks the GiST
-- index on (user_id, normalized_name). Each row carries the size recorded
-- on the product and whether the user rejected the key for that product.
SELECT a.product_id, a.normalized_name, a.store_id, p.size_value, p.size_unit,
       EXISTS (
           SELECT 1 FROM match_rejections r
           WHERE r.user_id = sqlc.arg(user_id)
             AND r.product_id = a.product_id
             AND r.normalized_name = sqlc.arg(name)::text
       ) AS rejected
FROM product_aliases a
JOIN products p ON p.user_id = a.user_id AND p.id = a.product_id
WHERE a.user_id = sqlc.arg(user_id)
  AND p.user_id = sqlc.arg(user_id)
ORDER BY a.normalized_name <-> sqlc.arg(name)::text, a.id
LIMIT sqlc.arg(max_aliases);

-- name: ListMatchCodeOwners :many
-- ListMatchCodeOwners returns the user's products that have a code, one row
-- per matching code row and alias, so that a product found only by its code
-- can still be scored. alias_name is null for a product without aliases.
-- Each row says whether the user rejected the key for that product.
SELECT c.product_id, c.code, c.kind, c.store_id AS code_store_id,
       p.canonical_name, p.size_value, p.size_unit,
       a.normalized_name AS alias_name, a.store_id AS alias_store_id,
       EXISTS (
           SELECT 1 FROM match_rejections r
           WHERE r.user_id = sqlc.arg(user_id)
             AND r.product_id = c.product_id
             AND r.normalized_name = sqlc.arg(name)::text
       ) AS rejected
FROM product_codes c
JOIN products p ON p.user_id = c.user_id AND p.id = c.product_id
LEFT JOIN product_aliases a ON a.user_id = sqlc.arg(user_id) AND a.product_id = c.product_id
WHERE c.user_id = sqlc.arg(user_id)
  AND p.user_id = sqlc.arg(user_id)
  AND c.code = sqlc.arg(code);

-- Candidate lookup for product matching: aliases by trigram similarity,
-- products that own a code, and rejected name and product pairs.
--
-- Tables: product_aliases, product_codes, match_rejections, products.
--
-- Rules: every query on a user-owned table filters by user_id; query names
-- are unique across all files; run `task generate` after editing.
--
-- Add queries above this description: sqlc copies the comment that precedes
-- a file's first query into that query's Go documentation.
