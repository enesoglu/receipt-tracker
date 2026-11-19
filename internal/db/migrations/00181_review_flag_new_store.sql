-- +goose Up

-- Store resolution (00180) raises a receipt-level flag when the printed
-- name matched nothing: new_store asks the user to confirm the store it
-- created or correct the link. The kind list in 00020 does not know it,
-- so the check is replaced with one that does.
ALTER TABLE review_flags
    DROP CONSTRAINT review_flags_kind_check,
    ADD CONSTRAINT review_flags_kind_check CHECK (kind IN (
        'low_confidence', 'missing_field', 'line_math', 'sum_mismatch',
        'uncertain_match', 'code_conflict', 'possible_duplicate', 'new_store'
    ));

-- +goose Down

-- The kind goes with the feature, so its flags go first: the 00020
-- constraint would reject them. The receipts they questioned stay.
DELETE FROM review_flags WHERE kind = 'new_store';
ALTER TABLE review_flags
    DROP CONSTRAINT review_flags_kind_check,
    ADD CONSTRAINT review_flags_kind_check CHECK (kind IN (
        'low_confidence', 'missing_field', 'line_math', 'sum_mismatch',
        'uncertain_match', 'code_conflict', 'possible_duplicate'
    ));
