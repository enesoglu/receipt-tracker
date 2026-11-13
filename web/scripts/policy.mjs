import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

function* walk(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) yield* walk(path);
    else yield path;
  }
}

const failures = [];
for (const path of walk('src')) {
  const source = readFileSync(path, 'utf8');
  if (path.endsWith('.tsx')) {
    if (/#[\da-f]{3,8}\b/i.test(source))
      failures.push(`${path}: hex colours must come from tokens`);
    if (/\b[\w-]+-\[[^\]]+\]|\[[\w-]+:[^\]]+\]|\b[\w-]+-\([^)]*\)/.test(source))
      failures.push(
        `${path}: Tailwind arbitrary values/properties are forbidden`,
      );
    if (/\b(?:transition|animate|duration|delay|ease)(?:-|\b)/.test(source))
      failures.push(`${path}: motion utilities are forbidden`);
  }
  if (
    path.endsWith('.css') &&
    /(?:^|[;{\s])(?:-\w+-)?(?:transition|animation)[\w-]*\s*:|@(?:-\w+-)?keyframes\b/i.test(
      source,
    )
  )
    failures.push(`${path}: CSS motion is forbidden`);
}
if (failures.length) {
  console.error(failures.join('\n'));
  process.exitCode = 1;
} else console.log('UI token and static-style policy passed.');
