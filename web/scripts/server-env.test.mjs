// @vitest-environment node
import { afterEach, expect, test } from 'vitest';
import { mkdtempSync, writeFileSync, unlinkSync, rmdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { loadServerEnvironment } from './server-env.mjs';

const variable = 'RECEIPT_SERVER_TEST_SOURCE';
const previous = process.env[variable];
afterEach(() => {
  if (previous === undefined) delete process.env.RECEIPT_SERVER_TEST_SOURCE;
  else process.env[variable] = previous;
});

test('server env preserves shell values and permits a missing file', () => {
  const directory = mkdtempSync(join(tmpdir(), 'receipt-server-env-'));
  const file = join(directory, 'synthetic.env');
  try {
    delete process.env.RECEIPT_SERVER_TEST_SOURCE;
    expect(() => loadServerEnvironment(file)).not.toThrow();
    writeFileSync(file, `${variable}="from synthetic file"\n`);
    loadServerEnvironment(file);
    expect(process.env[variable]).toBe('from synthetic file');
    process.env[variable] = 'from shell';
    loadServerEnvironment(file);
    expect(process.env[variable]).toBe('from shell');
  } finally {
    unlinkSync(file);
    rmdirSync(directory);
  }
});
