package main

// This file holds the capture, admit and handback verbs and what capture
// reads: events, blobs and the source bytes a source.intake names. run and
// reconcile live in run.go; acknowledgements and --admit's second act in
// acks.go.

import (
	"bytes"
	"context"
	"crypto/rand"
	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/store"
	"datum/internal/write"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type blobPaths []string

func (p *blobPaths) String() string { return strings.Join(*p, ", ") }
func (p *blobPaths) Set(value string) error {
	*p = append(*p, value)
	return nil
}

// captureVerb writes one immutable intake packet and publishes nothing; with
// --admit it then admits that packet as a second act (acks.go).
func captureVerb(fs *flag.FlagSet) func(*call) error {
	id := fs.String("command-id", "", "the packet's `ULID`; minted when omitted, given for an exact retry")
	actor := actorFlag(fs)
	jsonOutput := jsonFlag(fs)
	eventsPath := fs.String("events", "-", "the typed event array: a `FILE`, or - for stdin")
	var blobs blobPaths
	fs.Var(&blobs, "blob", "a `FILE` whose exact bytes are captured with the packet (repeatable)")
	admitAfter := fs.Bool("admit", false, "then admit the packet as accepted under the same actor")
	admitID := fs.String("admit-command-id", "", "--admit: the admission's `ULID`; minted when omitted")
	reason := fs.String("reason", "", "--admit: the review reason `TEXT` (required with --admit)")
	return func(c *call) error {
		if err := noPositionals(c, "capture"); err != nil {
			return err
		}
		if *admitAfter && model.Blank(*reason) {
			return usageError("datum capture --admit needs --reason: the review reason is authored, never defaulted")
		}
		if !*admitAfter && (isSet(fs, "reason") || isSet(fs, "admit-command-id")) {
			return usageError("datum capture: --reason and --admit-command-id belong to --admit")
		}
		open := c.checkout
		if *admitAfter {
			open = c.project
		}
		project, err := open()
		if err != nil {
			return err
		}
		author := actor(c)
		ref, events, err := captureCLI(c.ctx, project, model.ID(*id), author, *eventsPath, blobs, c.stdin)
		if err != nil {
			return err
		}
		count := len(events)
		for _, line := range createdIDs(events) {
			if !*jsonOutput { // --json is the one answer on stdout; notes are for a reader
				fmt.Fprintln(c.stderr, line)
			}
		}
		if *admitAfter {
			return captureAndAdmit(c.ctx, project, c.stdout, *jsonOutput, ref, count, model.ID(*admitID), author, *reason)
		}
		return printResult(c.stdout, *jsonOutput, ref, captureAck(ref, count))
	}
}

// admitVerb reviews a packet set: the only command that publishes a bundle.
func admitVerb(fs *flag.FlagSet) func(*call) error {
	id := fs.String("command-id", "", "the admission's `ULID`; minted when omitted, given for an exact retry")
	actor := actorFlag(fs)
	jsonOutput := jsonFlag(fs)
	outcome := fs.String("outcome", "", "the review `DISPOSITION`: accepted, rejected or correction-requested")
	reason := fs.String("reason", "", "the review reason `TEXT` (required)")
	return func(c *call) error {
		if len(c.args) == 0 || c.argv != nil {
			return usageError("datum admit needs one or more PACKET_ID arguments")
		}
		project, err := c.project()
		if err != nil {
			return err
		}
		ids := make([]model.ID, len(c.args))
		for i, value := range c.args {
			ids[i] = model.ID(value)
		}
		command := model.ID(*id)
		if command == "" {
			if command, err = model.NewID(time.Now(), rand.Reader); err != nil {
				return err
			}
		}
		bundle, err := write.Admit(c.ctx, project, write.AdmitRequest{CommandID: command, PacketIDs: ids, Admitter: actor(c), Outcome: *outcome, Reason: *reason})
		if err != nil {
			return err
		}
		return printResult(c.stdout, *jsonOutput, bundle, admitAck(bundle, *outcome))
	}
}

// handbackVerb captures an attempt's terminal receipt, with its hold when
// one is given; admitting it is a separate act.
func handbackVerb(fs *flag.FlagSet) func(*call) error {
	id := fs.String("command-id", "", "the packet's `ULID`; minted when omitted, given for an exact retry")
	actor := actorFlag(fs)
	jsonOutput := jsonFlag(fs)
	var r write.HandbackRequest
	var hold write.HandbackHold
	attemptID := fs.String("attempt-id", "", "the admitted attempt's `ULID`")
	outcome := fs.String("outcome", "", "the authored `OUTCOME`, one of nine (datum help outcomes)")
	reason := fs.String("reason", "", "the authored reason `TEXT` (required)")
	fs.StringVar(&r.NextAction, "next-action", "", "the authored next action `TEXT` (required)")
	fs.BoolVar(&r.CommitsDenied, "commits-denied", false, "commits were denied")
	fs.BoolVar(&r.ReconciliationOwed, "reconciliation-owed", false, "reconciliation is owed (the observer died too)")
	deliveryPath := fs.String("delivery-refs", "", "an artifact reference array: a `FILE`, or - for stdin")
	holdID := fs.String("hold-id", "", "a hold captured with the receipt: its `ULID`")
	holdReason := fs.String("hold-reason", "", "the hold's `REASON`: prerequisite, awaiting-acceptance, resume or reconciliation")
	holdActor := fs.String("hold-actor", "", "the hold's assigned actor `ID`; missing is unknown, never the receipt's actor")
	fs.StringVar(&hold.Criterion, "hold-criterion", "", "the hold's authored discharge criterion `TEXT`")
	return func(c *call) error {
		if err := noPositionals(c, "handback"); err != nil {
			return err
		}
		project, err := c.project()
		if err != nil {
			return err
		}
		r.CommandID, r.Author = model.ID(*id), actor(c)
		r.AttemptID, r.Outcome, r.Reason = model.ID(*attemptID), model.AttemptOutcome(*outcome), *reason
		holdSet := false
		fs.Visit(func(f *flag.Flag) { holdSet = holdSet || strings.HasPrefix(f.Name, "hold-") })
		if holdSet {
			hold.BlockerID, hold.Reason, hold.Actor = model.ID(*holdID), model.BlockerReason(*holdReason), model.Actor{ID: *holdActor}
			if model.Blank(*holdActor) {
				hold.Actor = model.Actor{UnknownReason: "no actor supplied by --hold-actor"}
			}
			r.Holds = []write.HandbackHold{hold}
		}
		if *deliveryPath != "" {
			if err := handbackDeliveryRefs(*deliveryPath, c.stdin, &r.DeliveryRefs); err != nil {
				return err
			}
		}
		ref, err := write.Handback(c.ctx, project, r)
		if err != nil {
			return err
		}
		return printResult(c.stdout, *jsonOutput, ref, fmt.Sprintf("handback captured %s outcome %s; admit it: %s\n",
			ref.CommandID, *outcome, admitCommand("", model.Actor{}, "", []model.ID{ref.CommandID})))
	}
}

// printResult writes the full result as JSON, or the one-line acknowledgement.
func printResult(stdout io.Writer, jsonOutput bool, result any, ack string) error {
	if !jsonOutput {
		_, err := io.WriteString(stdout, ack)
		return err
	}
	encoded, err := model.Encode(result)
	if err != nil {
		return err
	}
	_, err = stdout.Write(encoded)
	return err
}

func captureAck(ref model.PacketRef, events int) string {
	return fmt.Sprintf("captured %s (%d events) command %s\n", ref.CommandID, events, ref.CommandID)
}

// captureAndAdmit is capture --admit's second step and its report: the
// spec's partial-success JSON with --json, else one line per act.
func captureAndAdmit(ctx context.Context, project store.Project, stdout io.Writer, jsonOutput bool, ref model.PacketRef, count int, admitID model.ID, actor model.Actor, reason string) error {
	packets := []model.ID{ref.CommandID}
	a, bundle, err := admitCaptured(ctx, project, admitID, actor, reason, packets)
	if jsonOutput {
		type captured struct {
			Status    string   `json:"status"`
			CommandID model.ID `json:"command_id"`
			PacketID  model.ID `json:"packet_id"`
		}
		out := struct {
			Capture captured `json:"capture"`
			admission
		}{captured{"captured", ref.CommandID, ref.CommandID}, a}
		if perr := printResult(stdout, true, out, ""); perr != nil {
			return perr
		}
	} else if err == nil {
		io.WriteString(stdout, captureAck(ref, count))
	}
	return reportAdmission(stdout, jsonOutput, a, bundle, err, packets)
}

func handbackDeliveryRefs(path string, stdin io.Reader, refs *[]model.ArtifactRef) error {
	reader := stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		reader = f
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	// The model's strict decoder refuses duplicate keys, case aliases, invalid
	// UTF-8, trailing values and null, so no authored selector is dropped.
	decoded, err := model.DecodeArtifactRefs(data, "delivery_refs")
	if err != nil {
		return err
	}
	*refs = decoded
	return nil
}

func captureCLI(ctx context.Context, project store.Project, id model.ID, author model.Actor, eventsPath string, blobs []string, stdin io.Reader) (model.PacketRef, []model.Event, error) {
	reader := stdin
	if eventsPath != "-" {
		f, err := os.Open(eventsPath)
		if err != nil {
			return model.PacketRef{}, nil, err
		}
		defer f.Close()
		reader = f
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return model.PacketRef{}, nil, err
	}
	// The model's strict decoder refuses duplicate keys, case aliases such as
	// "TYPE" beside "type", invalid UTF-8 and trailing values, so the packet
	// binds the author's words rather than encoding/json's choice among them.
	events, err := model.DecodeEvents(data)
	if err != nil {
		return model.PacketRef{}, nil, err
	}
	for _, event := range events {
		if _, err := model.DecodeEvent(event); err != nil {
			return model.PacketRef{}, nil, err
		}
	}
	readers := make([]io.Reader, 0, len(blobs))
	for _, path := range blobs {
		f, err := os.Open(path)
		if err != nil {
			return model.PacketRef{}, nil, err
		}
		defer f.Close()
		readers = append(readers, f)
	}
	sources, err := sourceBlobs(ctx, project, events)
	if err != nil {
		return model.PacketRef{}, nil, err
	}
	readers = append(readers, sources...)
	// The store refuses a source.intake whose bytes are not among the blobs.
	ref, err := store.WriteIntake(ctx, project, store.IntakeRequest{CommandID: id, Author: author, Blobs: readers, Events: events})
	return ref, events, err
}

// sourceBlobs implements capture durability (contract: intake durably saves
// incoming source before acknowledging capture). Each source.intake's reference
// is resolved inside the invoking checkout, and bytes that verify against the
// original's digest and length are captured into the packet with it, so the
// source survives its original being deleted before admission. A reference that
// does not resolve is left to --blob; store.WriteIntake refuses what neither
// supplied.
func sourceBlobs(ctx context.Context, project store.Project, events []model.Event) ([]io.Reader, error) {
	resolver := evidence.NewResolverAt(project.ExecRoot(), project.ArtifactDir())
	var readers []io.Reader
	for _, raw := range events {
		event, err := model.DecodeEvent(raw)
		if err != nil {
			return nil, err
		}
		source, ok := event.(*model.SourceIntake)
		if !ok {
			continue
		}
		resolved, err := resolver.Resolve(ctx, source.SourceRef)
		if err == nil && resolved.SHA256 == source.OriginalDigest && resolved.Length == source.Length {
			readers = append(readers, bytes.NewReader(resolved.Bytes))
		}
	}
	return readers, nil
}

// createdIDs names the ids the captured events create (template's minted
// paths), so the next command can name them: "new      claim.assert id = ID".
// A criterion.fix past revision 1 reuses its criterion's id, so it is not new.
func createdIDs(events []model.Event) []string {
	var out []string
	for i, event := range events {
		tree, err := templateValue(event.Data)
		if err != nil {
			continue
		}
		if event.Type == "criterion.fix" {
			if rev, _ := templateGet(tree, []templateStep{{key: "revision", index: -1}}); rev != json.Number("1") {
				continue
			}
		}
		for _, path := range templateMints[event.Type] {
			steps, err := parseTemplatePath(path)
			if err != nil {
				continue
			}
			label := string(event.Type)
			if len(events) > 1 {
				label = fmt.Sprintf("event %d %s", i, event.Type)
			}
			for _, found := range templateGetAll(tree, steps, "") {
				out = append(out, fmt.Sprintf("new      %s %s = %v", label, found[0], found[1]))
			}
		}
	}
	return out
}
