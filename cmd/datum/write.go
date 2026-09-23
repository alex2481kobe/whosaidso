package main

import (
	"bytes"
	"context"
	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/store"
	"datum/internal/write"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const writeUsage = `datum capture [--command-id ULID] [--actor ID] [--events FILE|-] [--blob FILE ...]
datum admit --command-id ULID [--actor ID] --outcome accepted|rejected|correction-requested --reason TEXT PACKET_ID ...
datum handback [--command-id ULID] [--actor ID] --attempt-id ULID --outcome OUTCOME --reason TEXT --next-action TEXT
              [--commits-denied] [--reconciliation-owed] [--delivery-refs FILE|-]
              [--hold-id ULID --hold-reason REASON --hold-actor ID --hold-criterion TEXT]
datum run [--actor ID] --attempt-id ULID --instrument ID [--claim ID --claim-revision N
          --criterion-id ULID --criterion-revision N] [--timeout DURATION] -- ARGV ...
datum reconcile [--actor ID] --invocation-id ULID --reason TEXT
datum id [N]
datum template EVENT-TYPE
datum criterion check --events FILE [--blob FILE] [--output FILE]
datum proof check [--actor ID] --events FILE [--packet ID ...]

Capture reads a JSON array of typed events and writes only immutable intake.
A source.intake is captured with its original bytes, read from its reference
or from --blob; capture refuses a source whose bytes it cannot save.
Admission reviews a packet set and is the only command that publishes a bundle.
Handback captures a receipt for an admitted attempt; admit its returned packet ID.
OUTCOME is one of nine; no meaning is defaulted. Only success says the work got done,
and it closes the attempt, not the task. Every other outcome leaves the task open.
  success                 the work is done; with no witness the task awaits acceptance
  stopped                 interrupted before finishing; say why and the unfinished step
  refused                 the instrument or tool declined to measure; not a pass
  no-reading              no reading was obtained; say what would get one, never a zero
  measurement-impossible  cannot be measured here; name the missing capability
  runner-died             the process died; observer died too: --reconciliation-owed
  harness-broken          the producer or setup failed, not the work under test
  out-of-scope            needs work outside the task; kept owed, reassignment proposed
  blocked-mid-task        cannot go on until something happens; admitted with its hold
Delivery refs are a JSON array of artifact references. A hold is captured with the receipt.
Blocked-mid-task admission needs an open hold in the same bundle; out-of-scope
needs a resume hold with an authored reassignment criterion and actor.
Hold reasons: prerequisite, awaiting-acceptance, resume, reconciliation.
Missing hold attribution is unknown; --hold-actor never inherits the receipt actor.
Run executes ARGV without a shell, captures its start and seal as two packets
and prints both packet IDs; admit them. The instrument and any criterion must
already be admitted: a criterion admitted after launch cannot freeze this run.
Reconcile captures an UNKNOWN-outcome seal, with no reading, for an admitted run
whose observer died; admit its packet. It needs an identified actor.
Template prints a capture-ready skeleton of any event type; the two checks
dry-run a criterion and a proof through admission's own evaluator and gate,
and write nothing. Each prints its own usage with --help.
Actor falls back to DATUM_ACTOR. Missing attribution is recorded as unknown.
`

type blobPaths []string

func (p *blobPaths) String() string { return strings.Join(*p, ", ") }
func (p *blobPaths) Set(value string) error {
	*p = append(*p, value)
	return nil
}

func writeCLI(ctx context.Context, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(stdout, writeUsage)
		return err
	}
	verb := args[0]
	if verb == "run" || verb == "reconcile" {
		return runCLI(ctx, args, cwd, stdout, stderr, getenv)
	}
	if verb == "template" {
		return templateCLI(args[1:], stdout, stderr)
	}
	if verb == "criterion" || verb == "proof" {
		return checkCLI(ctx, args, cwd, stdin, stdout, stderr, getenv)
	}
	if verb != "capture" && verb != "admit" && verb != "handback" {
		return fmt.Errorf("unavailable-until-integrated: command %q is not enabled by the first gate", verb)
	}
	flags := flag.NewFlagSet(verb, flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("command-id", "", "id retained for retries")
	actor := flags.String("actor", "", "attributed actor")
	var eventsPath, outcome, reason string
	var blobs blobPaths
	var handback write.HandbackRequest
	var attemptID, holdID, holdReason, holdActor, deliveryPath string
	var hold write.HandbackHold
	if verb == "capture" {
		flags.StringVar(&eventsPath, "events", "-", "typed event array file or stdin")
		flags.Var(&blobs, "blob", "file whose exact bytes are captured")
	} else if verb == "admit" {
		flags.StringVar(&outcome, "outcome", "", "review disposition")
		flags.StringVar(&reason, "reason", "", "review reason")
	} else {
		flags.StringVar(&attemptID, "attempt-id", "", "admitted attempt ULID")
		flags.StringVar(&outcome, "outcome", "", "authored outcome: success|stopped|refused|no-reading|measurement-impossible|runner-died|harness-broken|out-of-scope|blocked-mid-task")
		flags.StringVar(&reason, "reason", "", "authored reason (required)")
		flags.StringVar(&handback.NextAction, "next-action", "", "authored next action (required)")
		flags.BoolVar(&handback.CommitsDenied, "commits-denied", false, "commits were denied")
		flags.BoolVar(&handback.ReconciliationOwed, "reconciliation-owed", false, "reconciliation is owed")
		flags.StringVar(&deliveryPath, "delivery-refs", "", "artifact reference array file or stdin (-)")
		flags.StringVar(&holdID, "hold-id", "", "bundled hold ULID")
		flags.StringVar(&holdReason, "hold-reason", "", "prerequisite|awaiting-acceptance|resume|reconciliation")
		flags.StringVar(&holdActor, "hold-actor", "", "authored hold assignment; missing is unknown")
		flags.StringVar(&hold.Criterion, "hold-criterion", "", "authored hold discharge criterion")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	actorSet, holdSet := false, false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "actor" {
			actorSet = true
		}
		if strings.HasPrefix(f.Name, "hold-") {
			holdSet = true
		}
	})
	if !actorSet {
		*actor = getenv("DATUM_ACTOR")
	}
	attribution := model.Actor{ID: *actor}
	if model.Blank(*actor) {
		attribution = model.Actor{UnknownReason: "no actor supplied by --actor or DATUM_ACTOR"}
	}
	project, err := store.Discover(cwd)
	if err != nil {
		return err
	}
	var result any
	if verb == "capture" {
		if len(flags.Args()) != 0 {
			return fmt.Errorf("capture takes event and blob flags, not positional arguments")
		}
		result, err = captureCLI(ctx, project, model.ID(*id), attribution, eventsPath, blobs, stdin)
	} else if verb == "handback" {
		if len(flags.Args()) != 0 {
			return fmt.Errorf("handback takes flags, not positional arguments")
		}
		handback.CommandID, handback.Author = model.ID(*id), attribution
		handback.AttemptID, handback.Outcome, handback.Reason = model.ID(attemptID), model.AttemptOutcome(outcome), reason
		if holdSet {
			hold.BlockerID, hold.Reason, hold.Actor = model.ID(holdID), model.BlockerReason(holdReason), model.Actor{ID: holdActor}
			if model.Blank(holdActor) {
				hold.Actor = model.Actor{UnknownReason: "no actor supplied by --hold-actor"}
			}
			handback.Holds = []write.HandbackHold{hold}
		}
		if deliveryPath != "" {
			if err := handbackDeliveryRefs(deliveryPath, stdin, &handback.DeliveryRefs); err != nil {
				return err
			}
		}
		result, err = write.Handback(ctx, project, handback)
	} else {
		ids := make([]model.ID, len(flags.Args()))
		for i, value := range flags.Args() {
			ids[i] = model.ID(value)
		}
		result, err = write.Admit(ctx, project, write.AdmitRequest{
			CommandID: model.ID(*id), PacketIDs: ids, Admitter: attribution, Outcome: outcome, Reason: reason,
		})
	}
	if err != nil {
		return err
	}
	encoded, err := model.Encode(result)
	if err != nil {
		return err
	}
	_, err = stdout.Write(encoded)
	return err
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

func captureCLI(ctx context.Context, project store.Project, id model.ID, author model.Actor, eventsPath string, blobs []string, stdin io.Reader) (model.PacketRef, error) {
	reader := stdin
	if eventsPath != "-" {
		f, err := os.Open(eventsPath)
		if err != nil {
			return model.PacketRef{}, err
		}
		defer f.Close()
		reader = f
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return model.PacketRef{}, err
	}
	// The model's strict decoder refuses duplicate keys, case aliases such as
	// "TYPE" beside "type", invalid UTF-8 and trailing values, so the packet
	// binds the author's words rather than encoding/json's choice among them.
	events, err := model.DecodeEvents(data)
	if err != nil {
		return model.PacketRef{}, err
	}
	for _, event := range events {
		if _, err := model.DecodeEvent(event); err != nil {
			return model.PacketRef{}, err
		}
	}
	readers := make([]io.Reader, 0, len(blobs))
	for _, path := range blobs {
		f, err := os.Open(path)
		if err != nil {
			return model.PacketRef{}, err
		}
		defer f.Close()
		readers = append(readers, f)
	}
	sources, err := sourceBlobs(ctx, project, events)
	if err != nil {
		return model.PacketRef{}, err
	}
	readers = append(readers, sources...)
	// The store refuses a source.intake whose bytes are not among the blobs.
	return store.WriteIntake(ctx, project, store.IntakeRequest{CommandID: id, Author: author, Blobs: readers, Events: events})
}

// sourceBlobs implements capture durability (contract: intake durably saves
// incoming source before acknowledging capture). Each source.intake's reference
// is resolved inside the datum root, and bytes that verify against the
// original's digest and length are captured into the packet with it, so the
// source survives its original being deleted before admission. A reference that
// does not resolve is left to --blob; store.WriteIntake refuses what neither
// supplied.
func sourceBlobs(ctx context.Context, project store.Project, events []model.Event) ([]io.Reader, error) {
	resolver := evidence.NewResolverAt(project.Root, project.ArtifactDir())
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
