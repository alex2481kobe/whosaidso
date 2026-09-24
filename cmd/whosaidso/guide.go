package main

// This file holds the guide's topics: how to use WhoSaidSo, in reading order.
// It replaces the README's "Using it" and the agent skill's guidance, so the
// manual ships with the binary it describes. Every `whosaidso VERB` and --flag
// named here is checked against the registry (help_test.go). Verb usage
// lives in registry.go; rendering in help.go.

type topic struct{ name, summary, text string }

var guide = []topic{
	{"loop", "what WhoSaidSo is; capture, admit, run, handback, accept", `The admitted ledger (.whosaidso/events, one bundle per admission) is the only
authority. Status (READY, BLOCKED, MEASURED, PROVEN ...) is projected from
it, never written by anyone. UNKNOWN is a real answer: FALSE means "compared
and disagreed"; what could not be compared is UNKNOWN.

  capture   proposes: a packet in local intake; nothing is published
  admit     judges: the review, and the only act that writes a bundle
  run       measures: its start and seal are two packets to admit
  handback  reports how an attempt ended: a receipt to admit
  accept    a witnessed task.close ends the task (whosaidso help accept)

Setup. Build with: go build -o whosaidso ./cmd/whosaidso. The project is the nearest
whosaidso.toml above the working directory. It holds exactly two keys, both
required, each a quoted string on its own line (# starts a comment):
  id = 'animation/toy'          # the project id every record names
  ledger = '.whosaidso/events'  # the ledger folder, inside the checkout
Bind the checkout holding the live ledger once per machine:
whosaidso home PATH (whosaidso help home). WHOSAIDSO_HOME, when set, must be
an absolute path.
Set WHOSAIDSO_ACTOR to your name; --actor overrides it; a missing actor is
recorded unknown, never guessed. Ids: whosaidso id prints one, whosaidso id 5 five.
Never hand-write an id.

Writing is two acts: capture, then admit the packet id it prints.
  whosaidso template task.create > task.json      # fill every placeholder
  whosaidso capture --events task.json            # captured PACKET ...
  whosaidso admit --outcome accepted --reason "why this is right" PACKET
or both at once: whosaidso capture --events task.json --admit --reason "...".
A template fills what WhoSaidSo can compute (whosaidso help template); give the
rest with --set PATH=VALUE, a reference with --pin PATH=FILE, and capture it
in one step (HOLD is the blocker_id a blocker.hold prints):
  whosaidso template blocker.clear --hold HOLD --pin resolving_witness=EVIDENCE \
      --capture --admit --reason "..."
Common records: task.create (new work; one task per piece of work),
task.start (take an attempt; keep its attempt_id for run and handback),
claim.assert, decision.open, and decision.dispose to record an owner's
ruling (its quote must equal, byte for byte, what the authority's selector
reads from a captured source, usually the owner's message captured as a
source.intake: whosaidso help sources).
Look records up with whosaidso show --json ID.
`},
	{"views", "the four reads: todo, continue, show, history", `  whosaidso todo                   everything owed, in flight first
  whosaidso continue RECORD_ID     resume any record (a plan: its items)
  whosaidso show [RECORD_ID]       one record; bare: summary and every record
  whosaidso history [RECORD_ID]    admitted events in order, and reviews
A source's source_id reads with show and history too; bare show lists sources.

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
validation first. Reads keep a disposable snapshot in .whosaidso/cache, a
local speed-up used only while it matches the ledger's chained hash;
deleting it changes no answer. Its blind spot: a cache edited on purpose,
with its checksum recomputed, can change what reads display (the ledger,
the authority, is untouched). For final acceptance or any check that
matters, set WHOSAIDSO_NO_CACHE=1: reads replay the whole ledger and neither
read nor write the cache; any other value is refused.
`},
	{"admit", "admission is the review; any actor; self-admission", `  whosaidso admit --outcome accepted|rejected|correction-requested --reason TEXT PACKET ...
Any actor may admit. Admitting your own packet is allowed and recorded:
whosaidso history --self-admitted lists those reviews (--self-admitted=false
and =unknown too). --command-id is minted when omitted; give the printed id
again to retry exactly. A refused packet stays in intake until you admit it
--outcome rejected with the reason; do that, so the refusal is on record.
capture --admit and run --admit admit as a second act: if admission is
refused, the capture stands, the packet stays pending, the retry command is
printed and the exit status is 4 (partial success).
One admission applies a packet after the packets creating what it names,
otherwise in capture (packet id) order, each packet's events as authored. A
proposal naming a revision that an earlier one supersedes is refused as stale
(revision-conflict or invalid-transition); re-capture it against the current
revision.
`},
	{"accept", "closing a task: witnesses, outcomes, withheld acceptance", `A success handback closes the attempt, not the task: the task waits in
todo's awaiting-acceptance section until a task.close ends it. A task may
name its accepter; then only that actor may close it, otherwise anyone may,
and only the accepter may remove or change it in an amendment.
closer_authored_receipt says whether the closer also wrote one of the task's
attempt receipts (UNKNOWN when either author is unknown); FALSE does not show
someone else did the work. A waived close shows the authority it cites, or
"(no authority cited)"; the gate requires none. Visible, never blocking. Keep tasks small so success honestly means
"this piece is done"; a large task stays open with the small ones as its
prerequisites. Start to close (A is the attempt_id start prints, PACKET the
receipt handback prints):
  whosaidso template task.start --task T --capture --admit --reason "..."
  whosaidso handback --attempt-id A --outcome success --reason "..." --next-action "..."
  whosaidso admit --outcome accepted --reason "..." PACKET
  whosaidso template task.close --task T --set outcome=success \
      --pin 'acceptance_witness_refs[0].witness_ref=EVIDENCE' \
      --pin 'delivery_witness_refs[0]=DELIVERED' --capture --admit --reason "..."
success needs both witnesses: an acceptance witness for each acceptance
criterion of the revision closed (--task fills each criterion_id and
revision; its witness_ref pins the evidence that criterion is met) and at
least one delivery witness, pinning what was handed over (a commit, a file).
Every attempt needs its receipt first. cancelled, withdrawn and waived
close without witnesses; only success satisfies a task-success prerequisite.
On someone else's word, cite them as the close's authority: capture their
message as a source.intake (whosaidso help sources), then set authority.actor.id
to them, --pin authority.source_ref to the same file, and name the task
revision in authority.scope.context_refs.
Not accepted yet: record why as a hold (it prints blocker_id = HOLD), and
clear it when that is met:
  whosaidso template blocker.hold --task T --set reason=awaiting-acceptance \
      --set actor.id=WHO --set criterion="not accepted because ...; \
      accepted when ..." --capture --admit --reason "..."
  whosaidso template blocker.clear --hold HOLD --pin resolving_witness=EVIDENCE \
      --capture --admit --reason "..."
A success close is refused while any hold on the task is open (a clear
earlier in the same bundle counts); cancelled, withdrawn and waived still
close, and show and continue then list each hold left open. A hold belongs
to its task and survives amendments; clear it against the task's current
revision (--hold fills that revision).
`},
	{"outcomes", "the nine handback outcomes, and holds", `  whosaidso handback --attempt-id ULID --outcome OUTCOME --reason TEXT --next-action TEXT
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
prerequisites are its items; whosaidso continue PLAN_ID shows each item with its
current status and what is owed. Add an item by amending the plan task's
prerequisites (task.amend).
`},
	{"sources", "what someone said, or an old record: source.intake", `A source.intake keeps the exact bytes someone said or wrote: an owner's
message, an old note, a file from outside the repo. It records only what
was said; nothing in it becomes a fact by being captured.
  whosaidso template source.intake --pin source_ref=PATH --set order=0 \
      --set speaker.id=WHO --set "referents=[$REF]" --capture --admit --reason "..."
with REF='{"project":"P","record_id":"ID","revision":1}' for each record.
PATH may lie outside the project: its bytes travel as a blob and no path is
stored. original_digest and length are filled from the pin. speaker is who
said it (speaker.unknown_reason when nobody knows); order is its zero-based
place in that speaker's own sequence (their first message 0, the next 1),
not ledger order; referents are the exact record revisions it concerns, and
a close or waiver authority counts only for a revision its source names.
An old record needs no import: capture it unchanged, then record what it
becomes, by judgment: a task.create for work it asks for, a task.amend
--from whose progress cites it (--set replacement.progress.summary=...
--pin 'replacement.progress.witness_refs[0]=PATH'), or a claim with its
falsifier. The home's staging/ folder holds runs' outputs, not notes.
`},
	{"proof", "criterion first, the whole family, a verdict", `A proof needs an instrument I (a validated one for PROVEN) and a claim C.
Each capture prints "minted id = ID": that ID is I, or C. TOOL is the
instrument's implementation file, VALIDATION the file that validated it:
  whosaidso template instrument.declare --set 'spec.question_answered=...' \
      --set 'spec.blind_to=...' --set 'spec.not_answered=...' \
      --set 'spec.valid_range=...' --set spec.config_surface=[] \
      --set spec.dangerous_defaults=[] --set provenance.source_refs=[] \
      --pin spec.implementation_ref=TOOL --pin spec.validation.value.ref=VALIDATION \
      --set spec.validation.value.version=... --capture --admit --reason "..."
  whosaidso template claim.assert --set 'spec.assertion=...' \
      --set 'spec.falsifier=...' --set 'spec.scope.applies_when=...' \
      --set 'spec.scope.limitations=...' --set spec.scope.source_paths=[] \
      --set spec.scope.context_refs=[] --set spec.external_refs=[] \
      --set provenance.source_refs=[] --capture --admit --reason "..."
Or print either to a file (whosaidso template claim.assert > claim.json),
fill it, then capture and admit it as whosaidso help loop shows.
1. Fix the criterion (criterion.fix) and admit it in an EARLIER bundle than
   any run it judges; freezing is checked against WhoSaidSo's capture stamp, not
   the started_at you write. Dry-run it first: whosaidso check criterion.
   whosaidso template criterion.fix --claim C --example stdout=FILE \
       --pin expression.result_selector=stdout#/PTR \
       --pin expression.population.selector=stdout#/PTR > crit.json
   pins an example run output (an example, never an observation): the
   result selector reads the value, the population selector the members it
   must cover (the same pointer when the result is one reading). The unit,
   population identity and denominator are filled when the example states
   each one alike; operator, target and reducer are yours. Its capture needs
   the example's bytes, --blob FILE (the template prints it):
   whosaidso capture --events crit.json --blob FILE --admit --reason "..."
   What a selector reads: at a JSON pointer, a bare value, or an object whose
   "value" (one reading) or "values" (a set; each a value or an object with
   its own "value") is compared. That object, else its parent, states what
   the number is:
     {"value": 0.75, "unit": "world units",
      "population": "the quarter-second step", "denominator": "one step"}
   unit is what the value is counted in, population which things the
   reading covers, denominator what those are counted as. The result must
   state its unit; a stated field must equal the criterion's unit,
   population.identity and population.denominator; a blank or disagreeing
   one reads UNKNOWN. The population selector's reading is counted: the
   result must cover every member (count: at most that many). A whole
   selector reads the artifact's digest (unit sha256, population and
   denominator artifact).
2. Run: whosaidso run --attempt-id A --instrument I --claim C -- ARGV. Omitted
   revisions (and the criterion, when the claim has one) resolve to the
   current admitted ones; the run records and prints them. Name
   --claim-revision or --criterion-revision only to pin an older one. ARGV
   runs without a shell (need a pipeline? -- sh -c '...'). One run carries
   one criterion.
   Its stdout is the output named stdout; a criterion's locator path is the
   name of the output it reads. More outputs: write files under
   $WHOSAIDSO_RUN_DIR and list them in the report at $WHOSAIDSO_RUN_REPORT,
     {"version":1,"outputs":[{"path":"result.json","media_type":"application/json"}],
      "config_effective":{"samples":{"type":"number","number":8}},"conditions_observed":{}}
   and each is an output named by its path, as stdout is. Paths are regular
   files relative to the run dir, no symlinks or "..", each named once with a
   media_type (not stdout, stderr or producer.json). config_effective gives
   the instrument's config_surface knobs, conditions_observed the run's
   declared conditions, each a typed scalar; a declared one left out, or a
   map left out, is UNKNOWN; visual is optional. At most 1 MiB and 256
   outputs, no unknown or duplicate keys: an invalid report is kept as an
   output and supplies no facts. Admit the start and seal.
3. Prove: proof.admit lists the whole family, every run of the criterion
   including rejected runs and earlier revisions, each with a disposition,
   and states verdict: supports or refutes. whosaidso check admission --family C
   lists that family as the gate counts it, with each run's criterion
   verdict, and prints a proof skeleton; this writes the same to a file:
   whosaidso template proof.admit --claim C > proof.json
   Each disposition, reason, the judgment and the verdict are yours. Dry-run
   it with whosaidso check admission --events proof.json, then capture and
   admit it: whosaidso capture --events proof.json --admit --reason "...".
PROVEN needs a validated instrument. A FAILING member can only be
contradicts, or inapplicable with a code_change git verifies over the
claim's scope; you choose its two commits, and the template fills
their object formats and the scoped changed paths from git. Record a failing criterion with a refutes proof: the
claim reads REFUTED. Every proof judges the claim's current criterion
revision. A FALSE reason names the failing member: its path or id, else
"member N" (its index), then its reading.
An observer that died before sealing: whosaidso reconcile --invocation-id ID
--reason TEXT, then admit its packet; the outcome stays UNKNOWN.
`},
	{"check", "the three dry runs and what each did NOT check", `  whosaidso check criterion --events crit.json [--blob EXAMPLE] [--output RUN_OUTPUT]
      criterion preview only: instrument validation, proof-family
      completeness, comparability and admission were NOT checked
  whosaidso check admission --events ev.json [--blob FILE ...] [--packet ID ...]
      admission dry run at watermark W: full gate, evidence and authority
      checks; result may change if the ledger moves. --blob is capture's:
      the bytes the events would be captured with. A packet's blobs resolve
      as on admission, held in memory, never copied
  whosaidso check admission --family CLAIM [--criterion ID]
      proof family at watermark W, confirmed by an admission dry run;
      dispositions, reasons, judgment and verdict were NOT chosen
  whosaidso check disposal --digest SHA256 [--git FORMAT:COMMIT:PATH]
      disposal-loss preview at watermark W: admission recomputes and stays
      the authority; reasons are yours to write
Each prints that scope first. None writes anything. Exit status: 0 TRUE,
would-admit, a listed family or check disposal's list (it has no FALSE);
1 FALSE, would-refuse or a family the gate disagrees with; 2 usage; 3
UNKNOWN. A TRUE criterion preview is not an admission.
WhoSaidSo never deletes bytes: an artifact.dispose records the loss,
and check disposal lists what it must account for.
`},
	{"stale", "re-measuring after a code change", `whosaidso show --stale adds, per observed current claim, stale TRUE, FALSE or
UNKNOWN with the reason: whether code under the claim's scope changed
between its last run's commit and this checkout's HEAD. It runs git, so it
is off unless asked, and it cannot see uncommitted changes. Re-measure a
stale claim; do not change its criterion because the code changed. An old
failing run is set aside only as inapplicable with a verified code_change.
`},
	{"home", "where the ledger, intake and runs live", `Every clone and worktree of a project reads and admits through ONE ledger
on this machine: the one in its home, the checkout bound with
whosaidso home PATH. The binding is explicit: in an unbound project reads and
admission refuse, and a missing home refuses too; WhoSaidSo never falls back to
the invoking checkout's .whosaidso or creates an empty ledger. Output from
another checkout names the home it used.
Two roots: the ledger, definitions, admission, canonical artifacts and the
cache use the home; run, source capture and fresh git HEAD and dirty state
use the checkout you invoke them in. A git object missing from the home
repository stays unavailable. Plain capture needs no home: intake is routed
by the declared project id.
Commit the whole .whosaidso/ folder (events and artifacts) together: a move,
or any binding, refuses a home missing evidence its admitted records cite.
Per machine, one WhoSaidSo home holds the registry (projects/), intake,
staging (runs' outputs while they run) and the machine id:
$WHOSAIDSO_HOME, else $HOME/.whosaidso. Set WHOSAIDSO_HOME to a scratch
directory to rehearse without touching the real ones.
`},
	{"pitfalls", "rules that prevent the known mistakes", `- Never invent a value: a missing actor, reading, unit or validation stays
  UNKNOWN with its reason. Two unknowns never match.
- Check the real thing: a path, time or quote you write is a claim, not a
  fact WhoSaidSo observed. Comparability needs the same clean HEAD or equal pins.
- Instruments declare what they cannot see (blind_to). KNOWN validation
  cites a resolvable pinned artifact and is judged at admission.
- Every "<kind: hint>" in a template is a placeholder. A revise restates the
  whole spec: whosaidso template claim.revise --from ID copies the current one,
  so change only what changed. Judgment is never filled for you.
- A leftover "<kind: hint>" is refused by every capture, run and check, not
  only template --capture: write the value, never the hint.
- Tests read a fixed ledger prefix, never the live head; commit ledger
  bundles only after the full test run.
- Quote shell variables. An error naming a flag as a value (--claim
  --attempt-id) is word-splitting; in zsh a variable holding two ids is
  one argument, use an array.
- Flags go before or after positional ids: whosaidso show ID --json works.
- Exit status: 0 ok, 1 refused or false, 2 usage, 3 UNKNOWN (check), 4
  partial success (--admit). With --json a failure still prints one JSON
  object, {"error":{"code","message"}}.
`},
}
