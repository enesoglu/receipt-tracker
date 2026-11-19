import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './app/App';
import './styles/tokens.css';

async function bootstrap() {
  if (import.meta.env.VITE_API_MOCK === '1') {
    const { startMockWorker } = await import('./mocks/browser');
    await startMockWorker();
  }

  const root = document.getElementById('root');
  if (!root) throw new Error('Missing application root');

  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );

  // The queue resumes on every app open; the service worker adds
  // background finishing in production builds only — dev keeps plain
  // resume-on-open. Best-effort: a failed registration changes nothing.
  if (import.meta.env.PROD && navigator.serviceWorker) {
    window.addEventListener('load', () => {
      void navigator.serviceWorker.register('/sw.js').catch(() => undefined);
    });
  }
}

void bootstrap();
