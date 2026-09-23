package main

// This file holds `datum check criterion|admission|disposal`: three dry runs
// that write nothing. Each mode prints first what it checked and what it did
// NOT check (its scope), then its result. The criterion evaluation lives in
// criterion_check.go, the admission dry run in write.CheckAdmission, and the
// disposal-loss list in the reducer; no rule lives here.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

// Scope statements: what each mode checked, and what it did NOT.
const (
	scopeCriterion = "criterion preview only: instrument validation, proof-family completeness, comparability and admission were NOT checked"
	scopeAdmission = "admission dry run at watermark %d: full gate, evidence and authority checks; result may change if the ledger moves"
	scopeDisposal  = "disposal-loss preview at watermark %d: admission recomputes and stays the authority; reasons are yours to write"
)

// checkHeader opens every check answer.
type checkHeader struct {
	Mode      string   `json:"mode"`
	Scope     string   `json:"scope"`
	Watermark any      `json:"watermark"`
	Result    string   `json:"result"`
	Reasons   []string `json:"reasons"`
}

type checkRefusal struct {
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

type checkMember struct {
	Invocation  model.InvocationRef `json:"invocation"`
	Class       string              `json:"class"`
	Disposition string              `json:"disposition"`
	Verdict     string              `json:"verdict,omitempty"`
	Reason      string              `json:"reason,omitempty"`
	Validation  string              `json:"validation"`
}

// checkVerb is one `datum check` mode, with only that mode's flags. It
// prints the scope first, then the result; --json prints the whole answer.
func checkVerb(mode string) func(*flag.FlagSet) func(*call) error {
	return func(fs *flag.FlagSet) func(*call) error {
		jsonOutput := jsonFlag(fs)
		var eventsPath, blob, output, digest, git string
		var packets blobPaths
		var actor func(*call) model.Actor
		switch mode {
		case "criterion":
			fs.StringVar(&eventsPath, "events", "", "a JSON event array holding one criterion.fix: a `FILE`, or - for stdin")
			fs.StringVar(&blob, "blob", "", "a `FILE` with the criterion example's bytes, when its pin does not resolve here")
			fs.StringVar(&output, "output", "", "a candidate run output `FILE`; default: the criterion's pinned example")
		case "admission":
			fs.StringVar(&eventsPath, "events", "", "a JSON event array, dry-run as one uncaptured packet: a `FILE`, or - for stdin")
			actor = actorFlag(fs)
			fs.Var(&packets, "packet", "a captured packet `ID` to admit with the events (repeatable)")
		case "disposal":
			fs.StringVar(&digest, "digest", "", "the `SHA256` of the artifact a disposal would name (required)")
			fs.StringVar(&git, "git", "", "the disposal's git pin, as `FORMAT:COMMIT:PATH`, when it names one")
		}
		return func(c *call) error {
			if err := noPositionals(c, "check "+mode); err != nil {
				return err
			}
			if mode == "criterion" && eventsPath == "" || mode == "admission" && eventsPath == "" && len(packets) == 0 {
				return usageError("datum check %s needs --events", mode)
			}
			project, err := c.project()
			if err != nil {
				return err
			}
			var events []model.Event
			if eventsPath != "" {
				if events, err = readCheckEvents(eventsPath, c.stdin); err != nil {
					return err
				}
			}
			var answer any
			var header *checkHeader
			var text string
			switch mode {
			case "criterion":
				a, err := criterionAnswerOf(c.ctx, project, events, blob, output)
				if err != nil {
					return err
				}
				answer, header, text = a, &a.checkHeader, a.text
			case "admission":
				a, err := admissionCheck(c.ctx, project, events, packets, actor(c))
				if err != nil {
					return err
				}
				answer, header, text = a, &a.checkHeader, a.text
			case "disposal":
				a, err := disposalCheck(project, digest, git)
				if err != nil {
					return err
				}
				answer, header, text = a, &a.checkHeader, a.text
			}
			if *jsonOutput {
				encoder := json.NewEncoder(c.stdout)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(answer); err != nil {
					return err
				}
			} else if _, err := io.WriteString(c.stdout, header.Scope+"\nresult: "+header.Result+"\n"+text); err != nil {
				return err
			}
			switch header.Result {
			case string(evidence.False), "would-refuse":
				return &exitError{code: 1}
			case string(evidence.Unknown):
				return &exitError{code: 3}
			}
			return nil
		}
	}
}

type criterionAnswer struct {
	checkHeader
	Evaluation criterionEvaluation `json:"evaluation"`
	Selectors  []model.ArtifactRef `json:"selectors"`
	text       string
}

// criterionAnswerOf previews one criterion over one candidate output. It
// reads no ledger, so its watermark is UNKNOWN, never a guessed position.
func criterionAnswerOf(ctx context.Context, project store.Project, events []model.Event, blob, output string) (*criterionAnswer, error) {
	e, selectors, err := criterionCheck(ctx, project, events, blob, output)
	if err != nil {
		return nil, err
	}
	a := &criterionAnswer{checkHeader: checkHeader{Mode: "criterion", Scope: scopeCriterion, Result: string(e.Verdict), Reasons: []string{},
		Watermark: map[string]string{"state": "UNKNOWN", "reason": "a criterion preview does not read the ledger"}}, Evaluation: e, Selectors: selectors}
	a.text = fmt.Sprintf("criterion %s revision %d on claim %s, over %s\n", e.Criterion.CriterionID, e.Criterion.Revision, e.Criterion.Claim.RecordID, e.Over)
	if e.Reason != "" {
		a.Reasons = append(a.Reasons, e.Reason)
		a.text += "why: " + e.Reason + "\n"
	}
	a.text += "result reading: " + e.ResultReading + "\npopulation reading: " + e.PopulationReading + "\n"
	return a, nil
}

type admissionAnswer struct {
	checkHeader
	Packets  []model.ID     `json:"packets"`
	Refusals []checkRefusal `json:"refusals"`
	Stopped  string         `json:"stopped_at,omitempty"`
	Members  []checkMember  `json:"members"`
	text     string
}

// admissionCheck puts the events (as one uncaptured packet by the actor) and
// any captured packets through the admission gate against the published
// ledger, and collects every refusal the gate's stages allow.
func admissionCheck(ctx context.Context, project store.Project, events []model.Event, packets []string, author model.Actor) (*admissionAnswer, error) {
	var uncaptured []model.Packet
	if len(events) > 0 {
		packet, err := write.UncapturedPacket(project, author, events)
		if err != nil {
			return nil, err
		}
		uncaptured = append(uncaptured, packet)
	}
	ids := make([]model.ID, len(packets))
	for i, id := range packets {
		ids[i] = model.ID(id)
	}
	c, err := write.CheckAdmission(ctx, project, ids, uncaptured, author)
	if err != nil {
		return nil, err
	}
	a := &admissionAnswer{checkHeader: checkHeader{Mode: "admission", Scope: fmt.Sprintf(scopeAdmission, c.Head), Result: "would-admit",
		Watermark: map[string]uint64{"sequence": c.Head}, Reasons: []string{}}, Packets: ids, Refusals: []checkRefusal{}, Members: []checkMember{}, Stopped: c.StoppedAt}
	var b strings.Builder
	if len(c.Refusals) > 0 {
		a.Result = "would-refuse"
		fmt.Fprintf(&b, "%d refusal(s) collected; admission reports the first:\n", len(c.Refusals))
	}
	for i, r := range c.Refusals {
		reason := r.Err.Error()
		for _, digest := range c.Unpreserved {
			if strings.Contains(reason, string(digest)) {
				reason += " [these bytes are in a captured blob admission copies into the artifact store first; the dry run copies nothing, so this may resolve on admission]"
				break
			}
		}
		a.Reasons = append(a.Reasons, reason)
		a.Refusals = append(a.Refusals, checkRefusal{Stage: r.Stage, Reason: reason})
		fmt.Fprintf(&b, "  %d. [%s] %s\n", i+1, r.Stage, reason)
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
		member := checkMember{Invocation: m.Invocation, Class: string(m.Class), Disposition: m.Disposition, Verdict: string(m.Verdict),
			Reason: strings.TrimPrefix(m.Reason, string(m.Invocation.InvocationID)+": "), Validation: m.Validation}
		if member.Class == "" {
			member.Class = "outside the family"
		}
		if member.Validation == "" {
			member.Validation = "holds"
		}
		a.Members = append(a.Members, member)
		fmt.Fprintf(&b, "  %s  %s, disposition %s", m.Invocation.InvocationID, member.Class, m.Disposition)
		if m.Verdict != "" {
			fmt.Fprintf(&b, "; criterion %s", m.Verdict)
			if member.Reason != "" {
				fmt.Fprintf(&b, ": %s", member.Reason)
			}
			fmt.Fprintf(&b, "; instrument validation %s", member.Validation)
		}
		b.WriteByte('\n')
	}
	a.text = b.String()
	return a, nil
}

type disposalAnswer struct {
	checkHeader
	Digest       model.Digest              `json:"digest"`
	Git          *model.GitPin             `json:"git,omitempty"`
	SupportLoss  []model.RecordRef         `json:"support_loss"`
	CitingEvents []reduce.ArtifactCitation `json:"citing_events"`
	text         string
}

// disposalCheck asks the reducer the gate's own question at this watermark:
// which record revisions an artifact.dispose of exactly this identity must
// list in support_loss, and which admitted events cite the artifact. A
// content-pinned citation matches by digest; a git-pinned one only by the
// same git pin.
func disposalCheck(project store.Project, digest, git string) (*disposalAnswer, error) {
	e := model.ArtifactDispose{Digest: model.Digest(digest)}
	if !model.ValidDigest(e.Digest) {
		return nil, usageError("check disposal: --digest must be 64 lowercase sha-256 hex characters")
	}
	var pin *model.GitPin
	if git != "" {
		parts := strings.SplitN(git, ":", 3)
		if len(parts) != 3 {
			return nil, usageError("check disposal: --git must be FORMAT:COMMIT:PATH")
		}
		pin = &model.GitPin{ObjectFormat: parts[0], Commit: parts[1], Path: parts[2]}
		e.Artifact = model.ArtifactRef{Kind: "git", Git: pin, Selector: model.Selector{Kind: "whole"}}
		if err := model.ValidateArtifactRef(e.Artifact, "git"); err != nil {
			return nil, usageError("check disposal: %v", err)
		}
	}
	loaded, err := store.Load(project)
	if err != nil {
		return nil, err
	}
	s := loaded.Snapshot()
	w := s.Watermark()
	a := &disposalAnswer{checkHeader: checkHeader{Mode: "disposal", Scope: fmt.Sprintf(scopeDisposal, w.Sequence), Result: "listed", Reasons: []string{},
		Watermark: map[string]any{"sequence": w.Sequence, "bundle": nil}}, Digest: e.Digest, Git: pin,
		SupportLoss: nonNil(s.DisposalLoss(e)), CitingEvents: nonNil(s.DisposalCitations(e))}
	if w.Bundles > 0 {
		a.Watermark = map[string]any{"sequence": w.Sequence, "bundle": fmt.Sprintf("%08d-%s", w.Sequence, w.CommandID)}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "support_loss: %d\n", len(a.SupportLoss))
	for _, ref := range a.SupportLoss {
		fmt.Fprintf(&b, "  %s rev %d\n", ref.RecordID, ref.Revision)
	}
	fmt.Fprintf(&b, "citing events: %d\n", len(a.CitingEvents))
	for _, c := range a.CitingEvents {
		fmt.Fprintf(&b, "  sequence %d event %d %s\n", c.Origin.Sequence, c.Origin.EventIndex, c.Type)
	}
	a.text = b.String()
	return a, nil
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
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
