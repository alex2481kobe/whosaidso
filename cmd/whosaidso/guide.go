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
whosaidso.toml above the working directory. It holds two required keys,
each a quoted string on its own line (# starts a comment):
  id = 'animation/toy'          # the project id every record names
  ledger = '.whosaidso/events'  # the ledger folder, inside the checkout
and optionally team = 'worker-a, worker-b' for todo's idle alarm (help todo),
stale_after (default '24h'): todo flags a claim with no
admitted activity for longer as a takeover candidate; and self_admission
('allow', the default, or 'refuse': an accepting admission then needs a
known admitter other than each packet's known author).
Reads and writes use the checkout you run them in: each clone and worktree
records on its own branch, and merging joins them (whosaidso help branches).
WHOSAIDSO_HOME, when set, must be an absolute path (whosaidso help home).
Set WHOSAIDSO_ACTOR to your name; --actor overrides it; a missing actor is
recorded unknown, never guessed. When many agents on many machines share a
ledger, name yourself ACCOUNT/MACHINE/ROLE (biz1/m2/implementer), so every
view says which account, machine and role did what. A name is asserted,
not authenticated. Ids: whosaidso id prints one, whosaidso id 5 five.
Never hand-write an id.

Writing is two acts: capture, then admit the packet id it prints.
  whosaidso template task.create > task.json      # fill chosen values; delete unused keys
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
whosaidso ui shows the same four answers in a browser, read-only.
Asked to show the owner a file (an image, video, log), run
whosaidso gallery add PATH --title "short title" --note "what to look at";
it appears in the viewer's Gallery. The gallery is not a record.

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
Any actor may admit, unless the project sets self_admission = 'refuse'.
Admitting your own packet is otherwise allowed and recorded:
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
Review on another machine: intake is per machine, so carry the packet. On
the author's machine whosaidso home prints its intake directory (SRC), on
the reviewer's too (DST); copy the packet's directory, owner-only:
  ssh AUTHOR 'tar -C SRC -cf - PACKET' | (umask 077 && mkdir -p DST && tar -C DST -xpf -)
The reviewer's todo then lists it with its author and events, and admission
verifies its bytes as for any packet: a copy changed on the way is refused.
Admit it in one checkout only.
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
A holder who left owes a receipt nobody else may write on their own say-so.
With the owner's approval admitted as a source.intake naming the task, at
the attempt's revision or the current one, record it yourself:
  whosaidso handback --attempt-id A --outcome stopped --authority-source SOURCE \
      --reason "..." --next-action "..."
then admit it. The receipt stays yours, recorded on the owner's word, and
the task is READY again: claim it to take it over, or close it.
A packet admitted under someone else's name (a receipt captured as the
departed holder, say) is annotated, never rewritten: its writer captures
  whosaidso template author.correct --set 'packets[0]=PACKET' \
      --set writer.id=ME --set reason="..." --capture --admit --reason "..."
as themselves. Views show the writer beside the recorded author, and
closer_authored_receipt compares the closer with the writer.
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
      --set 'spec.valid_range=...' --set 'spec.config_surface=[]' \
      --set 'spec.dangerous_defaults=[]' --set 'provenance.source_refs=[]' \
      --pin spec.implementation_ref=TOOL --pin spec.validation.value.ref=VALIDATION \
      --set spec.validation.value.version=... --capture --admit --reason "..."
  whosaidso template claim.assert --set 'spec.assertion=...' \
      --set 'spec.falsifier=...' --set 'spec.scope.applies_when=...' \
      --set 'spec.scope.limitations=...' --set 'spec.scope.source_paths=[]' \
      --set 'spec.scope.context_refs=[]' --set 'spec.external_refs=[]' \
      --set 'provenance.source_refs=[]' --capture --admit --reason "..."
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
	{"check", "the dry runs and the claim check, and what each did NOT check", `  whosaidso check criterion --events crit.json [--blob EXAMPLE] [--output RUN_OUTPUT]
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
  whosaidso check claim ATTEMPT_ID
      claim status at watermark W in this checkout's ledger: HELD (0), LOST,
      TAKEN-OVER or ENDED (1), UNKNOWN (3); other branches are NOT seen
      until merged (whosaidso help branches)
  whosaidso check ledger
      the ledger at HEAD, no cache, no home: bundles and artifacts
      committed, git pins reachable from HEAD, every set-aside bundle
      resolved; clean (0) or problems (1)
The dry runs print that scope first; check claim prints its status first.
None writes anything. Exit status: 0 TRUE, would-admit, a listed family,
check disposal's list (it has no FALSE) or HELD; 1 FALSE, would-refuse, a
family the gate disagrees with, a claim to stop, or ledger problems; 2 usage;
3 UNKNOWN. A TRUE criterion preview is not an admission.
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
	{"home", "where the ledger, intake and runs live", `The project is the nearest whosaidso.toml above the working directory, and
its ledger, artifacts and cache are that checkout's own: every clone and
worktree records on its own branch, and git merges join them (whosaidso help
branches). Runs, source capture and git observation use the same checkout.
Commit the whole .whosaidso/ folder (events and artifacts) together.
Per machine, one WhoSaidSo home holds intake, staging (runs' outputs while
they run), the machine id, the gallery and the viewer's registry
(projects/): $WHOSAIDSO_HOME, else $HOME/.whosaidso. Two profiles under one
OS user share it unless one sets WHOSAIDSO_HOME. Intake is shared by every
checkout of a project on this machine; todo lists a packet another checkout
captured apart, with that checkout's path, so each packet is admitted in one
checkout only. Set WHOSAIDSO_HOME to a scratch directory to rehearse without
touching the real ones. whosaidso home PATH only tells whosaidso ui
which checkout to open from outside any checkout.
`},
	{"branches", "recording on git branches and merging them", `Record on any branch and merge it with a merge commit: there is no extra
step (records pin commits, which a squash or rebase orphans). Two people who admit on separate branches each
publish the next sequence number, so after the merge two bundles share it.
That is expected. Every machine reads the files in one order (sequence,
then command id), so everyone who pulls the merge gets the same answers.
The next admission after a merge joins the branches: it follows the last
bundle and names the other branch tips in its bundle, so the ledger shows
it saw everything.
When both branches changed the same thing (two amendments of one revision,
one packet admitted on both, one speaker's message order captured on both,
one decision disposed on both), the later bundle in that order is SET ASIDE:
it stays in the ledger and nothing in it applies. A later bundle on that
branch is set aside too when it names something the other branch recorded
in its place (unseen-fact: say, the revision it amends); one that names
nothing of the kind still applies. todo lists each one with the reason;
history marks its events, also under history RECORD. todo says what to do
for each (to keep a change, re-record it against the current records) and
prints the setaside.resolve command that records it was dealt with; the
bundle remains as history, and check ledger fails until each is resolved.
A ledger is still refused if a sequence number is missing, a bundle names
a predecessor that is not there, or two first bundles start two histories.
Commit only .whosaidso/events and .whosaidso/artifacts; keep
.whosaidso/cache out of git, or every merge conflicts on it.
Records pin commits: a squash or rebase leaves a branch's own commits
unreachable once the branch is gone, so nothing here requires either.
Claiming a task when many agents share the ledger. A claim counts only once
it is on origin's main: claim on main, push, and start work only after the
push succeeds and whosaidso check claim A (A: the attempt id start printed)
exits 0. A claim that cannot be pushed is no claim; do not work on it.
  git pull --ff-only                  start from a current main
  whosaidso template task.start --task T --capture --admit --reason "..."
  git add .whosaidso && git commit -m "claim T" && git push origin main
If the push is rejected, someone pushed first:
  git fetch origin && git merge --no-edit origin/main
(a merge: git pull --ff-only cannot succeed once a claim is committed), then
whosaidso check claim A. Exit 0: push again.
Exit 1: stop; the claim lost and the task is someone else's. When two claims
of one task merge, the one first in the order above applies and the other is
set aside; todo calls it a lost claim. Then work on any branch; after every
merge from main run whosaidso check claim A, and stop on a nonzero exit:
rarely, a claim admitted moments earlier elsewhere and pushed moments later
still sorts first, and the merge decides for it.
todo's in-flight tasks list each claim's holder, claim time and last
activity (its runs and receipt); a claim idle past stale_after is flagged
in attention as a takeover candidate, never released by itself.
Merging work into main: git merge --no-ff, then whosaidso check ledger on
the result, and push only on exit 0 (whosaidso help check).
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
