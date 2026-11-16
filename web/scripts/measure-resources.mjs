import { randomBytes, randomUUID } from 'node:crypto';
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { run } from './subprocess.mjs';

const root = fileURLToPath(new URL('../..', import.meta.url));
const temporaryParent = realpathSync(tmpdir());
const directory = mkdtempSync(join(temporaryParent, 'rt-resources-'));
const identifier = randomUUID();
const prefix = `rt-resources-${identifier}`;
const app = `${prefix}-app`,
  database = `${prefix}-db`,
  volume = `${prefix}-images`;
const label = 'receipt-tracker.resource-probe';
const image = 'receipt-tracker:resources';
const delay = (milliseconds) =>
  new Promise((resolve) => setTimeout(resolve, milliseconds));
const command = (name, args, options = {}) =>
  run(name, args, {
    cwd: root,
    stdio: 'pipe',
    encoding: 'utf8',
    maxBuffer: 4 << 20,
    ...options,
  }).stdout.trim();
const docker = (...args) => command('docker', args);
const report = {
  measuredUtc: new Date().toISOString(),
  sourceRevision: command('git', ['rev-parse', 'HEAD']),
  dimensions: [4032, 3024],
  filesPerBatch: 10,
  extractor: 'fake',
  imageStorage: 'local disk volume',
  limitsBytes: {
    app: 512 * 1024 ** 2,
    database: 256 * 1024 ** 2,
    temporary: 64 * 1024 ** 2,
  },
  samplingIntervalMs: 200,
};
let base, cookie;

function memory(name) {
  const [current, ...lines] = docker(
    'exec',
    name,
    'sh',
    '-c',
    'cat /sys/fs/cgroup/memory.current; cat /sys/fs/cgroup/memory.stat',
  ).split('\n');
  const stats = Object.fromEntries(
    lines.map((line) => line.trim().split(/\s+/)),
  );
  return {
    current: Number(current),
    anon: Number(stats.anon),
    workingSet: Math.max(0, Number(current) - Number(stats.inactive_file)),
  };
}
function sample() {
  return { app: memory(app), database: memory(database) };
}
function maximum(peak, next) {
  for (const name of ['app', 'database'])
    for (const field of ['current', 'anon', 'workingSet'])
      peak[name][field] = Math.max(peak[name][field], next[name][field]);
}
async function request(path, options = {}, timeout = 10000) {
  const headers = new Headers(options.headers);
  if (cookie) headers.set('Cookie', cookie);
  return fetch(base + path, {
    redirect: 'manual',
    ...options,
    headers,
    signal: AbortSignal.timeout(timeout),
  });
}
async function batch(extension) {
  const data = readFileSync(join(directory, `synthetic.${extension}`));
  if (data.length + 12 > 15 * 1024 ** 2)
    throw new Error('Synthetic fixture exceeds the upload cap');
  const form = new FormData();
  for (let index = 0; index < 10; index++) {
    // Legal BMFF free boxes keep HEIC valid while ensuring ten conversions,
    // instead of one conversion followed by nine hash-duplicate responses.
    const suffix =
      extension === 'heic' ? Buffer.alloc(12) : Buffer.from([index]);
    if (extension === 'heic') {
      suffix.writeUInt32BE(12, 0);
      suffix.write('free', 4);
      suffix.writeUInt32BE(index, 8);
    }
    form.append(
      'files',
      new Blob([data, suffix]),
      `synthetic-${index}.${extension}`,
    );
  }
  const peak = sample();
  let running = true,
    monitoringError;
  const monitor = (async () => {
    while (running) {
      try {
        maximum(peak, sample());
      } catch (error) {
        monitoringError = error;
        break;
      }
      await delay(200);
    }
  })();
  try {
    const response = await request(
      '/api/v1/receipts',
      { method: 'POST', body: form },
      600000,
    );
    const result = await response.json();
    if (
      response.status !== 200 ||
      result.results?.length !== 10 ||
      result.results.some((item) => item.outcome !== 'created')
    )
      throw new Error('Upload did not create ten synthetic receipts');
    const ids = new Set(result.results.map((item) => item.receiptId));
    if (ids.size !== 10)
      throw new Error('Upload did not create ten distinct receipts');
    const deadline = Date.now() + 600000;
    for (;;) {
      const response = await request('/api/v1/receipts?limit=100');
      const page = await response.json();
      if (response.status !== 200)
        throw new Error('Cannot read processing status');
      const receipts = page.items.filter((item) => ids.has(item.id));
      if (
        receipts.length !== 10 ||
        receipts.some((item) =>
          ['failed', 'waiting_quota'].includes(item.status),
        )
      )
        throw new Error('Synthetic processing failed');
      if (
        receipts.every((item) =>
          ['ready', 'needs_review'].includes(item.status),
        )
      )
        break;
      if (Date.now() > deadline)
        throw new Error('Synthetic processing timed out');
      await delay(100);
    }
  } finally {
    running = false;
    await monitor;
  }
  if (monitoringError) throw monitoringError;
  return { inputBytes: data.length, peak };
}
function removeOwned(kind, name) {
  let resource;
  try {
    resource = JSON.parse(docker(kind, 'inspect', name))[0];
  } catch {
    return;
  }
  const labels =
    kind === 'container' ? resource.Config.Labels : resource.Labels;
  if (labels?.[label] !== identifier)
    throw new Error(`Refusing to remove an unowned ${kind}`);
  if (kind === 'container') docker('rm', '-f', '-v', name);
  else docker(kind, 'rm', name);
}
const failures = [];
try {
  run(
    'go',
    [
      'test',
      './internal/ingest',
      '-run',
      '^TestGenerateResourceFixtures$',
      '-count=1',
    ],
    {
      cwd: root,
      env: { ...process.env, INGEST_RESOURCE_FIXTURE_DIR: directory },
    },
  );
  report.dockerVersion = docker('version', '--format', '{{.Server.Version}}');
  report.storageDriver = docker('info', '--format', '{{.Driver}}');
  report.imageBytes = Number(
    docker('image', 'inspect', image, '--format', '{{.Size}}'),
  );
  report.imageId = docker('image', 'inspect', image, '--format', '{{.Id}}');
  docker('network', 'create', '--label', `${label}=${identifier}`, prefix);
  docker('volume', 'create', '--label', `${label}=${identifier}`, volume);
  docker(
    'run',
    '--rm',
    '--network',
    'none',
    '--read-only',
    '--user',
    '0',
    '--mount',
    `type=volume,source=${volume},target=/data/images`,
    '--entrypoint',
    'sh',
    image,
    '-c',
    'chown app:app /data/images',
  );
  docker(
    'run',
    '-d',
    '--name',
    database,
    '--label',
    `${label}=${identifier}`,
    '--network',
    prefix,
    '--memory',
    '256m',
    '--memory-swap',
    '256m',
    '-e',
    'POSTGRES_USER=receipt',
    '-e',
    'POSTGRES_PASSWORD=receipt',
    '-e',
    'POSTGRES_DB=receipt',
    'postgres:18.4-alpine3.23',
    'postgres',
    '-c',
    'shared_buffers=64MB',
    '-c',
    'max_connections=20',
  );
  const databaseDeadline = Date.now() + 60000;
  for (;;) {
    try {
      docker(
        'exec',
        database,
        'pg_isready',
        '-h',
        '127.0.0.1',
        '-U',
        'receipt',
        '-d',
        'receipt',
      );
      break;
    } catch {
      if (Date.now() > databaseDeadline)
        throw new Error('Resource database did not become ready');
      await delay(500);
    }
  }
  docker(
    'run',
    '-d',
    '--name',
    app,
    '--label',
    `${label}=${identifier}`,
    '--network',
    prefix,
    '--memory',
    '512m',
    '--memory-swap',
    '512m',
    '--read-only',
    '--tmpfs',
    '/tmp:size=64m,mode=1777',
    '--mount',
    `type=volume,source=${volume},target=/data/images`,
    '-p',
    '127.0.0.1::8080',
    '-e',
    'APP_ENV=dev',
    '-e',
    'APP_BASE_URL=http://localhost:8080',
    '-e',
    `DATABASE_URL=postgres://receipt:receipt@${database}:5432/receipt?sslmode=disable`,
    '-e',
    `APP_ENCRYPTION_KEY=${randomBytes(32).toString('base64')}`,
    '-e',
    'ALLOWED_EMAILS=resource@example.com',
    '-e',
    'ADMIN_EMAIL=resource@example.com',
    '-e',
    'DEV_LOGIN_EMAIL=resource@example.com',
    '-e',
    'STORAGE_BACKEND=local',
    '-e',
    'STORAGE_LOCAL_DIR=/data/images',
    '-e',
    'EXTRACTOR=fake',
    '-e',
    'BACKUP_DESTINATION=none',
    image,
  );
  base = `http://${docker('port', app, '8080/tcp')}`;
  const appDeadline = Date.now() + 60000;
  for (;;) {
    try {
      if ((await request('/healthz')).status === 200) break;
    } catch {
      /* Starting. */
    }
    if (Date.now() > appDeadline)
      throw new Error('Resource app did not become ready');
    await delay(100);
  }
  const login = await request('/auth/dev/login');
  cookie = login.headers.get('set-cookie')?.split(';')[0];
  if (login.status !== 303 || !cookie)
    throw new Error('Synthetic sign-in failed');
  await delay(3000);
  report.idle = sample();
  report.jpeg = await batch('jpg');
  report.heic = await batch('heic');
  report.cgroupPeak = {
    app: Number(docker('exec', app, 'cat', '/sys/fs/cgroup/memory.peak')),
    database: Number(
      docker('exec', database, 'cat', '/sys/fs/cgroup/memory.peak'),
    ),
  };
  report.oomKilled = {
    app: JSON.parse(docker('inspect', app, '--format', '{{.State.OOMKilled}}')),
    database: JSON.parse(
      docker('inspect', database, '--format', '{{.State.OOMKilled}}'),
    ),
  };
  if (report.oomKilled.app || report.oomKilled.database)
    throw new Error('Resource container exceeded its memory limit');
  mkdirSync(join(root, 'bin'), { recursive: true });
  writeFileSync(
    join(root, 'bin/resource-report.json'),
    JSON.stringify(report, null, 2) + '\n',
  );
  console.log(JSON.stringify(report, null, 2));
} catch (error) {
  failures.push(error);
} finally {
  for (const [kind, name] of [
    ['container', app],
    ['container', database],
    ['volume', volume],
    ['network', prefix],
  ]) {
    try {
      removeOwned(kind, name);
    } catch (error) {
      failures.push(error);
    }
  }
  if (
    dirname(resolve(directory)) !== temporaryParent ||
    !basename(directory).startsWith('rt-resources-')
  )
    failures.push(new Error('Invalid disposable fixture directory'));
  else {
    try {
      rmSync(directory, { recursive: true, force: true });
    } catch (error) {
      failures.push(error);
    }
  }
}
if (failures.length)
  throw new AggregateError(failures, 'Resource measurement failed');
