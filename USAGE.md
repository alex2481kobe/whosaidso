# Usage

`whosaidso --version` (or `-V`) prints the version. Every other command runs inside a Git worktree; `ui` can also open registered projects from elsewhere. Arguments are positional except the options shown; quote an argument containing spaces. Answers go to stdout and errors to stderr (a few validation errors print to stdout). Exit status is 0 on success and 1 on error, including invalid arguments. A successful `prove` can record a failing result, so read its output. A search with no match exits 0; `find` without words exits 1.

| Command | What it does |
| --- | --- |
| `check` | Validates records, references, instruments and media. Prints problems, or a count of phases, goals, instruments and proofs. Missing media bytes and superseded proofs are warnings. |
| `install-hooks` | Installs a `prepare-commit-msg` hook and sets `core.hooksPath` to it (replacing that setting). Every clone needs this once. |
| `start <goal-id>` | Selects the goal whose `Goal:` trailer this worktree's commits carry; `none` selects `Goal: none`. Refuses unless this clone's hooks are installed. Also claims the goal as `refs/whosaidso/claims/<goal-id>`, published to `origin` with a lease so exactly one of two racing workers wins. It refuses (exit 1) while another worktree, on any workstation, holds a live claim, naming its branch, author, age and path. Starting another goal or `none` releases the claim. A claim lapses 72 hours after its holder last ran `start` and binds nothing once the goal has landed. Without `origin` a claim binds this clone's worktrees only. It also prints a warning for each other branch with recent unlanded work on the goal. |
| `gate [<base>]` | Checks every non-merge commit in `base..HEAD` for exactly one known `Goal:` trailer or `none`. `base` defaults to local `main` and may be a local branch, local tag, `HEAD` or full commit ID (with optional `~`/`^`). Also refuses while phase, goal, instrument, proof, ask or media records are uncommitted, so evidence is never left out of a merge. |
| `ask <goal-id> <words...>` | Records the words and the current commit as an ask on the goal. |
| `show <id>` | Prints up to 40 lines for a goal, phase or instrument. A goal shows its state, check, branches, up to five newest asks, the check's latest eligible proof and the latest media. |
| `goals` | Prints phases, the goal tree, states, branches (name, author, age, local worktree path) and counts. Requires local `main`. |
| `prove <goal-id>` | Runs the goal's check instrument and records its output and exit code at HEAD. Requires clean tracked `.whosaidso/` files and a clean tree outside it, before and after the run. Needs a readable `HEAD` reflog (`core.logAllRefUpdates`) and refuses to record if `HEAD` moved during the run. |
| `prune` | Run by whoever lands work, on local `main` after merging; commit the deletions with the landing. Keeps each goal's selected check proof, the newest proof per (goal, instrument, commit) on live local refs and the newest 5 asks per goal; deletes other proofs and asks, proofs and instruments no goal requires, and unreferenced media bytes. Media records are archived, never discarded (see Media). If interrupted, run it again. |
| `media <goal-id> <file> <title>` | Stores the file's bytes locally and records them as evidence. The title is one nonempty argument of at most 80 characters. |
| `sync [<remote>]` | Shares media bytes and archive bundles through `refs/whosaidso/blobs` on the remote (default `origin`). Prints `sync: N received, M sent`. |
| `ui [--port N] [--open]` | Serves the local read-only viewer (Goals and Gallery) until killed and prints its token-bearing URL. Binds 127.0.0.1 on a free port by default. `--open` launches a browser. |
| `find <words...> [--limit N]` | Searches records, files and main history with ranking and typo correction. `kind:<kind>` restricts the type. Prints 8 matches by default and `shown K of N`. |
| `grep <text...> [--in <path-or-prefix>]...` | Case-insensitive text search over visible files (at least three characters). Repeated `--in` takes the union of path prefixes. Prints up to 20 lines of at most 160 characters, and lists skipped files (over 1 MiB, binary or not UTF-8). |
| `code defs <name>` / `code refs <name>` | Up to 20 definitions or references, each labeled precise or syntactic. Qualified names (`a::b::name`, `Type.name`) match a suffix of the enclosing scopes. |
| `code deps <path>` / `code includers <path>` | Up to 20 syntactic includes from a file, or the files that include it (matched by basename). |
| `code import-scip <file>` | Imports a SCIP index so matching answers are labeled precise. Prints document, occurrence, symbol and stale counts. |

## Records

Records are JSON files under `.whosaidso/`. All fields are required unless marked optional, and unknown fields are rejected. Phase, goal and instrument files are named `<id>.json`; an ID is 1-64 lowercase letters, digits or hyphens, unique across record kinds, and `none` is reserved. Proof, ask and media files are named by the SHA-256 of their exact bytes and are created by their commands; commit them to share them.

| Directory | Fields |
| --- | --- |
| `phases` | `title`, `order` (unique integer). |
| `goals` | `title`, `done_when` (nonempty), `check` (instrument ID), `broader` (phase or goal ID; chains must not loop). |
| `instruments` | `run` (nonempty argument list, e.g. `["cargo", "test", "--offline"]`), `answers`. Executables must resolve and no two instruments may share a `run`. |
| `proofs` | `goal`, `instrument`, `commit`, `exit_code`, `output_sha256`, `output_tail` (at most 16 KiB), `ran_at` (Unix nanoseconds). Exit code 0 passes; anything else fails. A run that cannot start records no proof. |
| `asks` | `goal`, `said` (nonempty), `ran_at`, `commit`. |
| `media` | `goal`, `shows` (title), `media_sha256`, `ext`, `byte_size`, `commit`, `ran_at`. |

A goal has one `check` instrument, and it must cover everything the goal needs. If a goal needs several things checked, make the instrument run a script that runs them all (`["sh", "-c", "..."]` or a script path). Instruments run as direct argument lists with no shell parsing.

Loading validates cross-references, shapes and blob hashes. Invalid records are left out of every answer and reported by `check`. Symlinks in record paths are rejected. Limits: 50 phases, 200 goals (100 not done), 64 KiB per record file, and 5 asks per goal kept by `prune`. `check` warns when asks or superseded proofs exceed what `prune` would keep.

## Derived state

A goal's state is never stored. It is worked out from non-merge commits with `Goal:` trailers on local `main`, local branches and fetched remote-tracking branches, and from the goal's check proofs. Commits that change only `.whosaidso/` never count as work.

A proof counts only if its commit is on local `main` and the instrument file at that commit is byte-identical to the current one. The newest such proof of the goal's check decides, by `ran_at` and then record hash.

- `open`: no eligible proof, no landed goal commit, no branch work.
- `active` / `idle`: no eligible proof or landed commit, but branch work exists whose newest commit is less than 72 hours old (`active`) or older (`idle`).
- `landed`: a goal commit is on `main` and no eligible proof exists.
- `done`: the check's newest eligible proof passed and no later `main` commit carries the goal.
- `stale`: the check passed, but a later `main` commit carries the goal.
- `failing`: the check's newest eligible proof failed.

Fetch before `goals` to see other workstations' branches. A local branch shadows a remote branch of the same name. Only reachability from local `main` counts as landed.

## Media

`media` accepts PNG, JPEG, GIF, WebP, MP4, MOV, WebM and PDF, detected from the bytes, up to 64 MiB each. Bytes live in `<git common dir>/whosaidso/blobs`, shared by the clone's worktrees (`WHOSAIDSO_BLOBS` overrides it). They are not part of the record and not in Git. `check` verifies stored bytes against each record's type, size and hash.

`prune` keeps the newest 100 media records (by `ran_at`, then hash) live. Older records and media for retired goals move, with their bytes, into monthly tar bundles `media-YYYY-MM.tar` in `<git common dir>/whosaidso/archive/` (`WHOSAIDSO_ARCHIVE` overrides it). Bundles are never deleted; `tar -tf` lists one and `tar -xf` restores a record and its bytes. Unreferenced bytes older than 14 days move to a trash folder and are deleted after 14 more days.

To share media across workstations, commit and push the records, then run `whosaidso sync` on each machine. Sync verifies every received hash before publishing bytes and pushes with a lease, retrying a rejected push up to five times. `check` stays offline; it warns when records point at bytes missing here.

Landing recipe: pull, prune, commit the deletions, push, `whosaidso sync`. If the push is rejected, pull and prune again.

## Search and code queries

`find`, `grep` and `code` index tracked files, working-tree changes and unignored untracked files on demand into a disposable SQLite cache in `<git dir>/whosaidso/cache/` (outside the working tree, one per worktree); it can be deleted at any time. Symlinks and submodules are not followed. Answer lines are capped at 200 characters (160 for `grep`).

Code answers are labeled `syntactic` (tree-sitter, for C, C++, Rust and Python) or `precise`. A `precise` answer needs an imported SCIP index whose occurrences still match the current file's text; any byte change to the file makes its answers syntactic again. Syntactic answers do not resolve receiver types or imports.

## Viewer

`whosaidso ui` prints a URL with a one-use token; open it once and keep the process running. It exchanges the token for a session cookie, serves GET requests only and writes nothing to the repository. The sidebar has Goals (states, phase progress, branches, and each goal's check, newest proof, asks and media) and Gallery (the newest 100 media, filterable; images and video uncropped, PDFs as cards). The project picker reads `$WHOSAIDSO_HOME/projects` (default `$HOME/.whosaidso/projects`), which `ui` and `install-hooks` add the current repository to.

## Contributor test runs

The invariant suite runs two recorded seeds for 200 steps by default. `WHOSAIDSO_SEEDS=all` runs every seed, and `WHOSAIDSO_LONG=1 cargo test --release --offline --test invariant` runs all seeds at 2,000 steps. CI runs `cargo test --release`, and on Linux also the fault-injection suites in debug builds.
