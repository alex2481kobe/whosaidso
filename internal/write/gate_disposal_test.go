package write

// Admission tests for artifact.dispose: manual disposal is recorded with its
// loss (every record revision that becomes unverifiable) and a named authority;
// Datum neither resolves, preserves nor deletes the disposed bytes; afterwards
// nothing may use the artifact as available evidence. Supersede lives in
// gate_supersede_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

const disposalRuling = `{"ruling":"delete that run output"}`

func (w *proofWorld) disposal(body string, loss ...model.RecordRef) *model.ArtifactDispose {
	proofPut(w.f.t, w.f.project.Root, "rulings/dispose.json", disposalRuling)
	artifact := proofPin(body, proofPath)
	d := &model.ArtifactDispose{Artifact: artifact, Digest: artifact.Content.SHA256, PreviousLocation: proofPath, SupportLoss: []model.SupportLoss{},
		Authority: model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: proofPin(disposalRuling, "rulings/dispose.json"),
			Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: w.f.task().Spec.Scope}}
	for _, target := range loss {
		d.SupportLoss = append(d.SupportLoss, model.SupportLoss{Target: target, Reason: "its supporting run output is deleted"})
	}
	return d
}

func (w *proofWorld) proven(t *testing.T) model.InvocationRef {
	t.Helper()
	pass, start, seal := w.run(w.criterion, proofPass)
	w.f.accept(start, seal)
	w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})))
	if w.status(t) != reduce.StatusProven {
		t.Fatal("control proof did not reach PROVEN")
	}
	return pass
}

func TestDisposalLeavesACitingProofUnverifiable(t *testing.T) {
	w := newProofWorld(t, true)
	w.proven(t)
	w.f.accept(w.f.capture(nil, w.disposal(proofPass, w.claim)))
	p, ok := w.f.snapshot().ClaimAt(w.claim)
	if !ok || p.Status != reduce.StatusProven {
		t.Fatalf("disposal rewrote the achievement: %+v", p)
	}
	if p.Support.EvidenceAvailable != reduce.TruthFalse || p.Support.Current() == reduce.TruthTrue {
		t.Fatalf("a proof citing a disposed artifact still counts as verified: %+v", p.Support)
	}
	// Datum records the disposal; it does not delete the bytes itself.
	if _, err := os.Stat(filepath.Join(w.f.project.Root, proofPath)); err != nil {
		t.Fatalf("admission deleted bytes: %v", err)
	}
}

func TestDisposalRecordsItsLossAndAuthority(t *testing.T) {
	w := newProofWorld(t, true)
	w.proven(t)
	// The claim loses support, so an empty loss list is a false statement.
	w.f.refuse(w.f.request(w.f.capture(nil, w.disposal(proofPass))), "loss-unaccounted")
	unrelated := w.disposal(proofPass, w.instrument)
	w.f.refuse(w.f.request(w.f.capture(nil, unrelated)), "loss-unaccounted")
	unnamed := w.disposal(proofPass, w.claim)
	unnamed.Authority.Actor = model.Actor{UnknownReason: "nobody recorded who ruled"}
	w.f.refuse(w.f.request(w.f.capture(nil, unnamed)), "authority-unavailable")
	absent := w.disposal(proofPass, w.claim)
	absent.Authority.SourceRef = proofPin(`{"ruling":"never written"}`, "rulings/missing.json")
	w.f.refuse(w.f.request(w.f.capture(nil, absent)), "unavailable")
	for _, drop := range []string{"digest", "previous_location", "support_loss", "authority"} {
		t.Run("without "+drop, func(t *testing.T) {
			raw := admissionTestEvent(t, w.disposal(proofPass, w.claim))
			var fields map[string]any
			if err := json.Unmarshal(raw.Data, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, drop)
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := model.DecodeEvent(model.Event{Type: raw.Type, Data: data}); err == nil {
				t.Fatalf("a disposal without %s decoded", drop)
			}
		})
	}
	if len(w.f.snapshot().ArtifactDisposals()) != 0 {
		t.Fatal("a refused disposal was recorded")
	}
}

func TestDisposalNeverResolvesOrPreservesTheDisposedBytes(t *testing.T) {
	w := newProofWorld(t, true)
	gone := `{"bytes":"already deleted by hand"}`
	w.f.accept(w.f.capture(nil, w.disposal(gone)))
	if _, err := os.Stat(filepath.Join(w.f.project.Root, ".datum", "artifacts", string(model.HashBytes([]byte(gone))))); !os.IsNotExist(err) {
		t.Fatalf("disposed bytes were preserved as evidence: %v", err)
	}
	if len(w.f.snapshot().ArtifactDisposals()) != 1 {
		t.Fatal("disposal of bytes that are already gone was not recorded")
	}
}

func TestDisposedArtifactIsNeverAvailableAgain(t *testing.T) {
	w := newProofWorld(t, true)
	gone := `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,0.0300]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`
	first, s1, e1 := w.run(w.criterion, gone)
	second, s2, e2 := w.run(w.criterion, proofPass)
	w.f.accept(s1, e1, s2, e2)
	w.f.accept(w.f.capture(nil, w.disposal(gone)))
	// The bytes are still preserved on disk; the disposal alone makes the run
	// unverifiable, so it can never be support again.
	both := map[model.InvocationRef]string{first: "supports", second: "supports"}
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, both))), "invalid-transition")
	cites := w.f.claim()
	cites.Provenance.SourceRefs = []model.ArtifactRef{proofPin(gone, proofPath)}
	w.f.refuse(w.f.request(w.f.capture(nil, cites)), "artifact-disposed")
	// The owner then deletes the bytes by hand. The disposed member is
	// accounted for as inconclusive, and the verifiable run still proves.
	if err := os.Remove(filepath.Join(w.f.project.Root, ".datum", "artifacts", string(model.HashBytes([]byte(gone))))); err != nil {
		t.Fatal(err)
	}
	w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{first: "inconclusive", second: "supports"})))
	if w.status(t) != reduce.StatusProven {
		t.Fatal("the remaining verifiable run could not prove the claim")
	}
}
