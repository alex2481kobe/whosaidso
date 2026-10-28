package main

// This file holds the usage registry: the one table every verb, its flags and
// its help come from. Dispatch (main.go), `whosaidso help VERB`, `VERB --help`
// and the help-honesty test all read it. A verb's flags are declared by its
// define function, so help lists the flags the verb really parses. What each
// verb does lives in its own file; the guide's topics live in guide.go.

import (
	"flag"
)

type verb struct {
	name    string // one word, or "group mode" (check criterion)
	args    string // positional synopsis after the flags; "" for none
	summary string // one line, for the index
	detail  string // what `whosaidso help VERB` adds after the flags
	define  func(*flag.FlagSet) func(*call) error
}

// registry is filled in init: help reads the registry it is listed in.
var registry []verb

func init() {
	registry = []verb{
		{name: "help", args: "[all|TOPIC|VERB]", summary: "keyword index; all for the guide; topic or verb for detail",
			detail: "Bare, prints the keyword index. all prints the complete guide. A topic or verb prints that part.\n",
			define: helpVerb},
		{name: "todo", summary: "everything owed, in flight first", detail: `Sections: in flight (with each task's runs), awaiting acceptance (with its
accepter, or anyone), blocked (typed reasons, who it waits on), ready (the
only section --limit cuts), open decisions, intake by what its review says
is owed: unreviewed packets await review; correction-requested ones are
listed (whether a corrected packet answered the request is not recorded);
rejected ones owe nothing and are only
counted (whosaidso history lists their reviews); pending packets another
checkout of this project captured on this machine are listed apart, with
that checkout's path (admit each packet in one checkout only); attention,
with a stale-claim note for each claim idle past stale_after. Each in-flight
task lists the claims that hold it: attempt, holder, claimed_at,
last_activity_at, and stale, judged at the time todo runs.
Default text gives one verified header per actionable intake packet;
--intake-details expands those packet summaries. --json retains the complete
verified packet representation for every packet not accepted.
The packets_not_accepted field keeps each disposition (pending,
captured-elsewhere, correction-requested, rejected) and
captured_in when noted, and totals counts intake_unreviewed,
intake_correction_requested, intake_rejected and intake_elsewhere.
--actor NAME keeps tasks whose next_actor names NAME or whose live claim NAME
holds, in every task section, and attention about NAME or those tasks.
Open decisions keep their waiting actor; intake keeps its packet author;
set-aside bundles are not actor-scoped and are hidden. Totals stay project-wide.
Filtering precedes --limit; its offered/omitted counts describe filtered READY
tasks. Names match exactly; an unknown name succeeds with an empty view and note.
The optional config team = 'worker-a, worker-b' enables actor-idle attention:
each roster actor with no live claim and no READY task naming it is flagged.
The nearest BLOCKED task is the actor's task with fewest immediate blocking
reasons, ties by record id (including awaiting acceptance); all its reasons
are included. With no such task the note says so. No roster means no idle
alarms. Nothing is assigned automatically.
`, define: viewVerb("todo")},
		{name: "packets", args: "[PACKET_ID]", summary: "verified packet headers, or one packet's payload", detail: `Bare packets lists bounded headers for local intake and packets named by
reviews in this project. packets PACKET_ID returns one header and the complete
verified local packet with --json. A reviewed packet absent from this machine
has a null payload, explicit UNKNOWN availability and a watermarked UNKNOWN
result. An ID here is a packet command ID, not a record ID. Intake is local to
this machine; an absent header does not prove absence elsewhere.
`, define: viewVerb("packets")},
		{name: "continue", args: "RECORD_ID", summary: "resume any record: detail, closure, what is owed", detail: `Any kind of record. A task adds its progress, every attempt, every run and
what is owed, item by item for a plan. Every kind gets every amendment since
its creation, each with the fields it changed (a plan item removed at any
revision stays listed, with the packet and review reason that removed it),
its mandatory closure (never cut), optional one-hop context (cut by
--limit), attention, and a fresh look at git HEAD, dirty state and the time
in this checkout. Writes nothing: there is no handoff record.
`, define: viewVerb("continue")},
		{name: "show", args: "[RECORD_ID]", summary: "one record, or a summary and every current record", detail: `show ID is that record's detail, superseded or not. Bare show opens with
counts per status, attention and owed work, then every current record by
kind, then every run, unsealed ones included. --stale is HEAD-only: it
cannot see uncommitted changes.
`, define: viewVerb("show")},
		{name: "history", args: "[RECORD_ID]", summary: "admitted events in order, and per-packet reviews", detail: `With an ID: every revision and every event or run that names it. Without:
every event and every per-packet review. An amending event's row shows what
it changed from the revision before (fields added, removed or changed; a
plan item by its record id) and its review's reason, computed from the two
recorded revisions. A review.admit row's author is the reviewer it records.
--self-admitted cannot be combined with an ID; false excludes unknown, and
an unknown author stays UNKNOWN.
`, define: viewVerb("history")},
		{name: "ui", summary: "a local, read-only viewer of the views in a browser", detail: `Serves todo, show, continue and history as a web page on a random
127.0.0.1 port, for as long as the command runs (Ctrl-C stops it). The URL
it prints carries a one-time token: the first visit trades it for a session
cookie, and the URL opens nothing after that (run ui again for a new one).
The page writes no ledger. Its Gallery Remove captures a retraction packet
for admission through the CLI, attributed to --actor or WHOSAIDSO_ACTOR.
Reveal in Finder opens the file manager on
macOS. Every screen is the --json answer of the same
view; the Gallery is whosaidso gallery list's. Run inside a
checkout it opens that checkout; elsewhere it lists every project this
machine registered with whosaidso home PATH, an unavailable one with its reason.
`, define: uiVerb},
		{name: "capture", summary: "write a packet to immutable intake; publishes nothing", detail: `Reads a JSON array of typed events (whosaidso template makes one). A
source.intake is captured with its original bytes, from its reference or
from --blob; capture refuses a source whose bytes it cannot save. Prints
"captured PACKET (N events) command ID", and (without --json) on stderr
each id identified as new ("new      claim.assert id = ID"); with --json those ids are in
the answer's created list instead (event, type, path, id), for scripts; a raw task.amend
lists none, since its event cannot tell new criteria from carried ones. A packet that still
holds template placeholders is refused with every one named at once. With --admit the packet
is then admitted as a second act; if that is refused the capture stands, the packet stays
pending, the retry command is printed and the exit status is 4.
`, define: captureVerb},
		{name: "admit", args: "PACKET_ID ...", summary: "review a packet set; the only publisher", detail: `Admission is the review, and the only command that writes a bundle. Any
actor may admit; one who admits their own packet is recorded as
self-admitted. Prints "admitted|rejected|correction-requested PACKET...
bundle SEQ-ID". Several packet ids are separate arguments.
`, define: admitVerb},
		{name: "handback", summary: "capture an attempt's terminal receipt", detail: `No outcome is defaulted; whosaidso help outcomes defines the nine. The receipt
and any hold are one packet; admit it with whosaidso admit. blocked-mid-task
needs an open hold admitted with it; out-of-scope needs a resume hold with a
reassignment criterion and actor. Only the attempt's holder writes its
receipt, unless --authority-source names an admitted source.intake whose
speaker (not you) approved it and which names the task: then the receipt
cites that authority and stays attributed to you.
`, define: handbackVerb},
		{name: "run", args: "-- ARGV", summary: "run a measurement; capture its start and seal", detail: `Runs ARGV in this checkout without a shell, and captures the start (before
launch) and the seal as two packets; admit both, or pass --admit. The
instrument and any criterion must already be admitted, the criterion in an
earlier bundle. What of the criterion is omitted resolves once, before
launch, to the current admitted one: the claim that alone carries
--criterion-id, the claim's current revision, its one criterion, that
criterion's highest revision there. A named revision is checked, never
replaced; an omission with no single answer is refused with the candidates.
The exact revisions are recorded, and printed on stderr when any was
omitted; the acknowledgement prints the admit command for the packets. A
failed run still prints its packets: it is evidence.
`, define: runVerb},
		{name: "reconcile", summary: "seal a run whose observer died, outcome UNKNOWN", detail: `Captures an UNKNOWN-outcome seal with no reading for an admitted, unsealed
run. It needs an identified actor. Admit its packet with whosaidso admit.
`, define: reconcileVerb},
		{name: "check criterion", summary: "dry-run a criterion: TRUE, FALSE or UNKNOWN", detail: `Evaluates the one criterion.fix in --events with admission's evaluator, as if
one completed run produced the candidate. Writes nothing. Exit 0 TRUE,
1 FALSE, 3 UNKNOWN.
`, define: checkVerb("criterion")},
		{name: "check admission", summary: "dry-run admission; list every refusal", detail: `Puts --events (one uncaptured packet by the actor) and any captured --packet
through the admission gate at this watermark and collects every refusal the
gate's stages allow, with each proof member's criterion verdict and its
instrument's validation. The events packet carries what capture would store
with it: the --blob files and a source.intake's source bytes. Writes nothing.
Exit 0 would-admit, 1 would-refuse. --family CLAIM instead lists every run a
proof of the claim's current criterion must name (earlier revisions and
rejected runs included), confirms the list through the same dry run, shows
each run's criterion verdict, and prints a proof skeleton whose
dispositions, reasons, judgment and verdict stay placeholders. Exit 1 when
the gate disagrees with the list.
`, define: checkVerb("admission")},
		{name: "check claim", args: "ATTEMPT_ID", summary: "may this attempt's holder keep working?", detail: `Reads this checkout's ledger, after a sync, for the claim (task.start or
task.takeover) that minted ATTEMPT_ID. HELD (exit 0): keep working. LOST
(exit 1): another branch claimed the task first and this claim was set aside
at the merge; stop and hand nothing back on it. TAKEN-OVER (exit 1): a
takeover replaced it; stop. ENDED (exit 1): its receipt is admitted or the
task is closed. UNKNOWN (exit 3): no such claim here; sync and ask again.
Each answer names the attempt that holds the task now, when one does, and
the next step. Writes nothing.
`, define: claimCheckVerb},
		{name: "check ledger", summary: "before pushing a merge: is the ledger at HEAD whole for a fresh clone?", detail: `In this checkout, with no cache and no home or intake, replays the ledger
and lists: bundles and cited content artifacts not committed at HEAD (git's
tracked tree, so a file only in the working tree counts as missing); cited
artifacts whose bytes the store lacks; git pins that do not resolve or that
HEAD cannot reach (a squash or rebase orphans them); set-aside bundles no
setaside.resolve has dealt with. A shallow repository is refused; deepen it
with git fetch --unshallow. Exit 0 clean, 1 problems. Writes nothing.
JSON preserves the existing fields and adds classes: set_asides, git_pins,
replay_error, artifacts_missing, not_committed. Each has state (measured or
unmeasured), count (only when measured), and identities (when nonempty).
Artifacts count distinct missing content digests; git pins use commit:path;
set-asides use sequence:command_id; not_committed uses kind and repository-relative
path; replay_error uses the replay diagnostic. Replay failure leaves the
other classes unmeasured, never zero. If a checkout check cannot run, its
comparison side has check_error and all classes unmeasured.
--against-checkout PATH checks PATH (base) and this checkout (head), printing
per-class base -> head counts and added/resolved identities, even with equal
counts. JSON returns base/head checks, classes with base/head measurements
and added/resolved lists, allowed, and refusals. Exit 0 only if every class
was measured on both sides and no count increased; otherwise exit 1 with
refused: <class> increased: a -> b or refused: <class> unmeasured at base|head.
Both checkouts must declare the same project.
Unreachable pins additionally report git_pin_locations: commit, refs (local
branches and remote-tracking refs containing it), and error if the ref query
could not be measured. Empty refs means none contain it. No fetch is run;
these locations do not remove the pin from git_pins or make the check clean.
`, define: ledgerCheckVerb},
		{name: "check disposal", summary: "list what an artifact.dispose must record", detail: `Prints the support_loss targets an artifact.dispose of exactly this identity
must record at this watermark, and the admitted events citing it. Give --git
when the disposal names a git pin. Writes nothing.
`, define: checkVerb("disposal")},
		{name: "template", args: "EVENT-TYPE", summary: "print a capture-ready JSON skeleton of one event", detail: `Every "<kind: hint>" string is a placeholder to fill. WhoSaidSo fills only what
has one computable answer: minted ids (revision 1), the project id, the
packet author where it is the author or the attempt holder (from --actor or
WHOSAIDSO_ACTOR), a choice with one member, a handback's commits_denied and
reconciliation_owed as false (--set them true, as whosaidso handback's flags
do), and with bind flags the exact references at their
CURRENT revisions: --from copies a record's current spec into an amend or
revise (change what changed), except an instrument's validation, a verdict on
the replaced implementation, which stays a placeholder to judge again; --task, --claim, --criterion and --attempt
fill those references (a proof gets its criterion's whole family as
evidence), --hold finds an open hold for blocker.clear. --pin NAME=PATH
builds a reference from real bytes: PATH (project-relative) is a content pin
(digest, length, a media type the bytes pass), PATH@REV the committed object
(full commit, object format), #POINTER a json-pointer selector, else whole;
a pinned file travels with --capture as a blob. A PATH outside the project
is pinned by content alone: no locator is stored, the bytes are the blob.
--example OUTPUT=FILE lets --pin NAME=OUTPUT pin FILE's bytes under a run
output's name, for a criterion's example (never an observation). What the
pinned bytes state is filled too: a source.intake's original_digest and
length; a criterion's unit, population identity and denominator when every
declaration its selectors read states the same one (else they stay yours).
Judgment never:
assertions, falsifiers, blind spots, reasons, dispositions, verdicts,
acceptance and validation stay placeholders; fill them with --set PATH=VALUE
(paths as the notes print them, e.g. evidence[0].disposition) or by editing.
VALUE is typed by the field: a text field takes it as text (a JSON string
literal is decoded), so a commit like 79461799e564 stays text; a number,
boolean, object or array field takes it as JSON.
Filling one member of a choice chooses it: --set actor.id=X drops
actor.unknown_reason, a key of one member sets the tag, and the other
members' keys nobody filled go; a filled one stays for the gate.
--set PATH=null omits an optional key; a required one is refused. --set or
--pin at a key the tree lacks adds it when the event's schema has it there
(replacement.progress after --from), and at one past a list's end appends
an element (acceptance_witness_refs[1].witness_ref); anything else is refused.
--capture captures the result through capture's own path, refused while any
placeholder remains; an optional key you put nothing into is omitted, and
the ids it minted are named on stderr, or with --json listed in the answer's created list.
--admit then admits it, as capture --admit does. Notes go to stderr. Event types:
` + templateEventList() + "\n", define: templateVerb},
		{name: "task new", summary: "capture a new task from flags", detail: taskNewHelp, define: taskAuthoringVerb(false)},
		{name: "task amend", args: "TASK", summary: "capture changes to a task's current specification", detail: taskAmendHelp, define: taskAuthoringVerb(true)},
		{name: "task close", args: "TASK", summary: "capture a witnessed task closure", detail: taskCloseHelp, define: taskCloseVerb},
		{name: "source add", args: "FILE", summary: "capture original source bytes and attribution", detail: sourceAddHelp, define: sourceAddVerb},

		{name: "gallery add", args: "PATH", summary: "capture a content-pinned gallery entry", detail: `Captures an entry with --title, optional --note and repeatable --task ID.
Prints its entry id on stderr and packet id on stdout. Admit the packet to
publish it, or use --admit --reason TEXT under the project's self-admission
policy. The sha256, byte length and locator are recorded; media bytes are
never copied into the ledger or artifact store. A relative locator resolves
in each checkout. An external absolute locator works only on that machine.
`, define: galleryVerb("add")},
		{name: "gallery list", summary: "admitted entries, verdicts and local availability", detail: `Reads only the ledger. Shows present, missing or changed by comparing the
actual file's sha256 and length with the admitted pin. Availability is a
local observation, never a replay fact. No legacy per-machine list is read.
`, define: galleryVerb("list")},
		{name: "gallery rm", args: "ENTRY", summary: "capture a gallery retraction, never delete media", detail: `Captures gallery.retract. Admit its packet (or --admit --reason TEXT) to
hide the entry. Its history and media file remain intact.
`, define: galleryVerb("rm")},
		{name: "gallery approve", args: "ENTRY", summary: "capture approval of the pinned content", detail: `Requires --by ACTOR, optional --note. The file must still match the entry's
pin. Captures gallery.approve attributed to that actor; it has no effect
until admitted. --admit --reason TEXT uses the usual self-admission policy.
Actor names are asserted, not authenticated.
`, define: galleryVerb("approve")},
		{name: "gallery reject", args: "ENTRY", summary: "capture a visual rejection", detail: `Requires --by ACTOR and --note TEXT. Captures gallery.reject for the pinned
content. Admit its packet to replace the entry's verdict. Rejection prevents
visual acceptance. --admit --reason TEXT uses the usual policy.
`, define: galleryVerb("reject")},
		{name: "gallery import", summary: "capture the retired per-machine gallery once", detail: `Captures each existing local entry as a gallery.add packet. Admit those
packets, or --admit --reason TEXT. A final gallery.import packet seals the
completed import. The old list stays in place and is never
read again after that completion is admitted. list and ui read only the
ledger. Already admitted import ids are skipped, and so is an entry whose
file pin (content and locator) an entry added by hand already holds. An entry
whose file is missing is not imported: the command prints it (legacy id, path,
title) and the sealing gallery.import packet records it with the reason
"file missing", so every entry is accounted for. Any other error fails the
whole import. Re-running after a completed import captures nothing. Captured
packets remain pending until admitted; retry admission instead of importing
them twice.
`, define: galleryVerb("import")},

		{name: "home", args: "[PATH]", summary: "show or set the checkout the viewer opens for this project", detail: `Reads and writes always use the checkout you run them in; this changes
none of them. Bare, shows this checkout and the checkout this machine's
viewer opens for the project when whosaidso ui runs outside any checkout,
and whether it is available. With PATH, registers the checkout at PATH,
which must hold whosaidso.toml declaring the same project id and a readable
ledger. A checkout every read would refuse is not registered: intake that
is not owner-only, a ledger this binary cannot decode, or an artifact store
lacking bytes its admitted records cite (each digest and its citer named).
An entry that cannot be read is replaced.
Optional whosaidso.toml key team = 'worker-a, worker-b' lists actors for todo's
idle alarm. Use a single quoted comma-separated string, following the config's
normal literal or basic string quoting rules. Whitespace around names is
trimmed, duplicates are counted once, and empty names are refused. Omit team
to disable the alarm. It changes reads only and never assigns tasks.
`, define: homeVerb},
		{name: "id", args: "[N]", summary: "print N fresh record ids (default one)", detail: `Use it rather than inventing an id: hand-typed Crockford base32 parses and
means nothing.
`, define: idVerb},
		{name: "version", summary: "which build this is, for a report", detail: `Prints one line: the release tag of a go install build, else the version Go
recorded from the checkout, else dev and the commit it was built from, marked
modified when the checkout had uncommitted changes.
`, define: versionVerb},
	}
}
