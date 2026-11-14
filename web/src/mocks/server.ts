import { setupServer } from 'msw/node';
import { handlers } from './handlers';

/** MSW server for Node/Vitest integration tests. */
export const server = setupServer(...handlers);
