# Contributing

WhoSaidSo is in early construction and the design is still settling. Open an issue
before writing code, so you do not build against a decision that is about to
change.

## Ground rules

1. Standard library only. A new dependency needs a named operation that the
   standard library cannot provide.
2. Every check ships with the fixture that proves it can fail. A test nobody
   has watched fail is not a test.
3. The attack on a check is written by someone other than the check's author.
   Self-review finds only what you already thought of.
4. A refusal table opens with a good control. A validator that rejects
   everything passes every negative case while being completely broken.
5. Say what a measurement cannot see. An instrument without declared blind
   spots is not ready to be trusted.
6. Tests build their own fixtures in temp directories. A test never reads or
   writes this repository's own `.whosaidso/` record.

## Running it

The build, test and review commands are in [AGENTS.md](AGENTS.md#the-build).

## Workflows and review

Workflows from first-time and outside contributors wait for maintainer
approval before they run. That is deliberate. CI has read-only permissions and
holds no secrets, so an approved run cannot reach anything sensitive.

Changes to workflow files, install scripts, lockfiles, package lifecycle hooks
and generated binaries get read closely, whoever sends them.
