package main

// This file holds the guide's topics: how to use Datum, in reading order.
// It replaces the README's "Using it" and the agent skill's guidance, so the
// manual ships with the binary it describes. Every `datum VERB` and --flag
// named here is checked against the registry (help_test.go). Verb usage
// lives in registry.go; rendering in help.go.

type topic struct{ name, summary, text string }

var guide = []topic{
	{"loop", "what Datum is; capture, admit, run, handback, accept", `The admitted ledger (.datum/events, one bundle per admission) is the only
authority. Status (READY, BLOCKED, MEASURED, PROVEN ...) is projected from
it, never written by anyone. UNKNOWN is a real answer: FALSE means "compared
and disagreed"; what could not be compared is UNKNOWN.

  capture   proposes: a packet in local intake; nothing is published
  admit     judges: the review, and the only act that writes a bundle
  run       measures: its start and seal are two packets to admit
  handback  reports how an attempt ended: a receipt to admit
  accept    a witnessed task.close ends the task (datum help accept)

Setup. Build with: go build -o datum ./cmd/datum. The project is the nearest
datum.toml above the working directory; it names the project id and the
ledger path, conventionally .datum/events.
Set DATUM_ACTOR to your name; --actor overrides it; a missing actor is
recorded unknown, never guessed. Ids: datum id prints one, datum id 5 five.
Never hand-write an id.

Writing is two acts: capture, then admit the packet id it prints.
  datum template task.create > task.json      # fill every placeholder
  datum capture --events task.json            # captured PACKET ...
  datum admit --outcome accepted --reason "why this is right" PACKET
or both at once: datum capture --events task.json --admit --reason "...".
Common records: task.create (new work; one task per piece of work),
task.start (take an attempt; keep its attempt_id for run and handback),
claim.assert, decision.open, and decision.dispose to record an owner's
ruling (its quote must equal, byte for byte, what the authority's selector
reads from a captured source, usually the owner's message captured as a
source.intake with --blob). Look records up with datum show --json ID.
`},
	{"views", "the four reads: todo, continue, show, history", `  datum todo                   everything owed, in flight first
  datum continue RECORD_ID     resume any record (a plan: its items)
  datum show [RECORD_ID]       one record; bare: summary and every record
  datum history [RECORD_ID]    admitted events in order, and reviews

Every answer opens with its watermark (ledger sequence, bundles, events and
head bundle): quote it with any status you report. result is KNOWN, or
UNKNOWN with the reason (an absent record is a watermarked UNKNOWN, not an
error). The default text is the brief: one short block per record, every
value read from the --json export at a fixed path, long text cut at its
first line or 72 characters and marked with …. Never quote a brief as the
whole record; --json is the complete answer, with snake_case keys.
--limit N cuts only optional results (ready tasks, context refs) and says
how many; blockers, closure, corrections and attention are never cut.
show --kind task|claim|decision|instrument keeps one kind; instruments come
validation first. Reads keep a disposable snapshot in .datum/cache: it is
checked against every ledger byte before use, and deleting it changes no
answer.
`},
	{"admit", "admission is the review; any actor; self-admission", `  datum admit --outcome accepted|rejected|correction-requested --reason TEXT PACKET ...
Any actor may admit. Admitting your own packet is allowed and recorded:
datum history --self-admitted lists those reviews (--self-admitted=false
and =unknown too). --command-id is minted when omitted; give the printed id
again to retry exactly. A refused packet stays in intake until you admit it
--outcome rejected with the reason; do that, so the refusal is on record.
capture --admit and run --admit admit as a second act: if admission is
refused, the capture stands, the packet stays pending, the retry command is
printed and the exit status is 4 (partial success).
`},
	{"accept", "who accepts work; SELF-ACCEPTED", `A success handback closes the attempt, not the task: with no acceptance
witness the task waits in todo's awaiting-acceptance section. A task.close
with its witnesses ends it. A task may name its accepter; then only that
actor may close it, otherwise anyone may. A closer who also did the work
reads self_accepted: TRUE (SELF-ACCEPTED): visible, never blocked. Keep
tasks small (R14.3) so success honestly means "this piece is done"; a large
task stays open with the small ones as its prerequisites.
`},
	{"outcomes", "the nine handback outcomes, and holds", `  datum handback --attempt-id ULID --outcome OUTCOME --reason TEXT --next-action TEXT
Only success says the work got done, and it closes the attempt, not the
task. Every other outcome leaves the task open. No outcome is defaulted.
  success                 the work is done; with no witness the task awaits acceptance
  stopped                 interrupted before finishing; say why and the unfinished step
  refused                 the instrument or tool declined to measure; not a pass
  no-reading              no reading was obtained; say what would get one, never a zero
  measurement-impossible  cannot be measured here; name the missing capability
  runner-died             the process died; observer died too: --reconciliation-owed
  harness-broken          the producer or setup failed, not the work under test
  out-of-scope            needs work outside the task; kept owed, reassignment proposed
  blocked-mid-task        cannot go on until something happens; admitted with its hold
A hold travels with the receipt: --hold-id, --hold-reason (prerequisite,
awaiting-acceptance, resume, reconciliation), --hold-actor, --hold-criterion.
blocked-mid-task needs an open hold admitted with it; out-of-scope needs a
resume hold with an authored reassignment criterion and actor. --hold-actor
never inherits the receipt's actor. "My slice is done, the task is not" is
not stopped: give the slice its own small task and hand that back success.
`},
	{"plans", "a plan is a task whose prerequisites are its items", `There is no plan record and no focus event. A plan is a task whose
prerequisites are its items; datum continue PLAN_ID shows each item with its
current status and what is owed. Add an item by amending the plan task's
prerequisites (task.amend).
`},
	{"proof", "criterion first, the whole family, a verdict", `1. Fix the criterion (criterion.fix) and admit it in an EARLIER bundle than
   any run it judges; freezing is checked against Datum's capture stamp, not
   the started_at you write. Dry-run it first: datum check criterion.
2. Run: datum run --attempt-id A --instrument I --claim C --claim-revision N
   --criterion-id K --criterion-revision N -- ARGV. ARGV runs without a
   shell (need a pipeline? -- sh -c '...'). One run carries one criterion.
   Its stdout is the output named stdout, in the run's own directory, where a
   criterion's locator path resolves. Admit the start and seal.
3. Prove: proof.admit lists the whole family, every run of the criterion
   including rejected runs and earlier revisions, each with a disposition,
   and states verdict: supports or refutes. Dry-run it with
   datum check admission --events proof.json, then capture and admit it.
PROVEN needs a validated instrument (R9). A FAILING member can only be
contradicts, or inapplicable with a code_change git verifies over the
claim's scope (R14.2). Record a failing criterion with a refutes proof: the
claim reads REFUTED. Every proof judges the claim's current criterion
revision. "value 0 does not satisfy" means member 0, not a reading of zero.
An observer that died before sealing: datum reconcile --invocation-id ID
--reason TEXT, then admit its packet; the outcome stays UNKNOWN.
`},
	{"check", "the three dry runs and what each did NOT check", `  datum check criterion --events crit.json [--blob EXAMPLE] [--output RUN_OUTPUT]
      criterion preview only: instrument validation, proof-family
      completeness, comparability and admission were NOT checked
  datum check admission --events ev.json [--packet ID ...]
      admission dry run at watermark W: full gate, evidence and authority
      checks; result may change if the ledger moves
  datum check disposal --digest SHA256 [--git FORMAT:COMMIT:PATH]
      disposal-loss preview at watermark W: admission recomputes and stays
      the authority; reasons are yours to write
Each prints that scope first. None writes anything. Exit status: 0 TRUE or
would-admit, 1 FALSE or would-refuse, 3 UNKNOWN. A TRUE criterion preview
is not an admission. Datum never deletes bytes (R11.1): an artifact.dispose
records the loss, and check disposal lists what it must account for.
`},
	{"stale", "re-measuring after a code change", `datum show --stale adds, per observed current claim, stale TRUE, FALSE or
UNKNOWN with the reason: whether code under the claim's scope changed
between its last run's commit and this checkout's HEAD. It runs git, so it
is off unless asked, and it cannot see uncommitted changes. Re-measure a
stale claim; do not change its criterion because the code changed. An old
failing run is set aside only as inapplicable with a verified code_change.
`},
	{"home", "where the ledger, intake and runs live", `The ledger is the datum.toml's ledger path under the project root (the
nearest datum.toml). Intake is per machine: $HOME/.datum/intake/<encoded
project id>/, shared by every clone and worktree of the project; the
machine id lives in $HOME/.datum too. To rehearse without touching the real
inbox, run with HOME set to a scratch directory on a scratch copy. run and
fresh git observations use the checkout you invoke them in.
`},
	{"pitfalls", "rules that prevent the known mistakes", `- Never invent a value: a missing actor, reading, unit or validation stays
  UNKNOWN with its reason. Two unknowns never match.
- Check the real thing: a path, time or quote you write is a claim, not a
  fact Datum observed. Comparability needs the same clean HEAD or equal pins.
- Instruments declare what they cannot see (blind_to). KNOWN validation
  cites a resolvable pinned artifact and is judged at admission (R9).
- Every "<kind: hint>" in a template is a placeholder; revise events restate
  the whole spec (copy it from datum show --json ID, change what changed).
- Tests read a fixed ledger prefix, never the live head; commit ledger
  bundles only after the full test run.
- Quote shell variables. An error naming a flag as a value (--claim
  --claim-revision) is word-splitting; in zsh a variable holding two ids is
  one argument, use an array.
- Flags go before or after positional ids: datum show ID --json works.
- Exit status: 0 ok, 1 refused or false, 2 usage, 3 UNKNOWN (check), 4
  partial success (--admit). With --json a failure still prints one JSON
  object, {"error":{"code","message"}}.
`},
}
