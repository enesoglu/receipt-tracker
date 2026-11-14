-- name: GetDriveConnection :one
SELECT * FROM storage_connections WHERE backend = 'drive';

-- name: SaveDriveConnection :exec
INSERT INTO storage_connections (backend, connected_by, encrypted_refresh_token, root_folder_ref, updated_at)
VALUES ('drive', $1, $2, $3, $4)
ON CONFLICT (backend) DO UPDATE SET connected_by = EXCLUDED.connected_by,
    encrypted_refresh_token = EXCLUDED.encrypted_refresh_token,
    root_folder_ref = EXCLUDED.root_folder_ref, status = 'connected', last_error = NULL,
    updated_at = EXCLUDED.updated_at;

-- name: MarkDriveReconnect :exec
UPDATE storage_connections SET status = 'reconnect_needed', last_error = $1, updated_at = $2
WHERE backend = 'drive' AND encrypted_refresh_token = $3;

-- name: DeleteDriveConnection :exec
DELETE FROM storage_connections WHERE backend = 'drive';
