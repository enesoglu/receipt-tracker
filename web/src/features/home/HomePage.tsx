export function HomePage() {
  return (
    <main className="min-h-screen bg-canvas px-6 py-16 font-sans text-body text-ink">
      <section
        className="mx-auto max-w-content rounded-card border border-border bg-surface p-8"
        aria-labelledby="page-title"
      >
        <p className="mb-4 text-small font-semibold text-accent">
          RECEIPT TRACKER
        </p>
        <h1 id="page-title" className="mb-4 text-title font-semibold">
          Your groceries, clearly recorded.
        </h1>
        <p className="mb-6 text-muted">
          Turn grocery receipts into a useful record of what you buy and spend.
        </p>
        <p className="text-small text-muted" role="status">
          No receipts yet. Uploads, sign-in, and reports are coming in later
          work packages.
        </p>
      </section>
    </main>
  );
}
