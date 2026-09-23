---
name: datum
description: Use when reading or writing a project's Datum ledger (a repo with datum.toml and .datum/) - finding owed work, starting or handing back a task, running a measurement, proving a claim, or recording an owner ruling.
---

# Datum, for agents (v1)

Verified against the CLI built from this repo on 2026-09-23. If a command here
disagrees with `datum` (no args) or `datum template <type>`, the CLI wins.

## INDEX (each keyword is a heading: grep "^## what")

- `what`     what Datum is, in five lines
- `setup`    build, project root, actor, ids
- `write`    capture then admit: the only way anything is written
- `owed`     see what is owed: now, todo, context, continue
- `start`    pick up a task (task.start)
- `run`      run a measurement (datum run, claim + criterion)
- `handback` hand back honestly: the nine outcomes
- `prove`    criterion check, proof check, proof.admit
- `ruling`   record an owner ruling (decision.dispose, exact quote)
- `newtask`  record a new task (task.create)
- `template` get any event's JSON
- `rules`    rules that prevent the known mistakes
- `planned`  NOT BUILT yet; do not try these
- `reading`  reading answers: brief, --json, --full, watermark
- `stuck`    which check explains what; where the rulings and friction log live

## what

1. The admitted ledger (`.datum/events/`, one bundle per admission) is the only authority.
2. `capture` proposes: it writes a packet to local intake and publishes nothing.
3. `admit` judges: admission is the review, and the only command that writes a bundle.
4. Status (READY, BLOCKED, MEASURED, ...) is projected from the ledger, never written by anyone.
5. UNKNOWN is a real answer. FALSE means "compared and disagreed"; what could not be compared is UNKNOWN.

## setup

```sh
go build -o datum ./cmd/datum      # in the Datum repo; one binary, stdlib only
datum                              # full usage; capture/admit/handback/run take --help
export DATUM_ACTOR=your-lane-name  # --actor overrides; missing actor is recorded as unknown
datum id        # one fresh ULID
datum id 5      # five
```

- The project is found by walking up to the nearest `datum.toml` (`id = "..."`,
  `ledger = ".datum/events"`).
- Never hand-write an id. Hand-typed Crockford base32 parses and means nothing.

## write

Every write is two steps. Capture, then admit the packet id it prints.

```sh
datum capture --events ev.json [--blob FILE ...]        # prints a packet id
datum admit --command-id "$(datum id)" --outcome accepted \
    --reason "why this is right" PACKET_ID [PACKET_ID ...]
```

- `--outcome` is `accepted | rejected | correction-requested`. The reason is the review; write a real one.
- `admit` echoes the whole bundle. Redirect it and read `sequence` if you need it.
- Several packet ids are separate arguments. In zsh, `$pks` holding two ids is ONE
  argument; use an array (`${=pks}` or `"${arr[@]}"`).
- A source or example whose bytes must resolve at admission travels with `--blob`.

## owed

```sh
datum now           # IN FLIGHT tasks + runs, OPEN decisions, attention (holds a BLOCKED task waits on)
datum todo          # blocked, awaiting-acceptance and READY queues, plus intake
datum todo --json --limit 20
datum context       # attention and claims with their status and support
datum continue TASK_ID          # resume brief for one task (fresh git/clock observations)
datum show TASK_ID              # one record; flags go BEFORE the id
datum history TASK_ID
datum intake pending            # unreviewed packets AND rejected/correction-requested ones
```

- `now` can read empty while a task sits BLOCKED; always check `todo` too.
- `datum show ID --json` fails ("unexpected positional arguments"). Write `datum show --json ID`.

## start

```sh
datum template task.start > start.json   # attempt_id is freshly minted for you
# fill task.project, task.record_id, task.revision (from: datum show --json TASK_ID)
# keep exactly one of actor.id / actor.unknown_reason
datum capture --events start.json
datum admit --command-id "$(datum id)" --outcome accepted --reason "..." PACKET_ID
```

Keep the `attempt_id`: `run` and `handback` need it. Check the task's `next_actor`
first; starting a task assigned to someone else is accepted but nothing records the mismatch.

## run

The instrument, claim and criterion must already be admitted, the criterion in an
EARLIER bundle than the run (see rules).

```sh
datum run --attempt-id ATTEMPT --instrument INSTRUMENT_ID \
    --claim CLAIM_ID --claim-revision 1 \
    --criterion-id CRITERION_ID --criterion-revision 1 \
    [--timeout 10m] -- go test ./... -run '^$' -bench . -count 5
# prints start_packet and seal_packet; admit both:
datum admit --command-id "$(datum id)" --outcome accepted --reason "..." START SEAL
```

- ARGV runs without a shell. Need a pipeline? Say so: `-- sh -c '...'`.
- The instrument's current revision is used automatically; the two claim/criterion
  revisions must be typed.
- One run carries one criterion. Three claims need three runs.
- A run's stdout is stored as the output named `stdout`, in the run's own directory
  (`.datum/artifacts/runs/<invocation-id>/`). A criterion's locator path resolves there.
- Observer died before sealing: `datum reconcile --invocation-id ID --reason TEXT`
  (needs an identified actor), then admit its packet. The outcome is UNKNOWN, never a guess.

## handback

```sh
datum handback --attempt-id ATTEMPT --outcome OUTCOME \
    --reason "what happened" --next-action "what must happen next" \
    [--commits-denied] [--reconciliation-owed] [--delivery-refs refs.json] \
    [--hold-id "$(datum id)" --hold-reason REASON --hold-actor ID --hold-criterion TEXT]
# then admit the packet id it prints
```

Only `success` says the work got done, and it closes the ATTEMPT, not the task.
Every other outcome leaves the task open. No outcome is defaulted.

| outcome | use when |
|---|---|
| `success` | the work is done. Without an acceptance witness the task waits in awaiting-acceptance |
| `stopped` | interrupted before finishing; name why and the exact unfinished step |
| `refused` | the instrument or tool declined to measure. Not a pass |
| `no-reading` | no reading obtained (not run, not kept, invalid, unreachable); say what would get one. Never a fake zero |
| `measurement-impossible` | cannot be measured here; name the missing capability (local, not universal) |
| `runner-died` | the process died; if the observer died too add `--reconciliation-owed` |
| `harness-broken` | the setup or producer failed, not the work under test; keep the partial output |
| `out-of-scope` | finishing needs work outside the task; needs a resume hold with criterion and actor |
| `blocked-mid-task` | cannot go on until something happens; admitted with its hold, atomically |

- Hold reasons: `prerequisite | awaiting-acceptance | resume | reconciliation`.
- `--hold-actor` never inherits the receipt's actor. Missing is recorded as unknown.
- "My slice is done, the big task is not": do not use `stopped` for this. Give the slice
  its own small task and hand that back `success` (R14.3, see rules).

## prove

1. Dry-run the criterion BEFORE admitting it. It shows unit, population, denominator,
   values and the verdict it would give. It writes nothing.

```sh
datum template criterion.fix > crit.json      # fill it
datum criterion check --events crit.json --blob example-output.json
datum criterion check --events crit.json --blob example.json --output candidate-run-output.json
```

2. After the runs, dry-run the proof through admission's own gate. It lists EVERY
   refusal at once, per family member, with the instrument's validation state.

```sh
datum template proof.admit > proof.json       # fill it
datum proof check --events proof.json [--packet CAPTURED_ID ...]
```

3. Only then capture and admit `proof.json`.

- The family is every run of that criterion, including rejected runs and runs under
  earlier revisions. Each needs a per-member disposition; none can be left out.
- PROVEN needs a validated instrument (R9). An unvalidated instrument cannot support a proof.
- Every proof states `verdict: supports | refutes` (R14.1); admission refuses one without it.
- A FAILING member can only be `contradicts`, or `inapplicable` with a `code_change` git
  verifies over the claim's scope (R14.2). A contradicted claim cannot be proven under that
  criterion revision otherwise; a new criterion or claim revision overcomes it (R10.2).
- Record a failing criterion with a `refutes` proof: it lists the contradicting runs, is
  admitted, and the claim reads REFUTED.
- Every proof, either verdict, judges the claim's CURRENT criterion revision. A proof on a
  revision a later one superseded is refused.
- "value 0 does not satisfy" means member 0, not a reading of zero.

## ruling

Recording what the owner said (decision.dispose). Any agent writes the packet; the
author stays whoever wrote it. The owner is named as `authority.actor`.

```sh
datum template decision.dispose > ruling.json
```

- `quote` must EQUAL the text the authority's selector reads from its source,
  byte for byte. Not a substring, not a paraphrase, not trimmed. Point `authority.source_ref`
  at a captured source (usually the owner's whole message, captured as `source.intake`
  with `--blob`) and select exactly that text.
- `disposition`: `approved | rejected | withdrawn`.
- Look up `decision.record_id` and `revision` with `datum show --json`. The decision must exist
  (`decision.open`).

## newtask

```sh
datum template task.create > task.json
datum capture --events task.json && datum admit --command-id "$(datum id)" --outcome accepted --reason "..." PACKET_ID
```

One task per piece of work. Keep a large task open with the small ones as prerequisites.

## template

```sh
datum template                   # lists all event types
datum template claim.assert      # capture-ready JSON array on stdout; choices on stderr
```

Event types: task.create task.amend task.start task.takeover attempt.terminal task.close
blocker.hold blocker.clear invocation.start invocation.seal source.intake claim.assert
claim.revise criterion.fix proof.admit decision.open decision.revise decision.dispose
supersede correction instrument.declare instrument.revise trust.withdraw review.admit
artifact.dispose

- Every `"<kind: hint>"` is a placeholder. An unfilled template does not decode, so it
  cannot be captured by accident.
- Ids the event creates are minted; every other id is a reference: look it up.
- stderr lines: `choose` (keep exactly one branch), `only` (a choice with one allowed
  branch), `optional`, `required` (the model decodes it absent, admission does not), `minted`.
- Reasons, judgments and dispositions are never filled for you. Write them.
- A revise event restates the whole spec plus `target` and `expected_revision`.
  Copy the current one from `datum show --json ID` and change only what changed.

## rules

1. **Small tasks (R14.3).** One task per piece of work. `success` then honestly means
   "this piece is done". Closing needs a separate, witnessed acceptance. A task may name
   its `accepter` (then only that actor may close it); a closer who also did the work reads
   `self_accepted: TRUE`, visible, never blocked (R15.1). Self-admission is recorded and
   queryable (`datum history --self-admitted`).
2. **Criterion first.** Admit `criterion.fix` in an EARLIER bundle than any run it
   judges. A criterion admitted after launch cannot freeze that run. Freezing is checked
   against Datum's capture stamp, not the `started_at` you write.
3. **A failing run stays counterevidence.** Do not change the criterion because the code
   changed; revise it only when its target or method changes. Today an old FAILING run
   is set aside under the same criterion revision only as `inapplicable` with a
   `code_change` that Datum verifies with git: code under the claim's scope paths changed
   between that run's commit and the proof's (R14.2). `datum state --stale` lists claims
   whose scoped code changed since their last run.
4. **Never invent a value.** Missing actor, reading, unit or validation stays UNKNOWN,
   with `unknown_reason` saying why. Never round UNKNOWN to a default; two unknowns never match.
5. **Tests read a fixed ledger prefix** (bundles 1..N), never the live head. An honest new
   bundle must not break a test.
6. **Commit ledger bundles only after the full test run** (`go test ./... -count=1`),
   same as code.
7. **Intake is per machine**: `~/.datum/intake/<encoded project id>/`. Every clone or
   worktree of the same project shares it. To rehearse without touching the real inbox,
   run with `HOME=<scratch dir>` on a scratch copy of the repo.
8. **Instruments declare blind spots, and validation is judged.** Write what the instrument
   cannot see. KNOWN validation cites a resolvable pinned artifact; the admitter judges it
   (R9). Validation covers the whole instrument, so if the known-answer test covers only
   some readings, leave it UNKNOWN or say exactly what it covers in the reason. Mutation-check
   the validation before calling it KNOWN.
9. **Check the real thing.** A path, time or quote you write is a claim, not a fact Datum
   observed. Comparability needs established-equal source: same clean git HEAD or equal pins.
10. **Datum never deletes bytes** (R11.1). `artifact.dispose` records the loss; run
    `datum disposal-loss --digest SHA256` first to list what the record must account for.

## planned

NOT BUILT. Do not write these fields or expect these commands.

- Authoring helpers (`hold clear`, `criterion fix --example`, `instrument revise --impl`),
  `instruments --stale`, and a documented intake override. None exist.

## reading

```sh
datum todo            # brief text: one block per record (kind, id, rev, status, why here, next actor)
datum todo --json     # for agents: lossless, snake_case keys
datum todo --full     # lossless text outline of the same JSON
```

- Every answer starts with its watermark: `sequence`, `bundles`, `events`, `head`
  (command id and time). Quote it with any status you report.
- The brief cuts long text at its first line or 72 characters, marked `…`. Never quote
  a brief as the whole record; read `--json`.
- `result` KNOWN/UNKNOWN is about the answer itself. Read it.
- `datum instruments` "trust TRUE" does not say who judged validation; check
  `datum history --self-admitted`.

## stuck

| question | command |
|---|---|
| what shape is this event? | `datum template TYPE` (and its stderr) |
| what will this criterion say? | `datum criterion check --events crit.json --blob EXAMPLE [--output RUN_OUTPUT]` |
| why would admission refuse? | `datum proof check --events ev.json [--packet ID]` (works for any event, all refusals at once) |
| what does disposal lose? | `datum disposal-loss --digest SHA256 [--git FORMAT:COMMIT:PATH]` |
| what is this record now? | `datum show --json ID`, `datum history --json ID` |
| what is still in intake? | `datum intake pending` |
| who admitted their own work? | `datum history --self-admitted` |

- A refused packet stays in intake until you admit it `--outcome rejected` with the reason.
  Do that, so the refusal is in the record.
- An error naming a flag as a value (`--claim --claim-revision`) means shell word-splitting.
  Quote your variables.
- Owner rulings: `animation-engine/documentation/design/research/OWNER-RULINGS-DATUM.md`
  in the realm-shaper workspace (R8-R14 cover build, validation, contradiction, small tasks).
- Friction log: `docs/DOGFOOD-2026-09-23.md` in the datum workspace (where agents got stuck, and why).
- Repo rules: `AGENTS.md`, `CONTRIBUTING.md`, README "Using it".
