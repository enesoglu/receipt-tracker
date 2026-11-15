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
