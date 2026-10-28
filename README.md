# WhoSaidSo

WhoSaidSo keeps a project's engineering record in Git as small JSON files.
It derives goal progress from commits and proofs and searches records and code.
Its concise command-line answers are designed for people and coding agents.

## Install

Requires Rust, Cargo, Git and a Unix shell; CI verifies the supported toolchain.

```sh
cargo install --path .
```

## Quick start

In a Git repository, create the first records (there is no initialization command):

```sh
mkdir -p .whosaidso/{phases,goals,instruments}
printf '%s\n' '{"title":"First phase","order":1}' > .whosaidso/phases/p1.json
printf '%s\n' '{"command":"cargo test","answers":"Tests pass"}' > .whosaidso/instruments/test.json
printf '%s\n' '{"title":"First goal","done_when":"Tests pass","check":"test","broader":"p1"}' > .whosaidso/goals/first-goal.json
whosaidso check
whosaidso install-hooks
git add .whosaidso
git commit -m 'Add records'
```

Push the commit to `origin/main` and fetch that reference before asking for progress. Select a goal with `whosaidso start first-goal`; later commits will carry its `Goal:` trailer. After committing work, run:

```sh
whosaidso goals
whosaidso prove first-goal
whosaidso find first goal
whosaidso grep search text --in src/
whosaidso code defs physics::cross
whosaidso code deps src/main.cpp
whosaidso code includers src/types.h
whosaidso view
```

`find` prints 8 ranked results and a total; use `--limit N` for more. Files appear once as paths, and commits use 10-character SHAs with subjects capped at 72 characters. `grep` accepts repeated `--in <path-or-prefix>` options; without scope, `.whosaidso/` hits come last. `code deps` lists includes, and `code includers` lists reverse includes (up to 20, with a total when truncated). Qualified definitions and references match enclosing scope suffixes from SCIP descriptors or C++ and Rust syntax. Search lines end in `...` when cut at 200 characters, or 160 for `grep`.

`prove` creates a proof record to add and commit. It requires a readable, nonempty HEAD reflog with writing enabled (`core.logAllRefUpdates`) and refuses a proof if HEAD or its reflog changes during the run, including A to B and back. Content edits made and reverted without a commit or checkout are not detected. Record commit fields use 40 lowercase hex digits. `view` prints the path to a local HTML gallery and also writes a progress page.

Whoever lands work runs `whosaidso prune` on main after merging; it keeps proofs selected at HEAD and origin/main, including time ties, and live unlanded proofs; deletes abandoned or missing-commit proofs; keeps 5 asks and 5 images per goal and collects unreferenced blobs (skipped with `WHOSAIDSO_BLOBS`).

`ran_at` uses Unix nanoseconds for proofs and images. Proofs bind to the instrument record bytes at HEAD; `prove` requires clean tracked records and runs all gap-covering instruments.

Loading and publication enforce 64 KiB records and 16 KiB proof tails; output is trimmed to fit serialized bytes and oversized asks are refused.

Image evidence lives in `<git common dir>/whosaidso/blobs` (shared by this clone’s worktrees; `WHOSAIDSO_BLOBS` overrides it), outside the disposable cache; interrupted writes never publish partial blobs and corrupt destinations are repaired.

`sync`, `grep-index` and `code index` are removed; queries index automatically and `code import-scip` remains.

SCIP `[precise]` answers mean every occurrence whose name the import check could derive matched the current file, and the bytes have not changed since import. An occurrence added since indexing is not listed; files with mismatches use syntactic answers.

See [usage and record formats](USAGE.md), [contributing](CONTRIBUTING.md), [changelog](CHANGELOG.md), [security](SECURITY.md), and [license](LICENSE).

- `check` verifies every proof's `instrument_sha256` against the instrument bytes at that proof's own commit (`git show <commit>:<path>`); rejected proofs cannot determine goal state.
- Blob collection protects every worktree's working tree and HEAD (including detached worktrees), blobs modified within 14 days, and live ref tips; bytes referenced by a live branch or tag deliberately stay until that ref goes.
- Publishing proofs, asks and images refuses symlinks or non-directories in `.whosaidso/` or the kind folder before running instruments or writing anything.
- `prune` also collects proofs, asks and images whose goal no longer exists at HEAD.
- Records omit `kind` and `id`: the folder supplies the kind and `<id>.json` supplies the ID; proofs, asks and images retain content hash filenames, with no old-format compatibility. Phases have no five-phase cap; image titles need only be nonempty and at most 80 characters.
- Git history keeps every committed proof, each capped at 64 KiB (16 KiB output tail); pruning never rewrites history.
