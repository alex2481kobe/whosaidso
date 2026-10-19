package main

// This file holds the usage registry: the one table every verb, its flags and
// its help come from. Dispatch (main.go), `whosaidso help VERB`, `VERB --help`
// and the help-honesty test all read it. A verb's flags are declared by its
// define function, so help lists the flags the verb really parses. What each
// verb does lives in its own file; the guide's topics live in guide.go.

import (
	"flag"
	"sort"
	"strings"
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
		{name: "help", args: "[TOPIC|VERB]", summary: "this guide, one topic, or one verb's usage",
			detail: "Bare, prints the keyword index and the whole guide. With a topic or verb, prints that part.\n",
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
task lists its claims: attempt, holder, claimed_at, last_activity_at, and,
judged at the time todo runs, idle_for and stale.
--json keeps every packet not accepted in packets_not_accepted, each with its
disposition (pending, captured-elsewhere, correction-requested, rejected) and
captured_in when noted, and totals counts intake_unreviewed,
intake_correction_requested, intake_rejected and intake_elsewhere.
`, define: viewVerb("todo")},
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
The page reads with GET only; it writes no ledger and no intake. Its only
changes are the Gallery's Remove, which drops an entry (never the file), and
Reveal in Finder (macOS). Every screen is the --json answer of the same
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
		{name: "check merge", summary: "the merge gate: is the ledger at a commit whole for a fresh clone?", detail: `Clones this checkout's repository with its full history into a temporary
directory, checks out --ref (default HEAD), and there, with no cache and no
home or intake, replays the ledger and checks: every content artifact an
admitted event cites is in the committed artifact store; every git pin
resolves and is reachable from the commit (a squash or rebase orphans pins);
no bundle is set aside; every bundle in this checkout's ledger is in the
commit. A refused shallow repository needs git fetch --unshallow. Exit 0
clean, 1 problems. Run it on the merge result before pushing main. Writes
nothing in the checkout.
`, define: mergeCheckVerb},
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
		{name: "gallery add", args: "PATH", summary: "show a file to the owner in the viewer's Gallery", detail: `Adds PATH (an existing file) to this project's gallery with a short
one-line title and an optional note, attributed to WHOSAIDSO_ACTOR when set.
Prints the entry id. The gallery is a per-machine list, not a record: it
lives in this machine's WhoSaidSo home, never in the ledger or the
repository. whosaidso ui previews images, videos and text; anything else is
listed with no preview.
`, define: galleryVerb("add")},
		{name: "gallery list", summary: "every gallery entry, present or missing", detail: `Lists each entry with its id, date, whether its file is present or
missing now, who added it, its title, path and note.
`, define: galleryVerb("list")},
		{name: "gallery rm", args: "ID", summary: "remove an entry from the gallery, never its file", detail: `Removes the entry from the gallery. The file it names is not touched.
`, define: galleryVerb("rm")},
		{name: "home", args: "[PATH]", summary: "show or set the checkout the viewer opens for this project", detail: `Reads and writes always use the checkout you run them in; this changes
none of them. Bare, shows this checkout and the checkout this machine's
viewer opens for the project when whosaidso ui runs outside any checkout,
and whether it is available. With PATH, registers the checkout at PATH,
which must hold whosaidso.toml declaring the same project id and a readable
ledger. A checkout every read would refuse is not registered: intake that
is not owner-only, a ledger this binary cannot decode, or an artifact store
lacking bytes its admitted records cite (each digest and its citer named).
An entry that cannot be read is replaced.
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

func helpVerb(fs *flag.FlagSet) func(*call) error {
	return func(c *call) error {
		text, ok := helpFor(strings.Join(c.args, " "))
		if !ok || c.argv != nil {
			return usageError("whosaidso help: no topic or verb %q; run whosaidso help", strings.Join(c.args, " "))
		}
		_, err := c.stdout.Write([]byte(text))
		return err
	}
}

// lookup finds the verb args name, a two-word one first, and returns the
// arguments after its name.
func lookup(args []string) (*verb, []string) {
	for words := 2; words >= 1; words-- {
		if len(args) < words {
			continue
		}
		name := strings.Join(args[:words], " ")
		for i := range registry {
			if registry[i].name == name {
				return &registry[i], args[words:]
			}
		}
	}
	return nil, nil
}

// groupModes lists the modes of a two-word verb group such as check.
func groupModes(group string) []string {
	var modes []string
	for _, v := range registry {
		if g, mode, ok := strings.Cut(v.name, " "); ok && g == group {
			modes = append(modes, mode)
		}
	}
	sort.Strings(modes)
	return modes
}
