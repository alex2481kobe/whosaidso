package write

// Ledger-check measurements use synthetic ledgers. Unknown classes must remain
// distinguishable from measured zeroes, including during comparisons.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func ledgerFixture(t *testing.T) *admissionFixture {
	t.Helper()
	f := newAdmissionFixture(t)
	if err := os.MkdirAll(f.project.Ledger, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.project.Root, f.project.ArtifactDir()), 0755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHOSAIDSO_HOME", home)
	proofPut(t, f.project.Root, "whosaidso.toml", "id = 'test/admission'\nledger = '.whosaidso/events'\n")
	identityGit(t, f.project.Root, "init", "-q")
	identityGit(t, f.project.Root, "add", "whosaidso.toml")
	identityGit(t, f.project.Root, "commit", "-q", "-m", "config")
	return f
}

func ledgerAnswer(t *testing.T, f *admissionFixture) map[string]any {
	t.Helper()
	a, err := CheckLedger(context.Background(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var answer map[string]any
	if err := json.Unmarshal(b, &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestLedgerClassesDistinguishUnmeasuredFromZero(t *testing.T) {
	f := ledgerFixture(t)
	check := func(wantReplay float64, state string) {
		t.Helper()
		answer := ledgerAnswer(t, f)
		classes, ok := answer["classes"].(map[string]any)
		if !ok {
			t.Fatalf("structured classes missing: %v", answer)
		}
		for _, name := range []string{"set_asides", "git_pins", "replay_error", "artifacts_missing", "not_committed"} {
			c, ok := classes[name].(map[string]any)
			if !ok {
				t.Fatalf("missing class %s", name)
			}
			want := state
			if name == "replay_error" {
				want = "measured"
			}
			if c["state"] != want {
				t.Errorf("%s: %v, want %s", name, c, want)
			}
			if want == "unmeasured" {
				if _, ok := c["count"]; ok {
					t.Errorf("unknown %s offers count: %v", name, c)
				}
			} else {
				n := float64(0)
				if name == "replay_error" {
					n = wantReplay
				}
				if c["count"] != n {
					t.Errorf("%s count %v, want %v", name, c["count"], n)
				}
			}
		}
	}
	check(0, "measured")
	proofPut(t, f.project.Root, ".whosaidso/events/00000001-00000000000000000000000001.json", "not a bundle")
	check(1, "unmeasured")
}

func TestLedgerMissingArtifactsCountIdentitiesNotCitations(t *testing.T) {
	f := ledgerFixture(t)
	var digests []string
	for order, body := range []string{"first", "second", "first"} {
		ref := proofPin(body, "")
		ref.Content.Locators = []model.Locator{}
		ref.Content.MediaType = "text/plain"
		digests = append(digests, string(ref.Content.SHA256))
		f.accept(f.capture([][]byte{[]byte(body)}, &model.SourceIntake{SourceID: f.id(), SourceRef: ref, Order: uint64(order),
			OriginalDigest: ref.Content.SHA256, Length: ref.Content.Length, Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{}}))
	}
	identityGit(t, f.project.Root, "add", ".whosaidso")
	identityGit(t, f.project.Root, "commit", "-q", "-m", "sources")
	// Moving the bytes away models a clone whose committed store is damaged.
	for _, digest := range digests[:2] {
		if err := os.Rename(filepath.Join(f.project.Root, f.project.ArtifactDir(), digest), filepath.Join(t.TempDir(), digest)); err != nil {
			t.Fatal(err)
		}
	}
	a := ledgerAnswer(t, f)
	classes, ok := a["classes"].(map[string]any)
	if !ok {
		t.Fatalf("no classes: %v", a)
	}
	c := classes["artifacts_missing"].(map[string]any)
	if c["state"] != "measured" || c["count"] != float64(2) {
		t.Fatalf("two missing digests cited three times: %v", c)
	}
	ids := c["identities"].([]any)
	if len(ids) != 2 || ids[0] != digests[0] || ids[1] != digests[1] {
		t.Fatalf("missing digest identities: %v", ids)
	}
}

func TestLedgerSetAsideCountsOnlyUnresolvedBundleIdentities(t *testing.T) {
	f := ledgerFixture(t)
	f.accept(f.capture(nil, f.task())) // The ledger has one shared genesis.
	first := f.accept(f.capture(nil, f.task()))
	branch := first
	branch.CommandID = f.id()
	name, err := model.BundleName(branch.Sequence, branch.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode(branch)
	if err != nil {
		t.Fatal(err)
	}
	proofPut(t, f.project.Root, ".whosaidso/events/"+name, string(data))
	a, err := CheckLedger(context.Background(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	c := a.Classes["set_asides"]
	want := fmt.Sprintf("%d:%s", branch.Sequence, branch.CommandID)
	if c.Count == nil || *c.Count != 1 || len(c.Identities) != 1 || c.Identities[0] != want {
		t.Fatalf("set-aside bundle identity: %+v", a)
	}
	f.accept(f.capture(nil, &model.SetAsideResolve{Bundle: branch.Ref(), Reason: "the same task on both branches"}))
	a, err = CheckLedger(context.Background(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	c = a.Classes["set_asides"]
	if c.Count == nil || *c.Count != 0 || len(c.Identities) != 0 || len(a.Resolved) != 1 {
		t.Fatalf("resolved bundle still counted: %+v", a)
	}
}
