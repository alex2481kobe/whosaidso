// Gallery commands use the existing admission policy and require explicit
// verdict authors; they never silently publish a captured record.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestGalleryAdmissionPolicyAndVerdictFlags(t *testing.T) {
	t.Setenv(store.HomeEnv, t.TempDir())
	root, _ := cliFixture(t)
	config := filepath.Join(root, "whosaidso.toml")
	f, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("self_admission = 'refuse'\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	out, diagnostic, code := cliRun(t, root, nil, "contributor", "gallery", "add", "clip.mp4", "--title", "Render", "--admit", "--reason", "reviewed")
	if code != 4 || !strings.Contains(out, "captured") {
		t.Fatalf("self-admission policy bypassed: %d %s %s", code, out, diagnostic)
	}
	listed, _ := galleryCommand(t, root, "gallery", "list")
	if !strings.Contains(listed, "empty") {
		t.Fatalf("refused admission published: %s", listed)
	}
	entry := galleryEntryID(t, diagnostic)
	packet := strings.TrimSuffix(strings.Fields(out)[1], ";")
	if _, errs, code := cliRun(t, root, nil, "reviewer", "admit", packet, "--outcome", "accepted", "--reason", "reviewed independently"); code != 0 {
		t.Fatalf("independent admission: %d %s", code, errs)
	}
	for _, args := range [][]string{{"gallery", "approve", entry}, {"gallery", "reject", entry, "--by", "reviewer"}, {"gallery", "reject", entry, "--by", "reviewer", "--note", " "}} {
		if _, _, code := cliRun(t, root, nil, "contributor", args...); code == 0 {
			t.Fatalf("missing verdict fields accepted: %v", args)
		}
	}
	out, _ = galleryCommand(t, root, "gallery", "add", "clip.mp4", "--title", "Another render", "--json")
	var captured struct {
		CommandID string `json:"command_id"`
		Created   []struct{ ID string }
	}
	if err := json.Unmarshal([]byte(out), &captured); err != nil || captured.CommandID == "" || len(captured.Created) != 1 || captured.Created[0].ID == "" {
		t.Fatalf("gallery capture omitted ids: %s %v", out, err)
	}
}
