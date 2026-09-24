package query

// Invariants every ledger's views must keep, checked over viewsWorld, a
// synthetic ledger that carries every fact kind the views read: validated,
// withdrawn and UNKNOWN instruments, proven and unproven claims, held, closed
// and in-flight tasks with their attempts. Exact answers for each view are
// asserted in the views_*_test.go files; these properties hold of any prefix.

import (
	"reflect"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

func invariantLedger(t *testing.T) (store.Project, reduce.Snapshot) {
	t.Helper()
	p := testProject(t)
	viewsWorld(t, p)
	prefix, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := reduce.Replay(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}

func TestInstrumentsViewNeverHidesUnknownValidation(t *testing.T) {
	p, s := invariantLedger(t)
	a := view_(t, p, ViewRequest{View: "show", Kind: "instrument"}).(*ShowAnswer)
	assertViewHonest(t, a)
	if a.Watermark.Bundles != int(a.Watermark.Sequence) || a.Watermark.Bundles == 0 {
		t.Fatalf("the watermark must describe the whole selected prefix, got %+v", a.Watermark)
	}
	views := map[model.RecordRef]InstrumentView{}
	for _, d := range a.Records {
		views[d.Instrument.Ref] = *d.Instrument
	}
	current := s.Instruments()
	if len(views) != len(current) {
		t.Fatalf("show --kind instrument must list every current instrument: %d shown, %d admitted", len(views), len(current))
	}
	raised := map[model.RecordRef]bool{}
	for _, note := range a.Attention {
		if note.Kind == "instrument-validation-unknown" {
			raised[note.Ref] = true
		}
	}
	for _, ip := range current {
		v := views[asRef(ip.Instrument)]
		recorded := ip.Spec.Validation.State == model.Known
		if (v.Validation.State == "KNOWN") != recorded || !recorded && (strings.TrimSpace(v.Validation.Reason) == "" || !raised[v.Ref]) {
			t.Fatalf("instrument %s validation must be KNOWN only when recorded, and UNKNOWN with a raised reason otherwise; got %+v", v.Ref.RecordID, v.Validation)
		}
		if strings.TrimSpace(v.BlindTo) == "" || strings.TrimSpace(v.NotAnswered) == "" {
			t.Fatalf("instrument %s must show what it is blind to and does not answer", v.Ref.RecordID)
		}
	}
	// Identities are permanent in an append-only ledger: both declared
	// instruments, the withdrawn one included, stay listed at whatever revision
	// is current.
	for _, id := range []model.ID{testID(10), testID(11)} {
		rev, ok := s.CurrentRevision(reduce.Ident{Project: p.ID, ID: id})
		if _, shown := views[model.RecordRef{Project: p.ID, RecordID: id, Revision: rev}]; !ok || !shown {
			t.Fatalf("declared instrument %s must remain listed", id)
		}
	}
}

func TestViewsKeepTheirInvariants(t *testing.T) {
	p, s := invariantLedger(t)
	before := treeBytes(t, p.Ledger)
	for _, r := range []ViewRequest{{View: "show"}, {View: "show", Kind: "claim"}, {View: "todo"}} {
		a := view_(t, p, r)
		assertViewHonest(t, a)
		if again := view_(t, p, r); !reflect.DeepEqual(a, again) {
			t.Fatalf("%s must answer identically from the same prefix", r.View)
		}
		if show, ok := a.(*ShowAnswer); ok {
			for _, d := range show.Records {
				if c := d.Claim; c != nil && c.Status != reduce.StatusProven && (c.CurrentSupport == reduce.TruthTrue || !strings.Contains(c.Standing, "not established")) {
					t.Fatalf("%s claim %s reads as established without proof", c.Status, c.Ref.RecordID)
				}
			}
		}
	}
	full := view_(t, p, ViewRequest{View: "todo"}).(*TodoAnswer)
	cut := view_(t, p, ViewRequest{View: "todo", Limit: 1}).(*TodoAnswer)
	if !reflect.DeepEqual(full.Blocked, cut.Blocked) || !reflect.DeepEqual(full.AwaitingAcceptance, cut.AwaitingAcceptance) {
		t.Fatal("a todo limit hid blocked or awaiting-acceptance work")
	}
	for _, task := range s.Tasks() {
		c := view_(t, p, ViewRequest{View: "continue", ID: task.Task.ID}).(*ContinueAnswer)
		narrow := view_(t, p, ViewRequest{View: "continue", ID: task.Task.ID, Limit: 1}).(*ContinueAnswer)
		if !reflect.DeepEqual(c.Closure.Mandatory, narrow.Closure.Mandatory) {
			t.Fatalf("a limit changed task %s's mandatory closure", task.Task.ID)
		}
		assertViewHonest(t, c)
		if len(*c.Attempts) != len(task.Attempts) || c.Watermark != narrow.Watermark {
			t.Fatalf("continue for %s must show every attempt at the same watermark", task.Task.ID)
		}
	}
	if !reflect.DeepEqual(before, treeBytes(t, p.Ledger)) {
		t.Fatal("a read changed the ledger")
	}
}
