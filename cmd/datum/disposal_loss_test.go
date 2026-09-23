package main

// End-to-end tests for the disposal-loss read through fresh processes: the
// support_loss list it prints is exactly what admission requires of an
// artifact.dispose (pasted in whole it is admitted; with any one revision
// removed it is refused as loss-unaccounted), and printing it writes nothing.
// Admission rules live in internal/write; the list itself in internal/reduce.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/model"
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
		StartPacket model.PacketRef          `json:"StartPacket"`
		SealPacket  model.PacketRef          `json:"SealPacket"`
	}
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(900)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "run", string(run.StartPacket.CommandID), string(run.SealPacket.CommandID)); err != nil {
		t.Fatal(err)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "lane"}, Reason: "the run passed"},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: "test/cli", InvocationID: run.Envelope.InvocationID}, Disposition: "supports", Reason: "passed"}}}
	if err := e2eAdmitOne(t, root, proof, 901, 902, "lane"); err != nil {
		t.Fatal(err)
	}
	lane := model.Provenance{Author: model.Actor{ID: "lane"}, SourceRefs: []model.ArtifactRef{}}
	open := func(id int, context model.RecordRef) *model.DecisionOpen {
		scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{context}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
		return &model.DecisionOpen{ID: cliID(id), Provenance: lane, Spec: model.DecisionSpec{Question: "ship on this claim?", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: scope}}
	}
	first, second := model.RecordRef{Project: "test/cli", RecordID: cliID(910), Revision: 1}, model.RecordRef{Project: "test/cli", RecordID: cliID(911), Revision: 1}
	if err := e2eAdmitOne(t, root, open(910, criterion.Claim), 912, 913, "lane"); err != nil {
		t.Fatal(err)
	}
	if err := e2eAdmitOne(t, root, open(911, first), 914, 915, "lane"); err != nil {
		t.Fatal(err)
	}
	for _, ref := range *run.Envelope.OutputRefs.Value {
		if strings.HasSuffix(ref.Content.Locators[0].Path, "/out/result.json") {
			return root, ref, []model.RecordRef{criterion.Claim, first, second}
		}
	}
	t.Fatal("the run recorded no out/result.json output")
	return "", model.ArtifactRef{}, nil
}

func fileStamps(t *testing.T, root string) map[string]string {
	t.Helper()
	stamps := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
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

func TestCLIDisposalLossListIsExactlyWhatAdmissionRequires(t *testing.T) {
	root, output, dependents := disposalWorld(t)
	before := fileStamps(t, root)
	text := readProcess(t, root, nil, "disposal-loss", "--digest", string(output.Content.SHA256))
	answer := readJSON(t, readProcess(t, root, nil, "disposal-loss", "--json", "--digest", string(output.Content.SHA256)))
	t.Logf("datum disposal-loss --digest %s:\n%s", output.Content.SHA256, text)
	if after := fileStamps(t, root); len(after) != len(before) {
		t.Fatalf("disposal-loss wrote files: %d before, %d after", len(before), len(after))
	} else {
		for path, stamp := range before {
			if after[path] != stamp {
				t.Fatalf("disposal-loss changed %s", path)
			}
		}
	}
	if !strings.Contains(string(text), "watermark sequence") || answer.Watermark.Sequence == 0 || answer.Preset == nil || answer.Preset.Disposal == nil {
		t.Fatalf("disposal-loss must answer with a watermark and a disposal section: %+v", answer)
	}
	listed := answer.Preset.Disposal.SupportLoss
	for _, want := range dependents {
		found := false
		for _, ref := range listed {
			found = found || ref == want
		}
		if !found {
			t.Fatalf("support_loss %+v omits %+v, a direct or transitive dependent", listed, want)
		}
	}
	if len(answer.Preset.Disposal.CitedBy) == 0 {
		t.Fatalf("no admitted event reported as citing the disposed output")
	}
	authority := e2eRuling(t, root, "rulings/dispose.json", `{"ruling":"delete that run output"}`)
	dispose := func(refs []model.RecordRef) *model.ArtifactDispose {
		loss := []model.SupportLoss{}
		for _, ref := range refs {
			loss = append(loss, model.SupportLoss{Target: ref, Reason: "its supporting run output is deleted"})
		}
		return &model.ArtifactDispose{Artifact: output, Digest: output.Content.SHA256, PreviousLocation: output.Content.Locators[0].Path, SupportLoss: loss, Authority: authority}
	}
	// Every single omission is refused, so no entry the verb prints is surplus
	// the gate would not have asked for.
	for i := range listed {
		short := append(append([]model.RecordRef{}, listed[:i]...), listed[i+1:]...)
		err := e2eAdmitOne(t, root, dispose(short), 920+2*i, 921+2*i, "agent-sol")
		if err == nil || !strings.Contains(err.Error(), "loss-unaccounted") {
			t.Fatalf("support_loss without %+v must be refused as loss-unaccounted, got %v", listed[i], err)
		}
	}
	if err := e2eAdmitOne(t, root, dispose(listed), 990, 991, "agent-sol"); err != nil {
		t.Fatalf("the printed support_loss list must be admitted as a disposal's list: %v", err)
	}
}

func TestCLIDisposalLossRefusesAMalformedIdentity(t *testing.T) {
	root, _ := cliFixture(t)
	digest := strings.Repeat("a", 64)
	for want, args := range map[string][]string{
		"digest must be 64":     {"disposal-loss"},
		"digest must be":        {"disposal-loss", "--digest", "ABC"},
		"--git must be":         {"disposal-loss", "--digest", digest, "--git", "sha1:deadbeef"},
		"commit must be":        {"disposal-loss", "--digest", digest, "--git", "sha1:" + strings.Repeat("g", 40) + ":x.json"},
		"unexpected positional": {"disposal-loss", "--digest", digest, string(cliID(1))},
	} {
		if out, err := e2eInvoke(t, root, nil, args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%v must be refused with %q, got %s %v", args, want, out, err)
		}
	}
}

func TestCLIDisposalLossCarriesTheGitPinItWasGiven(t *testing.T) {
	root, _ := cliFixture(t)
	commit := strings.Repeat("a", 40)
	answer := readJSON(t, readProcess(t, root, nil, "disposal-loss", "--json", "--digest", strings.Repeat("b", 64), "--git", "sha1:"+commit+":docs/a.md"))
	got := answer.Preset.Disposal.Target.Git
	if got == nil || *got != (model.GitPin{ObjectFormat: "sha1", Commit: commit, Path: "docs/a.md"}) {
		t.Fatalf("disposal-loss planned for %+v, not the git pin it was given", got)
	}
}
