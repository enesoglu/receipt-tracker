-- +goose Up

-- Store resolution (ARCHITECTURE §5.1). A store is the chain: the printed
-- name and the seller tax number resolve to one store per chain per user,
-- the raw names seen become aliases so OCR noise keeps matching, and the
-- printed branch stays on the receipt as data.
--
-- btree_gist is guarded because the nearest-name index below pairs a
-- scalar with a trigram column: 00050 creates the extension for its own
-- index, but a database replaying only 00020..00170 would not have it.
-- pg_trgm comes from 00020 already.
CREATE EXTENSION IF NOT EXISTS btree_gist;

ALTER TABLE stores
    ADD COLUMN vkn text,
    ADD CONSTRAINT stores_vkn_check CHECK (vkn IS NULL OR vkn ~ '^[0-9]{10}$'),
    ADD CONSTRAINT stores_user_id_vkn_key UNIQUE (user_id, vkn);

-- The branch is printed data, never a store: it is kept on the receipt,
-- trimmed and bounded, and stays NULL when the text is unreadable.
ALTER TABLE receipts
    ADD COLUMN branch_name text,
    ADD CONSTRAINT receipts_branch_name_check
        CHECK (branch_name IS NULL OR (btrim(branch_name) = branch_name AND char_length(branch_name) BETWEEN 1 AND 200));

CREATE TABLE store_aliases (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id         uuid        NOT NULL,
    store_id        uuid        NOT NULL,
    raw_name        text        NOT NULL,
    normalized_name text        NOT NULL,
    seen_count      integer     NOT NULL DEFAULT 1,
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT store_aliases_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT store_aliases_store_fkey FOREIGN KEY (user_id, store_id) REFERENCES stores (user_id, id) ON DELETE CASCADE,
    CONSTRAINT store_aliases_user_id_id_key UNIQUE (user_id, id),
    CONSTRAINT store_aliases_raw_name_check CHECK (btrim(raw_name) <> ''),
    CONSTRAINT store_aliases_normalized_name_check CHECK (normalized_name <> ''),
    CONSTRAINT store_aliases_store_normalized_name_key UNIQUE (store_id, normalized_name)
);

-- Resolution looks up the 20 of the user's store and alias names nearest
-- to a receipt's name, the same shape as 00050's product-alias index: the
-- <-> ordering walks the GiST index inside one user's names.
CREATE INDEX store_aliases_user_id_normalized_name_gist_idx
    ON store_aliases USING gist (user_id, normalized_name gist_trgm_ops (siglen = 64));
-- The merge combines the aliases of two stores per user.
CREATE INDEX store_aliases_user_id_store_id_idx ON store_aliases (user_id, store_id);

-- Every existing store's current name becomes its first alias: each of
-- its receipts showed that name, so seen_count starts at the store's
-- receipt count. Raw name and comparison name are the store's own values,
-- which is the best the old data offers.
INSERT INTO store_aliases (user_id, store_id, raw_name, normalized_name, seen_count, last_seen_at, created_at)
SELECT s.user_id, s.id, s.name, s.normalized_name,
       (SELECT count(*)::integer FROM receipts r WHERE r.user_id = s.user_id AND r.store_id = s.id),
       s.updated_at, s.updated_at
FROM stores s;

-- +goose Down

-- Drops only what this migration added. btree_gist is kept: 00050's
-- product-alias index still uses it, and a rollback must not break that
-- migration.
DROP TABLE store_aliases;
ALTER TABLE receipts DROP COLUMN branch_name;
ALTER TABLE stores DROP COLUMN vkn;
