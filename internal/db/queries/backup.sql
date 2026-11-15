-- Backups are instance-owned and deliberately cover every application's user.
-- name: TryBackupLock :one
SELECT pg_try_advisory_lock(140014::bigint)::boolean;

-- name: ReleaseBackupLock :exec
SELECT pg_advisory_unlock(140014::bigint);

-- name: BackupTargetNonEmpty :one
SELECT EXISTS (
    SELECT 1 FROM pg_catalog.pg_class AS c
    JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND n.nspname NOT LIKE 'pg_toast%'
      AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
)::boolean;

-- This user-scoped fingerprint lets recovery tests compare content as well as counts.
-- name: BackupUserSnapshot :many
WITH rows AS (
    SELECT 'users'::text AS table_name, u.id::text AS row_key, to_jsonb(u)::text AS content
    FROM users AS u WHERE u.id = @user_id
    UNION ALL
    SELECT 'user_settings', s.user_id::text, to_jsonb(s)::text FROM user_settings AS s WHERE s.user_id = @user_id
    UNION ALL
    SELECT 'categories', c.id::text, to_jsonb(c)::text FROM categories AS c WHERE c.user_id = @user_id
    UNION ALL
    SELECT 'stores', s.id::text, to_jsonb(s)::text FROM stores AS s WHERE s.user_id = @user_id
    UNION ALL
    SELECT 'receipts', r.id::text, to_jsonb(r)::text FROM receipts AS r WHERE r.user_id = @user_id
    UNION ALL
    SELECT 'line_items', l.id::text, to_jsonb(l)::text FROM line_items AS l WHERE l.user_id = @user_id
)
SELECT table_name, count(*)::bigint AS row_count, md5(string_agg(content, '' ORDER BY row_key))::text AS checksum
FROM rows GROUP BY table_name ORDER BY table_name;
