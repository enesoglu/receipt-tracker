export const MAX_FILE_SIZE_BYTES = 15 * 1024 * 1024;
export const MAX_BATCH_FILES = 20;

export const UPLOAD_FILE_ACCEPT =
  '.jpg,.jpeg,.png,.heic,.heif,image/jpeg,image/png,image/heic,image/heif';

const ACCEPTED_MIME_TYPES = new Set([
  'image/jpeg',
  'image/jpg',
  'image/png',
  'image/heic',
  'image/heif',
]);

const ACCEPTED_EXTENSIONS = ['.jpg', '.jpeg', '.png', '.heic', '.heif'];

/** Checks whether a file matches an accepted receipt photo MIME type or extension. */
export function isAcceptedUploadType(file: File): boolean {
  const mime = file.type.trim().toLowerCase();
  if (ACCEPTED_MIME_TYPES.has(mime)) return true;
  if (mime === '' || mime === 'application/octet-stream') {
    const lowerName = file.name.trim().toLowerCase();
    return ACCEPTED_EXTENSIONS.some((ext) => lowerName.endsWith(ext));
  }
  return false;
}

/** Validates one selected photo for type, emptiness, and the 15 MB size limit. */
export function validateUploadFile(file: File): string | null {
  if (!isAcceptedUploadType(file)) {
    return 'Only JPEG, PNG, and HEIC/HEIF photos are accepted.';
  }
  if (file.size <= 0) {
    return 'File is empty.';
  }
  if (file.size > MAX_FILE_SIZE_BYTES) {
    return 'File exceeds the 15 MB size limit.';
  }
  return null;
}

/** Formats a byte count into a readable B, KB, or MB label. */
export function formatByteSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}
