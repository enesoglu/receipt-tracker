import { mkdirSync } from 'node:fs';
import { run, start } from './subprocess.mjs';

mkdirSync('bin', { recursive: true });
run('go', ['build', '-o', 'bin/', './cmd/server']);
const server = start(
  process.platform === 'win32' ? 'bin/server.exe' : 'bin/server',
  [],
);
const stop = () => server.kill();
process.on('SIGINT', stop);
process.on('SIGTERM', stop);
process.on('exit', stop);
server.on('exit', (code) => {
  process.exitCode = code ?? 0;
});
