package query

// Invariants over this repository's real ledger in .datum/events/. The ledger
// is append-only, so these tests never pin its size, head or record count;
// they check properties that must hold of every prefix it can grow into.

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func realLedger(t *testing.T) (store.Project, reduce.Snapshot) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Discover(cwd)
	if err != nil || p.ID != "datum/datum" {
		t.Fatalf("the repository's own datum.toml must be discoverable from the query package, got %+v, %v", p, err)
	}
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

func TestRealLedgerInstrumentsNeverHideUnknownValidation(t *testing.T) {
	p, s := realLedger(t)
	a := presetAnswer(t, p, Request{Command: "instruments"})
	assertHonestRendering(t, a)
	if a.Watermark.Bundles != int(a.Watermark.Sequence) || a.Watermark.Bundles == 0 {
		t.Fatalf("the watermark must describe the whole selected prefix, got %+v", a.Watermark)
	}
	views := map[model.RecordRef]InstrumentView{}
	for _, v := range *a.Preset.Instruments {
		views[v.Ref] = v
	}
	current := s.Instruments()
	if len(views) != len(current) {
		t.Fatalf("INSTRUMENTS must list every current instrument: %d shown, %d admitted", len(views), len(current))
	}
	raised := map[model.RecordRef]bool{}
	for _, note := range a.Preset.Attention {
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
	// Identities are permanent in an append-only ledger: the two instruments
	// declared at sequence 3 stay listed at whatever revision is current.
	for _, id := range []model.ID{"01M344A7W8PM8PQWTHH5CJBC85", "01M344A7W9XRX1DQQ0KSYG9JSS"} {
		rev, ok := s.CurrentRevision(reduce.Ident{Project: p.ID, ID: id})
		if _, shown := views[model.RecordRef{Project: p.ID, RecordID: id, Revision: rev}]; !ok || !shown {
			t.Fatalf("instrument %s declared at sequence 3 must remain listed", id)
		}
	}
}

func TestRealLedgerPresetsKeepTheirInvariants(t *testing.T) {
	p, s := realLedger(t)
	before := treeBytes(t, p.Ledger)
	for _, command := range []string{"state", "now", "todo", "context"} {
		a := presetAnswer(t, p, Request{Command: command})
		assertHonestRendering(t, a)
		again := presetAnswer(t, p, Request{Command: command})
		if !reflect.DeepEqual(a, again) {
			t.Fatalf("%s must answer identically from the same prefix", command)
		}
		if a.Preset.Claims != nil {
			for _, c := range *a.Preset.Claims {
				if c.Status != reduce.StatusProven && (c.CurrentSupport == reduce.TruthTrue || !strings.Contains(c.Standing, "not established")) {
					t.Fatalf("%s claim %s reads as established without proof", c.Status, c.Ref.RecordID)
				}
			}
		}
	}
	full := presetAnswer(t, p, Request{Command: "todo"})
	cut := presetAnswer(t, p, Request{Command: "todo", Limit: 1})
	if !reflect.DeepEqual(full.Preset.Blocked, cut.Preset.Blocked) || !reflect.DeepEqual(full.Preset.AwaitingAcceptance, cut.Preset.AwaitingAcceptance) {
		t.Fatal("a TODO limit hid blocked or awaiting-acceptance work on the real ledger")
	}
	for _, task := range s.Tasks() {
		ctx := presetAnswer(t, p, Request{Command: "context", ID: task.Task.ID})
		narrow := presetAnswer(t, p, Request{Command: "context", ID: task.Task.ID, Limit: 1})
		if !reflect.DeepEqual(ctx.Preset.Closure.Mandatory, narrow.Preset.Closure.Mandatory) {
			t.Fatalf("a limit changed task %s's mandatory closure", task.Task.ID)
		}
		c := presetAnswer(t, p, Request{Command: "continue", ID: task.Task.ID})
		assertHonestRendering(t, c)
		if len(c.Preset.Continue.Attempts) != len(task.Attempts) || c.Watermark != ctx.Watermark {
			t.Fatalf("continue for %s must show every attempt at the same watermark", task.Task.ID)
		}
	}
	if !reflect.DeepEqual(before, treeBytes(t, p.Ledger)) {
		t.Fatal("a read changed the real ledger")
	}
}
