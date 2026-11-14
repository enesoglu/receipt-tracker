import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { UiPreviewPage } from './features/dev/UiPreviewPage';
import { HomePage } from './features/home/HomePage';
import './styles/tokens.css';

const isDevUiPreview =
  import.meta.env.DEV && window.location.pathname === '/dev/ui';

const root = document.getElementById('root');
if (!root) throw new Error('Missing application root');
createRoot(root).render(
  <StrictMode>{isDevUiPreview ? <UiPreviewPage /> : <HomePage />}</StrictMode>,
);
