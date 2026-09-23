package write

// Tests for the admission dry run (admit_check.go) and for refusals naming
// only what exists (DOGFOOD entry 13). The CLI over them is tested in cmd/datum.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/store"
)

// checkTree fingerprints every file under the project root and the intake,
// so "the check wrote nothing" is asked of the real directories. The one
// exception is the disposable snapshot cache's image, which changes no answer.
func checkTree(t *testing.T, f *admissionFixture) string {
	t.Helper()
	inbox, err := store.IntakeDir(f.project)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, root := range []string{f.project.Root, inbox} {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if path == f.project.CacheDir() || filepath.Dir(path) == f.project.CacheDir() && (name == "snapshot" || strings.HasPrefix(name, ".snapshot-")) {
				return nil // the disposable snapshot cache may be refreshed by any read
			}
			info, _ := d.Info()
			sum := ""
			if !d.IsDir() {
				data, _ := os.ReadFile(path)
				h := sha256.Sum256(data)
				sum = hex.EncodeToString(h[:])
			}
			lines = append(lines, fmt.Sprintf("%s %v %s", path, info.Mode(), sum))
			return nil
		})
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestCheckAdmissionCollectsEveryProofRefusalAndWritesNothing(t *testing.T) {
	w := newProofWorld(t, true)
	pass, passStart, passSeal := w.run(w.criterion, proofPass)
	fail, failStart, failSeal := w.run(w.criterion, proofFail)
	_, omittedStart, omittedSeal := w.run(w.criterion, proofPass)
	w.f.accept(passStart, passSeal, failStart, failSeal, omittedStart, omittedSeal)
	// Three independent faults: a passing run called a contradiction, a failing
	// run called support, and a sealed family member left out.
	proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "contradicts", fail: "supports"}))
	before := checkTree(t, w.f)
	check, err := CheckAdmission(context.Background(), w.f.project, []model.ID{proof.CommandID}, nil, model.Actor{ID: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if after := checkTree(t, w.f); after != before {
		t.Fatalf("the dry run wrote to the project or intake:\nbefore\n%s\nafter\n%s", before, after)
	}
	_, admitErr := Admit(context.Background(), w.f.project, w.f.request(proof))
	if admitErr == nil {
		t.Fatal("control: admission must refuse this proof")
	}
	if len(check.Refusals) == 0 || check.Refusals[0].Err.Error() != admitErr.Error() {
		t.Fatalf("the check's first refusal must be admission's refusal %q, got %+v", admitErr, check.Refusals)
	}
	var codes []string
	for _, r := range check.Refusals {
		codes = append(codes, r.Stage+" "+admissionErrorCode(r.Err))
	}
	want := []string{"replay invalid-transition", "replay incomplete-family", "proofs counterevidence-unresolved"}
	for _, code := range want {
		found := false
		for _, got := range codes {
			found = found || got == code
		}
		if !found {
			t.Errorf("missing refusal %q among %v", code, codes)
		}
	}
	if len(check.Members) != 2 {
		t.Fatalf("each listed member must be reported: %+v", check.Members)
	}
	for _, m := range check.Members {
		want := map[model.InvocationRef]string{pass: "TRUE", fail: "FALSE"}[m.Invocation]
		if string(m.Verdict) != want || m.Validation != "" {
			t.Errorf("member %s: verdict %s (want %s), validation %q", m.Invocation.InvocationID, m.Verdict, want, m.Validation)
		}
	}
}

func TestCheckAdmissionAcceptsWhatAdmissionAccepts(t *testing.T) {
	w := newProofWorld(t, true)
	pass, start, seal := w.run(w.criterion, proofPass)
	w.f.accept(start, seal)
	proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"}))
	before := checkTree(t, w.f)
	check, err := CheckAdmission(context.Background(), w.f.project, []model.ID{proof.CommandID}, nil, model.Actor{ID: "coordinator"})
	if err != nil || len(check.Refusals) != 0 || check.StoppedAt != "" {
		t.Fatalf("a proof admission accepts must check clean: %+v, %v", check, err)
	}
	if checkTree(t, w.f) != before || w.f.snapshot().Watermark().Sequence != check.Head {
		t.Fatal("a clean check published or wrote")
	}
	w.f.accept(proof)
}

func TestCheckAdmissionOfAnUncapturedPacketCapturesNothing(t *testing.T) {
	w := newProofWorld(t, false)
	pass, start, seal := w.run(w.criterion, proofPass)
	w.f.accept(start, seal)
	event, err := model.EncodeEvent(w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"}))
	if err != nil {
		t.Fatal(err)
	}
	packet, err := UncapturedPacket(w.f.project, w.f.author, []model.Event{event})
	if err != nil {
		t.Fatal(err)
	}
	before := checkTree(t, w.f)
	check, err := CheckAdmission(context.Background(), w.f.project, nil, []model.Packet{packet}, w.f.author)
	if err != nil {
		t.Fatal(err)
	}
	if checkTree(t, w.f) != before {
		t.Fatal("checking an uncaptured packet wrote intake or artifacts")
	}
	// The instrument is unvalidated: the reducer and the artifact gate both say so,
	// and the member row still shows the criterion's own TRUE.
	if len(check.Refusals) < 2 || check.Refusals[0].Stage != "replay" || admissionErrorCode(check.Refusals[len(check.Refusals)-1].Err) != "validation-unknown" {
		t.Fatalf("expected the replay refusal and the proof-stage validation refusal: %+v", check.Refusals)
	}
	if len(check.Members) != 1 || check.Members[0].Verdict != "TRUE" || check.Members[0].Validation == "" {
		t.Fatalf("member row: %+v", check.Members)
	}
}

// DOGFOOD entry 13: a refused admission cited "(sequence N)" for the bundle it
// would have written. The refusal names the proposal instead, and no sequence.
func TestRefusalNamesNoUnwrittenBundle(t *testing.T) {
	w := newProofWorld(t, true)
	pass, start, seal := w.run(w.criterion, proofPass)
	w.f.accept(start, seal)
	head := w.f.snapshot().Watermark().Sequence
	_, err := Admit(context.Background(), w.f.project, w.f.request(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "inconclusive"}))))
	if err == nil || admissionErrorCode(err) != "invalid-transition" {
		t.Fatalf("control: the proof must be refused by replay, got %v", err)
	}
	if strings.Contains(err.Error(), fmt.Sprintf("(sequence %d)", head+1)) || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("refusal cites a bundle that was never written: %v", err)
	}
	task := w.f.goodControl()
	amend := &model.TaskAmend{Provenance: task.Provenance, Target: w.f.ref(task.ID, 1), ExpectedRevision: 1, Replacement: task.Spec}
	w.f.accept(w.f.capture(nil, amend))
	head = w.f.snapshot().Watermark().Sequence
	_, err = Admit(context.Background(), w.f.project, w.f.request(w.f.capture(nil, amend)))
	if admissionErrorCode(err) != "revision-conflict" || strings.Contains(err.Error(), fmt.Sprintf("(sequence %d)", head+1)) || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("a stale revision must stay a typed conflict naming no unwritten bundle: %v", err)
	}
}

// A run still in intake: admission would copy its captured output into the
// artifact store before evaluating it. The dry run copies nothing, and says so
// where that is why a reference did not resolve.
func TestCheckAdmissionWithPendingRunCopiesNoBlob(t *testing.T) {
	w := newProofWorld(t, true)
	// Bytes no earlier admission stored, so preserving them would show on disk.
	fresh := strings.Replace(proofPass, "0.0200", "0.0300", 1)
	pass, start, seal := w.run(w.criterion, fresh)
	failA, startA, sealA := w.run(w.criterion, proofFail)
	failB, startB, sealB := w.run(w.criterion, proofFail)
	w.f.accept(startA, sealA, startB, sealB)
	proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports", failA: "supports", failB: "supports"}))
	before := checkTree(t, w.f)
	check, err := CheckAdmission(context.Background(), w.f.project, []model.ID{start.CommandID, seal.CommandID, proof.CommandID}, nil, model.Actor{ID: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if after := checkTree(t, w.f); after != before {
		t.Fatalf("the dry run preserved or published:\nbefore\n%s\nafter\n%s", before, after)
	}
	if len(check.Unpreserved) == 0 {
		t.Fatalf("the pending run's output is what admission would preserve: %+v", check)
	}
	counter := map[string]bool{}
	for _, r := range check.Refusals {
		if admissionErrorCode(r.Err) == "counterevidence-unresolved" {
			counter[r.Err.Error()] = true
		}
	}
	if len(counter) != 2 {
		t.Fatalf("each failing member's own refusal must be collected, not only the first: %+v", check.Refusals)
	}
}
