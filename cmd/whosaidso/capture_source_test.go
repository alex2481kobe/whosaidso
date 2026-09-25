package main

// Capture durability for source.intake: the CLI saves the original's bytes into
// the packet, or refuses to acknowledge the capture. Admission rules do not
// belong here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func sourceEvents(t *testing.T, body []byte, locator string) []byte {
	t.Helper()
	locators := []model.Locator{}
	if locator != "" {
		locators = append(locators, model.Locator{Path: locator})
	}
	source, err := model.EncodeEvent(&model.SourceIntake{
		SourceID: cliID(40), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{},
		SourceRef: model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "text/plain", Locators: locators}, Selector: model.Selector{Kind: "whole"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode([]model.Event{source})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCaptureSavesSourceBytesBeforeAcknowledging(t *testing.T) {
	root, _ := cliFixture(t)
	body := []byte("the owner's words, which must outlive the file they arrived in")
	if err := os.WriteFile(filepath.Join(root, "ruling.txt"), body, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := callWriteCLI(t, root, sourceEvents(t, body, "ruling.txt"), "capture", "--command-id", string(cliID(41)), "--actor", "agent")
	if err != nil {
		t.Fatalf("control: a source resolving inside the root must be captured: %v", err)
	}
	var packet model.PacketRef
	if err := json.Unmarshal(out, &packet); err != nil {
		t.Fatal(err)
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := store.ReadVerifiedIntake(project, []model.ID{packet.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	want := store.CapturedBlob{SHA256: model.HashBytes(body), Length: uint64(len(body))}
	if len(verified[0].Blobs) != 1 || verified[0].Blobs[0] != want {
		t.Fatalf("acknowledged capture did not save the source bytes: %+v", verified[0].Blobs)
	}
	// The original disappears before review. The saved bytes carry admission.
	if err := os.Remove(filepath.Join(root, "ruling.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := callWriteCLI(t, root, nil, "admit", "--command-id", string(cliID(42)), "--actor", "reviewer",
		"--outcome", "accepted", "--reason", "the source survives its original", string(packet.CommandID)); err != nil {
		t.Fatalf("admission after the original was deleted: %v", err)
	}
}

func TestCaptureRefusesSourceBytesItCannotSave(t *testing.T) {
	root, _ := cliFixture(t)
	body := []byte("words nobody saved")
	for name, prepare := range map[string]func() string{
		"missing original": func() string { return "gone.txt" },
		"changed original": func() string {
			if err := os.WriteFile(filepath.Join(root, "edited.txt"), []byte("words somebody edited"), 0600); err != nil {
				t.Fatal(err)
			}
			return "edited.txt"
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := callWriteCLI(t, root, sourceEvents(t, body, prepare()), "capture", "--actor", "agent")
			if err == nil || !strings.Contains(err.Error(), "source-not-captured") {
				t.Fatalf("expected source-not-captured, got %v", err)
			}
		})
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if packets, err := store.ReadIntake(project, nil); err != nil || len(packets) != 0 {
		t.Fatalf("a refused capture left intake behind: %d packets, %v", len(packets), err)
	}
	// Control: the same source is acknowledged once its bytes are passed.
	blob := filepath.Join(t.TempDir(), "words.txt")
	if err := os.WriteFile(blob, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := callWriteCLI(t, root, sourceEvents(t, body, "gone.txt"), "capture", "--actor", "agent", "--blob", blob); err != nil {
		t.Fatalf("control: --blob must satisfy the source: %v", err)
	}
}
