package write

// Where a real run's outputs live between launch and admission: staged outside
// the project, captured into the seal's own blobs, then published once into the
// committed store by digest. Seal provenance refusals are in run_admission_test.go;
// process observation is in run_test.go.

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

const runStorageReport = `{"version":1,"config_effective":{},"conditions_observed":{},"outputs":[{"path":"out/result.json","media_type":"application/json"}]}`

// runTestStaging is where store.MakeRunStaging puts one invocation's staging.
func runTestStaging(t *testing.T, project store.Project, invocation model.ID) string {
	t.Helper()
	inbox, err := store.IntakeDir(project)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filepath.Dir(filepath.Dir(inbox)), "staging", filepath.Base(inbox), string(invocation))
}

// runStorageRequest is a real run of this test binary against the proof world's
// admitted attempt, instrument and criterion, writing body to out/result.json.
func runStorageRequest(t *testing.T, w *proofWorld, body, lock string) RunRequest {
	t.Helper()
	instrument, ok := w.f.snapshot().Instrument(reduce.Ident{Project: w.f.project.ID, ID: w.instrument.RecordID})
	if !ok {
		t.Fatal("fixture instrument missing")
	}
	return RunRequest{Author: w.f.author, AttemptID: w.attempt, InstrumentRef: w.instrument, Instrument: *instrument.Spec, CriterionRef: proofKnown(w.criterion),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: w.f.project.ID, MachineID: proofKnown(proofMachine), SourceRefs: []model.ArtifactRef{},
			Head: proofKnown(model.GitHead{ObjectFormat: "sha1", Commit: "0123456789abcdef0123456789abcdef01234567"}), Dirty: proofKnown(false)},
		Argv: []string{os.Args[0], "-test.run=^TestRunChildProcess$", "--", "output", proofPath, body, runStorageReport, lock}, Dir: w.f.project.Root}
}

// copiesIn counts regular files under root holding exactly data.
func copiesIn(t *testing.T, root string, data []byte) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		if err == nil && bytes.Equal(b, data) {
			found = append(found, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestRunAdmitCommitsEachOutputOnce(t *testing.T) {
	w := newProofWorld(t, true)
	result, err := Run(context.Background(), w.f.project, runStorageRequest(t, w, runOwnBody, ""))
	if err != nil || result.Envelope.Outputs.Value == nil {
		t.Fatalf("control: a real run must seal its outputs: %v", err)
	}
	if result.ArtifactDir != "" {
		t.Fatalf("a run whose seal captured every output must retire its staging, kept %s", result.ArtifactDir)
	}
	if _, err := os.Stat(runTestStaging(t, w.f.project, result.Envelope.InvocationID)); !os.IsNotExist(err) {
		t.Fatalf("retired staging still exists: %v", err)
	}
	w.f.accept(result.StartPacket, result.SealPacket)

	storeDir := filepath.Join(w.f.project.Root, filepath.FromSlash(w.f.project.ArtifactDir()))
	if _, err := os.Stat(filepath.Join(storeDir, "runs")); !os.IsNotExist(err) {
		t.Fatalf("the committed store must hold no run directory: %v", err)
	}
	for _, out := range *result.Envelope.Outputs.Value {
		want := filepath.Join(storeDir, string(out.SHA256))
		data, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("output %s was not published by digest: %v", out.Name, err)
		}
		if got := copiesIn(t, w.f.project.Root, data); len(got) != 1 || got[0] != want {
			t.Fatalf("output %s must exist exactly once in project storage, at %s; found %v", out.Name, want, got)
		}
	}
	// The output names still bind the observation to this run's bytes.
	ref := model.InvocationRef{Project: w.f.project.ID, InvocationID: result.Envelope.InvocationID}
	w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{ref: "supports"})))
	if status := w.status(t); status != reduce.StatusProven {
		t.Fatalf("a proof over the published output must reach PROVEN, got %s", status)
	}
}

func TestRunCaptureFailureKeepsStaging(t *testing.T) {
	project := runTestProject(t)
	runTestControl(t, project)
	inbox, err := store.IntakeDir(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(inbox, 0700) })
	result, err := Run(context.Background(), project, runTestRequest(project.Root, "output", proofPath, runOwnBody, runStorageReport, inbox))
	if err == nil || result.SealPacket.CommandID != "" || result.StartPacket.CommandID == "" {
		t.Fatalf("fixture: the seal's capture must fail after a durable start: %+v, %v", result, err)
	}
	if result.ArtifactDir == "" || result.ArtifactDir != runTestStaging(t, project, result.Envelope.InvocationID) {
		t.Fatalf("a run whose capture failed must report its kept staging, got %q", result.ArtifactDir)
	}
	data, err := os.ReadFile(filepath.Join(result.ArtifactDir, filepath.FromSlash(proofPath)))
	if err != nil || string(data) != runOwnBody {
		t.Fatalf("the uncaptured output must survive in staging: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(project.Root, filepath.FromSlash(project.ArtifactDir()))); !os.IsNotExist(err) {
		t.Fatalf("staging must never be inside the committed store: %v", err)
	}
}
