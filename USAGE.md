# Usage

WhoSaidSo keeps goals and evidence in Git. Commands run inside a Git worktree, except `help`, `--version` and the project picker. Exit status is 0 on success and 1 on error. A successful `prove` can record a failed check: read its output.

## Quick start

```sh
whosaidso install-hooks
git switch -c feature
whosaidso start <goal>
# Commit the work with the installed trailer hook.
whosaidso land
```

A check that already passes on main needs only `land --reason "<why>"`. There is no separate evidence-commit or merge step. See [README.md](README.md#quick-start) for a complete example.

## Goals

Create `.whosaidso/goals/<id>.json`:

```json
{"title":"Export a report","done_when":"A report is produced","parent":null,"order":1,"after":[],"check":["sh","-c","test -f report.pdf"]}
```

`title`, `done_when`, `order` and `after` are required. `parent` names another goal, or is null for a milestone. `check` is optional and contains a direct argument list, with no shell parsing. Use a short check of real behavior, such as one existing end-to-end test or a benchmark. Goals never require a new test or a full suite. IDs use 1–64 lowercase letters, digits and hyphens; `none` is reserved. Records are limited to 64 KiB. Unknown fields, missing references and cycles are reported together. Invalid records remain visible as `broken` and count in progress.

`.whosaidso/config.json` is optional. The default branch name comes from `origin/HEAD`, then `config.main`, then `main`. The destination supplies landed history. Fetch before viewing other machines' work.

| Command | Behavior |
| --- | --- |
| `--version` | Prints the installed version. |
| `help` | Lists commands and the testing rule. Works outside Git. |
| `check` | Checks record structure, references and media. Reports every problem. |
| `goals` | Shows milestones, nested goals, progress, broken records and branch details. |
| `show <id>` | Shows one goal, its check, state, children and selected evidence. |
| `install-hooks` | Installs commit trailers and the pre-push landing guard in this clone. |
| `next` | Shows the next available goal. |
| `assign <goal> <target>` | Assigns work to `agent:name` or `branch:name`. |
| `reopen <milestone> <destination-revision>` | Reopens a folded milestone against the destination bundle revision. |
| `evidence add <goal> <file> <shows>` | Attaches a file as evidence. |
| `start <goal>` | Claims a goal and adds its `Goal:` trailer to subsequent commits. `none` releases the selection. |
| `land [--reason <why>] [--override <name>]` | Fetches and merges destination main, checks the candidate, records proof and lossless folds in a receipt, publishes referenced media, and pushes by fast-forward. Retries re-merge and re-test. |
| `gate --ci <base>..<tip>` | Reruns checks on the queued tip for goals named by work commits or landing receipts, plus goals that became done since the base. |
| `gate [base]` | Checks non-merge commit trailers, receipt scope and note reconciliation, and refuses uncommitted records. Defaults to the destination branch. |
| `prove <goal> [--reason <why>]` | Optionally runs the committed check and keeps its commit, exit code, output hash and output tail. Commit the resulting proof record. |
| `media <goal> <file> <title>` | Attaches media, addressed by its content hash. |
| `prune` | Collects superseded evidence and archives older media. Run on the destination branch with committed records. Interrupted collection can resume. |
| `sync [remote]` | Shares media and archives through Git refs; default remote is `origin`. |
| `ui [--port N] [--open]` | Opens the read-only Goals, Flows and Gallery viewer on 127.0.0.1. |

`land` checks the exact merged candidate. A rejected push makes it merge and check again. Checks run in a disposable Git worktree restored to the tested commit. Absolute executable paths and separate absolute arguments under any worktree in this clone, including `--option=/path` arguments, are mapped to that same relative location in the scratch worktree. Paths inside shell command strings are not rewritten. Checks must leave tracked inputs unchanged; write generated output to `WHOSAIDSO_OUTPUT`. Cargo builds use a separate scratch target directory.

`land` chooses the current branch’s upstream remote, then the configured main branch’s upstream, then `origin`, then a sole remote. With multiple remotes and no default, set an upstream. Without a reachable remote, work and proofs remain local; retry `land` when connected.

The default `limits.land_lines` is 400 added plus removed implementation lines relative to fetched main. Records are excluded. A named `land --override "<why>"` is kept in the receipt; a numeric limit or `"off"` can be set in config. Branch age is shown in Goals and the viewer and never blocks landing.

For a protected host merge queue, set `"land":"branch"` in `.whosaidso/config.json`. Landing publishes `land/<goal>` and keeps the goal claimed until its receipt reaches main; require `gate --ci <base>..<queued-tip>` on the host so combined work is checked on the actual queued commit. This reruns checks for goals named by queued work or receipts even when their state was already done at the base; a queued edit cannot reuse an earlier passing result. Hooks stop accidental bypasses. Without required host checks, bypasses can only be detected afterward.

Claims name the holder and lapse after 72 hours. Without a remote, claims still exclude sibling worktrees. Local checks help prevent accidental mistakes; a determined bypass needs host enforcement.

## Evidence and state

Proofs in `.whosaidso/proofs` and media records in `.whosaidso/media` are named by the SHA-256 of their serialized bytes. Commit records to share them. Media bytes live outside tracked files, under the Git common directory; `sync` shares them.

State is derived. A goal with no work or evidence is `open`; branch work is `active` or `idle`; work on the destination branch without evidence is `landed`. A qualifying run on that branch is `done` when it passed, `failing` when it failed, and `stale` after later work on the same goal. Changing the check makes a previous run ineligible. Work that changes only records does not count as implementation.

## Notes

| Command | Behavior |
| --- | --- |
| `note list` | Lists current note topics. |

Keep one topic in `.whosaidso/notes/<slug>.md`. JSON front matter between `---` lines precedes ordinary Markdown:

```text
---
{"type":"decision","status":"open","title":"Storage choice","goal":"export"}
---
Use an embedded database.
```

Types are `decision`, `idea`, `question`, `claim`, `said` and `doc`; statuses are `open`, `resolved` and `retired`. `goal` is optional. A claim must name a goal or cite evidence record hashes in `evidence`. `note show <slug>` prints the revision to use for an edit. `note put <slug> <revision|new> <file>` refuses stale revisions. `note open|resolve|retire <slug> <revision>` changes the lifecycle status. `note list` and `note history <slug>` show current topics and Git history; `find` searches the text.

When both branches changed a topic since their merge base, `gate` requires explicit reconciliation, including when Git would merge the text cleanly. Review both versions, update the note, then use `note reconcile <slug> <revision> <destination-note-revision>` with the revision named by the refusal. Decisions and documents stay live until retired.

## Search and flows

| Command | Behavior |
| --- | --- |
| `find <words...> [--limit N]` | Searches records, file text, definitions and destination history; `kind:<kind>` filters results. |
| `context <topic...> [--budget N]` | Prints a bounded context pack using the same ranking, with omissions labelled. Diagnostics go to stderr. |
| `model fetch [--from <https-or-file-base-url>]` | Downloads and verifies pinned local model files in the user cache outside Git. |
| `review [--base REF] [--diff FILE] [--depth N] [--limit N] [--offset N]` | Shows the impact of a change. |
| `grep <text...> [--in <path>]...` | Searches visible file text. |
| `code defs <name>` / `code refs <name>` | Finds definitions or references, with evidence strength shown. |
| `code deps <path>` / `code includers <path>` | Shows includes and reverse includes. |
| `code import-scip <file>` | Imports precise code information. |
| `flow view [<id>] [--branch <revision>]` | Shows a flow or lists flows, with comparison against code. A branch view reads committed data only. |
| `flow map [<file-id>] [--branch <revision>]` | Shows the code map. |
| `flow compare <id> [--branch <revision>]` | Compares a flow with code. Flow progress never completes a goal. |
| `flow check [<id>]` | Checks flow records. |
| `flow put <id> <json>` | Writes a flow record from JSON. |
| `flow show [<id>]` | Shows the authored flow. |
| `flow drift-check` | Refuses drift between flows and code. |

Grep accelerates ASCII literals and basic ASCII extended regexes; context, backreferences, POSIX character classes, non-ASCII case matching and custom Git output or binary settings use `git grep`.

A file's best matching symbol names its search hit. Hybrid results label BM25, embeddings or both. Partial and semantic result counts can be lower bounds. Context packs label omitted entries and lower-bound counts.

Model fetching needs system `curl`; `--from` selects an HTTPS or file download source while hashes stay pinned. `WHOSAIDSO_MODEL` overrides the read location. Search keeps lexical results with one diagnostic when the model is missing or invalid. Once fetched, inference is offline and needs no service.

Search caches are disposable. Records remain the source of truth. Symlinks and submodules are not followed. Built-in code extraction is syntactic; precise results require a bound SCIP index and unchanged document bytes. Records and unrelated files do not invalidate other documents. Viewer reads stay per-document and label stale evidence. Landing and `flow drift-check` fall back to syntax for an indexed language when any indexed document changed or was deleted, or a new document in that language is missing from the index. Commit, index the language, then land; strict refusals name the indexing command once. Unchanged documents that fail occurrence validation stay unknown and use syntax locally, in both gates and viewer. Changes to the pinned build environment invalidate the run.

## Media and viewer

The gallery shows the newest 100 media items. Older media moves into monthly tar bundles and stays retrievable. Missing local bytes are warnings; use `sync` to fetch shared media. `WHOSAIDSO_BLOBS` and `WHOSAIDSO_ARCHIVE` can override storage locations. The viewer reads registered projects from `WHOSAIDSO_HOME/projects.list` and makes no changes to them.

## Tests

Use simple end-to-end tests of the real binary in isolated Git projects. Run one nearby test with `cargo test --locked --test <name>`. Run the whole suite only when needed, with `FULL_SUITE=<reason> cargo test --locked`. Large indexing and output fixtures are named ignored tests, outside the default suite. Optional model integration tests use `WHOSAIDSO_TEST_MODEL`; without it they print a named skip message. Required tests remain lexical and need no model download. This project supports macOS and Linux.
