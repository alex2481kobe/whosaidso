# WhoSaidSo

A ledger in your git repo for goals worked on by many agents. Goals have evidence: a recorded run or an attached file. Agents claim work and keep its proof; progress is derived from Git.

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
cargo install --git https://github.com/alex2481kobe/whosaidso --tag v0.3.2 --locked
```

## Quick start

In a Git repository with an initial commit and a configured destination remote, create a goal:

```sh
mkdir -p .whosaidso/goals
printf '%s\n' '{"title":"First goal","done_when":"The feature file exists","parent":null,"order":1,"after":[],"check":["sh","-c","test -f feature.txt"]}' > .whosaidso/goals/first-goal.json
whosaidso check
whosaidso install-hooks
git add .whosaidso
git commit -m 'Add records' -m 'Goal: none'
```

Create a branch, claim the goal, commit the work, and land it:

```sh
git switch -c first-goal
whosaidso start first-goal
printf '%s\n' 'Example feature' > feature.txt
git add feature.txt
git commit -m 'Add feature'
whosaidso land
whosaidso goals
```

`land` fetches and merges main, runs the goal’s short check, records evidence, and publishes a tested receipt. If the same check already passes on main, pass `land --reason "<why>"`; unchanged runs are reused. A rejected push merges and checks again. With a host merge queue, `gate --ci <base>..<tip>` reruns checks for goals named by queued work or receipts, including goals already done at the base. It never prompts or runs a full suite. Offline work stays local until the remote is available.

Checks run in a disposable worktree at the tested commit. Absolute executable paths and separate path arguments under a worktree in this clone are mapped into that scratch copy; shell command strings are not rewritten. Generate output in `WHOSAIDSO_OUTPUT` and leave tracked inputs unchanged.

Run `install-hooks` once per clone. Goal state is derived from Git; evidence on the default branch completes a goal. A check is optional: a file attachment can also provide evidence. Keep checks short and focused on real behavior.

## Code search

```sh
whosaidso find first goal
whosaidso grep search text --in src/
whosaidso code defs physics::cross
whosaidso code refs cross
whosaidso code deps src/main.cpp
whosaidso code includers src/types.h
```

`find` searches records, files and history; `grep` searches file text; `code defs`/`refs` find where a name is defined and used; `code deps`/`includers` show what a file includes and what includes it. C, C++, Rust, Python, Go, JavaScript, TypeScript, Swift and Java work out of the box; `whosaidso code import-scip <file>` adds exact answers from a SCIP index.

`context <topic...> [--budget N]` prints a bounded pack using the same search ranking. Hybrid search labels lexical and embedding matches; partial counts can be lower bounds. Run `whosaidso model fetch` once to download verified model files outside Git. Inference then runs offline. If the model is unavailable, search keeps lexical results with a diagnostic.

## Viewer

`whosaidso ui` serves a read-only viewer on your machine, with Goals, Flows and Gallery. `--open` opens your browser.

See [USAGE.md](USAGE.md) for commands and record formats. Contributing: [CONTRIBUTING.md](CONTRIBUTING.md). Apache-2.0.
