-- name: ListMonthlySpending :many
-- Receipt count and total per calendar month and currency. Only receipts
-- whose extraction finished (needs_review, ready) with a date and a total
-- count; months without receipts are absent and added by the caller.
SELECT
    date_trunc('month', purchased_on)::date AS month,
    currency,
    sum(total)::numeric                     AS amount,
    count(*)                                AS receipt_count
FROM receipts
WHERE user_id = @user_id
  AND purchased_on >= @from_date::date
  AND purchased_on < @to_date::date
  AND total IS NOT NULL
  AND status IN ('needs_review', 'ready')
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: CountExcludedReceipts :one
-- Finished receipts that no report can place: no purchase date or no total.
SELECT count(*)
FROM receipts
WHERE user_id = @user_id
  AND status IN ('needs_review', 'ready')
  AND (purchased_on IS NULL OR total IS NULL);

-- name: ListCategoryBreakdown :many
-- One month split by the category of each line's product, per currency.
-- Negative lines form the discounts group; the difference between receipt
-- totals and the sum of line totals is the unallocated group, so the groups
-- add up to the total. A currency without any group still yields one row
-- (empty kind) so that it is not lost. Share is computed from the NUMERIC
-- values, with 0 for a zero total. A nil group_id means "none".
WITH receipts_in_month AS (
    SELECT rc.id, rc.currency, rc.total
    FROM receipts rc
    WHERE rc.user_id = @user_id
      AND rc.purchased_on >= @from_date::date
      AND rc.purchased_on < @to_date::date
      AND rc.total IS NOT NULL
      AND rc.status IN ('needs_review', 'ready')
), lines AS (
    SELECT r.currency, li.line_total,
           CASE WHEN li.line_total < 0 THEN 'discounts'
                WHEN c.id IS NULL THEN 'uncategorised'
                ELSE 'category' END AS kind,
           CASE WHEN li.line_total < 0 THEN NULL ELSE c.id END AS group_id,
           CASE WHEN li.line_total < 0 THEN NULL ELSE c.name END AS group_name
    FROM receipts_in_month r
    JOIN line_items li ON li.user_id = @user_id AND li.receipt_id = r.id AND li.line_total IS NOT NULL
    LEFT JOIN products p ON p.user_id = li.user_id AND p.id = li.product_id
    LEFT JOIN categories c ON c.user_id = p.user_id AND c.id = p.category_id
), grouped AS (
    SELECT currency, kind, group_id, group_name,
           sum(line_total)::numeric AS amount, count(*) AS item_count
    FROM lines
    GROUP BY currency, kind, group_id, group_name
), totals AS (
    SELECT currency, sum(total)::numeric AS total
    FROM receipts_in_month
    GROUP BY currency
), unallocated AS (
    SELECT t.currency, 'unallocated' AS kind, NULL::uuid AS group_id, NULL::text AS group_name,
           t.total - COALESCE((SELECT sum(g.amount) FROM grouped g WHERE g.currency = t.currency), 0) AS amount,
           0::bigint AS item_count
    FROM totals t
), parts AS (
    SELECT * FROM grouped
    UNION ALL
    SELECT * FROM unallocated WHERE amount <> 0
)
SELECT t.currency::text AS currency, t.total::numeric AS total,
       COALESCE(p.kind, '')::text AS kind,
       COALESCE(p.group_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS group_id,
       COALESCE(p.group_name, '')::text AS group_name,
       COALESCE(p.amount, 0)::numeric AS amount, COALESCE(p.item_count, 0)::bigint AS item_count,
       COALESCE(round(p.amount / NULLIF(t.total, 0), 4), 0)::numeric AS share
FROM totals t
LEFT JOIN parts p ON p.currency = t.currency
ORDER BY t.currency, p.amount DESC NULLS LAST, p.kind, p.group_name, p.group_id;

-- name: ListStoreBreakdown :many
-- One month split by store, per currency: receipt totals by the receipt's
-- store, receipts without a store as one group. A currency always yields at
-- least one group. A nil group_id and an empty name mean "none".
WITH grouped AS (
    SELECT r.currency,
           CASE WHEN s.id IS NULL THEN 'uncategorised' ELSE 'store' END AS kind,
           s.id AS group_id, s.name AS group_name,
           sum(r.total)::numeric AS amount, count(*) AS item_count
    FROM receipts r
    LEFT JOIN stores s ON s.user_id = r.user_id AND s.id = r.store_id
    WHERE r.user_id = @user_id
      AND r.purchased_on >= @from_date::date
      AND r.purchased_on < @to_date::date
      AND r.total IS NOT NULL
      AND r.status IN ('needs_review', 'ready')
    GROUP BY r.currency, kind, s.id, s.name
), totals AS (
    SELECT currency, sum(amount)::numeric AS total FROM grouped GROUP BY currency
)
SELECT t.currency::text AS currency, t.total::numeric AS total,
       g.kind::text AS kind,
       COALESCE(g.group_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS group_id,
       COALESCE(g.group_name, '')::text AS group_name,
       g.amount::numeric AS amount, g.item_count::bigint AS item_count,
       COALESCE(round(g.amount / NULLIF(t.total, 0), 4), 0)::numeric AS share
FROM totals t
JOIN grouped g ON g.currency = t.currency
ORDER BY t.currency, g.amount DESC, g.kind, g.group_name, g.group_id;

-- Spending aggregation: totals per month and the breakdown of one month by
-- category or store. All arithmetic happens here, in NUMERIC.
--
-- Tables: receipts, line_items, products, categories, stores.
--
-- Rules: every query on a user-owned table filters by user_id; query names
-- are unique across all files; run `task generate` after editing.
--
-- Add queries above this description: sqlc copies the comment that precedes
-- a file's first query into that query's Go documentation.
