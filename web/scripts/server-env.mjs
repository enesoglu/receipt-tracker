import { existsSync } from 'node:fs';

// Both server entry points use Node's env parser. Existing shell values win;
// the binary and tests never load the developer's .env implicitly.
export function loadServerEnvironment(file = '.env') {
  if (existsSync(file)) process.loadEnvFile(file);
}
