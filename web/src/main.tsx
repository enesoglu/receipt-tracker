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
}

void bootstrap();
