import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { delimiter, dirname, join, isAbsolute } from 'node:path';

function invocation(command, args) {
  if (process.platform !== 'win32' || isAbsolute(command))
    return [command, args];
  if (command === 'npm')
    return [
      process.execPath,
      [
        join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'),
        ...args,
      ],
    ];
  const executable = (process.env.PATH ?? '')
    .split(delimiter)
    .map((directory) => join(directory, `${command}.exe`))
    .find(existsSync);
  return [executable ?? command, args];
}

export function run(command, args, options = {}) {
  const [executable, arguments_] = invocation(command, args);
  const result = spawnSync(executable, arguments_, {
    stdio: 'inherit',
    ...options,
  });
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(`${command} failed (${result.status})`);
  return result;
}

export function start(command, args, options = {}) {
  const [executable, arguments_] = invocation(command, args);
  const child = spawn(executable, arguments_, { stdio: 'inherit', ...options });
  child.on('error', (error) => {
    console.error(error);
    process.exitCode = 1;
  });
  return child;
}
