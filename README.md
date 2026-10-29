<h1 align="center">WhoSaidSo</h1>

<p align="center">
  <strong>Keep your agents accountable and your claims proven.</strong><br />
  Goals, claims, proofs and evidence live in your repository, and progress is worked out from what actually landed.
</p>

<p align="center">
  <a href="#get-started">Get started</a> ·
  <a href="#what-it-does">What it does</a> ·
  <a href="USAGE.md">Usage and record formats</a>
</p>

WhoSaidSo is the engineering ledger for a large project worked on by many agents across many machines. Agents
write down the goals they are working toward, claim one goal at a time, prove it by running the check you agreed
on, and attach evidence: screenshots, videos and documents. A new session runs `whosaidso goals` and knows where
things stand without being told again. You read along in the terminal or with `whosaidso ui`, the local read-only
viewer for goals and evidence.

It is one small Rust program. A project's record is small JSON files in `.whosaidso/`, committed with your code, so
it branches, merges and travels with the work. A goal's state is never stored: it is worked out from Git and the
proofs every time you ask.

<img src="assets/ui-goals.png" alt="Goals and branch work in the sample game engine project" width="912" />
<img src="assets/ui-gallery.png" alt="Evidence gallery for the sample game engine project" width="912" />
<img src="assets/ui-dark.png" alt="The sample game engine project in dark mode" width="912" />

## What it does

**Goals and claims.** Work is phases, goals and one check instrument per goal. `start` claims a goal through a ref
on `origin`, so two workers on any machines never hold the same goal, and a gate refuses commits without a known
`Goal:` trailer.

**Proven, not claimed.** A goal is done when its check has passed on the code that landed on main. A proof records
the exact commit, the exit code and the output, and stops counting when the instrument changes. The check is the
whole answer: if a goal needs several things verified, the instrument runs them all.

**State from Git.** Open, active, idle, landed, done, failing and stale are worked out from branches, local main and
the proofs. There is no status field to forget to update.

**Cleanup.** `prune` removes superseded proofs, old asks and unused instruments, and keeps the project small enough
for an agent to read. An interrupted prune is undone by the next one.

**Evidence that is kept.** Media is detected by its signature, stored by content hash and shared between machines
with `sync`. The newest 100 items stay in the gallery; older ones move into monthly archive bundles you can open
with `tar`.

**Code search.** `find` and `grep` search the records, files and history; `code defs`, `refs`, `deps` and
`includers` answer where a name is defined, used or included.

## Get started

Tell your coding agent:

```text
Install WhoSaidSo from https://github.com/alex2481kobe/whosaidso and set it up in this project.
```

Or do it yourself. Building needs Rust, Cargo, Git and a Unix shell:

```text
cargo install --git https://github.com/alex2481kobe/whosaidso --tag v0.3.0 --locked
```

From a checkout, `cargo build --release --locked` puts the binary at `target/release/whosaidso`.
`whosaidso --version` prints `whosaidso 0.3.0`.

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

Each clone runs `whosaidso install-hooks` once. `whosaidso start first-goal` selects the goal, and later commits
carry its `Goal:` trailer. After committing work:

```sh
whosaidso goals
whosaidso prove first-goal
whosaidso find first goal
whosaidso grep search text --in src/
whosaidso code defs physics::cross
whosaidso code deps src/main.cpp
whosaidso code includers src/types.h
whosaidso ui --help
```

`whosaidso ui` opens the viewer; add `--open` to launch your browser automatically.

`prove` writes a proof record to commit with your work. Attach evidence with `whosaidso media <goal> <file> <title>`: PNG, JPEG, GIF, WebP, MP4, MOV,
WebM or PDF, up to 64 MiB each.

## Working with a team

`whosaidso start <goal>` claims the goal and refuses, naming the holder, while another worktree on any workstation holds it. Fetch to see other workstations' branches, authors and worktree paths in `whosaidso goals`.

Whoever lands work on main runs `whosaidso prune` and commits its deletions with the landing. The sync recipe is:
pull, prune, commit, push, `whosaidso sync` to share media bytes and archive bundles across clones. If the push is rejected, pull and prune again. If prune is interrupted, run it again.

## Sample viewer project

Build the release binary, then create a fresh game engine repository for screenshots:

```sh
WHOSAIDSO="$PWD/target/release/whosaidso" sh scripts/sample-project.sh target/ui-sample
(cd target/ui-sample/game-engine && ../../release/whosaidso ui --port 0)
```

Open the printed URL, then select Goals or Gallery. The theme button switches to dark mode.
The fixture has three phases, nine goals in six derived states, two real proofs, three asks, five PNGs and one PDF,
two local worktrees and fetched remote work. Use a fresh destination for another sample.

## Works with

Any project and any agent that can run a command. Tested on Linux and macOS in CI; Windows is untested. Until 1.0
the record format may still change.

See [usage and record formats](USAGE.md), [contributing](CONTRIBUTING.md), [changelog](CHANGELOG.md),
[security](SECURITY.md) and the [license](LICENSE) (Apache-2.0).
