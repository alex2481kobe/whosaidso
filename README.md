<p align="center">
  <img src="assets/whosaidso-mark.png" alt="WhoSaidSo" width="140" height="140" />
</p>

<h1 align="center">WhoSaidSo</h1>

<p align="center">
  <strong>Keep your agents accountable and your claims proven.</strong><br />
  Every task, idea and decision is written down as it happens, and every answer points back to who said so.
</p>

<p align="center">
  <a href="#get-started">Get started</a> ·
  <a href="#what-it-is-built-on">What it is built on</a> ·
  <a href="USAGE.md">Usage and setup</a>
</p>

<img src="assets/ui-light.png" alt="The WhoSaidSo viewer" width="912" />

WhoSaidSo is for your agents, not for you to operate. Keep working with your agents the way you already do: they
use WhoSaidSo to write down what they are doing and to prove what they claim, and a new session runs
`whosaidso todo` and knows where things stand without being told again. You read along when you want to, in the
terminal or with `whosaidso ui` for a quick look at progress and the decisions waiting on you.

It is one small program: a single Go binary with no dependencies. A project's record is plain JSON files in an
append-only ledger inside the repository, committed with your code. It is bare-bones on purpose, just what one
project needs. It doesn't need a database; if a project ever outgrows plain files, SQLite could sit behind the same
commands.

## What it is built on

**Testing and proving.** Nothing is done because someone says it is. A claim is proven against a criterion fixed
before the run, with every result counted, failures included.

**Accountable agents.** Tasks, ideas and decisions are written down as they happen, so nothing is forgotten between
sessions, and every claim points back to who made it and what proved it.

**Accountable tools.** Instruments and measurements are held to the same standard: each one declares what it cannot
see, so a correct number can't quietly answer the wrong question. When something can't be checked, the answer is
UNKNOWN, never a guess.

**Knowing where you are.** One command shows what is done, what is owed, what is blocked, and why.

**Agent first.** Built for AI agents doing the work every day. The viewer and CLI read the same admitted
ledger; CLI `--json` answers keep the full detail for tools and other views.

## The viewer in v0.2.1

Run `whosaidso ui` to read your workspace locally:

- **Gallery** shows files with icon actions to reveal, copy the path, download or remove an entry from the gallery.
  Removing an entry records a retraction and keeps the file. Videos play inside their cards.
- **Overview** shows open task counts, work in flight with its holders, and work awaiting a decision.
- **Tasks** groups open work by existing prerequisite links. Open a parent to browse its nested items and counts.
  Agents can use `whosaidso todo --top` and `whosaidso todo --under ID` for the same navigation.
- **Records** browses admitted tasks, claims, decisions, instruments and sources.
- **Activity** shows the admitted history.

Lists load more as you scroll, without page controls. Use Refresh to read new changes; gallery entries are
not polled in the background. One theme icon sits beside the page title.

## Get started

Tell your coding agent:

```text
Install WhoSaidSo from https://github.com/alex2481kobe/whosaidso and set it up in this project.
```

Or do it yourself: download the file for your system from the
[latest release](https://github.com/alex2481kobe/whosaidso/releases/latest), or
`go install github.com/alex2481kobe/whosaidso/cmd/whosaidso@latest`, then run `whosaidso help`. Full steps and the
first loop are in [Usage and setup](USAGE.md).

## Works with

Any project and any agent that can run a command. Tested on macOS and, in CI, on Linux and Windows. Early:
the design is still settling. Until 1.0 the ledger format may change; when it does, the release includes a one-time
converter.

## What could come next

- **SQLite for large ledgers**, behind the same commands, if a project outgrows plain files.
- **Sub-projects**: tag records by sub-project inside one ledger, so a project's folders can move around without
  losing their records.
- **Moving records between ledgers**: a sub-project splits off from the top-level ledger into its own, in its own
  folder or its own repo, and can merge back.
- **Adding records from the viewer**, through the same capture and admit.

Apache-2.0.
