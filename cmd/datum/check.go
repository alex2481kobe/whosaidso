package main

// This file holds the flags and usage of the two dry-run checks, and `datum
// proof check`, which puts a proof.admit and its packet set through the
// admission gate by calling write.CheckAdmission. The criterion check's work
// lives in criterion_check.go. Neither check decides anything itself or
// writes; capture, admission and every rule they apply live elsewhere.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"datum/internal/model"
	"datum/internal/store"
	"datum/internal/write"
)

const criterionCheckUsage = `datum criterion check --events FILE [--blob FILE] [--output FILE]

Evaluates the one criterion.fix in FILE (a JSON event array, as capture reads)
with the evaluator admission uses, as if one completed run of it had produced
a candidate output, and prints TRUE, FALSE or UNKNOWN with the reason.
The candidate is --output when given, else the criterion's pinned example.
--blob supplies the example's bytes when its pin does not resolve in the
project (the file capture --blob would carry). The candidate is placed in a
temporary run directory outside the project; nothing is written.
Not checked here: instrument validation, the criterion family and
comparability across runs. datum proof check asks those.
`

const proofCheckUsage = `datum proof check [--actor ID] --events FILE [--packet ID ...]

Runs an accepted admission of the events in FILE (an uncaptured packet by
--actor, or DATUM_ACTOR) plus any captured --packet through the admission
gate against the published ledger, and prints every refusal it could collect.
Admission stops at its first refusal; the check records it and keeps going
where the gate's stages allow, so later refusals may depend on earlier ones.
Each listed proof member is also shown with its own criterion verdict and its
instrument's validation. Nothing is published, preserved or captured; the
FILE packet carries no blobs (capture it and pass --packet for that).
Exit status is 1 when admission would refuse.
`

func checkCLI(ctx context.Context, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	usage := criterionCheckUsage
	if args[0] == "proof" {
		usage = proofCheckUsage
	}
	if len(args) < 2 || args[1] != "check" {
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			_, err := io.WriteString(stdout, usage)
			return err
		}
		io.WriteString(stderr, usage)
		return fmt.Errorf("expected %s check", args[0])
	}
	flags := flag.NewFlagSet(args[0]+" check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { io.WriteString(stderr, usage) }
	eventsPath := flags.String("events", "", "JSON event array file, or - for stdin")
	blob := flags.String("blob", "", "the criterion example's bytes")
	output := flags.String("output", "", "a candidate run output")
	actor := flags.String("actor", "", "packet author for the events")
	var packets blobPaths
	flags.Var(&packets, "packet", "a captured packet id to admit with the events")
	if err := flags.Parse(args[2:]); err != nil {
		if err == flag.ErrHelp {
			_, err = io.WriteString(stdout, usage)
		}
		return err
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("%s check takes flags, not positional arguments", args[0])
	}
	project, err := store.Discover(cwd)
	if err != nil {
		return err
	}
	var events []model.Event
	if *eventsPath != "" {
		if events, err = readCheckEvents(*eventsPath, stdin); err != nil {
			return err
		}
	}
	if args[0] == "criterion" {
		if *eventsPath == "" {
			return fmt.Errorf("criterion check needs --events")
		}
		return criterionCheck(ctx, project, events, *blob, *output, stdout)
	}
	if *actor == "" {
		*actor = getenv("DATUM_ACTOR")
	}
	author := model.Actor{ID: *actor}
	if model.Blank(*actor) {
		author = model.Actor{UnknownReason: "no actor supplied by --actor or DATUM_ACTOR"}
	}
	var uncaptured []model.Packet
	if len(events) > 0 {
		packet, err := write.UncapturedPacket(project, author, events)
		if err != nil {
			return err
		}
		uncaptured = append(uncaptured, packet)
	}
	ids := make([]model.ID, len(packets))
	for i, id := range packets {
		ids[i] = model.ID(id)
	}
	check, err := write.CheckAdmission(ctx, project, ids, uncaptured, author)
	if err != nil {
		return err
	}
	io.WriteString(stdout, renderProofCheck(check))
	if len(check.Refusals) > 0 {
		return fmt.Errorf("admission would refuse: %d refusal(s) collected", len(check.Refusals))
	}
	return nil
}

func readCheckEvents(name string, stdin io.Reader) ([]model.Event, error) {
	data, err := readCheckFile(name, stdin)
	if err != nil {
		return nil, err
	}
	events, err := model.DecodeEvents(data)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if _, err := model.DecodeEvent(event); err != nil {
			return nil, err
		}
	}
	return events, nil
}

func readCheckFile(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(name)
}

func renderProofCheck(c write.AdmissionCheck) string {
	var b strings.Builder
	fmt.Fprintf(&b, "proof check against the published ledger at sequence %d (dry run; nothing was written)\n", c.Head)
	if len(c.Refusals) == 0 {
		b.WriteString("admission would accept this packet set\n")
	} else {
		fmt.Fprintf(&b, "admission would refuse; %d refusal(s) collected, admission reports the first:\n", len(c.Refusals))
	}
	for i, r := range c.Refusals {
		text := r.Err.Error()
		for _, digest := range c.Unpreserved {
			if strings.Contains(text, string(digest)) {
				text += " [these bytes are in a captured blob admission copies into the artifact store first; the dry run copies nothing, so this may resolve on admission]"
				break
			}
		}
		fmt.Fprintf(&b, "  %d. [%s] %s\n", i+1, r.Stage, text)
	}
	if c.StoppedAt != "" {
		fmt.Fprintf(&b, "stopped at the %s stage: the stages after it need what it refused, so they were not checked\n", c.StoppedAt)
	} else if len(c.Refusals) > 0 {
		b.WriteString("every stage ran: gate, replay (every proof rule), disposals, artifacts (first unresolved artifact only), proofs (each member), quotes\n")
	}
	if len(c.Members) > 0 {
		b.WriteString("proof members:\n")
	}
	for _, m := range c.Members {
		class := string(m.Class)
		if class == "" {
			class = "outside the family"
		}
		fmt.Fprintf(&b, "  %s  %s, disposition %s", m.Invocation.InvocationID, class, m.Disposition)
		if m.Verdict != "" {
			fmt.Fprintf(&b, "; criterion %s", m.Verdict)
			if m.Reason != "" {
				fmt.Fprintf(&b, ": %s", strings.TrimPrefix(m.Reason, string(m.Invocation.InvocationID)+": "))
			}
			if m.Validation == "" {
				b.WriteString("; instrument validation holds")
			} else {
				fmt.Fprintf(&b, "; instrument %s", m.Validation)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
