# Usage

`whosaidso --version` prints the version. Every other command runs inside a Git worktree (`ui` can also open registered projects). Arguments are positional except the options shown; quote arguments containing spaces. Exit status is 0 on success and 1 on error. A successful `prove` can record a failing result, so read its output. A search with no match exits 0.

## Commands

| Command | Does |
| --- | --- |
| `check` | Validates records, references, checks and media. Prints problems, or counts. Missing media bytes and superseded proofs are warnings. |
| `install-hooks` | Installs a `prepare-commit-msg` hook and sets `core.hooksPath`. Once per clone. |
| `start <goal-id>` | Selects the goal whose `Goal:` trailer this worktree's commits carry (`none` selects `Goal: none`) and claims it through `refs/whosaidso/claims/<goal-id>` on `origin`. Refuses, naming branch, author, age and path, while another worktree on any machine holds a live claim. A claim lapses 72 hours after the holder last ran `start` and binds nothing once the goal has landed. Starting another goal releases the claim. Without `origin`, a claim binds only this clone's worktrees. |
| `gate [<base>]` | Checks every non-merge commit in `base..HEAD` for exactly one known `Goal:` trailer or `none`. `base` defaults to local `main`; it may be a local branch, local tag, `HEAD` or full commit ID. Refuses while records are uncommitted. |
| `ask <goal-id> <words...>` | Records a question on the goal. |
| `show <id>` | Prints up to 40 lines for a goal, phase or instrument. A goal shows its state, check, branches, the five newest asks, latest proof and latest media. |
| `goals` | Prints phases, goals, states, branches (name, author, age, worktree path) and counts. Needs local `main`. |
| `prove <goal-id>` | Runs the goal's check and records its output and exit code at `HEAD`. Needs a clean tree before and after, and a readable `HEAD` reflog (`core.logAllRefUpdates`). Records nothing if `HEAD` moved during the run. |
| `prune` | Run by whoever lands work, on local `main`; commit the deletions with the landing. Keeps each goal's selected proof, the newest proof per goal, check and commit on live local refs, and the newest 5 asks per goal. Deletes other proofs and asks, unused instruments and proofs, and unreferenced media bytes. Older media is archived (see Media). If interrupted, run it again. |
| `media <goal-id> <file> <title>` | Stores the file and records it as evidence. Title: one argument, 1-80 characters. |
| `sync [<remote>]` | Shares media bytes and archive bundles through `refs/whosaidso/blobs` on the remote (default `origin`). |
| `ui [--port N] [--open]` | Serves the read-only viewer (Goals and Gallery) on 127.0.0.1 and prints its URL. `--open` launches a browser. |

## Search

| Command | Does |
| --- | --- |
| `find <words...> [--limit N]` | Searches records, files and main history, ranked, with typo correction. `kind:<kind>` restricts the type. Prints 8 matches by default and `shown K of N`. |
| `grep <text...> [--in <path>]...` | Case-insensitive text search over visible files (3+ characters). Repeated `--in` takes the union of path prefixes. Prints up to 20 lines of at most 160 characters and lists skipped files (over 1 MiB, binary or not UTF-8). |
| `code defs <name>` / `code refs <name>` | Up to 20 definitions or references. Qualified names (`a::b::name`, `Type.name`) match a suffix of the enclosing scopes. |
| `code deps <path>` / `code includers <path>` | Up to 20 includes from a file, or the files that include it (matched by basename). |
| `code import-scip <file>` | Imports a SCIP index. Prints document, occurrence, symbol and stale counts. |

Search indexes tracked files, working-tree changes and unignored untracked files on demand into a disposable SQLite cache in `<git dir>/whosaidso/cache/`, one per worktree; delete it any time. Symlinks and submodules are not followed. Code answers are `syntactic` (tree-sitter: C, C++, Rust, Python; no type or import resolution) or `precise`. A `precise` answer needs an imported SCIP index whose occurrences still match the file's current text.

## Records

JSON files under `.whosaidso/`. All fields are required unless marked optional; unknown fields are rejected. Phases, goals and instruments are named `<id>.json`; an ID is 1-64 lowercase letters, digits or hyphens, unique across record kinds, and `none` is reserved. Proofs, asks and media are named by the SHA-256 of their bytes and are created by their commands; commit them to share them.

| Directory | Fields |
| --- | --- |
| `phases` | `title`, `order` (unique integer). |
| `goals` | `title`, `done_when` (nonempty), `check` (instrument ID), `broader` (phase or goal ID; chains must not loop). |
| `instruments` | `run` (nonempty argument list, e.g. `["cargo", "test"]`), `answers`. The executable must resolve; no two instruments may share a `run`. |
| `proofs` | `goal`, `instrument`, `commit`, `exit_code`, `output_sha256`, `output_tail` (at most 16 KiB), `ran_at` (Unix nanoseconds). Exit code 0 passes. |
| `asks` | `goal`, `said` (nonempty), `ran_at`, `commit`. |
| `media` | `goal`, `shows` (title), `media_sha256`, `ext`, `byte_size`, `commit`, `ran_at`. |

A goal has one check, and it must cover everything the goal needs. To check several things, make `run` a script (`["sh", "-c", "..."]` or a path). `run` is a direct argument list with no shell parsing.

Invalid records are left out of every answer and reported by `check`. Limits: 50 phases, 200 goals (100 not done), 64 KiB per record file.

## States

State is never stored. It comes from non-merge commits with `Goal:` trailers on local `main`, local branches and fetched remote branches, and from the goal's proofs. Commits that change only `.whosaidso/` do not count as work. A local branch shadows a remote one of the same name. Only reachability from local `main` counts as landed.

A proof counts only if its commit is on local `main` and the instrument file at that commit is byte-identical to the current one. The newest such proof decides, by `ran_at` and then record hash.

- `open`: no proof, no landed commit, no branch work.
- `active` / `idle`: branch work exists and its newest commit is under 72 hours old (`active`) or older (`idle`).
- `landed`: a goal commit is on `main`, no proof.
- `done`: the newest proof passed and no later `main` commit carries the goal.
- `stale`: the proof passed, but a later `main` commit carries the goal.
- `failing`: the newest proof failed.

Fetch before `goals` to see other machines' branches.

## Media

`media` accepts PNG, JPEG, GIF, WebP, MP4, MOV, WebM and PDF (detected from the bytes), up to 64 MiB. Bytes live in `<git common dir>/whosaidso/blobs`, shared by the clone's worktrees (`WHOSAIDSO_BLOBS` overrides), not in Git. `check` verifies them against each record.

`prune` keeps the newest 100 media records live. Older ones, and media of retired goals, move with their bytes into monthly bundles `media-YYYY-MM.tar` in `<git common dir>/whosaidso/archive/` (`WHOSAIDSO_ARCHIVE` overrides). Bundles are never deleted; `tar -tf` lists one and `tar -xf` restores from it. Unreferenced bytes older than 14 days go to a trash folder and are deleted 14 days later.

To share media: commit and push the records, then run `whosaidso sync` on each machine. Sync verifies every hash before publishing and retries a rejected push up to five times. `check` stays offline and warns about bytes missing here.

Landing: pull, prune, commit the deletions, push, `whosaidso sync`. If the push is rejected, pull and prune again.

## Viewer

`whosaidso ui` prints a URL with a one-use token; open it once and keep the process running. It serves GET requests only and writes nothing to the repository. Goals shows phases, goal states, branches, and each goal's proof, asks and media. Gallery shows the newest 100 media. The project picker reads `$WHOSAIDSO_HOME/projects` (default `$HOME/.whosaidso/projects`), which `ui` and `install-hooks` add the current repository to.

## Tests

Run the default end-to-end suite in debug mode with `cargo test --locked`. Its fixed fault seeds exercise every prune fault hook and verify that the hook ran before checking recovery.

When claim or landing code changes, run the named manual fault sweep:

```sh
WHOSAIDSO_SEEDS=all cargo test --locked --test invariant manual_all_recorded_fault_seeds -- --ignored --nocapture
```

Large indexing and result-memory fixtures are separate manual checks: `cargo test --locked --test search_scale -- --ignored`. Large output and media-boundary fixtures use `cargo test --locked --test output_scale -- --ignored`. These manual runs are outside CI.
