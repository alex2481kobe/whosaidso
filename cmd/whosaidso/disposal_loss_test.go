package main

// End-to-end tests for `whosaidso check disposal` through fresh processes: the
// support_loss list it prints is exactly what admission requires of an
// artifact.dispose (pasted in whole it is admitted; with any one revision
// removed it is refused as loss-unaccounted), and printing it writes nothing.
// Admission rules live in internal/write; the list itself in internal/reduce.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// disposalWorld admits a proof that cites a run output, then two decisions
// that depend on the claim only transitively (the second via the first).
func disposalWorld(t *testing.T) (string, model.ArtifactRef, []model.RecordRef) {
	t.Helper()
	root, criterion, instrument, attempt := e2eWorld(t)
	out, err := e2eInvoke(t, root, nil, "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID),
		"--claim", string(criterion.Claim.RecordID), "--claim-revision", "1", "--criterion-id", string(criterion.CriterionID), "--criterion-revision", "1", "--", "/bin/sh", "tools/measure.sh")
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		Envelope    model.InvocationEnvelope `json:"envelope"`
		StartPacket model.PacketRef          `json:"start_packet"`
		SealPacket  model.PacketRef          `json:"seal_packet"`
	}
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(900)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "run", string(run.StartPacket.CommandID), string(run.SealPacket.CommandID)); err != nil {
		t.Fatal(err)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Verdict: model.VerdictSupports, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "agent"}, Reason: "the run passed"},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: "test/cli", InvocationID: run.Envelope.InvocationID}, Disposition: "supports", Reason: "passed"}}}
	if err := e2eAdmitOne(t, root, proof, 901, 902, "agent"); err != nil {
		t.Fatal(err)
	}
	agent := model.Provenance{SourceRefs: []model.ArtifactRef{}}
	open := func(id int, context model.RecordRef) *model.DecisionOpen {
		scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{context}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
		return &model.DecisionOpen{ID: cliID(id), Provenance: agent, Spec: model.DecisionSpec{Question: "ship on this claim?", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: scope}}
	}
	first, second := model.RecordRef{Project: "test/cli", RecordID: cliID(910), Revision: 1}, model.RecordRef{Project: "test/cli", RecordID: cliID(911), Revision: 1}
	if err := e2eAdmitOne(t, root, open(910, criterion.Claim), 912, 913, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := e2eAdmitOne(t, root, open(911, first), 914, 915, "agent"); err != nil {
		t.Fatal(err)
	}
	for _, out := range *run.Envelope.Outputs.Value {
		if out.Name == "out/result.json" {
			return root, out.Ref(), []model.RecordRef{criterion.Claim, first, second}
		}
	}
	t.Fatal("the run recorded no out/result.json output")
	return "", model.ArtifactRef{}, nil
}

// fileStamps fingerprints every file under root except the disposable
// snapshot cache's image and its temporaries, which a read may refresh.
func fileStamps(t *testing.T, root string) map[string]string {
	t.Helper()
	stamps := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || cacheImagePath(path) {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		stamps[path] = info.ModTime().String() + string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return stamps
}

// disposalJSON decodes `whosaidso check disposal --json`.
type disposalJSON struct {
	Mode         string                    `json:"mode"`
	Scope        string                    `json:"scope"`
	Watermark    map[string]any            `json:"watermark"`
	Result       string                    `json:"result"`
	Git          *model.GitPin             `json:"git"`
	SupportLoss  []model.RecordRef         `json:"support_loss"`
	CitingEvents []reduce.ArtifactCitation `json:"citing_events"`
}

// Disposal-loss is `whosaidso check disposal`, whose first line is its scope.
func TestCLIDisposalLossListIsExactlyWhatAdmissionRequires(t *testing.T) {
	root, output, dependents := disposalWorld(t)
	before := fileStamps(t, root)
	text := readProcess(t, root, nil, "check", "disposal", "--digest", string(output.Content.SHA256))
	answer := readJSON[disposalJSON](t, readProcess(t, root, nil, "check", "disposal", "--json", "--digest", string(output.Content.SHA256)))
	t.Logf("whosaidso check disposal --digest %s:\n%s", output.Content.SHA256, text)
	if after := fileStamps(t, root); len(after) != len(before) {
		t.Fatalf("check disposal wrote files: %d before, %d after", len(before), len(after))
	} else {
		for path, stamp := range before {
			if after[path] != stamp {
				t.Fatalf("check disposal changed %s", path)
			}
		}
	}
	scope := "disposal-loss preview at watermark " + fmt.Sprint(answer.Watermark["sequence"]) + ": admission recomputes and stays the authority; reasons are yours to write"
	if !strings.HasPrefix(string(text), scope+"\n") || answer.Scope != scope || answer.Mode != "disposal" || answer.Watermark["sequence"] == float64(0) {
		t.Fatalf("check disposal must open with its scope at its watermark: %s\n%+v", text, answer)
	}
	listed := answer.SupportLoss
	for _, want := range dependents {
		found := false
		for _, ref := range listed {
			found = found || ref == want
		}
		if !found {
			t.Fatalf("support_loss %+v omits %+v, a direct or transitive dependent", listed, want)
		}
	}
	if len(answer.CitingEvents) == 0 {
		t.Fatalf("no admitted event reported as citing the disposed output")
	}
	authority := e2eRuling(t, root, "rulings/dispose.json", `{"ruling":"delete that run output"}`)
	dispose := func(refs []model.RecordRef) *model.ArtifactDispose {
		loss := []model.SupportLoss{}
		for _, ref := range refs {
			loss = append(loss, model.SupportLoss{Target: ref, Reason: "its supporting run output is deleted"})
		}
		return &model.ArtifactDispose{Artifact: output, Digest: output.Content.SHA256, PreviousLocation: ".whosaidso/artifacts/" + string(output.Content.SHA256), SupportLoss: loss, Authority: authority}
	}
	// Every single omission is refused, so no entry the check prints is
	// surplus the gate would not have asked for.
	for i := range listed {
		short := append(append([]model.RecordRef{}, listed[:i]...), listed[i+1:]...)
		err := e2eAdmitOne(t, root, dispose(short), 920+2*i, 921+2*i, "recorder")
		if err == nil || !strings.Contains(err.Error(), "loss-unaccounted") {
			t.Fatalf("support_loss without %+v must be refused as loss-unaccounted, got %v", listed[i], err)
		}
	}
	if err := e2eAdmitOne(t, root, dispose(listed), 990, 991, "recorder"); err != nil {
		t.Fatalf("the printed support_loss list must be admitted as a disposal's list: %v", err)
	}
}

func TestCLIDisposalLossRefusesAMalformedIdentity(t *testing.T) {
	root, _ := cliFixture(t)
	digest := strings.Repeat("a", 64)
	for want, args := range map[string][]string{
		"digest must be 64": {"check", "disposal"},
		"digest must be":    {"check", "disposal", "--digest", "ABC"},
		"--git must be":     {"check", "disposal", "--digest", digest, "--git", "sha1:deadbeef"},
		"commit must be":    {"check", "disposal", "--digest", digest, "--git", "sha1:" + strings.Repeat("g", 40) + ":x.json"},
		"positional":        {"check", "disposal", "--digest", digest, string(cliID(1))},
		"not defined":       {"check", "disposal", "--digest", digest, "--events", "-"},
	} {
		if out, errs, code := cliRun(t, root, nil, "", args...); code != 2 || out != "" || !strings.Contains(errs, want) {
			t.Fatalf("%v must be a usage error naming %q, got %d %s %s", args, want, code, out, errs)
		}
	}
}

// A git-pinned citation is lost only by a disposal naming the same git pin; a
// content-pinned one by digest. The ledger is written directly: a git pin to
// a fixture commit could not pass admission, and the check reads the ledger.
func TestCLIDisposalLossPlansGitPinnedCitationsOnlyForTheSamePin(t *testing.T) {
	root, data := cliFixture(t)
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	pin := &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "docs/source.md"}
	body := []byte("source bytes")
	content := model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
	var events []model.Event
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	var tasks []model.Event
	for i, ref := range []model.ArtifactRef{{Kind: "git", Git: pin, Selector: model.Selector{Kind: "whole"}}, content} {
		typed, err := model.DecodeEvent(events[0])
		if err != nil {
			t.Fatal(err)
		}
		task := typed.(*model.TaskCreate)
		task.ID, task.Spec.AcceptanceCriteria[0].ID = cliID(10+i), cliID(20+i)
		task.Provenance.SourceRefs = []model.ArtifactRef{ref}
		encoded, err := model.EncodeEvent(task)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, encoded)
	}
	if _, err := store.Transact(context.Background(), project, cliID(30), model.HashBytes([]byte("pins")), func([]model.Bundle) (model.Bundle, error) {
		return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: []model.PacketRef{}, Events: tasks}, nil
	}); err != nil {
		t.Fatal(err)
	}
	plan := func(args ...string) disposalJSON {
		t.Helper()
		return readJSON[disposalJSON](t, readProcess(t, root, nil, append([]string{"check", "disposal", "--json"}, args...)...))
	}
	byDigest := plan("--digest", string(content.Content.SHA256))
	if len(byDigest.SupportLoss) != 1 || byDigest.SupportLoss[0].RecordID != cliID(11) || len(byDigest.CitingEvents) != 1 {
		t.Fatalf("a content disposal must lose exactly the content citer: %+v", byDigest)
	}
	withPin := plan("--digest", string(content.Content.SHA256), "--git", "sha1:"+pin.Commit+":"+pin.Path)
	if len(withPin.SupportLoss) != 2 || len(withPin.CitingEvents) != 2 || withPin.Git == nil || *withPin.Git != *pin {
		t.Fatalf("a disposal naming the git pin must also lose the git citer, and carry the pin it was given: %+v", withPin)
	}
	other := plan("--digest", string(model.HashBytes([]byte("unrelated"))), "--git", "sha1:"+pin.Commit+":docs/other.md")
	if len(other.SupportLoss) != 0 || len(other.CitingEvents) != 0 {
		t.Fatalf("a different pin and digest must lose nothing: %+v", other)
	}
}

// cacheImagePath reports the snapshot cache's image or one of its temporaries.
func cacheImagePath(path string) bool {
	name := filepath.Base(path)
	return filepath.Base(filepath.Dir(path)) == "cache" && (name == "snapshot" || strings.HasPrefix(name, ".snapshot-"))
}
