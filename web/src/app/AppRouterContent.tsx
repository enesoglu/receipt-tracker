import type { QueryClient } from '@tanstack/react-query';
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router';
import { QueryProvider } from '../api/QueryProvider';
import { CategoriesPage } from '../features/categories/CategoriesPage';
import { UiPreviewPage } from '../features/dev/UiPreviewPage';
import { PlaceholderPage } from '../features/placeholders/PlaceholderPage';
import { ProductDetailPage } from '../features/products/ProductDetailPage';
import { ProductsListPage } from '../features/products/ProductsListPage';
import { ReceiptDetailPage } from '../features/receipts/ReceiptDetailPage';
import { ReceiptsListPage } from '../features/receipts/ReceiptsListPage';
import { ReviewQueuePage } from '../features/review/ReviewQueuePage';
import { SettingsPage } from '../features/settings/SettingsPage';
import { SpendingPage } from '../features/spending/SpendingPage';
import { StoresPage } from '../features/stores/StoresPage';
import { UploadPage } from '../features/upload/UploadPage';
import { AuthGate } from './AuthGate';
import { LoginRoute } from './LoginRoute';

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
          <Route path="/products" element={<ProductsListPage />} />
          <Route path="/products/:productId" element={<ProductDetailPage />} />
          <Route path="/spending" element={<SpendingPage />} />
          <Route path="/stores" element={<StoresPage />} />
          <Route path="/categories" element={<CategoriesPage />} />
          <Route path="/settings" element={<SettingsPage />} />
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
