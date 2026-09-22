# Datum

An agent-first way of keeping a project accountable, testable, provable and
optimizable — with a seam for System 1 models.

Datum is how work finishes. An agent runs a measurement, hands back a receipt,
and the record exists; a task cannot close on prose alone. Everything you can
ask — what is being worked on, where we are, what is owed, what the tools can
and cannot see — is a query over one append-only log.

## What it records

| | |
|---|---|
| **CLAIM** | an assertion, its falsifier, and what measured it |
| **DECISION** | a question and the ruling that settled it |
| **INSTRUMENT** | a tool, and **what it cannot see** |
| **TASK** | what is owed, and what is blocking it |

Plus a generated **invocation envelope** for every run: what executed, and the
conditions that were actually true when it did — never the ones that were asked
for.

## Two ideas it is built on

**A measurement can be correct and still answer the wrong question.** So an
instrument declares its blind spots, a run records the conditions it observed,
and two runs compare only when those match.

**Proof is earned, not asserted.** A citation shows a run exists. Proof needs a
criterion fixed before the run, every observation in the family — including the
ones that failed — and a named responsible judgment.

## Status

Early construction. Nothing here is stable yet.

## Licence

Apache-2.0. See [LICENSE](LICENSE).
