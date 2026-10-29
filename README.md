# WhoSaidSo

A ledger in your git repo for goals worked on by many agents. Each goal has a check: a command that passes when the goal is done. Agents claim a goal, work on it and run its check, and the state of every goal is read from git.

<img src="assets/ui-goals.png" alt="Goals and branches" width="912" />
<img src="assets/ui-gallery.png" alt="Evidence gallery" width="912" />
<img src="assets/ui-dark.png" alt="Dark mode" width="912" />

## Install

Tell your coding agent:

```text
Install WhoSaidSo from https://github.com/alex2481kobe/whosaidso and set it up in this project.
```

Or install it yourself (needs Rust, Cargo and Git):

```text
cargo install --git https://github.com/alex2481kobe/whosaidso --tag v0.3.1 --locked
```

## Quick start

In a Git repository, create the first records:

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

Run `whosaidso install-hooks` once in each clone. Then claim a goal with `whosaidso start first-goal`; your commits carry its `Goal:` trailer. After committing work:

```sh
whosaidso goals
whosaidso prove first-goal
```

`prove` runs the check and records the result; commit it with your work. Attach a screenshot or video with `whosaidso media <goal> <file> <title>`.

## Code search

```sh
whosaidso find first goal
whosaidso grep search text --in src/
whosaidso code defs physics::cross
whosaidso code refs cross
whosaidso code deps src/main.cpp
whosaidso code includers src/types.h
```

`find` searches records, files and history. `grep` searches file text. `code defs` and `code refs` show where a name is defined and used; `code deps` and `code includers` show what a file includes and what includes it. Answers work for C, C++, Rust and Python out of the box; `whosaidso code import-scip <file>` adds exact answers from a SCIP index.

## Viewer

`whosaidso ui` serves a read-only viewer on your machine, with Goals and Gallery. `--open` opens your browser.

See [USAGE.md](USAGE.md) for every command and record format. Contributing: [CONTRIBUTING.md](CONTRIBUTING.md). Apache-2.0.
