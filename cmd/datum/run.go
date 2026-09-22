package main

// This file holds the run and reconcile verbs, which resolve admitted
// instruments and criteria and hand intent to write.Run. Capture, admit and
// handback stay in write.go.

import (
	"context"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
	"flag"
	"fmt"
	"io"
)

// runCLI (run and reconcile) resolves the admitted instrument and criterion from a fresh replay,
// then hands intent to write.Run. A failing measurement still prints its
// packets, because a failed run is family evidence that must be admitted.
func runCLI(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor", "", "attributed actor")
	invocation := flags.String("invocation-id", "", "reconcile: admitted unsealed invocation ULID")
	reason := flags.String("reason", "", "reconcile: why the observer did not seal")
	attempt := flags.String("attempt-id", "", "admitted attempt ULID")
	instrument := flags.String("instrument", "", "admitted instrument id; its current revision is used")
	claim := flags.String("claim", "", "claim id the criterion tests")
	claimRevision := flags.Uint64("claim-revision", 0, "exact claim revision")
	criterion := flags.String("criterion-id", "", "admitted criterion ULID")
	criterionRevision := flags.Uint64("criterion-revision", 0, "exact criterion revision")
	timeout := flags.Duration("timeout", 0, "execution deadline; zero leaves it to the caller")
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	actorSet := false
	flags.Visit(func(f *flag.Flag) { actorSet = actorSet || f.Name == "actor" })
	if !actorSet {
		*actor = getenv("DATUM_ACTOR")
	}
	author := model.Actor{ID: *actor}
	if model.Blank(*actor) {
		author = model.Actor{UnknownReason: "no actor supplied by --actor or DATUM_ACTOR"}
	}
	project, err := store.Discover(cwd)
	if err != nil {
		return err
	}
	if args[0] == "reconcile" {
		ref, err := write.Reconcile(ctx, project, write.ReconcileRequest{Author: author, InvocationID: model.ID(*invocation), Reason: *reason})
		if err != nil {
			return err
		}
		encoded, err := model.Encode(ref)
		if err == nil {
			_, err = stdout.Write(encoded)
		}
		return err
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		return err
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		return err
	}
	current, ok := snapshot.Instrument(reduce.Ident{Project: project.ID, ID: model.ID(*instrument)})
	if !ok {
		return fmt.Errorf("run: instrument %q is not admitted", *instrument)
	}
	request := write.RunRequest{Author: author, AttemptID: model.ID(*attempt), InstrumentRef: model.RecordRef{Project: project.ID, RecordID: current.Instrument.ID, Revision: current.Instrument.Revision},
		Instrument: *current.Spec, Argv: flags.Args(), Timeout: *timeout,
		CriterionRef:            model.Availability[model.CriterionRef]{State: model.Unknown, Reason: "no criterion named for this run"},
		ExecutionSourceIdentity: write.RunExecutionIdentity(ctx, project)}
	if *criterion != "" || *claim != "" {
		ref := model.CriterionRef{Claim: model.RecordRef{Project: project.ID, RecordID: model.ID(*claim), Revision: model.Revision(*claimRevision)}, CriterionID: model.ID(*criterion), Revision: model.Revision(*criterionRevision)}
		if _, ok := snapshot.Criterion(ref); !ok {
			return fmt.Errorf("run: criterion %s revision %d of claim %s revision %d is not admitted; fix and admit it before launch", *criterion, *criterionRevision, *claim, *claimRevision)
		}
		request.CriterionRef = model.Availability[model.CriterionRef]{State: model.Known, Value: &ref}
	}
	result, runErr := write.Run(ctx, project, request)
	if result.StartPacket.CommandID != "" {
		encoded, err := model.Encode(result)
		if err != nil {
			return err
		}
		if _, err := stdout.Write(encoded); err != nil {
			return err
		}
	}
	return runErr
}
