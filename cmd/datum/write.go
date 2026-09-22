package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"datum/internal/model"
	"datum/internal/store"
	"datum/internal/write"
)

const writeUsage = `datum capture [--command-id ULID] [--actor ID] [--events FILE|-] [--blob FILE ...]
datum admit --command-id ULID [--actor ID] --outcome accepted|rejected|correction-requested --reason TEXT PACKET_ID ...

Capture reads a JSON array of typed events and writes only immutable intake.
Admission reviews a packet set and is the only command that publishes a bundle.
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
	if verb != "capture" && verb != "admit" {
		return fmt.Errorf("unavailable-until-integrated: command %q is not enabled by the first gate", verb)
	}
	flags := flag.NewFlagSet(verb, flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("command-id", "", "id retained for retries")
	actor := flags.String("actor", "", "attributed actor")
	var eventsPath, outcome, reason string
	var blobs blobPaths
	if verb == "capture" {
		flags.StringVar(&eventsPath, "events", "-", "typed event array file or stdin")
		flags.Var(&blobs, "blob", "file whose exact bytes are captured")
	} else {
		flags.StringVar(&outcome, "outcome", "", "review disposition")
		flags.StringVar(&reason, "reason", "", "review reason")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	actorSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "actor" {
			actorSet = true
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
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var rawEvents []json.RawMessage
	if err := decoder.Decode(&rawEvents); err != nil {
		return model.PacketRef{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return model.PacketRef{}, fmt.Errorf("capture expects exactly one event array")
	}
	events := make([]model.Event, len(rawEvents))
	for i, raw := range rawEvents {
		// The model codec rejects duplicate fields and unknown envelope keys.
		// Ordinary unmarshalling alone would silently keep the last duplicate.
		if _, err := model.Encode(raw); err != nil {
			return model.PacketRef{}, err
		}
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		if err := d.Decode(&events[i]); err != nil {
			return model.PacketRef{}, err
		}
		if _, err := model.DecodeEvent(events[i]); err != nil {
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
	return store.WriteIntake(ctx, project, store.IntakeRequest{CommandID: id, Author: author, Events: events, Blobs: readers})
}
