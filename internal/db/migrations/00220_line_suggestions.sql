-- +goose Up
-- Suggestions are the model's answer for one line the matcher could not
-- confidently resolve. The user decides on them, so they live beside the
-- line, cascade away with it and are never written into products, aliases
-- or line items.
CREATE TABLE line_suggestions (
    id                     uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id                uuid        NOT NULL,
    receipt_id             uuid        NOT NULL,
    line_item_id           uuid        NOT NULL,
    raw_name               text        NOT NULL,
    status                 text        NOT NULL DEFAULT 'pending',
    suggested_name         text        NOT NULL,
    suggested_category_id  uuid,
    model                  text        NOT NULL,
    prompt_version         integer     NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    decided_at             timestamptz,
    CONSTRAINT line_suggestions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT line_suggestions_receipt_fkey FOREIGN KEY (user_id, receipt_id) REFERENCES receipts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT line_suggestions_line_item_fkey FOREIGN KEY (user_id, line_item_id) REFERENCES line_items (user_id, id) ON DELETE CASCADE,
    -- Deleting a category leaves its suggestions uncategorised.
    CONSTRAINT line_suggestions_category_fkey FOREIGN KEY (user_id, suggested_category_id) REFERENCES categories (user_id, id) ON DELETE SET NULL (suggested_category_id),
    CONSTRAINT line_suggestions_user_id_line_item_id_key UNIQUE (user_id, line_item_id),
    CONSTRAINT line_suggestions_status_check CHECK (status IN ('pending', 'accepted', 'ignored')),
    CONSTRAINT line_suggestions_suggested_name_check CHECK (char_length(suggested_name) BETWEEN 1 AND 200),
    CONSTRAINT line_suggestions_decided_at_check CHECK ((decided_at IS NULL) = (status = 'pending'))
);

CREATE INDEX line_suggestions_user_id_receipt_id_idx ON line_suggestions (user_id, receipt_id);

-- The feature is off for everyone while no endpoint is configured, so the
-- per-user switch is an opt-out rather than an opt-in.
ALTER TABLE user_settings ADD COLUMN llm_suggestions_enabled boolean NOT NULL DEFAULT true;

ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('extract_receipt', 'suggest_lines'));

-- +goose Down
DROP TABLE line_suggestions;

ALTER TABLE user_settings DROP COLUMN llm_suggestions_enabled;

ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('extract_receipt'));
