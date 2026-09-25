package main

// Tests for bound templates (step 5): every bound template decodes and admits
// through capture's own path, bind flags fill the CURRENT revisions, judgment
// is never filled, and capture refuses a template with any placeholder left,
// including a text placeholder whose type fits the field.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type boundFixture struct {
	root                                            string
	task, claim, instrument, criterion, attempt, ac model.ID
}

// boundWorld admits, in process, a task, a claim, a KNOWN-validated
// instrument, a frozen criterion on the claim and an attempt, all by "agent".
func boundWorld(t *testing.T) boundFixture {
	t.Helper()
	root, _ := cliFixture(t)
	for path, body := range map[string]string{"tools/measure.sh": e2eScript, "validation/measure.json": `{"validated":"against a known pose sweep"}`, "out/result.json": e2ePass} {
		proofWrite(t, root, path, body)
	}
	f := boundFixture{root: root}
	n := 300
	next := func() model.ID { n++; return cliID(n) }
	f.task, f.claim, f.instrument, f.criterion, f.attempt, f.ac = next(), next(), next(), next(), next(), next()
	agent := model.Provenance{SourceRefs: []model.ArtifactRef{}}
	scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
	boundAdmit(t, root, &model.TaskCreate{ID: f.task, Provenance: agent, Spec: model.TaskSpec{Intent: "measure", Subject: "pose sweep", Scope: scope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: f.ac, Revision: 1, Criterion: "measured"}}, ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: "agent"}}},
		&model.ClaimAssert{ID: f.claim, Provenance: agent, Spec: model.ClaimSpec{Assertion: "every pose is below 0.05 mm", Falsifier: "a pose reaches 0.05 mm", Scope: scope, ExternalRefs: []model.ExternalReference{}}},
		&model.InstrumentDeclare{ID: f.instrument, Provenance: agent, Spec: model.InstrumentSpec{QuestionAnswered: "pose penetration depth", BlindTo: "unmeasured poses",
			NotAnswered: "production behaviour", ConfigSurface: []string{}, DangerousDefaults: []string{}, ValidRange: "the fixture sweep",
			ImplementationRef: e2ePin(e2eScript, "tools/measure.sh", "text/plain"),
			Validation:        e2eKnown(model.InstrumentValidation{Ref: e2ePin(`{"validated":"against a known pose sweep"}`, "validation/measure.json", "application/json"), Version: "v1"})}})
	result, population := e2ePin(e2ePass, "out/result.json", "application/json"), e2ePin(e2ePass, "out/result.json", "application/json")
	result.Selector, population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}, model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	boundAdmit(t, root, &model.CriterionFix{Claim: model.RecordRef{Project: "test/cli", RecordID: f.claim, Revision: 1}, CriterionID: f.criterion, Revision: 1, Author: model.Actor{ID: "agent"}, SourceRefs: []model.ArtifactRef{},
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm", Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}})
	boundAdmit(t, root, &model.TaskStart{Task: model.RecordRef{Project: "test/cli", RecordID: f.task, Revision: 1}, Actor: model.Actor{ID: "agent"}, AttemptID: f.attempt})
	return f
}

func boundAdmit(t *testing.T, root string, events ...model.TypedEvent) {
	t.Helper()
	raw := make([]model.Event, len(events))
	for i, e := range events {
		encoded, err := model.EncodeEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		raw[i] = encoded
	}
	data, err := model.Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out, errs, code := cliRun(t, root, data, "agent", "capture", "--admit", "--reason", "fixture"); code != 0 {
		t.Fatalf("fixture admission: %d %s %s", code, out, errs)
	}
}

// boundTemplate prints a template in process and returns its data object.
func boundPrint(t *testing.T, root string, args ...string) map[string]any {
	t.Helper()
	out, errs, code := cliRun(t, root, nil, "agent", append([]string{"template"}, args...)...)
	if code != 0 {
		t.Fatalf("template %v: %d %s", args, code, errs)
	}
	var tree []map[string]any
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		t.Fatalf("template %v printed no event array: %v\n%s", args, err, out)
	}
	return tree[0]["data"].(map[string]any)
}

func boundSnapshot(t *testing.T, root string) reduce.Snapshot {
	t.Helper()
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	s, err := reduce.Replay(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func boundAt(data map[string]any, path string) any {
	steps, err := parseTemplatePath(path)
	if err != nil {
		panic(err)
	}
	var cur any = data
	for _, s := range steps {
		switch n := cur.(type) {
		case map[string]any:
			cur = n[s.key]
		case []any:
			if s.index >= len(n) {
				return nil
			}
			cur = n[s.index]
		default:
			return nil
		}
	}
	return cur
}

func boundCapture(t *testing.T, root string, args ...string) {
	t.Helper()
	full := append(append([]string{"template"}, args...), "--capture", "--admit", "--reason", "bound template")
	if out, errs, code := cliRun(t, root, nil, "agent", full...); code != 0 {
		t.Fatalf("%v: %d %s %s", full, code, out, errs)
	}
}

func openHold(t *testing.T, s reduce.Snapshot, task model.ID) model.ID {
	t.Helper()
	for _, b := range s.Blockers(reduce.Ident{Project: "test/cli", ID: task}) {
		if b.Open() {
			return b.Key.Blocker
		}
	}
	t.Fatal("no open hold")
	return ""
}

// Every bound template, filled only with judgment by --set, captures and
// admits through the real gate; each fills the revision current at the time.
func TestBoundTemplatesDecodeAndAdmit(t *testing.T) {
	f := boundWorld(t)
	witness := `{"kind":"content","content":{"sha256":"` + string(model.HashBytes([]byte(e2ePass))) + `","length":` + fmt.Sprint(len(e2ePass)) +
		`,"media_type":"application/json","locators":[{"path":"out/result.json"}]},"selector":{"kind":"whole"}}`
	boundCapture(t, f.root, "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", `actor={"id":"agent"}`, "--set", "criterion=the fixture output exists")
	hold := openHold(t, boundSnapshot(t, f.root), f.task)
	boundCapture(t, f.root, "blocker.clear", "--hold", string(hold), "--set", "resolving_witness="+witness)
	if b := boundSnapshot(t, f.root).Blockers(reduce.Ident{Project: "test/cli", ID: f.task}); len(b) != 1 || b[0].Open() {
		t.Fatalf("the bound clear did not clear the hold: %+v", b)
	}
	boundCapture(t, f.root, "task.amend", "--from", string(f.task), "--set", "provenance.source_refs=[]", "--set", "replacement.intent=measure the sweep again")
	// The amend moved the task to revision 2: a bound template now names 2.
	if data := boundPrint(t, f.root, "blocker.hold", "--task", string(f.task)); boundAt(data, "task.revision") != float64(2) {
		t.Fatalf("--task must fill the current revision 2, got %v", boundAt(data, "task"))
	}
	boundCapture(t, f.root, "criterion.fix", "--criterion", string(f.criterion), "--set", "source_refs=[]")
	claimRef := model.RecordRef{Project: "test/cli", RecordID: f.claim, Revision: 1}
	if _, ok := boundSnapshot(t, f.root).Criterion(model.CriterionRef{Claim: claimRef, CriterionID: f.criterion, Revision: 2}); !ok {
		t.Fatal("criterion.fix --criterion must admit revision 2")
	}
	if data := boundPrint(t, f.root, "criterion.fix", "--criterion", string(f.criterion)); boundAt(data, "revision") != float64(3) || boundAt(data, "expression.unit") != "mm" {
		t.Fatalf("the next revision is 3, copied from revision 2: %v", data)
	}
	boundCapture(t, f.root, "instrument.revise", "--from", string(f.instrument), "--set", "provenance.source_refs=[]", "--set", "replacement.blind_to=unmeasured poses and scale",
		"--set", "replacement.validation="+boundValidation(t))
	boundCapture(t, f.root, "claim.revise", "--from", string(f.claim), "--set", "provenance.source_refs=[]", "--set", "replacement.scope.limitations=the fixture sweep only")
	s := boundSnapshot(t, f.root)
	for id, want := range map[model.ID]model.Revision{f.task: 2, f.claim: 2, f.instrument: 2} {
		if got, _ := s.CurrentRevision(reduce.Ident{Project: "test/cli", ID: id}); got != want {
			t.Errorf("%s: current revision %d, want %d", id, got, want)
		}
	}
	if rec, _ := s.Current(reduce.Ident{Project: "test/cli", ID: f.claim}); rec.Claim.Assertion != "every pose is below 0.05 mm" || rec.Claim.Scope.Limitations != "the fixture sweep only" {
		t.Fatalf("--from must copy the spec and change only what --set changed: %+v", rec.Claim)
	}
	if data := boundPrint(t, f.root, "task.close", "--task", string(f.task)); len(boundAt(data, "acceptance_witness_refs").([]any)) != 1 ||
		boundAt(data, "acceptance_witness_refs[0].criterion_id") != string(f.ac) || boundAt(data, "task.revision") != float64(2) {
		t.Fatalf("task.close --task lists one witness per acceptance criterion at the current revision: %v", data)
	}
}

// Judgment is never filled: a bound template leaves every judgment field a
// placeholder, whatever the flags computed around it.
func TestBoundTemplatesLeaveJudgment(t *testing.T) {
	f := boundWorld(t)
	boundCapture(t, f.root, "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", `actor={"id":"agent"}`, "--set", "criterion=the fixture output exists")
	hold := openHold(t, boundSnapshot(t, f.root), f.task)
	for _, tc := range []struct {
		args     []string
		judgment []string
	}{
		{[]string{"blocker.hold", "--task", string(f.task)}, []string{"reason", "actor.id", "criterion"}},
		{[]string{"blocker.clear", "--hold", string(hold)}, []string{"resolving_witness.kind", "resolving_witness.selector.kind"}},
		{[]string{"task.close", "--task", string(f.task)}, []string{"outcome", "acceptance_witness_refs[0].witness_ref.kind"}},
		{[]string{"criterion.fix", "--claim", string(f.claim)}, []string{"expression.unit", "expression.operator", "expression.target.type", "expression.reducer",
			"expression.population.identity", "expression.population.denominator", "expression.result_selector.kind", "source_refs[0].kind"}},
		{[]string{"proof.admit", "--claim", string(f.claim)}, []string{"judgment.reason", "verdict"}},
		{[]string{"claim.assert"}, []string{"spec.assertion", "spec.falsifier", "spec.scope.limitations"}},
		{[]string{"instrument.declare"}, []string{"spec.blind_to", "spec.not_answered", "spec.validation.state"}},
		{[]string{"task.create"}, []string{"spec.intent", "spec.acceptance_criteria[0].criterion", "spec.non_goals[0]"}},
		{[]string{"decision.dispose"}, []string{"disposition", "quote"}},
	} {
		data := boundPrint(t, f.root, tc.args...)
		for _, path := range tc.judgment {
			if v, ok := boundAt(data, path).(string); !ok || !isPlaceholder(v) {
				t.Errorf("template %v filled the judgment field %s with %v", tc.args, path, boundAt(data, path))
			}
		}
	}
}

// capture refuses a template while any placeholder remains, including a text
// placeholder whose type fits the field; nothing reaches intake.
func TestTemplateCaptureRefusesAnyPlaceholder(t *testing.T) {
	f := boundWorld(t)
	project, err := store.Discover(f.root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadVerifiedIntake(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The only placeholder left is criterion, a text field: its type fits, and
	// the decoder refuses it too (placeholder_capture_test.go covers that path).
	args := []string{"template", "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", `actor={"id":"agent"}`}
	printed, _, code := cliRun(t, f.root, nil, "agent", args...)
	if err := decodeTemplate([]byte(printed)); code != 0 || err == nil || !strings.Contains(err.Error(), "event.data.criterion") {
		t.Fatalf("control: the one-placeholder template must print, and decode only to a refusal of criterion (%d): %v", code, err)
	}
	out, errs, code := cliRun(t, f.root, nil, "agent", append(args, "--capture")...)
	if code != 1 || out != "" || !strings.Contains(errs, "capture refused: 1 placeholder(s) unfilled: criterion") {
		t.Fatalf("capture must refuse the unfilled criterion: %d %q %q", code, out, errs)
	}
	after, err := store.ReadVerifiedIntake(project, nil)
	if err != nil || len(after) != len(before) {
		t.Fatalf("a refused capture wrote intake: %d -> %d, %v", len(before), len(after), err)
	}
	if _, errs, code := cliRun(t, f.root, nil, "agent", append(args, "--set", "criterion=the fixture output exists", "--capture")...); code != 0 || !regexp.MustCompile(`minted   blocker_id = `+ulid).MatchString(errs) {
		t.Fatalf("control: the filled template must capture and name the id it minted: %d %s", code, errs)
	}
	// An id a bind flag supplied was not minted, so it is not named as minted.
	if _, errs, _ := cliRun(t, f.root, nil, "agent", "template", "criterion.fix", "--criterion", string(f.criterion), "--set", "source_refs=[]", "--capture"); strings.Contains(errs, "minted") {
		t.Fatalf("a bound criterion id is not a minted one: %s", errs)
	}
}

// Every placeholder any template prints is one isPlaceholder recognizes, so
// the capture refusal cannot miss a kind; a filled template holds none.
func TestPlaceholderRecognitionCoversEveryTemplate(t *testing.T) {
	for _, event := range templateEvents {
		body, _, err := buildTemplateTree(event.EventType())
		if err != nil {
			t.Fatal(err)
		}
		var walk func(node any)
		count := 0
		walk = func(node any) {
			switch n := node.(type) {
			case templateObject:
				for _, m := range n {
					if strings.HasPrefix(m.key, "<") {
						if !isPlaceholder(m.key) {
							t.Errorf("%s: key %q is not recognized as a placeholder", event.EventType(), m.key)
						}
						count++ // a placeholder key is one unfilled entry, whatever it holds
						continue
					}
					walk(m.value)
				}
			case []any:
				for _, v := range n {
					walk(v)
				}
			case string:
				if strings.HasPrefix(n, "<") {
					if !isPlaceholder(n) {
						t.Errorf("%s: %q is not recognized as a placeholder", event.EventType(), n)
					}
					count++
				}
			}
		}
		walk(body)
		if count == 0 || len(templatePlaceholders(body, "")) != count {
			t.Errorf("%s: %d placeholders printed, %d found", event.EventType(), count, len(templatePlaceholders(body, "")))
		}
		filled, err := templateValue(fillTemplate(t, event.EventType(), nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		if left := templatePlaceholders(filled, ""); len(left) != 0 {
			t.Errorf("%s: a filled template still reads as unfilled at %v", event.EventType(), left)
		}
	}
}

// A bind flag that fills nothing in the event is a usage error, not a no-op.
func TestTemplateBindFlagMustApply(t *testing.T) {
	f := boundWorld(t)
	for _, args := range [][]string{{"claim.assert", "--task", string(f.task)}, {"task.start", "--from", string(f.task)}, {"task.start", "--admit", "--reason", "x"}} {
		if _, errs, code := cliRun(t, f.root, nil, "agent", append([]string{"template"}, args...)...); code != 2 {
			t.Errorf("%v must be a usage error, got %d %s", args, code, errs)
		}
	}
	if _, errs, code := cliRun(t, f.root, nil, "agent", "template", "claim.revise", "--from", string(f.task)); code != 1 || !strings.Contains(errs, "is a TASK, not a CLAIM") {
		t.Errorf("--from of the wrong kind must be refused, got %d %s", code, errs)
	}
}
