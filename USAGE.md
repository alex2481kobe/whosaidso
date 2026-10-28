# Usage

Run commands inside a Git worktree. Arguments are positional except the search options shown below; quote an argument containing spaces. Answers go to stdout and errors normally to stderr. Exit status is 0 on success and 1 on error, including invalid arguments. Some validation errors are printed to stdout. A successful `prove` can record a failing result; inspect its output. No-match search results also exit 0.

| Command | Arguments and output |
| --- | --- |
| `check` | Validates records, references, instruments and images. Prints problems or a count of phases, goals, instruments and proofs. Missing image bytes are warnings. |
| `install-hooks` | Installs a `prepare-commit-msg` hook and configures `core.hooksPath`; prints confirmation. This replaces that setting and hook. |
| `start <goal-id>` | Selects a goal for this worktree's commit hook; `none` selects `Goal: none`. Prints the selection. |
| `gate [<base>]` | Checks non-merge commits in `base..HEAD` for exactly one known `Goal:` trailer or `none`. Base defaults to `origin/main`. Prints violations or a count. |
| `ask <goal-id> <words...>` | Records nonempty words and the current commit; prints the goal and record hash prefix. |
| `show <id>` | Prints up to 40 lines of details for a goal, phase or instrument. Goal details include up to five recent asks and the latest eligible proof and image. |
| `goals` | Prints phases, the goal tree, states, branches and leaf counts. Requires `origin/main`. |
| `prove <goal-id>` | Requires clean tracked `.whosaidso/` files and a clean tree outside it; runs the goal's check and all gap-covering instruments (argument lists by direct exec), records its output and result at HEAD, and prints the result and hash prefix. |
| `prune` | The landing owner runs this on main after merging and commits the deletions with the landing; keeps proofs selected at HEAD and origin/main (including time ties), unlanded proofs reachable from any ref, and the newest 5 images and 5 asks per goal; deletes abandoned or missing-commit proofs and unreferenced blobs (skipped with `WHOSAIDSO_BLOBS`). |
| `tools` | Prints instruments, commands, answers and gaps, followed by a count. |
| `show-image <goal-id> <image> <title>` | Copies PNG, JPG, JPEG, WebP or GIF bytes into local blob storage and records an image; prints the goal and hash prefix. Title is one nonempty argument, at most 80 characters. |
| `view` | Writes local gallery and progress HTML and prints the gallery path. Requires `origin/main`. |
| `find <words...> [--limit N]` | Searches indexed records, visible files and main history, with ranking and typo correction. Optional `kind:<kind>` restricts the type. Prints 8 matches by default, followed by `shown K of N`, or no match. `--limit` takes a positive integer. Files print just their path; commits print a 10-character SHA and a subject capped at 72 characters. |
| `grep <text...> [--in <path-or-prefix>]...` | Searches visible text case-insensitively; at least three characters are required. Repeated `--in` options select the union of repository-relative path prefixes. Without scope, `.whosaidso/` hits follow all other matches. Prints up to 20 matching lines capped at 160 characters, counts when truncated, and skipped files. Files over 1 MiB, binary files and non-UTF-8 files are skipped. |
| `code defs <name>` / `code refs <name>` | Prints up to 20 definitions or references, labeled precise or syntactic. Qualified names (`a::b::name`, `Type::name`, `Type.name`) match a suffix of the enclosing scopes. |
| `code deps <path>` | Prints up to 20 syntactic includes from the file, matched by name. |
| `code includers <path>` | Prints who includes the file, matched by basename, capped at 20 with `shown 20 of N` when truncated. |
| `code import-scip <file>` | Imports a SCIP index into the cache and prints document, occurrence, symbol and stale counts. Precise answers require unchanged imported bytes and at least one checkable occurrence whose range spells its name; every checkable occurrence, including references, must match. |

`sync`, `grep-index` and `code index` are removed; queries index on demand and `code import-scip` remains.

SCIP import checks identifier names from descriptors (or named local symbols), skipping synthetic names, operators and macro expansion ranges. Files with no checkable occurrences remain stale and use syntactic answers. `WHOSAIDSO_TIMING=1` reports checked/total occurrences per document on stderr. `[precise]` means every checkable occurrence matched the current file; constructor references (methods named after their enclosing type), type references and references to names beginning with `__` are not checked. These spelling checks detect shifted or renamed occurrences when importing an old index, including copied indexes. An occurrence added since indexing is not listed. SCIP without source text or content hashes cannot detect arbitrary edits outside the checked ranges before import; after import, any byte change expires precise answers. Unspecified position encoding uses byte columns, as emitted by scip-clang; declared UTF-8, UTF-16 and UTF-32 columns are honored.

Search answer lines are capped at 200 characters (160 for `grep`); caps include the ending `...` when cut and count Unicode characters. Qualified precise answers use SCIP descriptors. Syntactic scopes come from tree-sitter ancestors: C++ namespaces, classes and structs; Rust inline modules, impl types and traits. Explicit call qualifiers are also preserved; syntactic references do not resolve receiver types or imports.

Search considers tracked files, working-tree changes and unignored untracked files. Indexed content can come from Git when files are not checked out. Search and code indexing never follow symlinks and skip submodules. Files over 64 MiB are not code-indexed. Code queries also update their index. `find` and `grep` update theirs automatically.

`prove` exits 1 without recording the current instrument's proof if tracked `.whosaidso/` files differ from HEAD or the tree outside `.whosaidso` is dirty before or after the run; untracked proofs, asks and images are allowed. It writes one proof per required instrument and stops at the first refusal. It requires a readable, nonempty HEAD reflog with reflog writing enabled (`core.logAllRefUpdates`); otherwise it exits 1 without a proof. It refuses to record if HEAD or its reflog changed during the run, including A to B and back. Content edits made and reverted without a commit or checkout are not detected.

## Records

Place JSON files under `.whosaidso/`. All fields below are required unless marked optional; unknown fields are rejected. Phase, goal and instrument filenames are `<id>.json`. IDs contain 1–64 lowercase ASCII letters, digits or hyphens and are unique across record kinds; `none` is reserved and cannot name a goal. Proof, ask and image filenames are the SHA-256 of their exact JSON bytes plus `.json`.

| Directory | Fields |
| --- | --- |
| `phases` | `title`, `order` (integer, unique). |
| `goals` | `title`, `done_when` (nonempty), `check` (instrument ID), `broader` (phase or goal ID; chains must not loop). |
| `instruments` | `run` (nonempty argument list, e.g. `["cargo", "test", "--offline"]`), `answers`, optional `gaps` array. Each gap has `what`, `why` and `covered_by` (another instrument ID). Executables must resolve and argument lists must not duplicate another instrument. |
| `proofs` | `goal`, `instrument`, `commit` (40 lowercase hex digits), `exit_code` (integer), `output_sha256` (64 hex digits), `output_tail`, `ran_at` (Unix nanoseconds). Result is derived: exit code 0 passes; nonzero fails. A run that cannot start records no proof. |
| `asks` | `goal`, `said` (nonempty), `ran_at` (Unix nanoseconds), `commit` (40 lowercase hex digits). |
| `images` | `goal`, `shows` (title), `media_sha256` (64 hex digits), `ext` (`png`, `jpg`, `jpeg`, `webp`, `gif`), `commit` (40 lowercase hex digits), `ran_at` (Unix nanoseconds). |

Record loading validates cross-references, record shapes and blob hashes; rejected records are excluded from search and other answers, and symlinks anywhere in record paths are rejected. `check` reports the same validation errors. Proofs, asks and images are created by their commands; commit their JSON records to share them. Image bytes are local and are not included in the record.

- `check` allows at most 200 total goals and 100 open goals; every state except `done` counts as open. Retire a goal by deleting its file; Git history keeps it.
- Each record file is capped at 64 KiB at load and publication; oversized asks are refused and `check` reports invalid records.
- Proof output tails are capped at 16 KiB of UTF-8 bytes; `prove` retains a UTF-8 suffix that also fits the 64 KiB serialized record budget, hashes the full output, and loading rejects oversized tails.
- `check` warns about superseded proofs and goals with over 5 asks, naming `whosaidso prune`; these warnings do not fail validation. Show, view and prune order asks by `ran_at`, then record hash, newest first; prune keeps the first five. Branch commands only add records.

## Derived state

State uses non-merge commits and their `Goal:` trailers in `origin/main` and remote branches; commits changing only `.whosaidso/` never count as work or make a goal stale, regardless of trailers. A branch is active when its newest unmatched goal commit is less than 72 hours old; otherwise it is idle. Cherry-picked commits already on main do not count as branch work. Only proofs whose instrument bytes at their own commit match the current instrument record bytes count; the newest pass or fail proof for the goal's check whose commit is an ancestor of `origin/main` is selected by `ran_at`, then record hash. Unlanded proofs do not determine state.

- `open`: no eligible proof, landed goal commit or remote branch work.
- `active`: no eligible proof or landed goal commit, with recent remote branch work.
- `idle`: as above, but all matching remote branch work is at least 72 hours old.
- `landed`: a goal commit is on main without an eligible proof, or a required gap lacks a current passing covering proof.
- `failing`: the selected check or covering proof fails.
- `done`: the selected check and all gap-covering instruments have current passing proofs.
- `stale`: the check passed, but a later main commit has the goal trailer, provided gap coverage does not reduce the state to landed.

Proof states take precedence over branch activity. Gap coverage can override a passing or stale check; a failing check remains failing.

## Local cache

`.whosaidso/cache/` contains the shared SQLite index and generated HTML; it is Git-ignored and disposable. Image evidence lives in `<git common dir>/whosaidso/blobs`, shared by worktrees of this clone (override with `WHOSAIDSO_BLOBS`); writes publish by rename and verify or repair existing destinations.

- Instrument hashes are derived from the instrument file at each proof commit; proofs store no instrument hash, and byte changes expire eligibility.
- Image publication holds a shared lock on the blob store’s `.lock` until its record is written; collection holds an exclusive lock through verification and deletes every interrupted `.tmp-*` blob write.
- `prune` refuses before changing anything when tracked `.whosaidso/` files differ from HEAD or unignored untracked ledger files other than publication `.tmp-*` files exist.
- Blob collection protects every worktree's working tree and HEAD (including detached worktrees), blobs modified within 14 days, and live ref tips; bytes referenced by a live branch or tag deliberately stay until that ref goes.
- Publishing proofs, asks and images refuses symlinks or non-directories in `.whosaidso/` or the kind folder before running instruments or writing anything.
- `prune` also collects proofs, asks and images whose goal no longer exists at HEAD, plus proofs and instruments no goal at HEAD requires through its check or transitive gaps, including deleted instruments.
- Records omit `kind` and `id`: the folder supplies the kind and `<id>.json` supplies the ID; proofs, asks and images retain content hash filenames, with no old-format compatibility. Phases have no five-phase cap; image titles need only be nonempty and at most 80 characters.
- Git history keeps every committed proof, each capped at 64 KiB (16 KiB output tail); pruning never rewrites history.

- Prune makes recoverable record removals, verifies identical `goals` output and current blob references, and undoes removals on verification failure: "refs changed during prune; nothing was removed; run it again".
- Instruments execute `run` argument lists directly with no shell parsing; use `["sh", "-c", "..."]` for a shell and its real exit code. Spawn errors publish no proof; exit 127 after launch records failure.
- Proof, ask and image record publication holds the blob store’s shared lock; prune holds it exclusively and collects interrupted record and blob `.tmp-*` files.
- Images are capped at 8 MiB each and the blob store at 1 GiB; a full store refuses publication and names prune. Asks and images share newest `ran_at`, then record hash ordering in show, view and prune.
- Unreferenced image bytes older than 14 days move by rename to `<blob store>/trash/`; readers also use trash, and prune restores referenced trash and permanently deletes only trash older than 14 days after rechecking references.
- The remaining concurrent-ref window matches git gc’s prune expiry; restoring an old commit after expiry can show missing bytes.
- The commit index under the cache grows with Git history, is derived, and can be deleted at any time.
