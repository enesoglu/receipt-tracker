import { useSearchParams } from 'react-router';
import { Card } from '../../components/ui/Card';

export interface SignInPageProps {
  accessClosed?: boolean;
}

/** Public sign-in screen linking to Google OAuth and displaying closed-access feedback. */
export function SignInPage({ accessClosed }: SignInPageProps) {
  const [searchParams] = useSearchParams();
  const isClosed =
    accessClosed ??
    (searchParams.get('error') === 'access_closed' ||
      searchParams.get('access') === 'closed');

  return (
    <main className="flex min-h-screen items-center justify-center bg-canvas px-4 py-12 font-sans text-body text-ink sm:px-6">
      <div className="w-full max-w-dialog">
        <Card padding="md">
          <div className="flex flex-col gap-5">
            <div className="flex flex-col gap-1.5">
              <p className="text-caption font-semibold tracking-wider text-accent uppercase">
                Receipt Tracker
              </p>
              <h1 className="text-title font-semibold text-ink">
                Sign in to your account
              </h1>
              <p className="text-small text-muted">
                Keep your grocery receipts, line items, and spending summaries
                organized in one place.
              </p>
            </div>

            {isClosed ? (
              <div
                role="alert"
                className="rounded-control border border-danger/30 bg-danger-soft px-4 py-3 text-small text-ink"
              >
                <p className="font-semibold text-danger">Access is closed</p>
                <p className="mt-1 text-muted">
                  This instance is limited to invited accounts. Ask the owner to
                  add your email before signing in.
                </p>
              </div>
            ) : null}

            <div className="flex flex-col gap-3 pt-1">
              <a
                href="/auth/google/login"
                className="inline-flex w-full items-center justify-center rounded-control border border-transparent bg-accent px-4 py-2 text-small font-medium text-on-accent hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              >
                Sign in with Google
              </a>
              <p className="text-center text-caption text-muted">
                You will be redirected to Google to sign in with your account.
              </p>
            </div>
          </div>
        </Card>
      </div>
    </main>
  );
}
