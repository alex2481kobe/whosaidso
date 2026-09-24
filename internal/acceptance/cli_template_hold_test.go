// Final-round confirmation probes belong here: the reported CLI cases and
// alternate paths through their fixes. Production changes and broad audits do not.
package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/query"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

func cliHoldNew(t *testing.T) *gateVerifyFixture {
	t.Helper()
	f := gateVerifyNew(t)
	t.Setenv(store.HomeEnv, filepath.Join(t.TempDir(), "cli-hold-home"))
	t.Setenv(store.NoCacheEnv, "1")
	t.Setenv("WHOSAIDSO_ACTOR", "holder")
	pvPut(t, f.p.Root, "whosaidso.toml", []byte(fmt.Sprintf("id = '%s'\nledger = '.whosaidso/events'\n", f.p.ID)))
	if _, err := store.Bind(context.Background(), f.p.Root, f.p.Root); err != nil {
		t.Fatal(err)
	}
	return f
}

func cliHoldRun(t *testing.T, f *gateVerifyFixture, bin string, input []byte, args ...string) ([]byte, string, error) {
	t.Helper()
	c := exec.Command(bin, args...)
	c.Dir, c.Stdin = f.p.Root, bytes.NewReader(input)
	var out, errs bytes.Buffer
	c.Stdout, c.Stderr = &out, &errs
	err := c.Run()
	return out.Bytes(), errs.String(), err
}

func cliHoldDraft(t *testing.T, f *gateVerifyFixture, bin string, args ...string) map[string]any {
	t.Helper()
	out, errs, err := cliHoldRun(t, f, bin, nil, append([]string{"template"}, args...)...)
	var events []map[string]any
	if err != nil || json.Unmarshal(out, &events) != nil || len(events) != 1 {
		t.Fatalf("template %v: %v %s %s", args, err, out, errs)
	}
	return events[0]["data"].(map[string]any)
}

func TestCLITemplateRetryAndHoldCapture(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "whosaidso")
	c := exec.Command("go", "build", "-o", bin, "./cmd/whosaidso")
	c.Dir = "../.."
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	t.Run("criterion-original-benchmark-and-reversed-flag-order", func(t *testing.T) {
		f := cliHoldNew(t)
		for name, path := range map[string]string{"good.json": ".whosaidso/artifacts/d44766e9ee3710079c90dba7bfa4765a0b23909a82c31e819fe007d111dec05d", "bad.txt": "README.md"} {
			b, err := os.ReadFile(filepath.Join("../..", path))
			if err != nil {
				t.Fatal(err)
			}
			pvPut(t, f.p.Root, name, b)
		}
		base := []string{"criterion.fix", "--example", "good=" + filepath.Join(f.p.Root, "good.json"), "--pin", "expression.result_selector=good#/readings/by_benchmark/BenchmarkCommands~1N1000~1Now/ns_per_op"}
		control := cliHoldDraft(t, f, bin, base...)
		if flowStr(control, "expression", "unit") != "ns/op" {
			t.Fatalf("benchmark control: %v", control)
		}
		for _, extra := range [][]string{
			{"--example", "bad=" + filepath.Join(f.p.Root, "bad.txt"), "--pin", "expression.result_selector=bad#/not_json"},
			{"--set", "expression.result_selector.selector.pointer=/not_json"},
		} {
			d := cliHoldDraft(t, f, bin, append(append([]string{}, base...), extra...)...)
			for _, path := range [][]any{{"expression", "unit"}, {"expression", "population", "identity"}, {"expression", "population", "denominator"}} {
				if !strings.HasPrefix(flowStr(d, path...), "<") {
					t.Errorf("replaced selector retains metadata at %v: %v", path, flowGet(d, path...))
				}
			}
		}
		// --set is applied after pins even when the flags appear in reverse order.
		args := append([]string{"criterion.fix", "--set", "expression.result_selector.selector.pointer=/missing"}, base[1:]...)
		if d := cliHoldDraft(t, f, bin, args...); !strings.HasPrefix(flowStr(d, "expression", "unit"), "<") {
			t.Errorf("early --set retained an earlier reading: %v", d)
		}
	})
	t.Run("invalid-cache-CLI-retries-and-capture-admit", func(t *testing.T) {
		f := cliHoldNew(t)
		a := model.Actor{ID: "holder"}
		for _, outcome := range []string{"accepted", "rejected", "correction-requested"} {
			packet := f.capture(a, recEncode(t, f.claim(a)))
			r := write.AdmitRequest{CommandID: f.id(), PacketIDs: []model.ID{packet.CommandID}, Admitter: a, Outcome: outcome, Reason: "confirm retry"}
			if _, err := write.Admit(context.Background(), f.p, r); err != nil {
				t.Fatal(err)
			}
			args := []string{"admit", string(packet.CommandID), "--command-id", string(r.CommandID), "--outcome", outcome, "--reason", r.Reason}
			if _, errs, err := cliHoldRun(t, f, bin, nil, args...); err != nil {
				t.Fatalf("retry control: %v %s", err, errs)
			}
			t.Setenv(store.NoCacheEnv, "true")
			if _, errs, err := cliHoldRun(t, f, bin, nil, args...); err == nil || !strings.Contains(errs, store.NoCacheEnv) {
				t.Errorf("invalid-cache %s retry: %v %s", outcome, err, errs)
			}
			t.Setenv(store.NoCacheEnv, "1")
		}
		before := gateVerifyLedger(t, f.p)
		body, _ := json.Marshal([]model.Event{recEncode(t, f.claim(a))})
		t.Setenv(store.NoCacheEnv, "yes")
		if _, errs, err := cliHoldRun(t, f, bin, body, "capture", "--events", "-", "--admit", "--reason", "confirm combined path"); err == nil || !strings.Contains(errs, store.NoCacheEnv) {
			t.Errorf("capture --admit ignored invalid cache: %v %s", err, errs)
		}
		t.Setenv(store.NoCacheEnv, "1")
		if !bytes.Equal(before, gateVerifyLedger(t, f.p)) {
			t.Error("invalid cache appended a ledger bundle")
		}
	})
	t.Run("original-hold-template-capture-admit", func(t *testing.T) {
		f := cliHoldNew(t)
		a := model.Actor{ID: "holder"}
		create := &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: reduceSpec(1)}
		ref := model.RecordRef{Project: f.p.ID, RecordID: create.ID, Revision: 1}
		hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: a, Criterion: "clear before acceptance"}
		body, _ := json.Marshal([]model.Event{recEncode(t, create), recEncode(t, hold)})
		if _, errs, err := cliHoldRun(t, f, bin, body, "capture", "--events", "-", "--admit", "--reason", "create held task"); err != nil {
			t.Fatalf("held control: %v %s", err, errs)
		}
		pvPut(t, f.p.Root, "witness.txt", []byte("A fixture delivery exists.\n"))
		args := []string{"template", "task.close", "--task", string(create.ID), "--set", "outcome=success", "--pin", "acceptance_witness_refs[0].witness_ref=witness.txt", "--pin", "delivery_witness_refs[0]=witness.txt", "--capture", "--admit", "--reason", "close without clear"}
		before := gateVerifyLedger(t, f.p)
		if _, errs, err := cliHoldRun(t, f, bin, nil, args...); err == nil || !strings.Contains(errs, string(hold.BlockerID)) {
			t.Errorf("success over hold: %v %s", err, errs)
		}
		if !bytes.Equal(before, gateVerifyLedger(t, f.p)) {
			t.Error("refused close changed ledger")
		}
		witness := pvPin([]byte("A fixture delivery exists.\n"), "witness.txt")
		witness.Content.MediaType = "text/plain"
		if _, err := f.admit(a, a, &model.BlockerClear{Task: ref, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: ref, BlockerID: hold.BlockerID}, ResolvingWitness: witness}); err != nil {
			t.Fatal(err)
		}
		if _, errs, err := cliHoldRun(t, f, bin, nil, args...); err != nil {
			t.Errorf("cleared positive control: %v %s", err, errs)
		}
	})
	t.Run("hold-clear-after-amendment", func(t *testing.T) {
		f := cliHoldNew(t)
		a := model.Actor{ID: "holder"}
		create := &model.TaskCreate{ID: f.id(), Spec: reduceSpec(1), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}}
		old := model.RecordRef{Project: f.p.ID, RecordID: create.ID, Revision: 1}
		current := old
		current.Revision = 2
		hold := &model.BlockerHold{Task: old, BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: a, Criterion: "read the delivery"}
		if _, err := f.admit(a, a, create, hold, &model.TaskAmend{Target: old, Replacement: create.Spec, Provenance: create.Provenance}); err != nil {
			t.Fatal(err)
		}
		pvPut(t, f.p.Root, "witness.txt", []byte("delivery read"))
		witness := pvPin([]byte("delivery read"), "witness.txt")
		witness.Content.MediaType = "text/plain"
		for _, pair := range [][2]model.RecordRef{{old, old}, {current, current}, {current, old}} {
			clear := &model.BlockerClear{Task: pair[0], BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: pair[1], BlockerID: hold.BlockerID}, ResolvingWitness: witness}
			raw, schemaErr := model.EncodeEvent(clear)
			if schemaErr != nil {
				t.Logf("clear task r%d / hold r%d: %v", pair[0].Revision, pair[1].Revision, schemaErr)
				continue
			}
			packet := f.capture(a, raw)
			check, err := write.CheckAdmission(context.Background(), f.p, []model.ID{packet.CommandID}, nil, a)
			t.Logf("clear task r%d / hold r%d: %+v %v", pair[0].Revision, pair[1].Revision, check.Refusals, err)
		}
		_, errs, err := cliHoldRun(t, f, bin, nil, "template", "blocker.clear", "--hold", string(hold.BlockerID), "--pin", "resolving_witness=witness.txt", "--capture", "--admit", "--reason", "clear the held delivery")
		if err != nil {
			t.Errorf("an unchanged task amendment must leave its existing hold clearable; template --hold refuses and no library reference shape works: %v %s", err, errs)
		}
	})
	t.Run("proof-endpoints-only-capture-admit", func(t *testing.T) {
		t.Setenv(store.HomeEnv, filepath.Join(t.TempDir(), "cli-hold-home"))
		t.Setenv(store.NoCacheEnv, "1")
		t.Setenv("WHOSAIDSO_ACTOR", "lane")
		w := pvNew(t)
		if _, err := store.Bind(context.Background(), w.p.Root, w.p.Root); err != nil {
			t.Fatal(err)
		}
		f := &gateVerifyFixture{t: t, p: w.p}
		record, _ := w.snapshot().Record(w.claim)
		spec := *record.Claim
		spec.Scope.SourcePaths = []string{"step.go"}
		w.mustAdmit(w.lane, &model.ClaimRevise{Target: w.claim, Replacement: spec, Provenance: record.Provenance})
		w.claim.Revision = 2
		pvPut(t, w.p.Root, "out/result.json", []byte(pvFail))
		w.fix("out/result.json", []byte(pvFail))
		git := func(args ...string) string { return evidenceGit(t, w.p.Root, args...) }
		git("init", "-q")
		git("config", "user.name", "Confirmation Fixture")
		git("config", "user.email", "fixture@example.invalid")
		var commits []string
		for i, result := range []string{pvFail, pvPass} {
			pvPut(t, w.p.Root, "step.go", []byte(fmt.Sprintf("package step // revision %d\n", i)))
			git("add", "step.go")
			git("commit", "-qm", fmt.Sprintf("step %d", i))
			head := git("rev-parse", "HEAD")
			commits = append(commits, head)
			env := w.start(w.id(), true)
			env.ExecutionSourceIdentity.Head = recKnown(model.GitHead{ObjectFormat: "sha1", Commit: head})
			env.ExecutionSourceIdentity.Dirty = recKnown(false)
			seal := w.seal(env, w.produce(env.InvocationID, []byte(result), "out/result.json"))
			seal.Envelope.Isolation = recKnown(model.IsolationClean)
			w.mustAdmit(w.lane, &model.InvocationStart{Envelope: env}, seal)
		}
		args := []string{"template", "proof.admit", "--claim", string(w.claim.RecordID), "--set", "evidence[0].disposition=inapplicable", "--set", "evidence[0].reason=scoped implementation changed", "--set", "evidence[0].code_change.from.commit=" + commits[0], "--set", "evidence[0].code_change.to.commit=" + commits[1], "--set", "evidence[1].disposition=supports", "--set", "evidence[1].reason=passing remeasurement", "--set", "judgment.reason=whole family reviewed", "--set", "verdict=supports", "--capture", "--admit", "--reason", "endpoint-only proof"}
		if out, errs, err := cliHoldRun(t, f, bin, nil, args...); err != nil {
			t.Fatalf("endpoint-only proof must capture and admit: %v %s %s", err, out, errs)
		}
		if got := w.status(); got != reduce.StatusProven {
			t.Errorf("endpoint-only proof status %s, want PROVEN", got)
		}
	})
}

func TestTaskCitationRemoval(t *testing.T) {
	f := cliHoldNew(t)
	a := model.Actor{ID: "holder"}
	body := []byte(`{"source":"basis"}`)
	pvPut(t, f.p.Root, "basis.json", body)
	create := &model.TaskCreate{ID: f.id(), Spec: reduceSpec(1), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{pvPin(body, "basis.json")}}}
	if _, err := f.admit(a, a, create); err != nil {
		t.Fatal(err)
	}
	for rev := model.Revision(1); rev <= 2; rev++ {
		prov := model.Provenance{SourceRefs: []model.ArtifactRef{}}
		if rev == 2 {
			prov = create.Provenance
		}
		if _, err := f.admit(a, a, &model.TaskAmend{Target: model.RecordRef{Project: f.p.ID, RecordID: create.ID, Revision: rev}, Replacement: create.Spec, Provenance: prov}); err != nil {
			t.Fatal(err)
		}
	}
	for _, view := range []string{"continue", "history"} {
		answer, err := query.ReadView(f.p, query.ViewRequest{View: view, ID: create.ID})
		if err != nil {
			t.Fatal(err)
		}
		var amendments []query.Amendment
		switch v := answer.(type) {
		case *query.ContinueAnswer:
			amendments = *v.Amendments
		case *query.HistoryAnswer:
			for _, e := range v.Events {
				if e.Amendment != nil {
					amendments = append(amendments, *e.Amendment)
				}
			}
		}
		if len(amendments) != 2 {
			t.Fatalf("%s omitted amendments: %v", view, amendments)
		}
		for i, kind := range []string{"removed", "added"} {
			if len(amendments[i].Changes) != 1 || amendments[i].Changes[0].Path != "provenance.source_refs" || amendments[i].Changes[0].Change != kind {
				t.Errorf("%s %s source citation invisible: %+v", view, kind, amendments[i])
			}
		}
	}
}

func TestHoldReplayOrdering(t *testing.T) {
	create := reduceCreate(1, reduceSpec(1))
	close := reduceClose(1, 1, 1, model.ClosureSuccess)
	hold := &model.BlockerHold{Task: reduceRef(1, 1), BlockerID: reduceID(70), Reason: model.BlockerReconciliation, Actor: model.Actor{UnknownReason: "owner unknown"}, Criterion: "reconcile before acceptance"}
	first := reduceBundle(t, model.Bundle{}, create)
	if _, err := reduce.Replay([]model.Bundle{first, reduceBundle(t, first, close)}); err != nil {
		t.Fatalf("unheld control: %v", err)
	}
	for _, events := range [][]model.TypedEvent{{hold, close}, {close, hold}} {
		if _, err := reduce.Replay([]model.Bundle{first, reduceBundle(t, first, events...)}); recCode(err) != reduce.CodeInvalidTransition {
			t.Errorf("hold/close order admitted an invalid lifecycle: %v", err)
		}
	}
}

// Coordinator edit 2026-09-24: this test first required replay to refuse a clear
// naming the task's current revision for a hold recorded at an earlier one. That
// contradicts hold-clear-after-amendment above (the schema makes hold_ref.task equal
// the clear's task, and a clear must name the current revision), so the two could
// not both pass under any rule without a format change. Coordinator ruling: a hold
// is identified by (task, blocker id) and survives amendments; admission and replay
// share that one lookup. The test now pins that contract from the replay side:
// after an amendment the current-revision clear is accepted and the task may close,
// while a clear still naming the superseded revision is refused.
func TestReplayRequiresTheRecordedHoldRevision(t *testing.T) {
	create := reduceCreate(1, reduceSpec(1))
	hold := &model.BlockerHold{Task: reduceRef(1, 1), BlockerID: reduceID(70), Reason: model.BlockerAwaitingAcceptance, Actor: model.Actor{ID: "reviewer"}, Criterion: "read before accepting"}
	first := reduceBundle(t, model.Bundle{}, create, hold)
	clear := &model.BlockerClear{Task: hold.Task, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: hold.Task, BlockerID: hold.BlockerID}, ResolvingWitness: reduceArtifact("read")}
	if _, err := reduce.Replay([]model.Bundle{first, reduceBundle(t, first, clear, reduceClose(1, 1, 1, model.ClosureSuccess))}); err != nil {
		t.Fatalf("recorded hold revision control: %v", err)
	}
	amended := reduceBundle(t, first, &model.TaskAmend{Target: hold.Task, Replacement: create.Spec, Provenance: create.Provenance})
	stale := reduceBundle(t, amended, clear, reduceClose(1, 2, 1, model.ClosureSuccess))
	if _, err := reduce.Replay([]model.Bundle{first, amended, stale}); err == nil {
		t.Error("replay accepted a clear naming the superseded task revision 1 after the amendment to revision 2")
	}
	current := *clear
	current.Task.Revision, current.HoldRef.Task.Revision = 2, 2
	last := reduceBundle(t, amended, &current, reduceClose(1, 2, 1, model.ClosureSuccess))
	s, err := reduce.Replay([]model.Bundle{first, amended, last})
	if err != nil {
		t.Fatalf("replay must accept the current-revision clear of a hold that survived the amendment, as admission does: %v", err)
	}
	if p, _ := s.Task(reduceIdent(1)); p.Status != reduce.StatusClosed || p.Outcome != model.ClosureSuccess {
		t.Errorf("after clearing its hold at the current revision the task must close as success, got %s %s", p.Status, p.Outcome)
	}
}
