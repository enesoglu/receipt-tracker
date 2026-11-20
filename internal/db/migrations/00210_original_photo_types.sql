-- +goose Up

-- The `original_*` columns were built for the HEIC an ingest conversion
-- keeps, so the constraint admitted HEIC/HEIF only. The optional
-- `original` part of an upload accepts the same types as `files` itself,
-- and the scanner's camera photo is a JPEG: the constraint now admits
-- every accepted type.
ALTER TABLE receipt_images
    DROP CONSTRAINT receipt_images_original_content_type_check,
    ADD CONSTRAINT receipt_images_original_content_type_check CHECK (
        original_content_type IN ('image/jpeg', 'image/png', 'image/heic', 'image/heif'));

-- +goose Down

-- Down must work even when JPEG or PNG originals exist: the old
-- constraint returns NOT VALID, so it governs new rows only and rows
-- written under the wider rule are left as they are.
ALTER TABLE receipt_images
    DROP CONSTRAINT receipt_images_original_content_type_check,
    ADD CONSTRAINT receipt_images_original_content_type_check CHECK (
        original_content_type IN ('image/heic', 'image/heif')) NOT VALID;
