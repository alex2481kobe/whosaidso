package write

// Tests for decision.dispose admission (R10.1 as overruled): the contract's
// fields, a nonblank ruling quote and named authority, the packet author kept
// as whoever wrote it, and superseded history staying visible. Decision
// open/revise live in gate_close_test.go; still-refused operations live in
// TestGateOperationsStillUnavailable.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

const disposeRuling = `{"ruling":"ship revision one"}`

type disposeWorld struct {
	f     *admissionFixture
	id    model.ID
	scope model.Scope
}

func newDisposeWorld(t *testing.T) *disposeWorld {
	t.Helper()
	f := newAdmissionFixture(t)
	proofPut(t, f.project.Root, "rulings/decision.json", disposeRuling)
	scope := f.task().Spec.Scope
	open := &model.DecisionOpen{ID: f.id(), Provenance: model.Provenance{Author: f.author, SourceRefs: []model.ArtifactRef{}},
		Spec: model.DecisionSpec{Question: "ship revision one", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: scope}}
	f.accept(f.capture(nil, open))
	return &disposeWorld{f: f, id: open.ID, scope: scope}
}

func (w *disposeWorld) dispose(revision model.Revision, disposition string) *model.DecisionDispose {
	return &model.DecisionDispose{Decision: w.f.ref(w.id, revision), Disposition: disposition, Quote: "  ship revision one\n", Scope: w.scope,
		Authority: model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: proofPin(disposeRuling, "rulings/decision.json"),
			Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: w.scope}}
}

func (w *disposeWorld) decision(t *testing.T, revision model.Revision) reduce.DecisionProjection {
	t.Helper()
	d, ok := w.f.snapshot().DecisionAt(w.f.ref(w.id, revision))
	if !ok {
		t.Fatal("decision missing")
	}
	return d
}

func TestDecisionDisposeRecordsTheRulingAndItsRealAuthor(t *testing.T) {
	for _, disposition := range []string{"approved", "rejected", "withdrawn"} {
		t.Run(disposition, func(t *testing.T) {
			w := newDisposeWorld(t)
			// An agent (lane-c2) records the owner's ruling; it is not the owner.
			bundle := w.f.accept(w.f.capture(nil, w.dispose(1, disposition)))
			d := w.decision(t, 1)
			if d.Status != reduce.StatusDecided || len(d.Dispositions) != 1 {
				t.Fatalf("disposition did not decide the question: %+v", d)
			}
			got := d.Dispositions[0].Disposition
			if got.Disposition != disposition || got.Quote != "  ship revision one\n" || got.Authority.Actor.ID != "owner" {
				t.Fatalf("ruling not recorded exactly: %+v", got)
			}
			// The ledger names who wrote the packet, never the authority in its place.
			review, err := model.DecodeEvent(bundle.Events[len(bundle.Events)-1])
			if err != nil {
				t.Fatal(err)
			}
			r := review.(*model.ReviewAdmit)
			if !strings.Contains(r.Reason, "author "+strconv.Quote("lane-c2")) || strings.Contains(r.Reason, "author "+strconv.Quote("owner")) {
				t.Fatalf("packet author rewritten or missing: %q", r.Reason)
			}
			for _, state := range r.SelfAdmission {
				if state != model.SelfAdmissionFalse {
					t.Fatalf("author/admitter comparison changed: %s", state)
				}
			}
		})
	}
}

func TestDecisionDisposeRefusesBlankQuoteOrAuthority(t *testing.T) {
	w := newDisposeWorld(t)
	blank := w.dispose(1, "approved")
	blank.Quote = " ​\n"
	w.f.refuse(w.f.request(w.f.captureRaw(blank)), "invalid-field")
	unnamed := w.dispose(1, "approved")
	unnamed.Authority.Actor = model.Actor{UnknownReason: "nobody recorded who ruled"}
	w.f.refuse(w.f.request(w.f.capture(nil, unnamed)), "authority-unavailable")
	absent := w.dispose(1, "approved")
	absent.Authority.SourceRef = proofPin(`{"ruling":"never written"}`, "rulings/missing.json")
	w.f.refuse(w.f.request(w.f.capture(nil, absent)), "unavailable")
	// The ruling's bytes cannot be borrowed from outside the root.
	outside := t.TempDir()
	proofPut(t, outside, "ruling.json", disposeRuling)
	if err := os.Symlink(filepath.Join(outside, "ruling.json"), filepath.Join(w.f.project.Root, "escape.json")); err != nil {
		t.Fatal(err)
	}
	escaped := w.dispose(1, "approved")
	escaped.Authority.SourceRef = proofPin(disposeRuling, "escape.json")
	_, err := Admit(context.Background(), w.f.project, w.f.request(w.f.capture(nil, escaped)))
	if admissionErrorCode(err) != "unavailable" || !strings.Contains(err.Error(), "resolved outside the root") {
		t.Fatalf("a ruling was read through a symlink out of the root: %v", err)
	}
	if d := w.decision(t, 1); d.Status != reduce.StatusOpen {
		t.Fatalf("a refused disposition decided the question: %+v", d)
	}
}

func TestDecisionDisposeKeepsEarlierHistoryVisible(t *testing.T) {
	w := newDisposeWorld(t)
	w.f.accept(w.f.capture(nil, w.dispose(1, "approved")))
	open := w.decision(t, 1)
	revise := &model.DecisionRevise{Provenance: model.Provenance{Author: w.f.author, SourceRefs: []model.ArtifactRef{}}, Target: w.f.ref(w.id, 1), ExpectedRevision: 1, Replacement: *open.Spec}
	w.f.accept(w.f.capture(nil, revise))
	w.f.accept(w.f.capture(nil, w.dispose(2, "withdrawn")))
	if first := w.decision(t, 1); len(first.Dispositions) != 1 || first.Dispositions[0].Disposition.Disposition != "approved" {
		t.Fatalf("revision 1's ruling is no longer visible: %+v", first.Dispositions)
	}
	if second := w.decision(t, 2); second.Status != reduce.StatusDecided || second.Dispositions[0].Disposition.Disposition != "withdrawn" {
		t.Fatalf("revision 2's ruling missing: %+v", second)
	}
}
