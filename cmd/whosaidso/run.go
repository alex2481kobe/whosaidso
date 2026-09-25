package main

// This file holds the run and reconcile verbs, which resolve admitted
// instruments and criteria and hand intent to write.Run, and what run prints.
// Capture, admit and handback stay in write.go.

import (
	"encoding/base64"
	"flag"
	"fmt"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
	"io"
	"unicode/utf8"
)

// runOutput is what `whosaidso run` prints: snake_case keys, and each tail as
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

// reconcileVerb captures an UNKNOWN-outcome seal, with no reading, for an
// admitted run whose observer died.
func reconcileVerb(fs *flag.FlagSet) func(*call) error {
	actor := actorFlag(fs)
	jsonOutput := jsonFlag(fs)
	invocation := fs.String("invocation-id", "", "the admitted unsealed run's invocation `ULID`")
	reason := fs.String("reason", "", "why the observer did not seal, as `TEXT` (required)")
	return func(c *call) error {
		if err := noPositionals(c, "reconcile"); err != nil {
			return err
		}
		project, err := c.project()
		if err != nil {
			return err
		}
		ref, err := write.Reconcile(c.ctx, project, write.ReconcileRequest{Author: actor(c), InvocationID: model.ID(*invocation), Reason: *reason})
		if err != nil {
			return err
		}
		return printResult(c.stdout, *jsonOutput, ref, fmt.Sprintf("reconcile captured %s (UNKNOWN seal) for %s; admit it: %s\n",
			ref.CommandID, *invocation, admitCommand("", model.Actor{}, "", []model.ID{ref.CommandID})))
	}
}

// runVerb resolves the admitted instrument and criterion from a fresh replay,
// then hands intent to write.Run. A failing measurement still prints its
// packets, because a failed run is family evidence that must be admitted.
func runVerb(fs *flag.FlagSet) func(*call) error {
	actor := actorFlag(fs)
	jsonOutput := jsonFlag(fs)
	attempt := fs.String("attempt-id", "", "the admitted attempt's `ULID`")
	instrument := fs.String("instrument", "", "the admitted instrument `ID`; its current revision is used")
	claim := fs.String("claim", "", "the claim `ID` the criterion tests; omitted, the one claim that carries --criterion-id")
	claimRevision := fs.Uint64("claim-revision", 0, "the exact claim revision `N`; omitted, the current one")
	criterion := fs.String("criterion-id", "", "the admitted criterion `ULID`; omitted, the claim's one criterion")
	criterionRevision := fs.Uint64("criterion-revision", 0, "the exact criterion revision `N`; omitted, the current one")
	timeout := fs.Duration("timeout", 0, "the execution deadline as a `DURATION`; zero leaves it to the caller")
	admitAfter := fs.Bool("admit", false, "then admit the start and seal as accepted under the same actor")
	admitID := fs.String("admit-command-id", "", "--admit: the admission's `ULID`; minted when omitted")
	reason := fs.String("reason", "", "--admit: the review reason `TEXT` (required with --admit)")
	return func(c *call) error {
		if len(c.args) > 0 || len(c.argv) == 0 {
			return usageError("whosaidso run takes its command after --: whosaidso run [flags] -- ARGV")
		}
		if *admitAfter && model.Blank(*reason) {
			return usageError("whosaidso run --admit needs --reason: the review reason is authored, never defaulted")
		}
		if !*admitAfter && (isSet(fs, "reason") || isSet(fs, "admit-command-id")) {
			return usageError("whosaidso run: --reason and --admit-command-id belong to --admit")
		}
		project, err := c.project()
		if err != nil {
			return err
		}
		author := actor(c)
		loaded, err := store.Load(project)
		if err != nil {
			return err
		}
		snapshot := loaded.Snapshot()
		current, ok := snapshot.Instrument(reduce.Ident{Project: project.ID, ID: model.ID(*instrument)})
		if !ok {
			return fmt.Errorf("run: instrument %q is not admitted", *instrument)
		}
		request := write.RunRequest{Author: author, AttemptID: model.ID(*attempt), InstrumentRef: model.RecordRef{Project: project.ID, RecordID: current.Instrument.ID, Revision: current.Instrument.Revision},
			Instrument: *current.Spec, Argv: c.argv, Timeout: *timeout,
			CriterionRef:            model.Availability[model.CriterionRef]{State: model.Unknown, Reason: "no criterion named for this run"},
			ExecutionSourceIdentity: write.RunExecutionIdentity(c.ctx, project)}
		if *criterion != "" || *claim != "" {
			// Omitted parts resolve once, here, to the current admitted ones;
			// the run records the exact revisions it resolved.
			ref, err := write.ResolveCriterion(loaded, project.ID, write.CriterionChoice{Claim: model.ID(*claim), ClaimRevision: model.Revision(*claimRevision),
				CriterionID: model.ID(*criterion), CriterionRevision: model.Revision(*criterionRevision)})
			if err != nil {
				return fmt.Errorf("run: %w; fix and admit it before launch", err)
			}
			if *claim == "" || *claimRevision == 0 || *criterion == "" || *criterionRevision == 0 {
				fmt.Fprintf(c.stderr, "run: claim %s revision %d, criterion %s revision %d (the current admitted ones for what was omitted)\n",
					ref.Claim.RecordID, ref.Claim.Revision, ref.CriterionID, ref.Revision)
			}
			request.CriterionRef = model.Availability[model.CriterionRef]{State: model.Known, Value: &ref}
		}
		result, runErr := write.Run(c.ctx, project, request)
		if result.StartPacket.CommandID == "" {
			return runErr
		}
		printed := printedRun(result)
		packets := []model.ID{result.StartPacket.CommandID}
		if result.SealPacket.CommandID != "" {
			packets = append(packets, result.SealPacket.CommandID)
		}
		if !*admitAfter {
			if err := printResult(c.stdout, *jsonOutput, printed, runAck(result, packets)); err != nil {
				return err
			}
			return runErr
		}
		// A failed measurement is still family evidence: its packets are admitted.
		a, bundle, admitErr := admitCaptured(c.ctx, project, model.ID(*admitID), author, *reason, packets)
		if *jsonOutput {
			out := struct {
				Run runOutput `json:"run"`
				admission
			}{printed, a}
			if err := printResult(c.stdout, true, out, ""); err != nil {
				return err
			}
		} else if admitErr == nil {
			io.WriteString(c.stdout, runAck(result, packets))
		}
		if err := reportAdmission(c.stdout, *jsonOutput, a, bundle, admitErr, packets); err != nil {
			return err
		}
		return runErr
	}
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
