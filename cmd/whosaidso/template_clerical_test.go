package main

// Tests for the last clerical fields (dogfood run 5, items 60-62): a
// task.start's actor is the packet author, --set on one member of a union
// drops the others nobody filled, an optional key the author left unfilled is
// omitted (and --set PATH=null omits it on purpose), and a template handback
// defaults commits_denied and reconciliation_owed to false as the verb does.
// Also capture --events naming the ids its events create (item 65).

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// task.start fills its actor from the packet author; with no known author it
// stays a choice, and --set actor.id picks that member, dropping the other.
func TestTaskStartActorIsThePacketAuthor(t *testing.T) {
	f := boundWorld(t)
	data := boundPrint(t, f.root, "task.start", "--task", string(f.task))
	if actor, ok := boundAt(data, "actor").(map[string]any); !ok || len(actor) != 1 || actor["id"] != "agent" {
		t.Fatalf("task.start must fill actor with the packet author only: %v", boundAt(data, "actor"))
	}
	// Control: an unknown author fills nothing; the choice is still the author's.
	out, _, code := cliRun(t, f.root, nil, "", "template", "task.start", "--task", string(f.task))
	if code != 0 || !strings.Contains(out, `"unknown_reason": "<text`) || !strings.Contains(out, `"id": "<actor-id`) {
		t.Fatalf("with no author the actor stays both placeholders: %d %s", code, out)
	}
	// --set actor.id chooses the id member: unknown_reason goes, and the event
	// captures (the strict decoder refuses an actor holding both members).
	out, errs, code := cliRun(t, f.root, nil, "", "template", "task.start", "--task", string(f.task), "--set", "actor.id=agent", "--actor", "agent", "--capture")
	if code != 0 {
		t.Fatalf("--set actor.id must choose the id member and capture: %d %s %s", code, out, errs)
	}
	project, err := store.Discover(f.root)
	if err != nil {
		t.Fatal(err)
	}
	packets, err := store.ReadVerifiedIntake(project, []model.ID{model.ID(strings.Fields(out)[1])})
	if err != nil || len(packets) != 1 {
		t.Fatalf("the captured packet: %v", err)
	}
	var start model.TaskStart
	if err := json.Unmarshal(packets[0].Packet.Events[0].Data, &start); err != nil || start.Actor != (model.Actor{ID: "agent"}) {
		t.Fatalf("captured actor %+v (%v), want exactly id agent", start.Actor, err)
	}
}

// Union selection through --set: the tag or a member's own key decides, the
// other members' unfilled keys are dropped, and two filled members stay for
// the gate.
func TestSetChoosesAUnionMember(t *testing.T) {
	f := boundWorld(t)
	boundCapture(t, f.root, "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", "actor.id=agent", "--set", "criterion=the fixture output exists")
	hold := openHold(t, boundSnapshot(t, f.root), f.task)
	content := `{"sha256":"` + string(model.HashBytes([]byte(e2ePass))) + `","length":` + fmt.Sprint(len(e2ePass)) + `,"media_type":"application/json","locators":[{"path":"out/result.json"}]}`
	// A member's own key sets the tag: content chooses kind content, pointer json-pointer.
	data := boundPrint(t, f.root, "blocker.clear", "--hold", string(hold), "--set", "resolving_witness.content="+content, "--set", "resolving_witness.selector.pointer=/results")
	if boundAt(data, "resolving_witness.kind") != "content" || boundAt(data, "resolving_witness.git") != nil || boundAt(data, "resolving_witness.selector.kind") != "json-pointer" {
		t.Fatalf("a filled member key must choose its member and drop the other: %v", boundAt(data, "resolving_witness"))
	}
	// Setting the tag drops the other member's keys: whole keeps no pointer.
	data = boundPrint(t, f.root, "blocker.clear", "--hold", string(hold), "--set", "resolving_witness.kind=content", "--set", "resolving_witness.content="+content, "--set", "resolving_witness.selector.kind=whole")
	if sel, _ := boundAt(data, "resolving_witness.selector").(map[string]any); len(sel) != 1 || boundAt(data, "resolving_witness.git") != nil {
		t.Fatalf("kind=whole must drop the unfilled pointer, content the unfilled git: %v", boundAt(data, "resolving_witness"))
	}
	// A filled key of the member not chosen stays (a corroborating pin), for the gate to check.
	data = boundPrint(t, f.root, "blocker.clear", "--hold", string(hold), "--set", "resolving_witness.kind=content", "--set", "resolving_witness.content="+content,
		"--set", `resolving_witness.git={"object_format":"sha1","commit":"0123456789abcdef0123456789abcdef01234567","path":"out/result.json"}`)
	if boundAt(data, "resolving_witness.git.path") != "out/result.json" {
		t.Fatalf("a filled corroborating member must stay: %v", boundAt(data, "resolving_witness"))
	}
	boundCapture(t, f.root, "blocker.clear", "--hold", string(hold), "--set", "resolving_witness.content="+content, "--set", "resolving_witness.selector.kind=whole")
	// Two filled members: no choice is made for the author; both stay.
	out, errs, code := cliRun(t, f.root, nil, "", "template", "task.start", "--task", string(f.task), "--set", "actor.id=agent", "--set", "actor.unknown_reason=not sure")
	if code != 0 || !strings.Contains(out, `"id": "agent"`) || !strings.Contains(out, `"unknown_reason": "not sure"`) {
		t.Fatalf("two filled members must both stay for the gate: %d %s %s", code, out, errs)
	}
}

// A handback through the template needs only its judgment: the two booleans
// default to false as whosaidso handback's do, and --set states them when true.
// A close omits its unfilled optional authority, and --set authority=null
// omits it on purpose; null on a required key is refused.
func TestHandbackAndCloseNeedOnlyJudgment(t *testing.T) {
	f := boundWorld(t)
	data := boundPrint(t, f.root, "attempt.terminal", "--attempt", string(f.attempt), "--set", "commits_denied=true")
	if boundAt(data, "commits_denied") != true || boundAt(data, "reconciliation_owed") != false {
		t.Fatalf("the booleans default false and --set states true: %v", data)
	}
	boundCapture(t, f.root, "attempt.terminal", "--attempt", string(f.attempt), "--set", "outcome=success", "--set", "reason=measured",
		"--set", "next_action=close it", "--set", "delivery_refs=[]")
	close := []string{"task.close", "--task", string(f.task), "--set", "outcome=success", "--pin", "acceptance_witness_refs[0].witness_ref=out/result.json", "--pin", "delivery_witness_refs[0]=out/result.json"}
	if data := boundPrint(t, f.root, append(close, "--set", "authority=null")...); boundAt(data, "authority") != nil {
		t.Fatalf("--set authority=null must omit the key: %v", data)
	}
	if _, errs, code := cliRun(t, f.root, nil, "agent", "template", "task.close", "--set", "outcome=null"); code != 2 || !strings.Contains(errs, "omits only an optional key") {
		t.Fatalf("null on a required key must be refused: %d %s", code, errs)
	}
	// An optional key the author began to fill is not silently dropped.
	if _, errs, code := cliRun(t, f.root, nil, "agent", append(append([]string{"template"}, close...), "--set", "authority.actor.id=agent", "--capture")...); code != 1 || !strings.Contains(errs, "authority.source_ref") {
		t.Fatalf("a half-filled authority must be refused, not omitted: %d %s", code, errs)
	}
	boundCapture(t, f.root, close...)
	if _, closed := boundSnapshot(t, f.root).Closure(reduce.Ident{Project: "test/cli", ID: f.task}); !closed {
		t.Fatal("the close with its authority omitted must admit and close the task")
	}
}

// capture --events names the ids its events create, every element of an
// array included; a criterion.fix past revision 1 creates no id.
func TestCaptureNamesTheIDsItsEventsCreate(t *testing.T) {
	root, data := cliFixture(t)
	_, errs, code := cliRun(t, root, data, "agent", "capture")
	if code != 0 || !strings.Contains(errs, "new      task.create id = "+string(cliID(1))+"\n") ||
		!strings.Contains(errs, "new      task.create spec.acceptance_criteria[0].id = "+string(cliID(2))+"\n") {
		t.Fatalf("capture must name the task and criterion ids it creates: %d %q", code, errs)
	}
	two := []model.Event{{Type: "task.create", Data: json.RawMessage(`{"id":"A","spec":{"acceptance_criteria":[{"id":"B"},{"id":"C"}]}}`)}}
	if got := strings.Join(createdIDs(createdRecords(two, nil), len(two)), "|"); got != "new      task.create id = A|new      task.create spec.acceptance_criteria[0].id = B|new      task.create spec.acceptance_criteria[1].id = C" {
		t.Fatalf("every acceptance criterion's id is named: %s", got)
	}
	f := boundWorld(t)
	printed, _, code := cliRun(t, f.root, nil, "agent", "template", "criterion.fix", "--criterion", string(f.criterion), "--set", "source_refs=[]")
	if code != 0 {
		t.Fatal("control: the revision-2 criterion prints")
	}
	if _, errs, code := cliRun(t, f.root, []byte(printed), "agent", "capture"); code != 0 || strings.Contains(errs, "new ") {
		t.Fatalf("a later criterion revision reuses its id, so none is new: %d %q", code, errs)
	}
	printed, _, _ = cliRun(t, f.root, nil, "agent", "template", "criterion.fix", "--claim", string(f.claim))
	tree, err := templateValue([]byte(printed))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := templateGet(tree, []templateStep{{index: 0}, {key: "data", index: -1}, {key: "criterion_id", index: -1}})
	var events []model.Event
	if err := json.Unmarshal([]byte(printed), &events); err != nil {
		t.Fatal(err)
	}
	if got := createdIDs(createdRecords(events, nil), len(events)); len(got) != 1 || got[0] != fmt.Sprintf("new      criterion.fix criterion_id = %v", id) {
		t.Fatalf("a revision-1 criterion's id is new: %v", got)
	}
}

// A criterion appended to a task.amend is minted at revision 1 and named as
// created; the criteria --from carried and an id the author supplied are not,
// and an amend captured raw names none, since its event cannot tell new from
// carried.
func TestAmendNamesOnlyTheCriteriaItMinted(t *testing.T) {
	root, data := cliFixture(t)
	if out, errs, code := cliRun(t, root, data, "agent", "capture", "--admit", "--reason", "fixture"); code != 0 {
		t.Fatalf("control: the task must admit: %d %s %s", code, out, errs)
	}
	amend := []string{"template", "task.amend", "--from", string(cliID(1)), "--set", "provenance.source_refs=[]"}
	if _, errs, code := cliRun(t, root, nil, "agent", amend...); code != 0 || strings.Contains(errs, "minted   replacement") {
		t.Fatalf("a copied criterion is not noted as minted: %d %s", code, errs)
	}
	if _, errs, code := cliRun(t, root, nil, "agent", append(amend, "--set", "replacement.acceptance_criteria[1].criterion=second",
		"--set", "replacement.acceptance_criteria[1].id="+string(cliID(99)), "--capture")...); code != 0 || strings.Contains(errs, "minted   ") {
		t.Fatalf("carried and supplied ids are not minted: %d %s", code, errs)
	}
	out, errs, code := cliRun(t, root, nil, "agent", append(amend, "--set", "replacement.acceptance_criteria[1].criterion=second",
		"--capture", "--admit", "--reason", "fixture", "--json")...)
	var answer struct {
		Capture captureAnswer `json:"capture"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &answer) != nil {
		t.Fatalf("the appended amend must admit: %d %s %s", code, out, errs)
	}
	rec, ok := boundSnapshot(t, root).Current(reduce.Ident{Project: "test/cli", ID: cliID(1)})
	if !ok || len(rec.Task.AcceptanceCriteria) != 2 || rec.Task.AcceptanceCriteria[1].Revision != 1 {
		t.Fatalf("the appended criterion must land at revision 1: %+v", rec.Task)
	}
	want := []createdRecord{{Event: 0, Type: "task.amend", Path: "replacement.acceptance_criteria[1].id", ID: string(rec.Task.AcceptanceCriteria[1].ID)}}
	if fmt.Sprint(answer.Capture.Created) != fmt.Sprint(want) {
		t.Fatalf("created = %+v, want only the appended criterion %+v", answer.Capture.Created, want)
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	packets, err := store.ReadVerifiedIntake(project, []model.ID{answer.Capture.CommandID})
	if err != nil || len(packets) != 1 {
		t.Fatalf("control: the amend's packet must read back: %v", err)
	}
	raw, _ := json.Marshal(packets[0].Packet.Events)
	if out, errs, code := cliRun(t, root, raw, "agent", "capture", "--json"); code != 0 || !strings.Contains(out, `"created": []`) {
		t.Fatalf("an amend captured raw names no criterion: %d %s %s", code, out, errs)
	}
}
