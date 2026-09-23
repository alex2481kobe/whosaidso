package main

// This file holds `datum check admission --family CLAIM_ID`: the proof family
// of the claim's current criterion, listed by the gate's own membership rule
// (proofFamily, template_ledger.go) and then confirmed by the gate itself. A
// probe proof naming every listed member, each set aside, goes through
// write.CheckAdmission; a family refusal there means the list is wrong, and
// the probe's member rows are the gate's view of each run. The probe is never
// captured and its dispositions are never offered: the printed proof skeleton
// leaves every disposition, reason, the judgment and the verdict to the author.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

const scopeFamily = "proof family at watermark %d: every run the gate counts for this criterion, confirmed by an admission dry run; dispositions, reasons, judgment and verdict were NOT chosen"

// familyProbe authors the probe proof; it names no real actor and is never captured.
var familyProbe = model.Actor{ID: "datum-family-probe"}

type familyAnswer struct {
	checkHeader
	Criterion model.CriterionRef `json:"criterion"`
	Members   []familyMember     `json:"members"`
	Proof     any                `json:"proof"`
	text      string
}

// familyMember is one run as the gate classifies it. It has no disposition:
// choosing one is the author's judgment.
type familyMember struct {
	Invocation model.InvocationRef `json:"invocation"`
	Class      string              `json:"class"`
	Verdict    string              `json:"verdict,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	Validation string              `json:"validation,omitempty"`
}

// familyCheck lists and confirms the family, and drafts the proof skeleton.
func familyCheck(c *call, project store.Project, claim, criterion string, author model.Actor) (*familyAnswer, error) {
	state, err := store.Load(project)
	if err != nil {
		return nil, err
	}
	ref, err := write.ResolveCriterion(state, project.ID, write.CriterionChoice{Claim: model.ID(claim), CriterionID: model.ID(criterion)})
	if err != nil {
		return nil, fmt.Errorf("check admission --family: %w", err)
	}
	snapshot := state.Snapshot()
	members := proofFamily(snapshot, ref)
	head := snapshot.Watermark().Sequence
	a := &familyAnswer{checkHeader: checkHeader{Mode: "admission", Scope: fmt.Sprintf(scopeFamily, head), Result: "listed", Reasons: []string{},
		Watermark: map[string]uint64{"sequence": head}}, Criterion: ref, Members: []familyMember{}}
	if len(members) > 0 {
		mismatch, rows, err := familyConfirm(c.ctx, project, ref, members)
		if err != nil {
			return nil, err
		}
		a.Members = rows
		if len(mismatch) > 0 {
			a.Result, a.Reasons = "family-mismatch", mismatch
		}
	}
	t := &boundTemplate{c: c, event: "proof.admit", author: author, project: &project, state: &state}
	if t.body, t.notes, err = buildTemplateTree("proof.admit"); err != nil {
		return nil, err
	}
	if err := t.fill(templateBinds{claim: string(ref.Claim.RecordID), criterion: string(ref.CriterionID)}); err != nil {
		return nil, err
	}
	a.Proof = []any{templateObject{{"type", "proof.admit"}, {"data", t.body}}}
	skeleton, err := renderTemplate("proof.admit", t.body)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "criterion %s revision %d on claim %s revision %d: %d member(s), as the gate sees them\n",
		ref.CriterionID, ref.Revision, ref.Claim.RecordID, ref.Claim.Revision, len(a.Members))
	for _, m := range a.Members {
		fmt.Fprintf(&b, "  %s  %s", m.Invocation.InvocationID, m.Class)
		if m.Verdict != "" {
			fmt.Fprintf(&b, "; criterion %s", m.Verdict)
			if m.Reason != "" {
				fmt.Fprintf(&b, ": %s", m.Reason)
			}
			fmt.Fprintf(&b, "; instrument validation %s", m.Validation)
		}
		b.WriteByte('\n')
	}
	for _, r := range a.Reasons {
		fmt.Fprintf(&b, "the gate disagrees with this list: %s\n", r)
	}
	b.WriteString("proof skeleton (each disposition and reason, the judgment and the verdict are yours):\n")
	b.Write(skeleton)
	a.text = b.String()
	return a, nil
}

// familyConfirm puts a probe proof naming exactly the listed members through
// the admission dry run. It returns the gate's family refusals (the list is
// wrong when there are any) and the gate's row for each member.
func familyConfirm(ctx context.Context, project store.Project, ref model.CriterionRef, members []model.InvocationRef) ([]string, []familyMember, error) {
	probe := &model.ProofAdmit{Claim: ref.Claim, CriterionRef: ref, Verdict: model.VerdictSupports,
		Judgment: model.ResponsibleJudgment{Actor: familyProbe, Reason: "family probe"}}
	for _, m := range members {
		probe.Evidence = append(probe.Evidence, model.ObservationDisposition{InvocationRef: m, Disposition: "inconclusive", Reason: "family probe"})
	}
	event, err := model.EncodeEvent(probe)
	if err != nil {
		return nil, nil, err
	}
	packet, err := write.UncapturedPacket(project, familyProbe, []model.Event{event})
	if err != nil {
		return nil, nil, err
	}
	check, err := write.CheckAdmission(ctx, project, nil, []model.Packet{packet}, familyProbe)
	if err != nil {
		return nil, nil, err
	}
	var mismatch []string
	for _, r := range check.Refusals {
		var fault *model.Fault
		if errors.As(r.Err, &fault) && (fault.Code == reduce.CodeIncompleteFamily || fault.Code == reduce.CodeRejectedFamilyMember) {
			mismatch = append(mismatch, r.Err.Error())
		}
	}
	if len(check.Members) != len(members) {
		reason := fmt.Sprintf("the dry run stopped at the %s stage before it saw the family", check.StoppedAt)
		for _, r := range check.Refusals {
			reason += "; " + r.Err.Error()
		}
		mismatch = append(mismatch, reason)
	}
	rows := make([]familyMember, 0, len(check.Members))
	for _, m := range check.Members {
		row := familyMember{Invocation: m.Invocation, Class: string(m.Class), Verdict: string(m.Verdict),
			Reason: strings.TrimPrefix(m.Reason, string(m.Invocation.InvocationID)+": "), Validation: m.Validation}
		if row.Class == "" {
			row.Class = "outside the family"
			mismatch = append(mismatch, fmt.Sprintf("%s is listed but the gate counts it outside the family", m.Invocation.InvocationID))
		}
		if row.Verdict != "" && row.Validation == "" {
			row.Validation = "holds"
		}
		rows = append(rows, row)
	}
	return mismatch, rows, nil
}
