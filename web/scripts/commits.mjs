import { spawnSync } from 'node:child_process';
import { mkdtempSync, existsSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const types = '(?:feat|fix|chore|docs|refactor|test|ci)';
const subjectPattern = new RegExp(`^${types}\\([a-z][a-z0-9-]*\\): [a-z].+$`);
const branchPattern = new RegExp(`^${types}/[a-z0-9]+(?:-[a-z0-9]+)*$`);
function git(args) {
  const result = spawnSync('git', args, { cwd: root, encoding: 'utf8' });
  if (result.status !== 0)
    throw new Error(result.stderr || 'git command failed');
  return result.stdout.trim();
}
function validateSubject(subject) {
  if (
    subject.length > 72 ||
    !subjectPattern.test(subject) ||
    /\b(?:wip|fixup|squash|step|phase)\b|fix lint|address review/i.test(subject)
  )
    throw new Error(`Invalid Conventional Commit subject: ${subject}`);
}
function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(`${command} failed (${result.status})`);
}
function removeCheckout(checkout) {
  const resolved = resolve(checkout);
  if (
    dirname(resolved) !== resolve(tmpdir()) ||
    !resolved.startsWith(join(resolve(tmpdir()), 'receipt-commit-'))
  )
    throw new Error('Invalid temporary checkout path');
  rmSync(resolved, { recursive: true, force: true });
}

const [mode, ...args] = process.argv.slice(2);
if (mode === 'pr') {
  const event = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'));
  if (!event.pull_request) throw new Error('PR event required');
  validateSubject(event.pull_request.title);
  if (!branchPattern.test(event.pull_request.head.ref))
    throw new Error(`Invalid branch name: ${event.pull_request.head.ref}`);
  console.log('PR title and branch name passed.');
} else if (mode === 'check') {
  const [base, head = 'HEAD', metadataOnly] = args;
  if (!base) throw new Error('Base revision required');
  const commits = git(['rev-list', '--reverse', `${base}..${head}`])
    .split('\n')
    .filter(Boolean);
  if (!commits.length) throw new Error('No commits to validate');
  for (const sha of commits) {
    validateSubject(git(['show', '-s', '--format=%s', sha]));
    const parents = git(['show', '-s', '--format=%P', sha])
      .split(' ')
      .filter(Boolean);
    if (parents.length > 1)
      throw new Error(`${sha}: merge commits are forbidden`);
    const message = git([
      'show',
      '-s',
      '--format=%(trailers:key=Co-Authored-By,valueonly)',
      sha,
    ]);
    if (
      !/^(?:Codex|Claude|Gemini) .+ <noreply@(?:openai\.com|anthropic\.com|google\.com)>$/m.test(
        message,
      )
    )
      throw new Error(`${sha}: missing agent Co-Authored-By trailer`);
    console.log(`${sha}: commit metadata passed`);
    if (metadataOnly === '--metadata-only') continue;
    const files = git(['ls-tree', '-r', '--name-only', sha]).split('\n');
    if (!files.includes('go.mod')) {
      // Owner approved only these two pre-tooling bootstrap commits.
      if (!['c5ea1de', 'e750ad2'].includes(sha.slice(0, 7)))
        throw new Error(`${sha}: application build is missing`);
      console.log(`${sha}: approved pre-tooling bootstrap commit`);
      continue;
    }
    const checkout = mkdtempSync(join(tmpdir(), 'receipt-commit-'));
    try {
      const archive = spawnSync('git', ['archive', sha], {
        cwd: root,
        maxBuffer: 32 * 1024 * 1024,
      });
      if (archive.status !== 0) throw new Error('Cannot archive commit');
      const unpack = spawnSync('tar', ['-xf', '-', '-C', checkout], {
        input: archive.stdout,
      });
      if (unpack.status !== 0) throw new Error('Cannot extract commit');
      if (!existsSync(join(checkout, 'Taskfile.yml')))
        throw new Error(`${sha}: task check is missing`);
      run('git', ['init', '--quiet'], checkout);
      run('git', ['add', '.'], checkout);
      run(
        'git',
        [
          '-c',
          'user.name=Bootstrap verification',
          '-c',
          'user.email=verification@example.invalid',
          'commit',
          '--quiet',
          '-m',
          'verification snapshot',
        ],
        checkout,
      );
      run('task', ['setup'], checkout);
      run('task', ['check'], checkout);
      console.log(`${sha}: independently passed task check`);
    } finally {
      removeCheckout(checkout);
    }
  }
} else throw new Error('Expected pr or check mode');
