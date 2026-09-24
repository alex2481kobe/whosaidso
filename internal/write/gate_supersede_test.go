package write

// Admission tests for supersede: it acts only on an already-canonical record,
// once, never into a cycle, and with a named authority whose carrier resolves
// when an owner ruling is affected. The packet author stays whoever wrote it.
// artifact.dispose admission lives in gate_disposal_test.go.

import (
	"context"
	"testing"

	"whosaidso/internal/model"
)

func (w *disposeWorld) supersede(prior, replacement model.RecordRef, authority *model.Authority) *model.Supersede {
	return &model.Supersede{Prior: prior, Replacement: replacement, Reason: "the question was restated", Authority: authority}
}

func (w *disposeWorld) authority() *model.Authority {
	a := w.dispose(1, "approved").Authority
	return &a
}

func (w *disposeWorld) claim(t *testing.T) model.RecordRef {
	t.Helper()
	claim := w.f.claim()
	w.f.accept(w.f.capture(nil, claim))
	return w.f.ref(claim.ID, 1)
}

func TestSupersedeRefusesAPriorThatIsNotCanonical(t *testing.T) {
	w := newDisposeWorld(t)
	replacement := w.claim(t)
	// Never admitted at all.
	ghost := w.f.ref(w.f.id(), 1)
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(ghost, replacement, nil))), "supersede-not-canonical")
	// Its packet was reviewed and rejected: it stays in intake, not canonical.
	rejected := w.f.claim()
	packet := w.f.capture(nil, rejected)
	reject := w.f.request(packet)
	reject.Outcome = "rejected"
	if _, err := Admit(context.Background(), w.f.project, reject); err != nil {
		t.Fatal(err)
	}
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(w.f.ref(rejected.ID, 1), replacement, nil))), "supersede-not-canonical")
	// Proposed in the same admission set is not yet canonical either.
	fresh := w.f.claim()
	w.f.refuse(w.f.request(w.f.capture(nil, fresh), w.f.capture(nil, w.supersede(w.f.ref(fresh.ID, 1), replacement, nil))), "supersede-not-canonical")
	// Control: the same act on a canonical record admits.
	prior := w.claim(t)
	w.f.accept(w.f.capture(nil, w.supersede(prior, replacement, nil)))
	if _, ok := w.f.snapshot().Record(prior); !ok || len(w.f.snapshot().Supersessions()) != 1 {
		t.Fatal("supersession hid its prior or was not recorded")
	}
}

func TestSupersedeRefusesTwiceAndCycles(t *testing.T) {
	w := newDisposeWorld(t)
	a, b, c := w.claim(t), w.claim(t), w.claim(t)
	// No ruling is affected, but an authority that is given must name who ruled.
	unnamed := w.authority()
	unnamed.Actor = model.Actor{UnknownReason: "nobody recorded who ruled"}
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(a, b, unnamed))), "authority-unavailable")
	w.f.accept(w.f.capture(nil, w.supersede(a, b, nil)))
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(a, c, nil))), "already-superseded")
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(b, a, nil))), "supersede-cycle")
	w.f.accept(w.f.capture(nil, w.supersede(b, c, nil)))
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(c, a, nil))), "supersede-cycle")
	if got := len(w.f.snapshot().Supersessions()); got != 2 {
		t.Fatalf("refusals changed the ledger: %d supersessions", got)
	}
}

func TestSupersedeOfAnOwnerRulingNeedsItsAuthority(t *testing.T) {
	w := newDisposeWorld(t)
	prior := w.f.ref(w.id, 1)
	open := &model.DecisionOpen{ID: w.f.id(), Provenance: model.Provenance{Author: w.f.author, SourceRefs: []model.ArtifactRef{}},
		Spec: model.DecisionSpec{Question: "ship revision two", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: w.scope}}
	w.f.accept(w.f.capture(nil, open))
	replacement := w.f.ref(open.ID, 1)
	w.f.accept(w.f.capture(nil, w.dispose(1, "approved")))
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(prior, replacement, nil))), "authority-unavailable")
	unnamed := w.authority()
	unnamed.Actor = model.Actor{UnknownReason: "nobody recorded who ruled"}
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(prior, replacement, unnamed))), "authority-unavailable")
	absent := w.authority()
	absent.SourceRef = proofPin(`{"ruling":"never written"}`, "rulings/missing.json")
	w.f.refuse(w.f.request(w.f.capture(nil, w.supersede(prior, replacement, absent))), "unavailable")
	// An agent records the owner's supersession; it stays the packet author.
	bundle := w.f.accept(w.f.capture(nil, w.supersede(prior, replacement, w.authority())))
	review, err := model.DecodeEvent(bundle.Events[len(bundle.Events)-1])
	if err != nil {
		t.Fatal(err)
	}
	r := review.(*model.ReviewAdmit)
	if len(r.Packets) != 1 || r.Authors[r.Packets[0].CommandID].ID != "lane-c2" {
		t.Fatalf("packet author not recorded: %+v", r.Authors)
	}
	if d := w.decision(t, 1); len(d.Dispositions) != 1 {
		t.Fatalf("superseding the ruling hid it: %+v", d)
	}
}
