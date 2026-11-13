-- name: Ping :one
-- Ping proves that the database answers queries.
SELECT 1::integer AS ok;

-- This file holds instance-level checks that belong to no domain. sqlc
-- refuses to generate a package without any query, so Ping also keeps
-- generation working while the domain files are still empty.
--
-- The description sits below the query on purpose: sqlc copies the comment
-- above a file's first query into that query's Go documentation.
