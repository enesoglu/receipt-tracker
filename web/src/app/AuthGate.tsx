import { useMe } from '../api/auth';
import {
  getErrorMessage,
  isForbiddenError,
  isUnauthorizedError,
} from '../api/errors';
import { ErrorState } from '../components/ui/ErrorState';
import { Spinner } from '../components/ui/Spinner';
import { SignInPage } from '../features/auth/SignInPage';
import { AppShell } from '../features/shell/AppShell';

/** Gates the application shell on GET /me, rendering sign-in or error states otherwise. */
export function AuthGate() {
  const me = useMe();

  if (me.isPending) {
    return (
      <main className="flex min-h-screen items-center justify-center bg-canvas px-4 font-sans text-body text-ink">
        <Spinner size="lg" showLabel label="Loading your workspace..." />
      </main>
    );
  }

  if (me.isError) {
    if (isForbiddenError(me.error)) {
      return <SignInPage accessClosed />;
    }
    if (isUnauthorizedError(me.error)) {
      return <SignInPage />;
    }
    return (
      <main className="mx-auto max-w-content px-6 py-16 font-sans text-body text-ink">
        <ErrorState
          title="Could not verify your session"
          message={getErrorMessage(me.error)}
          onRetry={() => {
            void me.refetch();
          }}
        />
      </main>
    );
  }

  if (!me.data) {
    return <SignInPage />;
  }

  return <AppShell user={me.data} />;
}
