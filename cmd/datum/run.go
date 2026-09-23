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
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"time"
	"unicode/utf8"
)

// runOutput is what `datum run` prints: snake_case keys, and each tail as
// readable text when its bytes are valid UTF-8, otherwise as base64 under a
// _base64 key, so exactly one of the pair is present and a reader can tell.
type runOutput struct {
	Envelope         model.InvocationEnvelope `json:"envelope"`
	StartPacket      model.PacketRef          `json:"start_packet"`
	SealPacket       model.PacketRef          `json:"seal_packet"`
	ArtifactDir      string                   `json:"artifact_dir"`
	StdoutTail       *string                  `json:"stdout_tail,omitempty"`
	StdoutTailBase64 *string                  `json:"stdout_tail_base64,omitempty"`
	StderrTail       *string                  `json:"stderr_tail,omitempty"`
	StderrTailBase64 *string                  `json:"stderr_tail_base64,omitempty"`
}

func printedRun(r write.RunResult) runOutput {
	out := runOutput{Envelope: r.Envelope, StartPacket: r.StartPacket, SealPacket: r.SealPacket, ArtifactDir: r.ArtifactDir}
	out.StdoutTail, out.StdoutTailBase64 = tail(r.StdoutTail)
	out.StderrTail, out.StderrTailBase64 = tail(r.StderrTail)
	return out
}

func tail(b []byte) (text, encoded *string) {
	if utf8.Valid(b) {
		s := string(b)
		return &s, nil
	}
	s := base64.StdEncoding.EncodeToString(b)
	return nil, &s
}

// runCLI (run and reconcile) resolves the admitted instrument and criterion from a fresh replay,
// then hands intent to write.Run. A failing measurement still prints its
// packets, because a failed run is family evidence that must be admitted.
func runCLI(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor", "", "attributed actor")
	jsonOutput := flags.Bool("json", false, "print the full result as JSON")
	reason := flags.String("reason", "", "reconcile: why the observer did not seal; run: the review reason for --admit")
	var invocation, attempt, instrument, claim, criterion, admitID string
	var claimRevision, criterionRevision uint64
	var timeout time.Duration
	var admitAfter bool
	if args[0] == "reconcile" {
		flags.StringVar(&invocation, "invocation-id", "", "admitted unsealed invocation ULID")
	} else {
		flags.StringVar(&attempt, "attempt-id", "", "admitted attempt ULID")
		flags.StringVar(&instrument, "instrument", "", "admitted instrument id; its current revision is used")
		flags.StringVar(&claim, "claim", "", "claim id the criterion tests")
		flags.Uint64Var(&claimRevision, "claim-revision", 0, "exact claim revision")
		flags.StringVar(&criterion, "criterion-id", "", "admitted criterion ULID")
		flags.Uint64Var(&criterionRevision, "criterion-revision", 0, "exact criterion revision")
		flags.DurationVar(&timeout, "timeout", 0, "execution deadline; zero leaves it to the caller")
		flags.BoolVar(&admitAfter, "admit", false, "then admit the start and seal as accepted under the same actor")
		flags.StringVar(&admitID, "admit-command-id", "", "--admit: the admission's id; minted when omitted")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return usageError("%v", err)
	}
	actorSet, reasonSet, admitIDSet := false, false, false
	flags.Visit(func(f *flag.Flag) {
		actorSet = actorSet || f.Name == "actor"
		reasonSet = reasonSet || f.Name == "reason"
		admitIDSet = admitIDSet || f.Name == "admit-command-id"
	})
	if args[0] == "run" && admitAfter && model.Blank(*reason) {
		return usageError("run --admit needs --reason: the review reason is authored, never defaulted")
	}
	if args[0] == "run" && !admitAfter && (reasonSet || admitIDSet) {
		return usageError("run: --reason and --admit-command-id belong to --admit")
	}
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
		if flags.NArg() != 0 {
			return usageError("reconcile takes flags, not positional arguments")
		}
		ref, err := write.Reconcile(ctx, project, write.ReconcileRequest{Author: author, InvocationID: model.ID(invocation), Reason: *reason})
		if err != nil {
			return err
		}
		return printResult(stdout, *jsonOutput, ref, fmt.Sprintf("reconcile captured %s (UNKNOWN seal) for %s; admit it: %s\n",
			ref.CommandID, invocation, admitCommand("", model.Actor{}, "", []model.ID{ref.CommandID})))
	}
	loaded, err := store.Load(project)
	if err != nil {
		return err
	}
	snapshot := loaded.Snapshot()
	current, ok := snapshot.Instrument(reduce.Ident{Project: project.ID, ID: model.ID(instrument)})
	if !ok {
		return fmt.Errorf("run: instrument %q is not admitted", instrument)
	}
	request := write.RunRequest{Author: author, AttemptID: model.ID(attempt), InstrumentRef: model.RecordRef{Project: project.ID, RecordID: current.Instrument.ID, Revision: current.Instrument.Revision},
		Instrument: *current.Spec, Argv: flags.Args(), Timeout: timeout,
		CriterionRef:            model.Availability[model.CriterionRef]{State: model.Unknown, Reason: "no criterion named for this run"},
		ExecutionSourceIdentity: write.RunExecutionIdentity(ctx, project)}
	if criterion != "" || claim != "" {
		ref := model.CriterionRef{Claim: model.RecordRef{Project: project.ID, RecordID: model.ID(claim), Revision: model.Revision(claimRevision)}, CriterionID: model.ID(criterion), Revision: model.Revision(criterionRevision)}
		if _, ok := snapshot.Criterion(ref); !ok {
			return fmt.Errorf("run: criterion %s revision %d of claim %s revision %d is not admitted; fix and admit it before launch", criterion, criterionRevision, claim, claimRevision)
		}
		request.CriterionRef = model.Availability[model.CriterionRef]{State: model.Known, Value: &ref}
	}
	result, runErr := write.Run(ctx, project, request)
	if result.StartPacket.CommandID == "" {
		return runErr
	}
	printed := printedRun(result)
	packets := []model.ID{result.StartPacket.CommandID}
	if result.SealPacket.CommandID != "" {
		packets = append(packets, result.SealPacket.CommandID)
	}
	if !admitAfter {
		if err := printResult(stdout, *jsonOutput, printed, runAck(result, packets)); err != nil {
			return err
		}
		return runErr
	}
	// A failed measurement is still family evidence: its packets are admitted.
	a, bundle, admitErr := admitCaptured(ctx, project, model.ID(admitID), author, *reason, packets)
	if *jsonOutput {
		out := struct {
			Run runOutput `json:"run"`
			admission
		}{printed, a}
		if err := printResult(stdout, true, out, ""); err != nil {
			return err
		}
	} else if admitErr == nil {
		io.WriteString(stdout, runAck(result, packets))
	}
	if err := reportAdmission(stdout, *jsonOutput, a, bundle, admitErr, packets); err != nil {
		return err
	}
	return runErr
}

// runAck is `run INVOCATION exit CODE; start PACKET seal PACKET; admit: ...`.
func runAck(r write.RunResult, packets []model.ID) string {
	exit := "UNKNOWN"
	if o := r.Envelope.Outcome; o.State == model.Known && o.Value != nil {
		switch {
		case o.Value.ExitCode != nil:
			exit = fmt.Sprint(*o.Value.ExitCode)
		case o.Value.Signal != nil:
			exit = "signal " + *o.Value.Signal
		default:
			exit = o.Value.Kind
		}
	}
	seal := "none"
	if r.SealPacket.CommandID != "" {
		seal = string(r.SealPacket.CommandID)
	}
	return fmt.Sprintf("run %s exit %s; start %s seal %s; admit: %s\n", r.Envelope.InvocationID, exit, r.StartPacket.CommandID, seal,
		admitCommand("", model.Actor{}, "", packets))
}
