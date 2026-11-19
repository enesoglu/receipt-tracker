-- name: PageOwnedStores :many
SELECT s.*,
       (SELECT count(*)::integer FROM receipts r WHERE r.user_id = @user_id AND r.store_id = s.id) AS receipt_count,
       (SELECT count(*)::integer FROM store_aliases a WHERE a.user_id = @user_id AND a.store_id = s.id) AS alias_count
FROM stores s WHERE s.user_id = @user_id
 AND (NOT @has_after::boolean OR (lower(s.name),s.id) > (lower(@after_name::text),@after_id::uuid))
ORDER BY lower(s.name),s.id LIMIT @page_limit;

-- name: LockOwnedStore :one
SELECT * FROM stores WHERE user_id = @user_id AND id = @store_id FOR UPDATE;

-- name: RenameOwnedStore :one
UPDATE stores SET name = @name,normalized_name = @normalized_name,updated_at = @now
WHERE user_id = @user_id AND id = @store_id RETURNING *;

-- name: CountStoreReceipts :one
SELECT count(*)::integer FROM receipts WHERE user_id = @user_id AND store_id = @store_id;

-- name: CountStoreAliases :one
SELECT count(*)::integer FROM store_aliases WHERE user_id = @user_id AND store_id = @store_id;

-- name: GetOwnedStore :one
SELECT * FROM stores WHERE user_id = @user_id AND id = @store_id;

-- ListStoreAliases returns every raw name the store was seen under,
-- comparison name first; a store has few, so nothing is paginated.
-- name: ListStoreAliases :many
SELECT * FROM store_aliases WHERE user_id = @user_id AND store_id = @store_id
ORDER BY normalized_name, id;

-- ListStoreBranches counts the branch names printed on the store's
-- receipts.
-- name: ListStoreBranches :many
SELECT branch_name AS name, count(*)::integer AS receipt_count
FROM receipts
WHERE user_id = @user_id AND store_id = @store_id AND branch_name IS NOT NULL
GROUP BY branch_name
ORDER BY branch_name;

-- name: GetOwnedStoreByVKN :one
SELECT * FROM stores WHERE user_id = @user_id AND vkn = sqlc.arg(vkn)::text;

-- name: GetOwnedStoreByKey :one
SELECT * FROM stores WHERE user_id = @user_id AND normalized_name = @normalized_name;

-- ListNearestOwnedStoreNames returns one row per store name and per alias
-- name of the user's, nearest to name by trigram distance — the <->
-- ordering walks the GiST index on (user_id, normalized_name) — each
-- carrying its own similarity to it. A store's score is the maximum among
-- its rows, which the caller aggregates.
-- name: ListNearestOwnedStoreNames :many
SELECT n.store_id, n.normalized_name, similarity(n.normalized_name, sqlc.arg(name)::text)::float8 AS score
FROM (
    SELECT s.id AS store_id, s.normalized_name FROM stores s WHERE s.user_id = @user_id
    UNION ALL
    SELECT a.store_id, a.normalized_name FROM store_aliases a WHERE a.user_id = @user_id
) n
ORDER BY n.normalized_name <-> sqlc.arg(name)::text, n.store_id, n.normalized_name
LIMIT @max_rows;

-- name: CreateOwnedStore :one
INSERT INTO stores (user_id, name, normalized_name, vkn, created_at, updated_at)
VALUES (@user_id, @name, @normalized_name, sqlc.narg(vkn), @now, @now)
RETURNING *;

-- name: AdoptOwnedStoreVKN :one
UPDATE stores SET vkn = sqlc.arg(vkn)::text, updated_at = @now
WHERE user_id = @user_id AND id = @store_id AND vkn IS NULL
RETURNING *;

-- RememberStoreAlias records raw_name as a name the store printed. A
-- second sighting of the same reading bumps seen_count, and last_seen_at
-- follows the latest receipt; raw_name stays the first form seen.
-- name: RememberStoreAlias :exec
INSERT INTO store_aliases (user_id, store_id, raw_name, normalized_name, last_seen_at, created_at)
VALUES (@user_id, @store_id, @raw_name, @normalized_name, @now, @now)
ON CONFLICT (store_id, normalized_name) DO UPDATE
SET seen_count = store_aliases.seen_count + 1, last_seen_at = @now;

-- WriteStoreAudit records what a person did to a store. The detail must
-- explain the change on its own: the rows it describes may be gone later.
-- name: WriteStoreAudit :exec
INSERT INTO audit_events (user_id, entity, entity_id, action, detail, created_at)
VALUES (@user_id, 'store', @entity_id, @action, @detail, @now);

-- A merge folds the source store into the target in one transaction: raw
-- names combine, the rows bound to the source re-bind, receipts re-point,
-- and the source is deleted before the target adopts its tax number, so
-- the per-user uniqueness of vkn never sees two rows.
--
-- Tables: stores, store_aliases, receipts, product_aliases,
-- product_codes, match_rejections.

-- name: TouchStore :exec
UPDATE stores SET updated_at = @now WHERE user_id = @user_id AND id = @store_id;

-- name: MoveStoreReceipts :exec
UPDATE receipts SET store_id = @target_id, updated_at = @now
WHERE user_id = @user_id AND store_id = @source_id;

-- CombineMergedStoreAliases adds the counts of the source's raw names to
-- the target's raw name with the same comparison name.
-- DeleteCombinedStoreAliases then deletes those source names, and
-- MoveStoreAliases moves the rest.
-- name: CombineMergedStoreAliases :exec
UPDATE store_aliases t
SET seen_count = t.seen_count + s.seen_count, last_seen_at = GREATEST(t.last_seen_at, s.last_seen_at)
FROM store_aliases s
WHERE t.user_id = @user_id AND s.user_id = @user_id
  AND t.store_id = @target_id AND s.store_id = @source_id
  AND t.normalized_name = s.normalized_name;

-- name: DeleteCombinedStoreAliases :exec
DELETE FROM store_aliases s
WHERE s.user_id = @user_id AND s.store_id = @source_id
  AND EXISTS (
      SELECT 1 FROM store_aliases t
      WHERE t.user_id = @user_id AND t.store_id = @target_id
        AND t.normalized_name = s.normalized_name
  );

-- name: MoveStoreAliases :exec
UPDATE store_aliases SET store_id = @target_id
WHERE user_id = @user_id AND store_id = @source_id;

-- The next three queries re-bind the product names the source store
-- showed to the target store, combining the ones the target already has.
-- name: CombineMergedProductAliases :exec
UPDATE product_aliases t
SET seen_count = t.seen_count + s.seen_count, last_seen_at = GREATEST(t.last_seen_at, s.last_seen_at)
FROM product_aliases s
WHERE t.user_id = @user_id AND s.user_id = @user_id
  AND t.store_id = @target_id AND s.store_id = @source_id
  AND t.product_id = s.product_id AND t.normalized_name = s.normalized_name;

-- name: DeleteCombinedProductAliases :exec
DELETE FROM product_aliases s
WHERE s.user_id = @user_id AND s.store_id = @source_id
  AND EXISTS (
      SELECT 1 FROM product_aliases t
      WHERE t.user_id = @user_id AND t.store_id = @target_id
        AND t.product_id = s.product_id AND t.normalized_name = s.normalized_name
  );

-- name: MoveStoreProductAliases :exec
UPDATE product_aliases SET store_id = @target_id
WHERE user_id = @user_id AND store_id = @source_id;

-- Store-kind codes are the only codes bound to a store; a merge never
-- touches gtins.
-- name: CombineMergedProductCodes :exec
UPDATE product_codes t
SET seen_count = t.seen_count + s.seen_count
FROM product_codes s
WHERE t.user_id = @user_id AND s.user_id = @user_id
  AND t.store_id = @target_id AND s.store_id = @source_id
  AND t.product_id = s.product_id AND t.code = s.code AND t.kind = s.kind;

-- name: DeleteCombinedProductCodes :exec
DELETE FROM product_codes s
WHERE s.user_id = @user_id AND s.store_id = @source_id
  AND EXISTS (
      SELECT 1 FROM product_codes t
      WHERE t.user_id = @user_id AND t.store_id = @target_id
        AND t.product_id = s.product_id AND t.code = s.code AND t.kind = s.kind
  );

-- name: MoveStoreProductCodes :exec
UPDATE product_codes SET store_id = @target_id
WHERE user_id = @user_id AND store_id = @source_id;

-- A rejection the target already has cannot move; dropping the source's
-- copy keeps the same meaning.
-- name: DeleteDuplicateStoreRejections :exec
DELETE FROM match_rejections s
WHERE s.user_id = @user_id AND s.store_id = @source_id
  AND EXISTS (
      SELECT 1 FROM match_rejections t
      WHERE t.user_id = @user_id AND t.store_id = @target_id
        AND t.product_id = s.product_id AND t.normalized_name = s.normalized_name
  );

-- name: MoveStoreRejections :exec
UPDATE match_rejections SET store_id = @target_id
WHERE user_id = @user_id AND store_id = @source_id;

-- name: DeleteStore :exec
DELETE FROM stores WHERE user_id = @user_id AND id = @store_id;

-- A deleted store's rows in the three NO ACTION tables go before the
-- store itself; without the store they are inert. Its raw names follow
-- the store through the cascade.
-- name: DeleteStoreProductAliases :exec
DELETE FROM product_aliases WHERE user_id = @user_id AND store_id = @store_id;

-- name: DeleteStoreProductCodes :exec
DELETE FROM product_codes WHERE user_id = @user_id AND store_id = @store_id;

-- name: DeleteStoreRejections :exec
DELETE FROM match_rejections WHERE user_id = @user_id AND store_id = @store_id;
