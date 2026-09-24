package query

// Tests for the computed revision diff (revision_diff.go) and the two views
// that show it: history (each amending row) and continue (every amendment of
// the root), plus history's review.admit author. The committed-ledger test
// reads the fixed prefix it is about, bundles 1..76.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

// admitAs captures events as one packet and admits it with reason, so the
// amendment it carries has a recorded review.
func admitAs(t *testing.T, p store.Project, n int, reason string, events ...model.TypedEvent) model.ID {
	t.Helper()
	return admitWrittenBy(t, p, n, "author", reason, events...)
}

// admitWrittenBy is admitAs with a chosen packet author: only the named
// accepter may write an amendment that removes or changes the accepter.
func admitWrittenBy(t *testing.T, p store.Project, n int, author, reason string, events ...model.TypedEvent) model.ID {
	t.Helper()
	raw := []model.Event{}
	for _, e := range events {
		encoded, err := model.EncodeEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, encoded)
	}
	ref, err := store.WriteIntake(context.Background(), p, store.IntakeRequest{CommandID: testID(n),
		Author: model.Actor{ID: author}, Events: raw})
	if err != nil {
		t.Fatalf("control capture must succeed: %v", err)
	}
	if _, err := write.Admit(context.Background(), p, write.AdmitRequest{CommandID: testID(n + 1), PacketIDs: []model.ID{ref.CommandID},
		Admitter: model.Actor{ID: "reviewer"}, Outcome: "accepted", Reason: reason}); err != nil {
		t.Fatalf("control admission must succeed: %v", err)
	}
	return ref.CommandID
}

// changeLines renders changes as "change path before after", values in JSON.
func changeLines(changes []FieldChange) []string {
	out := []string{}
	for _, c := range changes {
		line := c.Change + " " + c.Path
		for _, v := range []any{c.Before, c.After} {
			if v != nil {
				raw, _ := json.Marshal(v)
				line += " " + string(raw)
			}
		}
		out = append(out, line)
	}
	return out
}

func prereq(n int, rev model.Revision) model.Prerequisite {
	return model.Prerequisite{Kind: "task-success", Target: testRef(n, rev), WaiverPolicy: "forbid"}
}

// planWorld: plan 5 over items 2 and 3, then three reviewed amendments:
// drop item 2; retarget 3, add 4, revise and add criteria, name an accepter,
// restate intent, add a non-goal; then drop the accepter again.
func planWorld(t *testing.T, p store.Project) (model.ID, model.ID, model.ID) {
	t.Helper()
	plan := testTask(5)
	plan.Spec.Prerequisites = []model.Prerequisite{prereq(2, 1), prereq(3, 1)}
	item3 := testTask(3).Spec
	item3.Intent = "item three, restated"
	appendEvents(t, p, 100, testTask(2), testTask(3), testTask(4), plan,
		&model.TaskAmend{Target: testRef(3, 1), Replacement: item3, Provenance: testTask(3).Provenance})
	r2 := plan.Spec
	r2.Prerequisites = []model.Prerequisite{prereq(3, 1)}
	first := admitAs(t, p, 200, "item two leaves the plan",
		&model.TaskAmend{Target: testRef(5, 1), Replacement: r2, Provenance: plan.Provenance})
	r3 := r2
	r3.Prerequisites = []model.Prerequisite{prereq(3, 2), prereq(4, 1)}
	r3.AcceptanceCriteria = []model.AcceptanceCriterion{{ID: testID(90), Revision: 2, Criterion: "text and JSON agree exactly"},
		{ID: testID(91), Revision: 1, Criterion: "history shows the diff"}}
	r3.Accepter = &model.Actor{ID: "owner"}
	r3.Intent = "Build U09, amended"
	r3.NonGoals = append([]string{}, r2.NonGoals...)
	r3.NonGoals = append(r3.NonGoals, "rewrite the ledger")
	second := admitAs(t, p, 300, "the plan follows item three",
		&model.TaskAmend{Target: testRef(5, 2), Replacement: r3, Provenance: plan.Provenance})
	r4 := r3
	r4.Accepter = nil
	// Coordinator merge edit 2026-09-24: the accepter-change rule (fix-correct) lets
	// only the named accepter remove it, so owner writes this amendment.
	third := admitWrittenBy(t, p, 400, "owner", "anyone may accept",
		&model.TaskAmend{Target: testRef(5, 3), Replacement: r4,
			Provenance: model.Provenance{Author: model.Actor{ID: "owner"}, SourceRefs: plan.Provenance.SourceRefs}})
	return first, second, third
}

func TestContinueShowsEveryAmendmentOfThePlanWithItsReview(t *testing.T) {
	p := testProject(t)
	first, second, third := planWorld(t, p)
	a := view_(t, p, ViewRequest{View: "continue", ID: testID(5)}).(*ContinueAnswer)
	if a.Amendments == nil || len(*a.Amendments) != 3 {
		t.Fatalf("continue must list all three amendments since creation, not the latest pair; got %+v", a.Amendments)
	}
	got := *a.Amendments
	want := []struct {
		rev     model.Revision
		packet  model.ID
		reason  string
		changes []string
	}{
		{2, first, "item two leaves the plan", []string{
			`removed prerequisites[` + string(testID(2)) + `] {"kind":"task-success","target":{"project":"datum/query-tests","record_id":"` + string(testID(2)) + `","revision":1},"waiver_policy":"forbid"}`}},
		{3, second, "the plan follows item three", []string{
			`changed acceptance_criteria[` + string(testID(90)) + `].criterion "text and JSON agree" "text and JSON agree exactly"`,
			`changed acceptance_criteria[` + string(testID(90)) + `].revision 1 2`,
			`added acceptance_criteria[` + string(testID(91)) + `] {"criterion":"history shows the diff","id":"` + string(testID(91)) + `","revision":1}`,
			`added accepter.id "owner"`,
			`changed intent "Build U09 of WhoSaidSo: the first usable read slice" "Build U09, amended"`,
			`added non_goals "rewrite the ledger"`,
			`changed prerequisites[` + string(testID(3)) + `].target.revision 1 2`,
			`added prerequisites[` + string(testID(4)) + `] {"kind":"task-success","target":{"project":"datum/query-tests","record_id":"` + string(testID(4)) + `","revision":1},"waiver_policy":"forbid"}`}},
		{4, third, "anyone may accept", []string{`removed accepter.id "owner"`}},
	}
	for i, w := range want {
		g := got[i]
		review, _ := g.Review.(AmendmentReview)
		if g.Revision != testRef(5, w.rev) || g.Packet != w.packet || review.Reason != w.reason || review.Actor.ID != "reviewer" {
			t.Fatalf("amendment %d must name revision %d, packet %s and its review %q; got %+v", i, w.rev, w.packet, w.reason, g)
		}
		if lines := changeLines(g.Changes); !reflect.DeepEqual(lines, w.changes) {
			t.Fatalf("amendment %d changes:\n got %q\nwant %q", i, lines, w.changes)
		}
	}
	var text bytes.Buffer
	if err := RenderViewBrief(&text, a); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"amendments: 3", "amendment: rev 2 sequence", "reason: item two leaves the plan",
		"removed prerequisites[" + string(testID(2)) + "]", "changed prerequisites[" + string(testID(3)) + "].target.revision 1 -> 2",
		"changed intent", "before: Build U09 of WhoSaidSo", "after: Build U09, amended", `removed accepter.id`} {
		if !strings.Contains(text.String(), line) {
			t.Fatalf("continue brief must show %q:\n%s", line, text.String())
		}
	}
}

func TestHistoryRowsCarryTheirAmendmentAndReviewAdmitNamesItsReviewer(t *testing.T) {
	p := testProject(t)
	planWorld(t, p)
	cont := view_(t, p, ViewRequest{View: "continue", ID: testID(5)}).(*ContinueAnswer)
	h := historyOf(t, p, testID(5))
	amended := []Amendment{}
	for _, e := range h.Events {
		if e.Event.Type == "task.amend" {
			if e.Amendment == nil || e.Author.Packet != e.Amendment.Packet || e.Author.Packet == "" {
				t.Fatalf("an amending row carries its amendment and its packet author: %+v", e)
			}
			amended = append(amended, *e.Amendment)
		} else if e.Amendment != nil {
			t.Fatalf("only an amending event has an amendment: %+v", e)
		}
	}
	if !reflect.DeepEqual(amended, *cont.Amendments) {
		t.Fatalf("history and continue must show the one computed diff:\n%+v\n%+v", amended, *cont.Amendments)
	}
	reviews := 0
	for _, e := range historyOf(t, p, "").Events {
		if e.Event.Type != "review.admit" {
			continue
		}
		reviews++
		if e.Author != (reduce.PacketAuthor{Author: model.Actor{ID: "reviewer"}}) {
			t.Fatalf("a review.admit is written by its recorded reviewer and names no packet, got %+v", e.Author)
		}
	}
	if reviews != 3 {
		t.Fatalf("control: three reviews were admitted, history shows %d", reviews)
	}
	var text bytes.Buffer
	if err := RenderViewBrief(&text, h); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "reason: the plan follows item three") || !strings.Contains(text.String(), "added accepter.id\n          after: owner") {
		t.Fatalf("history brief must show each amendment's review reason and changes:\n%s", text.String())
	}
}

// Every kind is diffed by the one implementation. An amendment no reviewed
// packet carried keeps its review UNKNOWN with the reason.
func TestEveryKindIsDiffedAndAnUnattributedReviewStaysUnknown(t *testing.T) {
	p := testProject(t)
	claim, decision, instrument := claimSpec(), decisionSpec(), instrumentSpec(false)
	appendEvents(t, p, 100, &model.ClaimAssert{ID: testID(20), Provenance: prov("a"), Spec: claim},
		&model.DecisionOpen{ID: testID(30), Provenance: prov("a"), Spec: decision},
		&model.InstrumentDeclare{ID: testID(10), Provenance: prov("a"), Spec: instrument})
	claim.Falsifier = "two replays of one prefix disagree"
	decision.Options = []string{"yes", "no", "defer"}
	instrument.BlindTo = "generated and vendored code"
	appendEvents(t, p, 101, &model.ClaimRevise{Target: testRef(20, 1), Replacement: claim, Provenance: prov("a")},
		&model.DecisionRevise{Target: testRef(30, 1), Replacement: decision, Provenance: prov("a")},
		&model.InstrumentRevise{Target: testRef(10, 1), Replacement: instrument, Provenance: prov("a")})
	for id, want := range map[int]string{20: `changed falsifier "two replays disagree" "two replays of one prefix disagree"`,
		30: `added options "defer"`, 10: `changed blind_to "generated code" "generated and vendored code"`} {
		a := view_(t, p, ViewRequest{View: "continue", ID: testID(id)}).(*ContinueAnswer)
		if len(*a.Amendments) != 1 || !reflect.DeepEqual(changeLines((*a.Amendments)[0].Changes), []string{want}) {
			t.Fatalf("record %d: want one amendment %q, got %+v", id, want, a.Amendments)
		}
		if (*a.Amendments)[0].Review != unknown("the ledger does not attribute this amendment to a reviewed packet") || (*a.Amendments)[0].Packet != "" {
			t.Fatalf("an unreviewed amendment's review must stay UNKNOWN, got %+v", (*a.Amendments)[0])
		}
	}
	if a := view_(t, p, ViewRequest{View: "continue", ID: testID(20)}).(*ContinueAnswer); a.Amendments == nil {
		t.Fatal("control: a record's amendments are always answered")
	}
}

func TestDiffListRules(t *testing.T) {
	x := func(v string) any { return map[string]any{"record_id": v, "revision": json.Number("1")} }
	for _, tc := range []struct {
		name          string
		before, after map[string]any
		want          []string
	}{
		{"absent and empty lists both say none", map[string]any{"xs": nil}, map[string]any{"xs": []any{}}, []string{}},
		{"a repeated value counts once per copy", map[string]any{"xs": []any{"a", "a"}}, map[string]any{"xs": []any{"a"}}, []string{`removed xs "a"`}},
		{"a reordering alone is not a change (declared blind spot)", map[string]any{"xs": []any{"a", "b"}}, map[string]any{"xs": []any{"b", "a"}}, []string{}},
		{"identified elements match by id", map[string]any{"xs": []any{x("A"), x("B")}}, map[string]any{"xs": []any{x("B"), x("C")}},
			[]string{`removed xs[A] {"record_id":"A","revision":1}`, `added xs[C] {"record_id":"C","revision":1}`}},
		{"a shared id falls back to whole values", map[string]any{"xs": []any{x("A"), x("A")}}, map[string]any{"xs": []any{x("A")}},
			[]string{`removed xs {"record_id":"A","revision":1}`}},
		{"a scalar that goes is removed", map[string]any{"s": "v"}, map[string]any{}, []string{`removed s "v"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := changeLines(diffSpecs(tc.before, tc.after)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Datum's own plan: bundle 75 dropped step 0 (revision 2), bundle 76 retargeted
// two items to revision 2 (revision 3). Comparing only the latest pair would
// hide the removal again; continue must show both, from the fixed prefix 1..76.
func TestCommittedPlanShowsStepZeroRemovedAtRevisionTwo(t *testing.T) {
	paths, err := filepath.Glob("../../.datum/events/*.json")
	if err != nil || len(paths) < 76 {
		t.Fatalf("committed history 1..76 missing: %d %v", len(paths), err)
	}
	var bundles []model.Bundle
	for _, path := range paths[:76] {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := model.DecodeBundle(data)
		if err != nil {
			t.Fatal(err)
		}
		bundles = append(bundles, bundle)
	}
	s, err := reduce.Replay(bundles)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := ReadViewFrom(store.Project{ID: "datum/datum"}, ViewRequest{View: "continue", ID: "01M37TMPM2553VCNV9KK3PXWXP"}, replayed{s, bundles})
	if err != nil {
		t.Fatal(err)
	}
	a := answer.(*ContinueAnswer)
	if a.Record.Revision != 3 || len(*a.Amendments) != 2 {
		t.Fatalf("control: the plan is at revision 3 after two amendments, got %+v %+v", a.Record, a.Amendments)
	}
	for _, item := range a.Owed.Items {
		if item.Target.RecordID == "01M37TMPJ07N34R115HJZ8B57P" {
			t.Fatal("control: step 0 is no longer a current item")
		}
	}
	removal, retarget := (*a.Amendments)[0], (*a.Amendments)[1]
	review, _ := removal.Review.(AmendmentReview)
	if removal.Revision.Revision != 2 || removal.Origin.Sequence != 75 || removal.Packet != "01M391H1RGJMGQVR9JDV7NAF9J" ||
		!strings.HasPrefix(review.Reason, "Step 0 (docs/contract) leaves the plan") ||
		!reflect.DeepEqual(changeLines(removal.Changes)[0][:52], "removed prerequisites[01M37TMPJ07N34R115HJZ8B57P] {\"") || len(removal.Changes) != 1 {
		t.Fatalf("step 0 must read removed at revision 2 in bundle 75 with its review, got %+v", removal)
	}
	if retarget.Origin.Sequence != 76 || !reflect.DeepEqual(changeLines(retarget.Changes), []string{
		"changed prerequisites[01M37TMPK1QBPTT68WCJBPM1ZG].target.revision 1 2",
		"changed prerequisites[01M37TMPKC3EDWA5JG7JP28G7F].target.revision 1 2"}) {
		t.Fatalf("bundle 76 must read as two retargets to revision 2, got %+v", retarget)
	}
}
