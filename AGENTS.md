# Agents working on WhoSaidSo

Any agent, any harness. Read CONTRIBUTING.md too; this file adds what an agent
needs that a human contributor would pick up from review. How to use WhoSaidSo
itself (capture, admit, run, handback, the views) is in `whosaidso help`, and
only there.

## The build

- Go 1.22 is the target and the standard library is the only dependency. Say
  which Go version you actually ran.
- Before calling work done: `gofmt -l .` (must print nothing), `go vet ./...`,
  `go test ./... -count=1`, and `go test -race` on the packages you touched.
  If you changed the package structure, run `go run ./tools/archtree -readme`
  and commit the README it regenerates.
- Files stay around 200-300 lines. A new file opens with a comment saying what
  belongs in it and what does not.

## Tests are about behaviour, not about this repository

- A test builds its own fixture: a temp project with its own `whosaidso.toml`
  and ledger, generated in the test or kept small under `testdata/` and built
  from synthetic events. Never read this repository's own `.whosaidso/`, its
  bundles or its artifact store, and never write into it.
- Set `WHOSAIDSO_HOME` (or `HOME`) to a temp directory in any test that binds a
  project or captures intake, so a run never touches a real machine home.
- Name test files and functions for the rule they check, not for when or by
  whom they were written.
- Comments state the rule and the failure it prevents on their own. Do not cite
  issue threads, review rounds or documents a reader of the code cannot see.

## Who owns what

- `internal/acceptance/` is the specification. It is written by reviewers who
  own no production code. Do not edit it unless your task names a file there
  as yours. If a test there contradicts your task, report it; do not change it.
- A reviewer owns one acceptance file and no production code. A failing test
  is the reviewer's deliverable: it says what was expected, what happened, and
  why it matters.
- Work only in the files your task names. If one item needs a file you do not
  own, stop that item, finish the rest, and report what you needed and why.
- Commit on your own branch with a plain message. Do not push unless asked.
- When several agents share one scratch directory, prefix every scratch file
  with a name unique to your task.

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
