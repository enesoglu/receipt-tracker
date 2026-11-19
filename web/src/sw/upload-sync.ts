/** The Background Sync tag the service worker answers to. */
export const UPLOAD_SYNC_TAG = 'receipt-uploads';

/**
 * Asks the browser to drain the upload queue in the background once it
 * sees fit — usually when connectivity returns. Best-effort by design: a
 * no-op without service workers or Background Sync support, and silent on
 * refusal, because the queue always finishes on the next app open.
 */
export function requestUploadSync(): void {
  if (typeof navigator === 'undefined' || !navigator.serviceWorker) return;
  void navigator.serviceWorker.ready
    .then((registration) => {
      // Background Sync is not in the DOM types yet; this is the one
      // capability probe the app performs.
      const sync = (
        registration as ServiceWorkerRegistration & {
          sync?: { register(tag: string): Promise<void> };
        }
      ).sync;
      return sync?.register(UPLOAD_SYNC_TAG);
    })
    .catch(() => {
      // Asking is best-effort; nothing here is worth surfacing.
    });
}
