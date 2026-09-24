package write

// Tests for correction admission: typed targets, transitive invalidation that
// reaches proof, artifact containment on the corrective and support-evidence
// references, and structural refusals. The proof fixture is in gate_proof_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func (w *proofWorld) correction(target model.CorrectionTarget) *model.Correction {
	proofPut(w.f.t, w.f.project.Root, "corrections/why.json", `{"finding":"the harness mis-scaled units"}`)
	return &model.Correction{Target: target, AffectedRevisions: []model.RecordRef{w.claim}, Reason: "the harness mis-scaled units",
		CorrectiveRef: proofPin(`{"finding":"the harness mis-scaled units"}`, "corrections/why.json")}
}

func TestCorrectionInvalidatesTransitivelyAndBlocksProof(t *testing.T) {
	for _, kind := range []string{"record", "criterion", "support"} {
		t.Run(kind, func(t *testing.T) {
			w := newProofWorld(t, true)
			pass, s, e := w.run(w.criterion, proofPass)
			w.f.accept(s, e)
			target := model.CorrectionTarget{Kind: kind}
			switch kind {
			case "record":
				target.Record = &w.claim
			case "criterion":
				target.Criterion = &w.criterion
			case "support":
				target.Support = &model.SupportLink{Dependent: w.claim, Evidence: proofPin(proofPass, proofPath)}
			}
			bundle := w.f.accept(w.f.capture(nil, w.correction(target)))
			support, _ := w.f.snapshot().Support(w.claim)
			if support.CorrectionFree != reduce.TruthFalse {
				t.Fatalf("correction did not reach the claim: %+v", support)
			}
			if !strings.Contains(string(bundle.Events[len(bundle.Events)-1].Data), w.f.author.ID) {
				t.Fatal("the correction's author is not recorded in its review")
			}
			w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"}))), "invalid-transition")
		})
	}
}

func TestCorrectionOfAProvenClaimKeepsHistoryAndRemovesCurrentSupport(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s, e := w.run(w.criterion, proofPass)
	w.f.accept(s, e)
	w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})))
	w.f.accept(w.f.capture(nil, w.correction(model.CorrectionTarget{Kind: "record", Record: &w.claim})))
	p, _ := w.f.snapshot().ClaimAt(w.claim)
	if p.Status != reduce.StatusProven || p.Support.CorrectionFree != reduce.TruthFalse || p.Support.Current() != reduce.TruthFalse {
		t.Fatalf("a correction must leave PROVEN as history and remove current support: %+v", p)
	}
}

func TestCorrectionRefusals(t *testing.T) {
	for _, route := range []string{"unknown-target", "unknown-affected", "corrective-missing", "corrective-escape", "support-evidence-escape", "tag-mismatch", "no-affected"} {
		t.Run(route, func(t *testing.T) {
			w := newProofWorld(t, true)
			c := w.correction(model.CorrectionTarget{Kind: "record", Record: &w.claim})
			code, raw := "unavailable", false
			ghost := w.f.ref(w.f.id(), 1)
			outside := t.TempDir()
			proofPut(t, outside, "secret.json", `{"outside":true}`)
			escape := proofPin(`{"outside":true}`, "escape.json")
			switch route {
			case "unknown-target":
				c.Target.Record, code = &ghost, "unknown-reference"
			case "unknown-affected":
				c.AffectedRevisions, code = []model.RecordRef{ghost}, "unknown-reference"
			case "corrective-missing":
				c.CorrectiveRef = proofPin(`{"never":"written"}`, "corrections/missing.json")
			case "corrective-escape", "support-evidence-escape":
				if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(w.f.project.Root, "escape.json")); err != nil {
					t.Fatal(err)
				}
				if route == "corrective-escape" {
					c.CorrectiveRef = escape
				} else {
					c.Target = model.CorrectionTarget{Kind: "support", Support: &model.SupportLink{Dependent: w.claim, Evidence: escape}}
				}
			case "tag-mismatch":
				c.Target.Kind, code, raw = "criterion", "invalid-field", true
			case "no-affected":
				c.AffectedRevisions, code, raw = []model.RecordRef{}, "invalid-field", true
			}
			var request AdmitRequest
			if raw {
				request = w.f.request(w.f.captureRaw(c))
			} else {
				request = w.f.request(w.f.capture(nil, c))
			}
			before := w.f.snapshot().Watermark()
			_, err := Admit(context.Background(), w.f.project, request)
			if admissionErrorCode(err) != code || strings.HasSuffix(route, "escape") && !strings.Contains(err.Error(), "resolved outside the root") {
				t.Fatalf("%s: want %s, got %v", route, code, err)
			}
			if w.f.snapshot().Watermark() != before {
				t.Fatal("refused correction published")
			}
		})
	}
}
