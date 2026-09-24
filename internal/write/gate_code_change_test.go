package write

// R14.2 through admission: a failing run of the proof's own criterion revision
// is set aside only beside a code change git verifies over the claim's scope,
// in the home repository (the ledger's), never the invoking checkout. The
// replay-side structure is tested in internal/reduce; the stale-claims read,
// which uses the invoking checkout, in internal/query and cmd/whosaidso.

import (
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// codeWorld is a proof world whose claim is scoped to src/ and whose root is a
// git repository with three commits: a (src/code.go v1), b (src/code.go v2)
// and c (only notes/todo.txt changes, so b..c leaves the scope alone).
type codeWorld struct {
	*proofWorld
	a, b, c model.GitHead
}

func newCodeWorld(t *testing.T) *codeWorld {
	t.Helper()
	f := newAdmissionFixture(t)
	root := f.project.Root
	identityGit(t, root, "init", "--quiet")
	commit := func(path, body string) model.GitHead {
		proofPut(t, root, path, body)
		identityGit(t, root, "add", path)
		identityGit(t, root, "commit", "--quiet", "-m", path)
		return model.GitHead{ObjectFormat: "sha1", Commit: identityGit(t, root, "rev-parse", "HEAD")}
	}
	w := &codeWorld{proofWorld: &proofWorld{f: f}}
	w.a = commit("src/code.go", "package src // v1\n")
	w.b = commit("src/code.go", "package src // v2\n")
	w.c = commit("notes/todo.txt", "outside the claim's scope\n")
	proofPut(t, root, proofPath, proofPass)
	proofPut(t, root, "validation/instrument.json", `{"validated":"fixture"}`)
	task, claim, instrument := f.task(), f.claim(), f.instrument()
	claim.Spec.Scope.SourcePaths = []string{"src"}
	instrument.Spec.Validation = proofKnown(model.InstrumentValidation{Ref: proofPin(`{"validated":"fixture"}`, "validation/instrument.json"), Version: "v1"})
	f.accept(f.capture([][]byte{[]byte("instrument implementation")}, task, claim, instrument))
	w.claim, w.instrument = f.ref(claim.ID, 1), f.ref(instrument.ID, 1)
	w.criterion = w.fix(w.claim)
	w.attempt = f.id()
	f.accept(f.capture(nil, &model.TaskStart{Task: f.ref(task.ID, 1), Actor: f.author, AttemptID: w.attempt}))
	return w
}

// runAt admits one run whose recorded head is head (known unless head is nil).
func (w *codeWorld) runAt(body string, head *model.GitHead) model.InvocationRef {
	env := w.envelope(w.criterion)
	if head != nil {
		env.ExecutionSourceIdentity.Head = proofKnown(*head)
	} else {
		env.ExecutionSourceIdentity.Head = proofUnknown[model.GitHead]("not observed")
	}
	start := w.f.capture(nil, &model.InvocationStart{Envelope: env})
	seal := w.f.capture([][]byte{[]byte(body)}, proofSealed(env, body))
	w.f.accept(start, seal)
	return model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}
}

func (w *codeWorld) proofSettingAside(pass, fail model.InvocationRef, change *model.CodeChange) *model.ProofAdmit {
	p := w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})
	p.Evidence = append(p.Evidence, model.ObservationDisposition{InvocationRef: fail, Disposition: "inapplicable",
		Reason: "it measured code the fix has since changed", CodeChange: change})
	return p
}

func TestCodeChangeVerifiedByGitSetsTheFailingRunAside(t *testing.T) {
	w := newCodeWorld(t)
	fail, pass := w.runAt(proofFail, &w.a), w.runAt(proofPass, &w.b)
	w.f.accept(w.f.capture(nil, w.proofSettingAside(pass, fail, &model.CodeChange{From: w.a, To: w.b, ChangedPaths: []string{"src/code.go"}})))
	if w.status(t) != reduce.StatusProven {
		t.Fatalf("a verified code change must let the re-measured claim prove, got %s", w.status(t))
	}
}

// Two roots (R19): the checkout the admission is invoked from sits at a HEAD
// the home does not have, and holds none of the home's commits, so a gate
// that asked it would refuse the change as unverified.
func TestCodeChangeIsVerifiedInTheHomeNotTheInvokingCheckout(t *testing.T) {
	w := newCodeWorld(t)
	checkout := t.TempDir()
	identityGit(t, checkout, "init", "--quiet")
	proofPut(t, checkout, "src/code.go", "package src // elsewhere\n")
	identityGit(t, checkout, "add", ".")
	identityGit(t, checkout, "commit", "--quiet", "-m", "another history")
	if identityGit(t, checkout, "rev-parse", "HEAD") == identityGit(t, w.f.project.Root, "rev-parse", "HEAD") {
		t.Fatal("control: the checkout and the home must sit at different commits")
	}
	w.f.project.Checkout = checkout
	fail, pass := w.runAt(proofFail, &w.a), w.runAt(proofPass, &w.b)
	w.f.accept(w.f.capture(nil, w.proofSettingAside(pass, fail, &model.CodeChange{From: w.a, To: w.b, ChangedPaths: []string{"src/code.go"}})))
	if w.status(t) != reduce.StatusProven {
		t.Fatalf("the code change is the home repository's to verify, got %s", w.status(t))
	}
}

func TestCodeChangeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		// failAt, passAt name the heads the runs record; nil is unknown.
		failAt, passAt func(w *codeWorld) *model.GitHead
		change         func(w *codeWorld) *model.CodeChange
	}{
		{"no code change: the run is still counterevidence", "counterevidence-unresolved",
			func(w *codeWorld) *model.GitHead { return &w.a }, func(w *codeWorld) *model.GitHead { return &w.b },
			func(w *codeWorld) *model.CodeChange { return nil }},
		{"the scope did not change between the commits", "code-change-unverified",
			func(w *codeWorld) *model.GitHead { return &w.b }, func(w *codeWorld) *model.GitHead { return &w.c },
			func(w *codeWorld) *model.CodeChange {
				return &model.CodeChange{From: w.b, To: w.c, ChangedPaths: []string{"src/code.go"}}
			}},
		{"recorded paths are not what git lists", "code-change-unverified",
			func(w *codeWorld) *model.GitHead { return &w.a }, func(w *codeWorld) *model.GitHead { return &w.b },
			func(w *codeWorld) *model.CodeChange {
				return &model.CodeChange{From: w.a, To: w.b, ChangedPaths: []string{"src/other.go"}}
			}},
		{"a commit the repository does not have", "code-change-unverified",
			func(w *codeWorld) *model.GitHead { return &w.a },
			func(w *codeWorld) *model.GitHead {
				return &model.GitHead{ObjectFormat: "sha1", Commit: "0123456789abcdef0123456789abcdef01234567"}
			},
			func(w *codeWorld) *model.CodeChange {
				return &model.CodeChange{From: w.a, To: model.GitHead{ObjectFormat: "sha1", Commit: "0123456789abcdef0123456789abcdef01234567"}, ChangedPaths: []string{"src/code.go"}}
			}},
		{"the failing run's head is unknown", "code-change-mismatch",
			func(w *codeWorld) *model.GitHead { return nil }, func(w *codeWorld) *model.GitHead { return &w.b },
			func(w *codeWorld) *model.CodeChange {
				return &model.CodeChange{From: w.a, To: w.b, ChangedPaths: []string{"src/code.go"}}
			}},
		{"a recorded path outside the scope", "code-change-mismatch",
			func(w *codeWorld) *model.GitHead { return &w.a }, func(w *codeWorld) *model.GitHead { return &w.b },
			func(w *codeWorld) *model.CodeChange {
				return &model.CodeChange{From: w.a, To: w.b, ChangedPaths: []string{"notes/todo.txt"}}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newCodeWorld(t)
			fail, pass := w.runAt(proofFail, tc.failAt(w)), w.runAt(proofPass, tc.passAt(w))
			w.f.refuse(w.f.request(w.f.capture(nil, w.proofSettingAside(pass, fail, tc.change(w)))), tc.code)
			if w.status(t) != reduce.StatusMeasured {
				t.Fatalf("a refused proof changed the claim to %s", w.status(t))
			}
		})
	}
}
