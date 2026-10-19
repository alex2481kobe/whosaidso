package main

// End-to-end parity checks compare captured event bytes with a template control.
// Only freshly minted record identities are aligned; envelope ids and clocks
// differ by construction. Admission and artifact checks have separate controls.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type authoredPacket struct {
	store.VerifiedPacket
	created []createdRecord
}

func authoringPacket(t *testing.T, root string, args ...string) authoredPacket {
	t.Helper()
	out, errs, code := cliRun(t, root, nil, "worker", append(args, "--json")...)
	var answer captureAnswer
	if code != 0 || json.Unmarshal([]byte(out), &answer) != nil {
		t.Fatalf("%v: %d %s %s", args, code, out, errs)
	}
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	packets, err := store.ReadVerifiedIntake(p, []model.ID{answer.CommandID})
	if err != nil || len(packets) != 1 {
		t.Fatalf("read captured packet: %v", err)
	}
	return authoredPacket{packets[0], answer.Created}
}

func authoringParity(t *testing.T, root string, control, candidate []string) {
	t.Helper()
	// Control is captured first; a command that captures nothing cannot pass.
	wantPacket := authoringPacket(t, root, append([]string{"template"}, append(control, "--capture")...)...)
	gotPacket := authoringPacket(t, root, candidate...)
	want, got := wantPacket.Packet, gotPacket.Packet
	if got.Author != want.Author || got.Project != want.Project {
		t.Fatalf("packet attribution differs: %+v %+v", got, want)
	}
	// Minting is intentionally fresh for each invocation. Align exactly the
	// schema's created-id fields, preserving every other byte of the event.
	wc, gc := wantPacket.created, gotPacket.created
	if len(wc) != len(gc) {
		t.Fatalf("created ids differ: %+v %+v", wc, gc)
	}
	data, err := model.Encode(got.Events)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range wc {
		g := gc[i]
		if g.Path != w.Path || g.Type != w.Type {
			t.Fatalf("created field differs: %+v %+v", g, w)
		}
		if g.ID == w.ID {
			t.Fatal("new record identity must be fresh")
		}
		data = bytes.ReplaceAll(data, []byte(g.ID), []byte(w.ID))
	}
	expected, err := model.Encode(want.Events)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, expected) {
		t.Fatalf("captured event bytes differ:\nwant %s\ngot  %s", expected, data)
	}
	if !bytes.Equal(mustAuthoringJSON(t, wantPacket.Blobs), mustAuthoringJSON(t, gotPacket.Blobs)) {
		t.Fatalf("blob identities differ: %+v %+v", wantPacket.Blobs, gotPacket.Blobs)
	}
}

func mustAuthoringJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAuthoringPacketParity(t *testing.T) {
	t.Run("task new", func(t *testing.T) {
		root, input := cliFixture(t)
		cliControl(t, root, input)
		proofWrite(t, root, "request.txt", "Export requested\n")
		ref := model.RecordRef{Project: "test/cli", RecordID: cliID(1), Revision: 1}
		control := []string{"task.create", "--set", "provenance.source_refs=[]", "--set", "spec.subject=Export report", "--set", "spec.intent=Export results",
			"--set", "spec.next_actor.id=worker", "--set", "spec.scope.applies_when=Report export", "--set", "spec.scope.limitations=CSV only",
			"--set", "spec.scope.source_paths=[]", "--set", "spec.scope.context_refs=[]", "--set", "spec.context_refs=[]", "--set", "spec.constraint_refs=[]",
			"--set", jsonSet("spec.prerequisites", []model.Prerequisite{{Kind: "task-success", Target: ref, WaiverPolicy: "forbid"}}),
			"--set", `spec.non_goals=["PDF export","Charts"]`, "--set", "spec.acceptance_criteria=[]", "--set", "spec.acceptance_criteria[0].criterion=CSV opens", "--set", "spec.acceptance_criteria[1].criterion=Rows match"}
		authoringParity(t, root, control, []string{"task", "new", "--subject", "Export report", "--intent", "Export results", "--for", "worker", "--criterion", "CSV opens", "--criterion", "Rows match", "--non-goal", "PDF export", "--non-goal", "Charts", "--applies-when", "Report export", "--limitations", "CSV only", "--after", string(cliID(1))})
	})
	t.Run("task amend", func(t *testing.T) {
		root, input := cliFixture(t)
		cliControl(t, root, input)
		authoringParity(t, root, []string{"task.amend", "--from", string(cliID(1)), "--set", "provenance.source_refs=[]", "--set", "replacement.next_actor.id=reviewer"}, []string{"task", "amend", string(cliID(1)), "--for", "reviewer"})
	})
	t.Run("task close", func(t *testing.T) {
		root, input := cliFixture(t)
		cliControl(t, root, input)
		proofWrite(t, root, "report.txt", "CSV opens\n")
		authoringParity(t, root, []string{"task.close", "--task", string(cliID(1)), "--set", "outcome=success", "--pin", "acceptance_witness_refs[0].witness_ref=report.txt", "--pin", "delivery_witness_refs[0]=report.txt"}, []string{"task", "close", string(cliID(1)), "--outcome", "success", "--witness", "report.txt", "--reason", "Reviewed the CSV"})
	})
	t.Run("source add", func(t *testing.T) {
		root, input := cliFixture(t)
		cliControl(t, root, input)
		proofWrite(t, root, "request.txt", "Export requested\n")
		ref := model.RecordRef{Project: "test/cli", RecordID: cliID(1), Revision: 1}
		authoringParity(t, root, []string{"source.intake", "--pin", "source_ref=request.txt", "--set", "speaker.id=requester", "--set", "order=0", "--set", jsonSet("referents", []model.RecordRef{ref})}, []string{"source", "add", "request.txt", "--speaker", "requester", "--order", "0", "--about", string(cliID(1))})
	})
}

func TestAuthoringMissingFieldsAreCollected(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"task", "new", "--admit"}, []string{"--subject", "--intent", "--for", "--criterion", "--non-goal", "--applies-when", "--limitations", "--reason"}},
		{[]string{"task", "amend", "--admit"}, []string{"TASK", "--reason"}},
		{[]string{"task", "close", "--authority", string(cliID(9))}, []string{"TASK", "--outcome", "--reason", "--applies-when", "--limitations"}},
		{[]string{"task", "amend", string(cliID(1)), "--subject", " ", "--intent", "", "--for", " ", "--criterion", "valid", "--criterion", " ", "--non-goal", "", "--source", "", "--pin", "", "--after", ""}, []string{"--subject", "--intent", "--for", "--criterion", "--non-goal", "--source", "--pin", "--after"}},
		{[]string{"source", "add", "--admit"}, []string{"FILE", "--speaker", "--order", "--reason"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			// No project exists: missing input must be diagnosed before project IO.
			out, errs, code := cliRun(t, t.TempDir(), nil, "", tc.args...)
			if code != 2 || out != "" {
				t.Fatalf("missing fields must be usage failure: %d %s %s", code, out, errs)
			}
			for _, flag := range tc.want {
				if !strings.Contains(errs, flag) {
					t.Errorf("missing %s in %q", flag, errs)
				}
			}
		})
	}
}

func TestSourceAddPinsLiteralFileBytes(t *testing.T) {
	root, _ := cliFixture(t)
	files := []struct{ name, body string }{
		{"request.txt", "plain request\n"},
		{"request.txt@", "request with a trailing at sign\n"},
		{"request.txt#", "request with a hash sign\n"},
	}
	for _, file := range files {
		proofWrite(t, root, file.name, file.body)
	}
	for _, file := range files {
		t.Run(file.name, func(t *testing.T) {
			packet := authoringPacket(t, root, "source", "add", file.name, "--speaker", "requester", "--order", "0")
			if len(packet.Packet.Events) != 1 {
				t.Fatalf("expected one source event, got %d", len(packet.Packet.Events))
			}
			event, err := model.DecodeEvent(packet.Packet.Events[0])
			if err != nil {
				t.Fatal(err)
			}
			source, ok := event.(*model.SourceIntake)
			if !ok {
				t.Fatalf("expected source.intake, got %T", event)
			}
			want := model.HashBytes([]byte(file.body))
			length := uint64(len(file.body))
			if source.OriginalDigest != want || source.Length != length {
				t.Errorf("%q: captured digest %s, length %d; want own digest %s, length %d", file.name, source.OriginalDigest, source.Length, want, length)
			}
			ref := source.SourceRef
			if ref.Kind != "content" || ref.Content == nil || ref.Content.SHA256 != want || ref.Content.Length != length || ref.Selector != (model.Selector{Kind: "whole"}) {
				t.Errorf("%q: expected whole-file content pin of its own bytes, got %+v", file.name, ref)
			}
			if len(packet.Blobs) != 1 || packet.Blobs[0] != (store.CapturedBlob{SHA256: want, Length: length}) {
				t.Errorf("%q: captured blobs do not preserve its own bytes: %+v", file.name, packet.Blobs)
			}
		})
	}
}
