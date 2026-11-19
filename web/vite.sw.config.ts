import { defineConfig } from 'vite';

/**
 * Build of the classic (non-module) service worker: one entry, one
 * stable /sw.js next to the app's assets. A second input in the main
 * config cannot work — the queue module is shared with the app, and IIFE
 * rejects code-splitting — so the worker gets this separate build. It
 * must not empty the output directory: the app build ran first.
 */
export default defineConfig({
  build: {
    emptyOutDir: false,
    rollupOptions: {
      input: 'src/sw/sw.ts',
      output: {
        entryFileNames: 'sw.js',
        format: 'iife',
        name: 'receiptTrackerUploadWorker',
      },
    },
  },
});
