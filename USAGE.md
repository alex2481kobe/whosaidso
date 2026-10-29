# Usage

Run commands inside a Git worktree; `ui` can also open registered projects from elsewhere. Arguments are positional except the options shown below; quote an argument containing spaces. Answers go to stdout and errors normally to stderr. Exit status is 0 on success and 1 on error, including invalid arguments. Some validation errors are printed to stdout. A successful `prove` can record a failing result; inspect its output. No-match search results also exit 0; `find` without words exits 1.

| Command | Arguments and output |
| --- | --- |
| `check` | Validates records, references, instruments and media. Prints problems or a count of phases, goals, instruments and proofs. Missing media bytes are warnings. |
| `install-hooks` | Each clone needs its own installation. Installs a `prepare-commit-msg` hook and configures `core.hooksPath`; prints confirmation. This replaces that setting and hook. |
| `start <goal-id>` | Selects a goal for this worktree's commit hook; `none` selects `Goal: none`. Refuses unless this clone’s hooks are installed (`run whosaidso install-hooks`). Prints the selection. It also claims the goal for this worktree as `refs/whosaidso/claims/<goal-id>`, published to `origin` with a lease so exactly one racing worker wins, and refuses (exit 1) while another worktree on any workstation holds a live claim, naming its branch, author, age and path. Starting another goal or `none` releases the claim; a claim lapses 72 hours after its holder last ran `start`, and binds nothing once the goal has landed. Without `origin` the claim binds this clone's worktrees only. |
| `gate [<base>]` | Checks non-merge commits in `base..HEAD` for exactly one known `Goal:` trailer or `none`. Base defaults to local `main`; only local branches, local tags, HEAD or full commit IDs (with optional `~`/`^` ancestry) are accepted. Prints violations or a count. Also refuses while phase, goal, instrument, proof, ask or media records are uncommitted, so evidence is never left out of a merge. |
| `ask <goal-id> <words...>` | Records nonempty words and the current commit; prints the goal and record hash prefix. |
| `show <id>` | Prints up to 40 lines of details for a goal, phase or instrument. Goal details include up to five recent asks and each required instrument’s latest eligible proof (including gap coverage) and the latest media. |
| `log [--limit N]` | Prints the newest 30 commits with Goal trailers, proofs, media and asks, one line each; `--limit` takes a positive integer. Requires local `main`. Times are UTC `YYYY-MM-DD HH:MM`. |
| `goals` | Prints phases, the goal tree, states, branches and leaf counts. Requires local `main`. |
| `prove <goal-id>` | Requires clean tracked `.whosaidso/` files and a clean tree outside it; runs the goal's check and all gap-covering instruments (argument lists by direct exec), records its output and result at HEAD, and prints the result and hash prefix. |
| `prune` | The landing owner runs this on main after merging and commits the deletions with the landing; keeps each required instrument’s selected proof on local main and the newest proof per (goal, instrument, commit) on live local refs, and the newest 5 asks per goal; archives media beyond the newest 100 across the ledger; deletes abandoned or missing-commit proofs and unreferenced blobs (skipped with `WHOSAIDSO_BLOBS`). |
| `tools` | Prints instruments, commands, answers and gaps, followed by a count. |
| `sync [<remote>]` | Explicitly shares media bytes and archive bundles through `refs/whosaidso/blobs`; remote defaults to `origin`. Prints `sync: N received, M sent`. |
| `media <goal-id> <file> <title>` | Copies supported evidence bytes, detected by signature, into local blob storage and records media; prints the goal and hash prefix. Title is one nonempty argument, at most 80 characters. |
| `ui [--port N] [--open]` | Serves the local read-only browser viewer until killed and prints its token-bearing URL. Uses 127.0.0.1 and a free port by default (`--port 0` also chooses a free port). `--open` launches the browser; otherwise nothing opens. `--help` prints usage. |
| `find <words...> [--limit N]` | Searches indexed records, visible files and main history, with ranking and typo correction. Optional `kind:<kind>` restricts the type. Prints 8 matches by default, followed by `shown K of N`, or no match. `--limit` takes a positive integer. Files print just their path; commits print a 10-character SHA and a subject capped at 72 characters. |
| `grep <text...> [--in <path-or-prefix>]...` | Searches visible text case-insensitively; at least three characters are required. Repeated `--in` options select the union of repository-relative path prefixes. Without scope, `.whosaidso/` hits follow all other matches. Prints up to 20 matching lines capped at 160 characters, counts when truncated, and skipped files. Files over 1 MiB, binary files and non-UTF-8 files are skipped. |
| `code defs <name>` / `code refs <name>` | Prints up to 20 definitions or references, labeled precise or syntactic. Qualified names (`a::b::name`, `Type::name`, `Type.name`) match a suffix of the enclosing scopes. |
| `code deps <path>` | Prints up to 20 syntactic includes from the file, matched by name. |
| `code includers <path>` | Prints who includes the file, matched by basename, capped at 20 with `shown 20 of N` when truncated. |
| `code import-scip <file>` | Imports a SCIP index into the cache and prints document, occurrence, symbol and stale counts. Precise answers require unchanged imported bytes and at least one checkable occurrence whose range spells its name; every checkable occurrence, including references, must match. |

`grep-index` and `code index` are removed; queries index on demand and `code import-scip` remains.

SCIP import checks identifier names from descriptors (or named local symbols), skipping synthetic names, operators and macro expansion ranges. Files with no checkable occurrences remain stale and use syntactic answers. `WHOSAIDSO_TIMING=1` reports checked/total occurrences per document on stderr. `[precise]` means every checkable occurrence matched the current file; constructor references (methods named after their enclosing type), type references and references to names beginning with `__` are not checked. These spelling checks detect shifted or renamed occurrences when importing an old index, including copied indexes. An occurrence added since indexing is not listed. SCIP without source text or content hashes cannot detect arbitrary edits outside the checked ranges before import; after import, any byte change expires precise answers. Unspecified position encoding uses byte columns, as emitted by scip-clang; declared UTF-8, UTF-16 and UTF-32 columns are honored.

Code queries retain only the 20 answers they print plus the total count.

Search answer lines are capped at 200 characters (160 for `grep`); caps include the ending `...` when cut and count Unicode characters. Qualified precise answers use SCIP descriptors. Syntactic scopes come from tree-sitter ancestors: C++ namespaces, classes and structs; Rust inline modules, impl types and traits. Explicit call qualifiers are also preserved; syntactic references do not resolve receiver types or imports.

Search considers tracked files, working-tree changes and unignored untracked files. Indexed content can come from Git when files are not checked out. Search and code indexing never follow symlinks and skip submodules. Files over 64 MiB are not code-indexed. Code queries also update their index. `find` and `grep` update theirs automatically.

`prove` exits 1 without recording the current instrument's proof if tracked `.whosaidso/` files differ from HEAD or the tree outside `.whosaidso` is dirty before or after the run; untracked proofs, asks and media are allowed. It writes one proof per required instrument and stops at the first refusal. It requires a readable, nonempty HEAD reflog with reflog writing enabled (`core.logAllRefUpdates`); otherwise it exits 1 without a proof. It refuses to record if HEAD or its reflog changed during the run, including A to B and back. Content edits made and reverted without a commit or checkout are not detected.

## Local viewer

Run `whosaidso ui`, open the printed URL once, and keep the process running. The first request exchanges its random token for an HttpOnly, SameSite=Strict session cookie; all subsequent page, asset, API and media requests need that cookie. Requests use GET only. The viewer writes no repository or cache files. `--open` invokes `open` on macOS or `xdg-open` elsewhere.

The sidebar follows Gallery, Overview, Goals, Records and Activity. Overview and Goals derive states from local `main`, with phase progress and branch work. Goal rows, goal details and Overview's in-flight list show each branch's name, author, age rounded down to hours or days, and local worktree path when present. Remote branches retain names such as `origin/render-shadows`. Goal details show the done condition, instrument, newest proof, the proof used for state when different, asks and media. Records searches phases, goals, instruments, proofs and asks.

Activity lists commits, proofs, media and asks newest first, using the same ordering as `log`. Kind and text filters search the newest 500 entries. Commit rows show short SHA, subject, branch and author; proof rows show instrument and pass or fail; asks show their first line. Media titles open the corresponding Gallery item, including evidence older than the grid's newest 100. An empty list means no matching activity.

Gallery shows the newest 100 media by descending `ran_at`, then record hash, with type and text filters and newest/oldest order. Images and videos are never cropped; videos have controls, no autoplay and metadata preload. PDFs are document cards. Open full size, copy path and download preserve the evidence. Archived months list item counts and bundle paths below live media.

The project picker reads `$WHOSAIDSO_HOME/projects`, or `$HOME/.whosaidso/projects`, one absolute repository root per line. `ui` and `install-hooks` register the current repository once. Missing repositories and paths without `.whosaidso/` are disabled with a reason. Outside a repository, the viewer selects the first available project. Switching projects changes every screen. The registry is the only viewer write, outside the repository; a registry inside the current repository is refused. Theme preference is stored in the browser.

The page reads JSON from `/api/projects`, `/api/overview`, `/api/goals`, `/api/goal?id=<id>`, `/api/records`, `/api/record?id=<id>`, `/api/gallery` and `/api/activity`. Each project endpoint accepts `project=<absolute-root>`. `/api/media/<sha256>` serves only recorded media, using its detected content type; `download=1` downloads it. Missing or unrecorded bytes return 404. Goal, overview and activity errors, including missing local `main`, are reported without treating unknown as pass or fail.

Goal objects in `/api/goals`, `/api/goal` and `/api/overview`'s `in_flight` include `branches: [{name, author, time, worktree}]`, alongside `active_branches`. `time` is Unix seconds; `worktree` is a local path or null. `/api/activity?project=<absolute-root>&limit=N` returns `{entries: [...]}`, default 100 and capped at 500; invalid or nonpositive limits return 400. Every entry has `kind`, `goal` and `time` (Unix seconds). Commits add `sha`, `subject`, `branch`, `author`; proofs add `instrument`, `exit_code`, `commit`; media add `title`, `media_id` (the Gallery record ID); asks add `text`. Equal times retain the underlying nanosecond and hash ordering used by `log`.

For screenshots, `WHOSAIDSO=/absolute/path/to/whosaidso sh scripts/sample-project.sh DIR` prints a new `DIR/game-engine` repository with three phases, nine goals, two proofs, three asks, five PNGs and one PDF. It also creates two sibling worktrees and a second clone for fetched remote work. The script uses POSIX sh, Git and the binary, with fixed media bytes; use a fresh DIR for each run. See the [README sample commands](README.md#sample-viewer-project).

## Records

Place JSON files under `.whosaidso/`. All fields below are required unless marked optional; unknown fields are rejected. Phase, goal and instrument filenames are `<id>.json`. IDs contain 1–64 lowercase ASCII letters, digits or hyphens and are unique across record kinds; `none` is reserved and cannot name a goal. Proof, ask and media filenames are the SHA-256 of their exact JSON bytes plus `.json`.

| Directory | Fields |
| --- | --- |
| `phases` | `title`, `order` (integer, unique). |
| `goals` | `title`, `done_when` (nonempty), `check` (instrument ID), `broader` (phase or goal ID; chains must not loop). |
| `instruments` | `run` (nonempty argument list, e.g. `["cargo", "test", "--offline"]`), `answers`, optional `gaps` array. Each gap has `what`, `why` and `covered_by` (another instrument ID). Executables must resolve and argument lists must not duplicate another instrument. |
| `proofs` | `goal`, `instrument`, `commit` (40 lowercase hex digits), `exit_code` (integer), `output_sha256` (64 hex digits), `output_tail`, `ran_at` (Unix nanoseconds). Result is derived: exit code 0 passes; nonzero fails. A run that cannot start records no proof. |
| `asks` | `goal`, `said` (nonempty), `ran_at` (Unix nanoseconds), `commit` (40 lowercase hex digits). |
| `media` | `goal`, `shows` (title), `media_sha256` (64 hex digits), `ext` (detected type: `png`, `jpg`, `gif`, `webp`, `mp4`, `mov`, `webm`, `pdf`), `byte_size` (integer, exact byte count), `commit` (40 lowercase hex digits), `ran_at` (Unix nanoseconds). |

Record loading validates cross-references, record shapes and blob hashes; rejected records are excluded from search and other answers, and symlinks anywhere in record paths are rejected. `check` reports the same validation errors. Proofs, asks and media are created by their commands; commit their JSON records to share them. Media bytes are local and are not included in the record.

- `check` caps phases at 50; retire a phase by deleting its file. It allows at most 200 total goals and 100 open goals; every state except `done` counts as open. Retire a goal by deleting its file; Git history keeps it.
- Each record file is capped at 64 KiB at load and publication; oversized asks are refused and `check` reports invalid records.
- Proof output tails are capped at 16 KiB of UTF-8 bytes; `prove` retains a UTF-8 suffix that also fits the 64 KiB serialized record budget, hashes the full output, and loading rejects oversized tails.
- `check` warns about superseded proofs and goals with over 5 asks, naming `whosaidso prune`; these warnings do not fail validation. Show, ui and prune order asks by `ran_at`, then record hash, newest first; prune keeps the first five. Branch commands only add records.

If prune is interrupted, run it again.

Sync recipe: pull, prune, commit the deletions, push, `whosaidso sync`; if push is rejected, pull and prune again. Landing and proof eligibility use local main; a missing main reports `no local branch main`.

## Working across workstations

Fetch before running `whosaidso goals` to see work pushed from other workstations. State includes local branches and remote-tracking branches such as `origin/b-work`, excluding every remote's `HEAD` and `main`. A local branch shadows the same branch name on every remote, even when its tip is older. Only reachability from local `main` counts as landed; fetch alone cannot land work. Branch work is active for less than 72 hours since its newest unmatched goal commit, then idle, using the same work and cherry-pick rules locally and remotely.

`goals` keeps its bracketed branch names and adds `name (author, 3h ago, /path/of/worktree)` details, with ages rounded down to whole hours or days. Each command uses one clock snapshot. The author and Unix time come from the newest matching work commit's author name and commit time. A path appears only for a local branch checked out in this clone's worktrees; other workstations' paths are unknown.

`whosaidso start <goal>` still selects the goal for this worktree. For each other branch with recent unmatched work on that goal, it also prints `warning: <goal> is active on <branch> (author, age); coordinate before working on it` and exits 0. The current branch and idle branches do not warn. Claims are derived from Git; selecting a goal creates no lock or shared claim record.

`whosaidso log [--limit N]` merges commits with Goal trailers on local main and the same selected work branches with validated proof, media and ask records. It prints newest first, default 30, as `<time> <kind> <goal> <text>`, with UTC times `YYYY-MM-DD HH:MM`. Commit lines include short SHA, subject, branch and author; a SHA seen on multiple branches appears once, preferring main, then branch name order. Proofs include instrument, exit code and commit; media shows its title and asks show their first line. Record times retain nanosecond ordering; equal times sort by descending record hash or commit SHA. Commit proof, ask and media JSON files to share their activity across clones.

## Derived state

State uses non-merge commits and their `Goal:` trailers in local `main`, local branches and fetched remote-tracking work branches; commits changing only `.whosaidso/` never count as work or make a goal stale, regardless of trailers. A branch is active when its newest unmatched goal commit is less than 72 hours old; otherwise it is idle. Cherry-picked commits already on main do not count as branch work. Only proofs whose instrument bytes at their own commit match the current instrument record bytes count; the newest pass or fail proof for the goal's check whose commit is an ancestor of local `main` is selected by `ran_at`, then record hash. Unlanded proofs do not determine state.

- `open`: no eligible proof, landed goal commit or branch work.
- `active`: no eligible proof or landed goal commit, with recent branch work.
- `idle`: as above, but all matching branch work is at least 72 hours old.
- `landed`: a goal commit is on main without an eligible proof, or a required gap lacks a current passing covering proof.
- `failing`: the selected check or covering proof fails.
- `done`: the selected check and all gap-covering instruments have current passing proofs.
- `stale`: the check passed, but a later main commit has the goal trailer, provided gap coverage does not reduce the state to landed.

Proof states take precedence over branch activity. Gap coverage can override a passing or stale check; a failing check remains failing.

## Local cache

`.whosaidso/cache/` contains the shared SQLite index; it is Git-ignored and disposable. Media evidence lives in `<git common dir>/whosaidso/blobs`, shared by worktrees of this clone (override with `WHOSAIDSO_BLOBS`); writes publish by rename and verify or repair existing destinations.

- Instrument hashes are derived from the instrument file at each proof commit; proofs store no instrument hash, and byte changes expire eligibility.
- Media publication holds a shared lock on the blob store’s `.lock` until its record is written; collection holds an exclusive lock through verification and deletes every interrupted `.tmp-*` blob write.
- `prune` refuses before changing anything when tracked `.whosaidso/` files differ from HEAD or unignored untracked ledger files other than publication `.tmp-*` files exist.
- Blob collection protects every worktree's working tree and HEAD (including detached worktrees), blobs modified within 14 days, and local branch and tag tips; bytes referenced by a live branch or tag deliberately stay until that ref goes.
- Publishing proofs, asks and media refuses symlinks or non-directories in `.whosaidso/` or the kind folder before running instruments or writing anything.
- `prune` also collects proofs and asks whose goal no longer exists at HEAD and archives media for retired goals, plus proofs and instruments no goal at HEAD requires through its check or transitive gaps, including deleted instruments.
- Records omit `kind` and `id`: the folder supplies the kind and `<id>.json` supplies the ID; proofs, asks and media retain content hash filenames, with no old-format compatibility. Phases are capped at 50 by `check`; media titles need only be nonempty and at most 80 characters.
- Git history keeps every committed proof, each capped at 64 KiB (16 KiB output tail); pruning never rewrites history.

- Prune makes recoverable record removals, verifies identical `goals` output and current blob references, and undoes removals on verification failure: "refs changed during prune; nothing was removed; run it again".
- Instruments execute `run` argument lists directly with no shell parsing; use `["sh", "-c", "..."]` for a shell and its real exit code. Spawn errors publish no proof; exit 127 after launch records failure.
- Proof, ask and media record publication holds the blob store’s shared lock; prune holds it exclusively and collects interrupted record and blob `.tmp-*` files.
- Media is capped at 64 MiB per file and the live blob store (including trash) at 4 GiB; a full store refuses publication and names prune. Asks and media share newest `ran_at`, then record hash ordering in show, ui and prune.
- Unreferenced media bytes older than 14 days have their mtime refreshed before moving by rename to `<blob store>/trash/`; readers also use trash, and prune restores referenced trash and permanently deletes only trash older than 14 days after rechecking references.
- The remaining concurrent-ref window matches git gc’s prune expiry; restoring an old commit after expiry can show missing bytes.
- The commit index under the cache grows with Git history, is derived, and can be deleted at any time.

## Evidence media and archives

Media bytes live in `<git common dir>/whosaidso/blobs`, shared by this clone's worktrees (`WHOSAIDSO_BLOBS` overrides it), outside the disposable cache. Interrupted writes never publish partial blobs, and a corrupt destination is replaced.

`whosaidso media <goal> <file> <title>` accepts PNG, JPEG, GIF, WebP, MP4, MOV, WebM and PDF. Detection uses signatures and headers, never filename extensions: ISO-BMFF `ftyp` brands distinguish MP4/MOV, and WebM uses its EBML header. JPEG is stored as `jpg`. Unsupported bytes are refused with the supported types. `check` validates the stored bytes against the record's type, exact byte size and SHA-256. Titles are nonempty, at most 80 characters. The old image command and record kind have no alias or migration path.

Media is never discarded by prune. It keeps the newest 100 media records across the ledger (descending `ran_at`, then descending record hash). Each older record and its original bytes move into a plain ustar monthly bundle, `media-YYYY-MM.tar`, using the record's UTC month. Media for retired goals is also archived. Missing or invalid bytes cannot be archived; their records remain live until repaired. Archived copies never count toward the live quota and are never collected. Other live refs and worktrees still protect live blob copies under the usual collection rules.

The archive directory defaults to `<git common dir>/whosaidso/archive/`. Set `WHOSAIDSO_ARCHIVE` to a durable directory such as a backup drive. Each bundle stores `<record-hash>.json` beside `<record-hash>.<ext>`. `tar -tf /path/media-YYYY-MM.tar` lists it; `tar -xf /path/media-YYYY-MM.tar` restores both files in the current directory. To use restored evidence in the ledger, copy the JSON to `.whosaidso/media/` and the bytes to the blob store as `<media_sha256>.<ext>` using the JSON fields. There is no extract command.

Prune writes a temporary bundle, fsyncs it, renames it and syncs the directory before removing any live media records. Interrupted writes publish no partial bundle. Failure restores live record removals; a completed archive may safely retain extra copies after a failed verification. Repeating prune preserves existing monthly bundle entries.

## Media across workstations

Commit and push media JSON records with the code, then run `whosaidso sync` (or `whosaidso sync <remote>` for another configured remote). On another workstation, pull the records and run sync to receive the original bytes. Attaching media stays offline; sync runs only when requested.

Sync fetches `refs/whosaidso/blobs` into `refs/whosaidso/remote-blobs`, verifies each received media SHA-256 before publishing bytes locally, unions the fetched tree with local blobs and archives, and pushes with a lease. A missing remote ref starts an empty store. Rejected pushes fetch and retry, up to five attempts. Received and sent counts are files, including bundles; repeated unchanged syncs report zero for both.

The separate ref holds `blobs/<sha256>.<ext>` and `archive/<bundle>.tar`, outside main's history. Its union never drops entries; prune's monthly archives travel too, and earlier blob and bundle versions remain in ref history. Independently extended monthly bundles merge their entries. Git hosting must allow fetching and pushing this custom ref.

`check` stays offline. When either local transport ref lists missing media, it adds `warning: N media missing here; run whosaidso sync`. An ordinary pull does not fetch custom refs: before the first sync, existing per-media missing-byte warnings still apply; fetching the transport ref manually also makes availability known to check. The stores and their overrides remain per clone, shared by worktrees; both overridden stores participate in explicit sync.

## Contributor test runs

The default invariant suite runs the first two recorded seeds for 200 steps; `WHOSAIDSO_SEEDS=all` runs every recorded seed, and Linux CI does so. Before release, run `WHOSAIDSO_LONG=1 cargo test --release --offline --test invariant` to run every recorded seed at 2,000 steps. CI tests in release, and on Linux also runs the fault-injection suites in debug, where their hooks are compiled. Lean exercises 120 real proofs and 60 asks while checking the same retained record, byte and output-tail bounds.
