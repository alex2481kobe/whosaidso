# Agents working on WhoSaidSo

Any agent, any harness. Read CONTRIBUTING.md too; this file adds what an agent
needs that a human contributor would pick up from review. How to use WhoSaidSo
itself (capture, admit, run, handback, the views) is in `whosaidso help`, and only
there.

## The build

- Go 1.22 is the target and the standard library is the only dependency. Say
  which Go version you actually ran.
- Before calling work done: `gofmt -l .`, `go vet ./...`,
  `go test ./... -count=1`, and `go test -race` on the packages you touched.
  If you changed the package structure, run `go run ./tools/archtree -readme`
  and commit the README it regenerates.
- Files stay around 200-300 lines. A new file opens with a comment saying what
  belongs in it and what does not.

## Who owns what

- `internal/acceptance/` is the specification. It is written by reviewers who
  own no production code. Never edit it unless your brief names a file there
  as yours. If a test there contradicts your brief, report it; do not change it.
- A reviewer owns one acceptance file and no production code. A failing test
  is the reviewer's deliverable: it says what was expected, what happened, and
  why it matters.
- Work only in the worktree and files your brief names. If one item needs a
  file you do not own, stop that item, finish the rest, and report what you
  needed and why.
- Commit on your branch with a plain message. Do not push.
- WhoSaidSo records its own work in `.datum/` (named before the rename; see whosaidso.toml), so its ledger grows. A test that reads the committed
  ledger reads the fixed prefix it is about (bundles 1..N), never the live head. Committing a new
  ledger bundle needs the same full test run as committing code.
- Parallel lanes may share a scratch directory: prefix every scratch file with
  your lane name.

## Semantics that are easy to get wrong

- UNKNOWN is a real answer. Never round it to a default or fill it from
  something nearby. FALSE means "compared and disagreed"; anything that could
  not be compared is UNKNOWN. Two unknowns never match.
- Validity must be decidable from the ledger alone, never from one machine's
  intake or clock.
- Check the real thing, not its spelling: a path, a time or a quote the author
  wrote is a claim, not a fact WhoSaidSo observed.

## Proving a change

Mutation-test every rule you add: make small deliberately wrong versions,
confirm a test fails for each, then restore. Report each mutation and whether
it was caught.

Defect shapes this codebase keeps producing, worth looking for first:
- a correct value answering a different question than the one asked;
- two unknowns treated as a match;
- a check that verifies some properties and reports the whole thing verified;
- a fix that closes one path and leaves a second path to the same place;
- a check hardened on the string and never asked of the real thing;
- a rule enforced at one entrance while the shape stays expressible elsewhere.
