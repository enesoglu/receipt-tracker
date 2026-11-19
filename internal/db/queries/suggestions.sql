-- name: ListSuggestibleLines :many
-- The lines the suggestion job may ask the model about: the three match
-- states matching could not confidently resolve, without discounts, lines
-- with no printed text and the unreadable placeholder, and without the
-- lines an earlier run already answered.
SELECT l.id, l.position, l.raw_name
FROM line_items l
WHERE l.user_id = @user_id
  AND l.receipt_id = @receipt_id
  AND l.match_status IN ('needs_review', 'new_product', 'unmatched')
  AND (l.line_total IS NULL OR l.line_total >= 0)
  AND btrim(l.raw_name) <> ''
  AND l.name <> 'Unreadable item'
  AND NOT EXISTS (
      SELECT 1 FROM line_suggestions s
      WHERE s.user_id = l.user_id AND s.line_item_id = l.id
  )
ORDER BY l.position;

-- The store name of one receipt when matching found one; no row when it
-- did not or the receipt is not the caller's.
-- name: GetSuggestionStoreName :one
SELECT s.name
FROM receipts r
JOIN stores s ON s.user_id = r.user_id AND s.id = r.store_id
WHERE r.user_id = @user_id AND r.id = @receipt_id;

-- The user's own categories, in the user's spelling: the only categories
-- the model may pick from, sent by name and stored by id.
-- name: ListSuggestionCategories :many
SELECT id, name FROM categories WHERE user_id = @user_id ORDER BY lower(name);

-- One pending answer: the raw line text as it was when the model saw it,
-- the validated name and category, and the model and prompt version that
-- produced them.
-- name: InsertLineSuggestion :one
INSERT INTO line_suggestions (user_id, receipt_id, line_item_id, raw_name, suggested_name, suggested_category_id, model, prompt_version, created_at)
VALUES (@user_id, @receipt_id, @line_item_id, @raw_name, @suggested_name, sqlc.narg(suggested_category_id), @model, @prompt_version, @now)
RETURNING *;

-- The newest suggestion job of one user, whatever became of it: the
-- settings badge reads its state and whether it carries an error, never
-- the error text.
-- name: GetNewestSuggestLinesJob :one
SELECT * FROM jobs
WHERE kind = 'suggest_lines' AND payload->>'user_id' = @user_id::text
ORDER BY updated_at DESC, id DESC
LIMIT 1;

-- The suggestion generation path: the eligible lines of one receipt, the
-- receipt's store name, the user's category names, the insert of one
-- answer, and the newest job for the settings badge.
--
-- Tables: line_items, line_suggestions, receipts, stores, categories, jobs.
--
-- Rules: every query on a user-owned table filters by user_id; query names
-- are unique across all files; run `task generate` after editing.
--
-- Add queries above this description: sqlc copies the comment that precedes
-- a file's first query into that query's Go documentation.
