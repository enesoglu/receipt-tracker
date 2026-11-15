-- +goose Up
-- Finished receipts that no report can place (no date or no total). The
-- reports count them on every request; a partial index keeps that count
-- proportional to the few such receipts, not to all receipts.
CREATE INDEX receipts_unreportable_idx ON receipts (user_id)
    WHERE status IN ('needs_review', 'ready') AND (purchased_on IS NULL OR total IS NULL);

-- The category breakdown reads the priced lines of one month's receipts. This
-- index carries every column it needs, so it never visits the table, and it
-- skips lines without a total.
CREATE INDEX line_items_receipt_id_priced_idx ON line_items (receipt_id)
    INCLUDE (user_id, line_total, product_id)
    WHERE line_total IS NOT NULL;

-- +goose Down
DROP INDEX line_items_receipt_id_priced_idx;
DROP INDEX receipts_unreportable_idx;
