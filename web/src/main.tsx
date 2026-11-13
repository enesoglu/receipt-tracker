import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { HomePage } from './features/home/HomePage';
import './styles/tokens.css';

const root = document.getElementById('root');
if (!root) throw new Error('Missing application root');
createRoot(root).render(
  <StrictMode>
    <HomePage />
  </StrictMode>,
);
