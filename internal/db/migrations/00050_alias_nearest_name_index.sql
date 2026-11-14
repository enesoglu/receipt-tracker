-- +goose Up

-- Product matching looks up the 20 aliases of one user whose names are most
-- similar to a receipt line (ARCHITECTURE §5.2). Without an index that means
-- computing the similarity of every alias the user has. A GiST trigram index
-- answers "ORDER BY normalized_name <-> name" by walking the index, and
-- btree_gist lets the same index start with user_id, so the walk stays inside
-- one user's aliases. A 64-byte signature (the default is 12) tells short
-- product names apart well enough to visit few entries. Measured over 5,000
-- aliases per user: 2.5 to 3.5 ms per lookup instead of 20 to 24 ms.
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE INDEX product_aliases_user_id_normalized_name_gist_idx
    ON product_aliases USING gist (user_id, normalized_name gist_trgm_ops (siglen = 64));

-- +goose Down

DROP INDEX product_aliases_user_id_normalized_name_gist_idx;
DROP EXTENSION IF EXISTS btree_gist;
