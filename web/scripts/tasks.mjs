import { spawnSync } from 'node:child_process';
import { run, start } from './subprocess.mjs';
import { createHash } from 'node:crypto';
import {
  cpSync,
  existsSync,
  mkdirSync,
  readdirSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
process.chdir(root);
const windows = process.platform === 'win32';
const lintVersion = '2.15.0';
const lintBinary = join(
  root,
  'bin',
  windows ? 'golangci-lint.exe' : 'golangci-lint',
);

function embed() {
  if (!existsSync('web/dist/index.html'))
    run('npm', ['--prefix', 'web', 'run', 'build']);
  mkdirSync('internal/web/dist', { recursive: true });
  // Replace the copied tree so removed Vite assets cannot survive a rebuild.
  const target = resolve('internal/web/dist');
  if (target !== join(root, 'internal', 'web', 'dist'))
    throw new Error('Invalid asset target');
  for (const entry of readdirSync(target, { withFileTypes: true })) {
    if (entry.name === '.gitkeep') continue;
    // rmSync is restricted to the verified build output directory.
    removeBuildOutput(join(target, entry.name));
  }
  cpSync('web/dist', target, { recursive: true });
}

function removeBuildOutput(path) {
  // Imported here only for disposable generated assets under internal/web/dist.
  rmSync(path, { recursive: true, force: true });
}

async function installLint() {
  if (existsSync(lintBinary)) {
    const installed = spawnSync(lintBinary, ['version'], { encoding: 'utf8' });
    if (
      installed.status === 0 &&
      installed.stdout.includes(`version ${lintVersion} `)
    )
      return;
  }
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  const platform = { win32: 'windows', linux: 'linux', darwin: 'darwin' }[
    process.platform
  ];
  if (!arch || !platform) throw new Error('Unsupported tool platform');
  const stem = `golangci-lint-${lintVersion}-${platform}-${arch}`;
  const name = `${stem}.${windows ? 'zip' : 'tar.gz'}`;
  const base = `https://github.com/golangci/golangci-lint/releases/download/v${lintVersion}`;
  const responses = await Promise.all([
    fetch(`${base}/${name}`),
    fetch(`${base}/golangci-lint-${lintVersion}-checksums.txt`),
  ]);
  for (const response of responses)
    if (!response.ok)
      throw new Error(`Tool download failed: ${response.status}`);
  const archive = Buffer.from(await responses[0].arrayBuffer());
  const checksums = await responses[1].text();
  const expected = checksums
    .split('\n')
    .find((line) => line.trim().endsWith(` ${name}`))
    ?.trim()
    .split(/\s+/)[0];
  if (
    !expected ||
    createHash('sha256').update(archive).digest('hex') !== expected
  )
    throw new Error('Linter checksum mismatch');
  mkdirSync('bin/tools', { recursive: true });
  const archivePath = join(root, 'bin', 'tools', name);
  writeFileSync(archivePath, archive);
  run('tar', ['-xf', archivePath, '-C', 'bin/tools']);
  cpSync(
    join(
      root,
      'bin',
      'tools',
      stem,
      windows ? 'golangci-lint.exe' : 'golangci-lint',
    ),
    lintBinary,
  );
}

function generate() {
  // Each generator runs once its source exists: sqlc from sqlc.yaml, the API
  // bindings from api/openapi.yaml.
  if (existsSync('sqlc.yaml')) run('go', ['tool', 'sqlc', 'generate']);
  if (existsSync('api/openapi.yaml')) {
    run('go', [
      'tool',
      'oapi-codegen',
      '-config',
      'api/oapi-codegen.yaml',
      'api/openapi.yaml',
    ]);
    run(
      'npm',
      [
        'exec',
        '--',
        'openapi-typescript',
        '../api/openapi.yaml',
        '-o',
        'src/api/schema.gen.ts',
      ],
      { cwd: join(root, 'web') },
    );
    // The generated schema is linted like any other source file, so it is
    // written in the project's own style.
    run('npm', ['exec', '--', 'prettier', '--write', 'src/api/schema.gen.ts'], {
      cwd: join(root, 'web'),
    });
  }
  console.log('Generation complete.');
}

function drift() {
  const paths = [
    'internal/db/sqlc',
    'internal/httpapi/api.gen.go',
    'web/src/api/schema.gen.ts',
  ];
  const diff = spawnSync('git', ['diff', '--name-only', '--', ...paths], {
    encoding: 'utf8',
  });
  const untracked = spawnSync(
    'git',
    ['ls-files', '--others', '--exclude-standard', '--', ...paths],
    { encoding: 'utf8' },
  );
  if (diff.status !== 0 || untracked.status !== 0)
    throw new Error('Cannot inspect generated drift');
  // Expected generated changes may be staged before the required pre-commit check.
  const changed = diff.stdout + untracked.stdout;
  if (changed.trim())
    throw new Error(`Generated files have unstaged drift:\n${changed}`);
}

function dev() {
  run('npm', ['--prefix', 'web', 'run', 'build']);
  embed();
  const children = [
    start(
      process.execPath,
      [
        '--watch',
        '--watch-path=cmd',
        '--watch-path=internal',
        'web/scripts/server-dev.mjs',
      ],
      { stdio: 'inherit' },
    ),
    start('npm', ['--prefix', 'web', 'run', 'dev']),
  ];
  const stop = () => {
    for (const child of children) child.kill();
  };
  process.on('SIGINT', stop);
  process.on('SIGTERM', stop);
  for (const child of children)
    child.on('exit', (code) => {
      stop();
      process.exitCode = code ?? 0;
    });
}

// Keep all destructive filesystem work inside the verified disposable output tree.
import { rmSync } from 'node:fs';
const command = process.argv[2];
if (command === 'install-lint') await installLint();
else if (command === 'embed') embed();
else if (command === 'generate') generate();
else if (command === 'drift') drift();
else if (command === 'lint-go') {
  const formatted = spawnSync(lintBinary, ['fmt', '--diff'], {
    encoding: 'utf8',
  });
  if (formatted.status !== 0 || formatted.stdout.trim())
    throw new Error(
      'Go formatting differs:\n' + formatted.stdout + formatted.stderr,
    );
  run(lintBinary, ['run', './cmd/...', './internal/...']);
} else if (command === 'dev') dev();
else throw new Error(`Unknown task ${command}`);
