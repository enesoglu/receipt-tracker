import type { QueryClient } from '@tanstack/react-query';
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router';
import { useMe } from '../api/auth';
import { isForbiddenError } from '../api/errors';
import { QueryProvider } from '../api/QueryProvider';
import { Spinner } from '../components/ui/Spinner';
import { SignInPage } from '../features/auth/SignInPage';
import { UiPreviewPage } from '../features/dev/UiPreviewPage';
import { PlaceholderPage } from '../features/placeholders/PlaceholderPage';
import { ReceiptDetailPage } from '../features/receipts/ReceiptDetailPage';
import { ReceiptsListPage } from '../features/receipts/ReceiptsListPage';
import { ReviewQueuePage } from '../features/review/ReviewQueuePage';
import { UploadPage } from '../features/upload/UploadPage';
import { AuthGate } from './AuthGate';

function LoginRoute() {
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

export interface AppRouterContentProps {
  queryClient?: QueryClient;
}

/** Provides QueryProvider inside a Router context so 401 responses can redirect to /login. */
export function AppRouterContent({ queryClient }: AppRouterContentProps) {
  const navigate = useNavigate();
  const location = useLocation();

  return (
    <QueryProvider
      client={queryClient}
      onUnauthorized={() => {
        if (location.pathname !== '/login') {
          void navigate('/login', { replace: true });
        }
      }}
    >
      <Routes>
        {import.meta.env.DEV ? (
          <Route path="/dev/ui" element={<UiPreviewPage />} />
        ) : null}

        <Route path="/login" element={<LoginRoute />} />

        <Route element={<AuthGate />}>
          <Route path="/" element={<Navigate to="/receipts" replace />} />
          <Route path="/receipts" element={<ReceiptsListPage />} />
          <Route path="/receipts/:receiptId" element={<ReceiptDetailPage />} />
          <Route path="/upload" element={<UploadPage />} />
          <Route path="/review" element={<ReviewQueuePage />} />
          <Route
            path="/products"
            element={
              <PlaceholderPage
                eyebrow="Catalog"
                title="Products"
                description="Manage canonical products, aliases, and cross-store unit price history."
                emptyTitle="Product catalog coming next"
                emptyDescription="Canonical products, merge/split tools, and price charts will appear here."
              />
            }
          />
          <Route
            path="/spending"
            element={
              <PlaceholderPage
                eyebrow="Reports"
                title="Spending"
                description="Track monthly grocery totals and breakdowns across categories and stores."
                emptyTitle="Spending reports coming next"
                emptyDescription="Monthly spending trends and category breakdowns will appear here."
              />
            }
          />
          <Route
            path="/categories"
            element={
              <PlaceholderPage
                eyebrow="Organization"
                title="Categories"
                description="Create, rename, and remove spending categories applied across your products."
                emptyTitle="Category management coming next"
                emptyDescription="Category CRUD controls and usage counts will appear here."
              />
            }
          />
          <Route
            path="/settings"
            element={
              <PlaceholderPage
                eyebrow="Preferences"
                title="Settings"
                description="Configure default currency, receipt title, OCR quota visibility, and Google Drive storage."
                emptyTitle="Settings screen coming next"
                emptyDescription="Account preferences, OCR usage meter, and Google Drive backup status will live here."
              />
            }
          />
          <Route
            path="*"
            element={
              <PlaceholderPage
                title="Page not found"
                description="The screen you requested does not exist."
                emptyTitle="Nothing matches this address"
                emptyDescription="Choose a section from the navigation menu or return to Receipts."
                actionLabel="Back to Receipts"
                actionTo="/receipts"
              />
            }
          />
        </Route>
      </Routes>
    </QueryProvider>
  );
}

/** Root web application component with browser routing. */
export function App() {
  return (
    <BrowserRouter>
      <AppRouterContent />
    </BrowserRouter>
  );
}
