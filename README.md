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
mkdir -p .whosaidso/phases .whosaidso/goals .whosaidso/instruments
printf '%s\n' '{"title":"First phase","order":1}' > .whosaidso/phases/p1.json
printf '%s\n' '{"run":["cargo","test"],"answers":"Tests pass"}' > .whosaidso/instruments/test.json
printf '%s\n' '{"title":"First goal","done_when":"Tests pass","check":"test","broader":"p1"}' > .whosaidso/goals/first-goal.json
whosaidso check
whosaidso install-hooks
git add .whosaidso
git commit -m 'Add records'
```

Each clone needs its own `whosaidso install-hooks`, even when the hook file is tracked. Select a goal with `whosaidso start first-goal`; later commits will carry its `Goal:` trailer. Progress uses local `main` and local branches. After committing work, run:

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

Whoever lands work runs `whosaidso prune` on main after merging; it keeps each required instrument’s selected proof on local main and the newest proof per (goal, instrument, commit) on live local refs; deletes abandoned or missing-commit proofs; keeps 5 asks per goal and the newest 100 media records across the ledger, archives older media and collects unreferenced blobs (skipped with `WHOSAIDSO_BLOBS`).

`ran_at` uses Unix nanoseconds for proofs, asks and media. Proofs bind to the instrument record bytes at HEAD; `prove` requires clean tracked records and runs all gap-covering instruments.

Loading and publication enforce 64 KiB records and 16 KiB proof tails; output is trimmed to fit serialized bytes and oversized asks are refused.

Evidence media lives in `<git common dir>/whosaidso/blobs` (shared by this clone’s worktrees; `WHOSAIDSO_BLOBS` overrides it), outside the disposable cache; interrupted writes never publish partial blobs and corrupt destinations are repaired.

`sync`, `grep-index` and `code index` are removed; queries index automatically and `code import-scip` remains.

SCIP `[precise]` answers mean every occurrence whose name the import check could derive matched the current file, and the bytes have not changed since import. An occurrence added since indexing is not listed; files with mismatches use syntactic answers.

Sync recipe: pull, prune, commit the deletions, push; if push is rejected, pull and prune again.

`start` refuses without installed hooks; empty `find` refuses; media types are detected by signature. Code queries retain only 20 answer lines and count the rest.

See [usage and record formats](USAGE.md), [contributing](CONTRIBUTING.md), [changelog](CHANGELOG.md), [security](SECURITY.md), and [license](LICENSE).

- Proof eligibility compares the current instrument bytes with the instrument at the proof commit; proofs store no instrument hash.
- Blob collection protects every worktree's working tree and HEAD (including detached worktrees), blobs modified within 14 days, and local branch and tag tips; bytes referenced by a live branch or tag deliberately stay until that ref goes.
- Publishing proofs, asks and media refuses symlinks or non-directories in `.whosaidso/` or the kind folder before running instruments or writing anything.
- `prune` also collects proofs and asks whose goal no longer exists at HEAD and archives media for retired goals.
- Records omit `kind` and `id`: the folder supplies the kind and `<id>.json` supplies the ID; proofs, asks and media retain content hash filenames, with no old-format compatibility. `check` caps phases at 50; deleting a phase retires it. Media titles need only be nonempty and at most 80 characters.
- Git history keeps every committed proof, each capped at 64 KiB (16 KiB output tail); pruning never rewrites history.

Record evidence with `whosaidso media <goal> <file> <title>`. Supported signatures are PNG, JPEG, GIF, WebP, MP4/MOV (`ftyp`), WebM (EBML) and PDF, regardless of extension. Each file is at most 64 MiB; the live store, including trash, is at most 4 GiB. Records in `.whosaidso/media/` keep the title, detected type, exact byte size and SHA-256. The old image command and kind are removed.

`view` shows images inline, videos with controls (no autoplay, preload metadata), and PDFs as links, newest time then record hash first. Media is never deleted by prune: older records and their bytes go to monthly plain ustar bundles under `<git common dir>/whosaidso/archive/`, or `WHOSAIDSO_ARCHIVE` on durable storage. Archives are never collected and do not count toward the live quota. Use `tar -tf` to list and `tar -xf` to restore them; see USAGE for the file layout.

Contributors run `cargo test --locked` for the default suite (200 invariant steps per recorded seed). Before release run `WHOSAIDSO_LONG=1 cargo test --release --offline --test invariant` for every recorded seed at 2,000 steps.
