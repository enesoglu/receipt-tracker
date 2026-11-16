import { Navigate } from 'react-router';
import { useMe } from '../api/auth';
import { isForbiddenError } from '../api/errors';
import { Spinner } from '../components/ui/Spinner';
import { SignInPage } from '../features/auth/SignInPage';

export function LoginRoute() {
  const me = useMe();

  if (me.isPending) {
    return (
      <main className="flex min-h-screen items-center justify-center bg-canvas px-4 font-sans text-body text-ink">
        <Spinner size="lg" showLabel label="Checking session..." />
      </main>
    );
  }

  if (me.data) {
    return <Navigate to="/receipts" replace />;
  }

  return (
    <SignInPage
      accessClosed={me.isError && isForbiddenError(me.error) ? true : undefined}
    />
  );
}
