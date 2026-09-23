package main

// This file holds the usage registry: the one table every verb, its flags and
// its help come from. Dispatch (main.go), `datum help VERB`, `VERB --help`
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
	detail  string // what `datum help VERB` adds after the flags
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
only section --limit cuts), open decisions, intake pending (unreviewed,
rejected and correction-requested packets with their review), attention.
`, define: viewVerb("todo")},
		{name: "continue", args: "RECORD_ID", summary: "resume any record: detail, closure, what is owed", detail: `Any kind of record. A task adds its progress, every attempt, every run and
what is owed, item by item for a plan. Every kind gets its mandatory closure
(never cut), optional one-hop context (cut by --limit), attention, and a
fresh look at git HEAD, dirty state and the time in this checkout. Writes
nothing: there is no handoff record.
`, define: viewVerb("continue")},
		{name: "show", args: "[RECORD_ID]", summary: "one record, or a summary and every current record", detail: `show ID is that record's detail, superseded or not. Bare show opens with
counts per status, attention and owed work, then every current record by
kind, then every run, unsealed ones included. --stale is HEAD-only: it
cannot see uncommitted changes.
`, define: viewVerb("show")},
		{name: "history", args: "[RECORD_ID]", summary: "admitted events in order, and per-packet reviews", detail: `With an ID: every revision and every event or run that names it. Without:
every event and every per-packet review. --self-admitted cannot be combined
with an ID; false excludes unknown, and legacy facts stay UNKNOWN.
`, define: viewVerb("history")},
		{name: "capture", summary: "write a packet to immutable intake; publishes nothing", detail: `Reads a JSON array of typed events (datum template makes one). A
source.intake is captured with its original bytes, from its reference or
from --blob; capture refuses a source whose bytes it cannot save. Prints
"captured PACKET (N events) command ID". With --admit the packet is then
admitted as a second act; if that is refused the capture stands, the packet
stays pending, the retry command is printed and the exit status is 4.
`, define: captureVerb},
		{name: "admit", args: "PACKET_ID ...", summary: "review a packet set; the only publisher", detail: `Admission is the review, and the only command that writes a bundle. Any
actor may admit; one who admits their own packet is recorded as
self-admitted. Prints "admitted|rejected|correction-requested PACKET...
bundle SEQ-ID". Several packet ids are separate arguments.
`, define: admitVerb},
		{name: "handback", summary: "capture an attempt's terminal receipt", detail: `No outcome is defaulted; datum help outcomes defines the nine. The receipt
and any hold are one packet; admit it with datum admit. blocked-mid-task
needs an open hold admitted with it; out-of-scope needs a resume hold with a
reassignment criterion and actor.
`, define: handbackVerb},
		{name: "run", args: "-- ARGV", summary: "run a measurement; capture its start and seal", detail: `Runs ARGV in this checkout without a shell, and captures the start (before
launch) and the seal as two packets; admit both, or pass --admit. The
instrument and any criterion must already be admitted, the criterion in an
earlier bundle. A failed run still prints its packets: it is evidence.
`, define: runVerb},
		{name: "reconcile", summary: "seal a run whose observer died, outcome UNKNOWN", detail: `Captures an UNKNOWN-outcome seal with no reading for an admitted, unsealed
run. It needs an identified actor. Admit its packet with datum admit.
`, define: reconcileVerb},
		{name: "check criterion", summary: "dry-run a criterion: TRUE, FALSE or UNKNOWN", detail: `Evaluates the one criterion.fix in --events with admission's evaluator, as if
one completed run produced the candidate. Writes nothing. Exit 0 TRUE,
1 FALSE, 3 UNKNOWN.
`, define: checkVerb("criterion")},
		{name: "check admission", summary: "dry-run admission; list every refusal", detail: `Puts --events (one uncaptured packet by the actor) and any captured --packet
through the admission gate at this watermark and collects every refusal the
gate's stages allow, with each proof member's criterion verdict and its
instrument's validation. The events packet carries no blobs. Writes nothing.
Exit 0 would-admit, 1 would-refuse.
`, define: checkVerb("admission")},
		{name: "check disposal", summary: "list what an artifact.dispose must record", detail: `Prints the support_loss targets an artifact.dispose of exactly this identity
must record at this watermark, and the admitted events citing it. Give --git
when the disposal names a git pin. Writes nothing.
`, define: checkVerb("disposal")},
		{name: "template", args: "EVENT-TYPE", summary: "print a capture-ready JSON skeleton of one event", detail: `Every "<kind: hint>" string is a placeholder to fill; an unfilled template
does not decode, so it cannot be captured by accident. Ids the event creates
are minted; every other id is a reference to look up. Reasons, dispositions
and judgments stay placeholders. Choices, optional and minted keys are
listed on stderr. Event types:
` + templateEventList() + "\n", define: templateVerb},
		{name: "home", args: "[PATH]", summary: "show or set where this project's live ledger is", detail: `Bare, shows this project's binding on this machine: its home, or unbound,
and whether the home is available. With PATH, binds the project to the
checkout at PATH, which must hold datum.toml declaring the same project id,
a ledger inside it and a readable history. Binding again elsewhere is the
move: while the old home exists, PATH's history must continue it bundle for
bundle; when it is gone, continuity: not-compared is printed.
`, define: homeVerb},
		{name: "id", args: "[N]", summary: "print N fresh record ids (default one)", detail: `Use it rather than inventing an id: hand-typed Crockford base32 parses and
means nothing.
`, define: idVerb},
	}
}

func helpVerb(fs *flag.FlagSet) func(*call) error {
	return func(c *call) error {
		text, ok := helpFor(strings.Join(c.args, " "))
		if !ok || c.argv != nil {
			return usageError("datum help: no topic or verb %q; run datum help", strings.Join(c.args, " "))
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
