package main

// This file holds the bind flags that read the admitted ledger through the
// home: --from copies a record's current spec, --task, --hold and --attempt
// fill exact task references at the revision admission checks, and --claim
// and --criterion fill a criterion.fix's next revision or a proof's current
// criterion and its whole family (reduce.CriterionFamily, the gate's own
// membership rule). Nothing here writes; the tree lives in template_tree.go.

import (
	"encoding/json"
	"fmt"
	"strconv"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

// ledger loads the home's admitted prefix once.
func (t *boundTemplate) ledger() (store.Project, store.State, error) {
	if t.state == nil {
		p, err := t.c.project()
		if err != nil {
			return store.Project{}, store.State{}, err
		}
		s, err := store.Load(p)
		if err != nil {
			return store.Project{}, store.State{}, err
		}
		t.project, t.state = &p, &s
	}
	return *t.project, *t.state, nil
}

func revisionNumber(r model.Revision) json.Number {
	return json.Number(strconv.FormatUint(uint64(r), 10))
}

// bindLedger applies the bind flags that were given.
func (t *boundTemplate) bindLedger(b templateBinds) error {
	switch {
	case b.from != "":
		return t.bindFrom(model.ID(b.from))
	case b.hold != "":
		return t.bindHold(model.ID(b.hold), model.ID(b.task))
	case b.task != "":
		return t.bindTask(model.ID(b.task))
	case b.attempt != "":
		return t.bindAttempt(model.ID(b.attempt))
	case t.event == "criterion.fix" && (b.claim != "" || b.criterion != ""):
		return t.bindCriterionFix(model.ID(b.claim), model.ID(b.criterion))
	case t.event == "proof.admit" && (b.claim != "" || b.criterion != ""):
		return t.bindProof(model.ID(b.claim), model.ID(b.criterion))
	}
	return nil
}

// current is the record's current admitted revision, which must be of kind.
func (t *boundTemplate) current(id model.ID, kind model.Kind) (model.RecordRef, reduce.Record, error) {
	p, s, err := t.ledger()
	if err != nil {
		return model.RecordRef{}, reduce.Record{}, err
	}
	rec, ok := s.Snapshot().Current(reduce.Ident{Project: p.ID, ID: id})
	if !ok {
		return model.RecordRef{}, reduce.Record{}, fmt.Errorf("template: record %s is not admitted in %s", id, p.ID)
	}
	if rec.Kind != kind {
		return model.RecordRef{}, reduce.Record{}, fmt.Errorf("template: %s is a %s, not a %s", id, rec.Kind, kind)
	}
	return model.RecordRef{Project: p.ID, RecordID: id, Revision: rec.Key.Revision}, rec, nil
}

// bindFrom copies the record's current spec, as written, as the replacement,
// except the judgments replacementRejudged names, which stay placeholders.
// The revise names that revision as its target.
func (t *boundTemplate) bindFrom(id model.ID) error {
	kind := map[model.EventType]model.Kind{"task.amend": model.Task, "claim.revise": model.Claim,
		"decision.revise": model.Decision, "instrument.revise": model.Instrument}[t.event]
	ref, rec, err := t.current(id, kind)
	if err != nil {
		return err
	}
	var spec any
	switch kind {
	case model.Task:
		spec = rec.Task
	case model.Claim:
		spec = rec.Claim
	case model.Decision:
		spec = rec.Decision
	case model.Instrument:
		spec = rec.Instrument
	}
	from := fmt.Sprintf("%s revision %d", id, ref.Revision)
	if err := t.put("target", ref, from); err != nil {
		return err
	}
	// The replacement's re-judged fields go back to the template's own
	// placeholders; everything else is the author's wording, copied.
	var rejudge []templateMember
	for _, path := range replacementRejudged[kind] {
		steps, _ := parseTemplatePath(path)
		if skeleton, ok := templateGet(t.body, steps); ok {
			rejudge = append(rejudge, templateMember{path, templateClone(skeleton)})
		}
	}
	if err := t.put("replacement", spec, from+"'s spec as written: change what changed"); err != nil {
		return err
	}
	for _, r := range rejudge {
		steps, _ := parseTemplatePath(r.key)
		if t.body, err = templateSet(t.body, steps, r.value, ""); err != nil {
			return err
		}
		t.filled = append(t.filled, r.key+": NOT copied from "+from+"; a verdict on the replaced spec, judge it again")
	}
	return nil
}

// replacementRejudged are the replacement fields --from leaves as placeholders:
// a judgment bound to the revision being replaced, never carried to the next.
// An instrument's validation is the admitter's verdict on THAT implementation;
// WhoSaidSo never fills a judgment. The rest of every spec is the
// author's own description (assertion, falsifier, blind spots, scope, options)
// and is copied as written.
var replacementRejudged = map[model.Kind][]string{
	model.Instrument: {"replacement.validation"},
}

// bindTask fills the task at its current revision, the one admission checks
// a task event against. A close gets one acceptance witness per criterion of
// that revision, each witness left to the author.
func (t *boundTemplate) bindTask(id model.ID) error {
	ref, rec, err := t.current(id, model.Task)
	if err != nil {
		return err
	}
	from := fmt.Sprintf("task %s current revision %d", id, ref.Revision)
	if err := t.put("task", ref, from); err != nil {
		return err
	}
	switch t.event {
	case "blocker.clear":
		return t.put("hold_ref.task", ref, from)
	case "task.close":
		steps, _ := parseTemplatePath("acceptance_witness_refs[0]")
		skeleton, _ := templateGet(t.body, steps)
		witnesses := []any{}
		for _, c := range rec.Task.AcceptanceCriteria {
			w := templateClone(skeleton)
			w, _ = templateSet(w, []templateStep{{key: "criterion_id", index: -1}}, string(c.ID), "")
			w, _ = templateSet(w, []templateStep{{key: "criterion_revision", index: -1}}, revisionNumber(c.Revision), "")
			witnesses = append(witnesses, w)
		}
		return t.put("acceptance_witness_refs", witnesses, from+"'s acceptance criteria; each witness_ref is yours")
	}
	return nil
}

// bindHold finds the open hold (on the named task, when --task is given) and
// fills the clear's task, blocker and hold reference. What the hold waits for
// is printed: the resolving witness must show it, and choosing it is judgment.
func (t *boundTemplate) bindHold(id, task model.ID) error {
	p, s, err := t.ledger()
	if err != nil {
		return err
	}
	var found []reduce.Blocker
	for _, tp := range s.Snapshot().Tasks() {
		for _, b := range tp.Blockers {
			if b.Key.Project == p.ID && b.Key.Blocker == id && (task == "" || b.Key.Task == task) {
				found = append(found, b)
			}
		}
	}
	switch {
	case len(found) == 0:
		return fmt.Errorf("template: no admitted hold %s", id)
	case len(found) > 1:
		return fmt.Errorf("template: hold %s is on %d tasks; name one with --task", id, len(found))
	case !found[0].Open():
		return fmt.Errorf("template: hold %s is already cleared", id)
	}
	if err := t.bindTask(found[0].Key.Task); err != nil {
		return err
	}
	who := found[0].Actor.ID
	if who == "" {
		who = "unknown (" + found[0].Actor.UnknownReason + ")"
	}
	from := fmt.Sprintf("hold %s (%s, waiting on %s; discharged when: %s)", id, found[0].Reason, who, found[0].Criterion)
	if err := t.put("blocker_id", string(id), from); err != nil {
		return err
	}
	return t.put("hold_ref.blocker_id", string(id), from)
}

// bindAttempt fills the attempt and its task: a receipt carries the revision
// the attempt started against, a takeover the current one.
func (t *boundTemplate) bindAttempt(id model.ID) error {
	p, s, err := t.ledger()
	if err != nil {
		return err
	}
	for _, tp := range s.Snapshot().Tasks() {
		for _, a := range tp.Attempts {
			if a.Key.Project != p.ID || a.Key.Attempt != id {
				continue
			}
			if t.event == "task.takeover" {
				if err := t.bindTask(a.Key.Task); err != nil {
					return err
				}
				return t.put("prior_attempt_id", string(id), "the attempt taken over")
			}
			ref := model.RecordRef{Project: p.ID, RecordID: a.Key.Task, Revision: a.TaskRevision}
			if err := t.put("task", ref, fmt.Sprintf("the revision attempt %s started against", id)); err != nil {
				return err
			}
			return t.put("attempt_id", string(id), "--attempt")
		}
	}
	return fmt.Errorf("template: no admitted attempt %s", id)
}

// bindCriterionFix fills the claim at its current revision and the revision
// this criterion.fix takes there. Revising an existing criterion (--criterion)
// copies its latest expression and policy as written: change what changed.
func (t *boundTemplate) bindCriterionFix(claim, criterion model.ID) error {
	_, s, err := t.ledger()
	if err != nil {
		return err
	}
	criteria, err := write.AdmittedCriteria(s)
	if err != nil {
		return err
	}
	// The filter stays what the author named for the whole enumeration: a
	// criterion on two claims is ambiguous unless --claim chose one.
	var latest *model.CriterionRef
	for i, c := range criteria {
		if criterion != "" && c.CriterionID == criterion && (claim == "" || c.Claim.RecordID == claim) {
			if latest != nil && latest.Claim.RecordID != c.Claim.RecordID {
				return fmt.Errorf("template: criterion %s is fixed on several claims; name one with --claim", criterion)
			}
			if latest == nil || c.Claim.Revision > latest.Claim.Revision || c.Claim.Revision == latest.Claim.Revision && c.Revision > latest.Revision {
				latest = &criteria[i]
			}
		}
	}
	if latest != nil {
		claim = latest.Claim.RecordID
	}
	if criterion != "" && latest == nil {
		return fmt.Errorf("template: criterion %s is not admitted%s", criterion, map[bool]string{true: " on claim " + string(claim)}[claim != ""])
	}
	ref, _, err := t.current(claim, model.Claim)
	if err != nil {
		return err
	}
	if err := t.put("claim", ref, fmt.Sprintf("claim %s current revision %d", claim, ref.Revision)); err != nil {
		return err
	}
	id := criterion
	if id == "" {
		minted, _ := templateGet(t.body, []templateStep{{key: "criterion_id", index: -1}})
		id = model.ID(fmt.Sprint(minted))
	} else {
		fixed, _ := s.Snapshot().Criterion(*latest)
		from := fmt.Sprintf("criterion %s revision %d as written: change what changed", criterion, latest.Revision)
		for _, f := range []struct {
			path string
			v    any
		}{{"criterion_id", string(criterion)}, {"expression", fixed.Fix.Expression}, {"policy", fixed.Fix.Policy}} {
			if err := t.put(f.path, f.v, from); err != nil {
				return err
			}
		}
	}
	if criterion == "" {
		return nil // a fresh criterion's revision 1 is already filled (templateMintRevisions)
	}
	next := write.NextCriterionRevision(criteria, ref, id)
	return t.put("revision", revisionNumber(next), fmt.Sprintf("the next revision of %s on claim revision %d", id, ref.Revision))
}

// bindProof fills the proof's claim and current criterion revision and lists
// its whole family as evidence: every member reduce.CriterionFamily admits,
// each disposition and reason left to the author.
func (t *boundTemplate) bindProof(claim, criterion model.ID) error {
	p, s, err := t.ledger()
	if err != nil {
		return err
	}
	ref, err := write.ResolveCriterion(s, p.ID, write.CriterionChoice{Claim: claim, CriterionID: criterion})
	if err != nil {
		return fmt.Errorf("template: %w", err)
	}
	from := fmt.Sprintf("claim %s revision %d, criterion %s revision %d (current)", ref.Claim.RecordID, ref.Claim.Revision, ref.CriterionID, ref.Revision)
	if err := t.put("claim", ref.Claim, from); err != nil {
		return err
	}
	if err := t.put("criterion_ref", ref, from); err != nil {
		return err
	}
	steps, _ := parseTemplatePath("evidence[0]")
	skeleton, _ := templateGet(t.body, steps)
	evidence := []any{}
	for _, m := range proofFamily(s.Snapshot(), ref) {
		e := templateClone(skeleton)
		invocation, _ := templateTree(m)
		e, _ = templateSet(e, []templateStep{{key: "invocation_ref", index: -1}}, invocation, "")
		evidence = append(evidence, e)
	}
	return t.put("evidence", evidence, fmt.Sprintf("the criterion's whole family (%d runs); each disposition and reason is yours", len(evidence)))
}

// proofFamily is every run a proof of criterion must list, by the gate's own
// membership rule: each admitted invocation reduce.CriterionFamily counts, then
// each run only a non-accepted review recorded that the ledger never admitted.
func proofFamily(s reduce.Snapshot, criterion model.CriterionRef) []model.InvocationRef {
	var out []model.InvocationRef
	for _, inv := range s.Invocations() {
		if member, _ := reduce.CriterionFamily(inv.Start.CriterionRef, criterion); member {
			out = append(out, model.InvocationRef{Project: inv.Key.Project, InvocationID: inv.Key.InvocationID})
		}
	}
	seen := map[model.InvocationRef]bool{}
	for _, r := range s.Reviews() {
		if r.Outcome == "accepted" {
			continue
		}
		for _, fact := range r.Invocations {
			ref := model.InvocationRef{Project: r.Key.Project, InvocationID: fact.InvocationID}
			if member, _ := reduce.CriterionFamily(fact.CriterionRef, criterion); !member || seen[ref] {
				continue
			}
			if _, admitted := s.Invocation(reduce.InvocationKey{Project: ref.Project, InvocationID: ref.InvocationID}); !admitted {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	return out
}
