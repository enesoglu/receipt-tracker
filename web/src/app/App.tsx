import { BrowserRouter } from 'react-router';
import { AppRouterContent } from './AppRouterContent';

/** Root web application component with browser routing. */
export function App() {
  return (
    <BrowserRouter>
      <AppRouterContent />
    </BrowserRouter>
  );
}
