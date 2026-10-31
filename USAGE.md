# Usage

WhoSaidSo keeps goals and evidence in Git. Commands run inside a Git worktree, except `help`, `--version` and the project picker. Exit status is 0 on success and 1 on error. A successful `prove` can record a failed check: read its output.

## Goals

Create `.whosaidso/goals/<id>.json`:

```json
{"title":"Export a report","done_when":"A report is produced","parent":null,"order":1,"after":[],"check":["sh","-c","test -f report.pdf"]}
```

`title`, `done_when`, `order` and `after` are required. `parent` names another goal, or is null for a milestone. `check` is optional and contains a direct argument list, with no shell parsing. Use a short check of real behavior, such as one existing end-to-end test or a benchmark. Goals never require a new test or a full suite. IDs use 1–64 lowercase letters, digits and hyphens; `none` is reserved. Records are limited to 64 KiB. Unknown fields, missing references and cycles are reported together. Invalid records remain visible as `broken` and count in progress.

`.whosaidso/config.json` is optional. The default branch name comes from `origin/HEAD`, then `config.main`, then `main`. The corresponding local branch supplies landed history. Fetch before viewing other machines' work.

| Command | Behavior |
| --- | --- |
| `help` | Lists commands and the testing rule. Works outside Git. |
| `check` | Checks record structure, references and media. Reports every problem. |
| `goals` | Shows milestones, nested goals, progress, broken records and branch details. |
| `show <id>` | Shows one goal, its check, state, children and selected evidence. |
| `install-hooks` | Installs the commit trailer hook in this clone. |
| `start <goal>` | Claims a goal and adds its `Goal:` trailer to subsequent commits. `none` releases the selection. |
| `gate [base]` | Checks non-merge commit trailers and refuses uncommitted records. Defaults to the destination branch. |
| `prove <goal>` | Runs the committed check and keeps its commit, exit code, output hash and output tail. Commit the resulting proof record. |
| `media <goal> <file> <title>` | Attaches media, addressed by its content hash. |
| `prune` | Collects superseded evidence and archives older media. Run on the destination branch with committed records. Interrupted collection can resume. |
| `sync [remote]` | Shares media and archives through Git refs; default remote is `origin`. |
| `ui [--port N] [--open]` | Opens the read-only Goals and Gallery viewer on 127.0.0.1. |

Claims name the holder and lapse after 72 hours. Without a remote, claims still exclude sibling worktrees. Local checks help prevent accidental mistakes; a determined bypass needs host enforcement.

## Evidence and state

Proofs in `.whosaidso/proofs` and media records in `.whosaidso/media` are named by the SHA-256 of their serialized bytes. Commit records to share them. Media bytes live outside tracked files, under the Git common directory; `sync` shares them.

State is derived. A goal with no work or evidence is `open`; branch work is `active` or `idle`; work on the destination branch without evidence is `landed`. A qualifying run on that branch is `done` when it passed, `failing` when it failed, and `stale` after later work on the same goal. Changing the check makes a previous run ineligible. Work that changes only records does not count as implementation.

## Notes

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
| `find <words...> [--limit N]` | Searches records, files and destination history; `kind:<kind>` filters results. |
| `grep <text...> [--in <path>]...` | Searches visible file text. |
| `code defs <name>` / `code refs <name>` | Finds definitions or references, with evidence strength shown. |
| `code deps <path>` / `code includers <path>` | Shows includes and reverse includes. |
| `code import-scip <file>` | Imports precise code information. |
| `flow <command>` | Authors and reads feature flows; see `flow help`. Flow progress never completes a goal. |

Grep accelerates ASCII literals and basic ASCII extended regexes; context, backreferences, POSIX character classes, non-ASCII case matching and custom Git output or binary settings use `git grep`.

Search caches are disposable. Records remain the source of truth. Symlinks and submodules are not followed. Built-in code extraction is syntactic; precise results require a fresh SCIP index.

## Media and viewer

The gallery shows the newest 100 media items. Older media moves into monthly tar bundles and stays retrievable. Missing local bytes are warnings; use `sync` to fetch shared media. `WHOSAIDSO_BLOBS` and `WHOSAIDSO_ARCHIVE` can override storage locations. The viewer reads registered projects from `WHOSAIDSO_HOME/projects` and makes no changes to them.

## Tests

Use simple end-to-end tests of the real binary in isolated Git projects. Run one nearby test with `cargo test --locked --test <name>`. Run the whole suite only when needed, with `FULL_SUITE=<reason> cargo test --locked`. Large indexing and output fixtures are named ignored tests, outside the default suite. This project supports macOS and Linux.
