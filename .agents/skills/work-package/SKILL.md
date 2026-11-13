---
name: work-package
description: Procedure for running one receipt-tracker work package from start to merge. Use when starting or resuming a work package, updating its progress file, tidying a branch, opening a pull request, or merging it.
---

# Work package procedure

A work package is one prompt from the owner, delivered as one or more small
pull requests that are **rebase-merged**: every commit lands on `main` as you
wrote it. The rules are in `AGENTS.md`.

## Where the planning files are

`planning/` is git-ignored and exists only in the main checkout. Find it with
`git worktree list`: the first line is the main checkout, and the folder is
`<main checkout>/planning`. Your progress file is
`planning/progress/<nn>-<slug>.md`. It is never committed.

## Start

1. Read the progress file. If it exists, go to **Resume**.
2. Read `AGENTS.md`, `docs/ARCHITECTURE.md` and the files your prompt lists.
3. `git fetch origin`, then create the first branch your prompt names from
   `origin/main`: `git switch -c <branch> origin/main`.
4. Create the progress file from the template below. List the pull requests
   your prompt names and, under each, the commits you plan, in order. Mark
   the first with `<- NEXT`.
5. If anything in the prompt is ambiguous or conflicts with the rules, write
   it under "Open questions" and ask the owner before building that part.

## Progress file

```markdown
# <nn> <title>

Status: in progress | merged
Updated: <date time>

## Pull requests
### 1. <branch>: open (#N) | merged (<first sha>..<last sha>) | not started
- [x] <planned commit subject>: verified with `<command>`
- [ ] <planned commit subject>   <- NEXT
### 2. <branch>: not started
- [ ] <planned commit subject>

## Decisions
- <date>: chose X over Y because Z

## Open questions for the owner
- ...

## Notes for the next session
- ...
```

Update it immediately after every commit, and before stopping for any reason.
Exactly one line carries `<- NEXT`.

## Committing

Plan the commits before writing code, so each one is a complete logical
change with its tests. Run `task check` before each commit.

When you find a mistake in an earlier commit of your branch, fix it and
record the fix against that commit:

```
git add <files>
git commit --fixup=<sha of the commit being corrected>
```

## Resume

1. Read the progress file and `git log --oneline -10`.
2. Run `git status`. Uncommitted changes that belong to the `<- NEXT` commit:
   finish and commit them. Anything else: `git stash` and note it in the
   progress file.
3. Run `task check` to confirm the tree works.
4. Continue from `<- NEXT`.

## Stopping early

When a usage limit is near, finish the current commit, push the branch,
update the progress file, and stop. A clean stop is better than a half-done
commit.

## Tidy the branch

Do this before opening a PR and again after every fix. All commands are
non-interactive and identical in PowerShell and bash.

1. Fold fixups into the commits they correct and rebase onto `main`:

   ```
   git fetch origin
   git -c sequence.editor=true rebase -i --autosquash origin/main
   ```

   On a conflict in a generated file, run `task generate`, `git add` the
   result and `git rebase --continue`.
2. Read the series: `git log --format="%h %s" origin/main..HEAD`. Every
   subject is a Conventional Commit describing one change. None says `wip`,
   `fixup!`, "fix lint", "address review" or "merge".
3. If the series is still not clean (changes in the wrong commit, commits to
   split or reorder), rebuild it: `git reset --mixed origin/main`, then
   re-commit in logical order with `git add <paths>` and `git commit` for
   each change. The working tree is untouched by the reset.
4. Prove every commit stands alone:

   ```
   git rebase --exec "task check" origin/main
   ```

   It stops at the first commit that fails. Fix that commit
   (`git commit --amend`), then `git rebase --continue`.
5. Check that every commit has your trailer:
   `git log --format="%h %(trailers:key=Co-Authored-By,valueonly)" origin/main..HEAD`
6. Publish: `git push --force-with-lease -u origin <branch>`.

## Pull request

`gh pr create --base main --title "<type>(<scope>): <summary>" --body-file <file>`

The description uses the repository's PR template: summary, the commits and
why they are ordered this way, how it was verified (commands and results),
contract or schema changes, follow-ups.

## Merge

1. `gh pr checks <number> --watch`. On failure, read the log with
   `gh run view --log-failed`, fix it with a `--fixup` commit, **tidy the
   branch again**, and watch again. A commit named "fix CI" never reaches
   `main`.
2. If `main` moved, tidy the branch again (step 1 rebases it).
3. When every check is green:
   `gh pr merge <number> --rebase --delete-branch`
4. Confirm: `gh pr view <number> --json state` and
   `git fetch origin && git log --oneline -<n> origin/main` shows your
   commits, in order, with no merge commit.
5. Record the PR as merged in the progress file. If the package has another
   PR, branch again from the new `origin/main` and continue.
6. After the last PR, set the progress file to `merged` and report to the
   owner: what was built, how it was verified, open questions, follow-ups.

A check that cannot be made green after three attempts is a blocker: record
the failure in the progress file and ask the owner. Never bypass a check,
merge with failures, squash-merge, create a merge commit, or change branch
protection.
