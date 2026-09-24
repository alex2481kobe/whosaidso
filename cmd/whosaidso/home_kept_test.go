package main

// whosaidso home PATH refuses a home whose artifact store lacks bytes its
// admitted records cite, on a move and on a first binding, and leaves the
// binding unchanged. Which artifacts a home must hold is decided in
// internal/write (HomeHoldsKeptArtifacts); the move's bundle comparison is
// tested with the rest of relocation in home_test.go.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

func TestCLIHomeRefusesAHomeMissingKeptArtifacts(t *testing.T) {
	root, _ := cliFixture(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("the owner's words, kept by admission in the artifact store")
	proofWrite(t, root, "ruling.txt", string(body))
	if _, errs, code := cliRun(t, root, sourceEvents(t, body, "ruling.txt"), "agent", "capture", "--admit", "--reason", "owner source", "--events", "-"); code != 0 {
		t.Fatalf("control: the source admits: %d %s", code, errs)
	}
	digest := string(model.HashBytes(body))
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(parent, "next")
	if err := exec.Command("cp", "-R", root, next).Run(); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(next, ".whosaidso", "artifacts", digest)
	still := func(want string) {
		t.Helper()
		if got, bound, err := store.Binding("test/cli"); err != nil || bound != (want != "") || got != want {
			t.Fatalf("a refused home changed the binding: %q %v %v, want %q", got, bound, err, want)
		}
	}
	refused := func(why string) {
		t.Helper()
		// ruling.txt, the source's locator, is still in next: only the store counts.
		_, errs, code := cliRun(t, next, nil, "", "home", ".")
		if code != 1 || !strings.Contains(errs, "home-evidence-missing") || !strings.Contains(errs, "sha256 "+digest+" cited by bundle 1 event 0 (source.intake)") || !strings.Contains(errs, why) {
			t.Fatalf("a home missing admitted evidence must not bind, naming the digest, its citer and %q: %d %q", why, code, errs)
		}
	}
	if err := os.Remove(kept); err != nil {
		t.Fatal(err)
	}
	refused("no such file")
	still(root)
	// The same length, different bytes: present is not the same as held.
	if err := os.WriteFile(kept, []byte(strings.Repeat("x", len(body))), 0600); err != nil {
		t.Fatal(err)
	}
	refused("hashes to")
	still(root)
	// A first binding has no old home to compare, and still refuses.
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "first"))
	refused("hashes to")
	still("")
	if err := os.WriteFile(kept, body, 0600); err != nil {
		t.Fatal(err)
	}
	if out, errs, code := cliRun(t, next, nil, "", "home", "."); code != 0 || !strings.HasPrefix(out, "bound test/cli") {
		t.Fatalf("control: a home holding its kept artifacts binds: %d %q %q", code, out, errs)
	}
}

// Only content artifacts are kept in the store: a git artifact resolves from
// the repository, and a recorded disposal says the bytes may be gone.
func TestCLIHomeAsksOnlyForArtifactsTheStoreKeeps(t *testing.T) {
	root, data := cliFixture(t)
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	pin := func(body []byte) model.ArtifactRef {
		return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
	}
	gone, kept := []byte("bytes the owner later disposed of"), []byte("bytes the store must keep")
	content, held := pin(gone), pin(kept)
	// The git pin carries a content pin as corroboration; the store keeps neither.
	elsewhere := []byte("bytes the repository holds")
	git := model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "docs/source.md"},
		Content: &model.ContentPin{SHA256: model.HashBytes(elsewhere), Length: uint64(len(elsewhere)), MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
	var events []model.Event
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	var tasks []model.Event
	for i, ref := range []model.ArtifactRef{git, content, held} {
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
	transact := func(id int, events []model.Event) {
		t.Helper()
		if _, err := store.Transact(context.Background(), project, cliID(id), model.HashBytes([]byte{byte(id)}), func([]model.Bundle) (model.Bundle, error) {
			return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: []model.PacketRef{}, Events: events}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	transact(30, tasks)
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "unbound"))
	_, errs, code := cliRun(t, root, nil, "", "home", ".")
	if code != 1 || !strings.Contains(errs, "does not hold 2 artifact(s)") || !strings.Contains(errs, "sha256 "+string(content.Content.SHA256)+" cited by bundle 1 event 1 (task.create)") ||
		!strings.Contains(errs, "sha256 "+string(held.Content.SHA256)+" cited by bundle 1 event 2 (task.create)") {
		t.Fatalf("control: both content citations' missing bytes refuse, and only those: %d %q", code, errs)
	}
	ruling := `{"ruling":"delete those bytes"}`
	if err := store.PublishArtifact(root, project.ArtifactDir(), []byte(ruling)); err != nil {
		t.Fatal(err)
	}
	authority := model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: e2ePin(ruling, "rulings/dispose.json", "application/json"), Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"},
		Scope: model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}}
	dispose, err := model.EncodeEvent(&model.ArtifactDispose{Artifact: content, Digest: content.Content.SHA256, PreviousLocation: ".whosaidso/artifacts/" + string(content.Content.SHA256),
		SupportLoss: []model.SupportLoss{{Target: model.RecordRef{Project: "test/cli", RecordID: cliID(11), Revision: 1}, Reason: "its source is deleted"}}, Authority: authority})
	if err != nil {
		t.Fatal(err)
	}
	transact(31, []model.Event{dispose})
	_, errs, code = cliRun(t, root, nil, "", "home", ".")
	if code != 1 || !strings.Contains(errs, "does not hold 1 artifact(s)") || !strings.Contains(errs, "sha256 "+string(held.Content.SHA256)) {
		t.Fatalf("a disposal excuses only the disposed bytes: %d %q", code, errs)
	}
	if err := store.PublishArtifact(root, project.ArtifactDir(), kept); err != nil {
		t.Fatal(err)
	}
	if out, errs, code := cliRun(t, root, nil, "", "home", "."); code != 0 || !strings.HasPrefix(out, "bound test/cli") {
		t.Fatalf("with the kept bytes held, a git citation and a disposed artifact ask nothing more of the store: %d %q %q", code, out, errs)
	}
}
