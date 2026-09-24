package query

// What a read guarantees about intake, per packet: every packet the answer
// presents (pending, or reviewed but not accepted) is fully verified, blobs
// included; a packet the ledger accepted is dropped from the answer after its
// directory and packet.json are checked as real owner-only entries whose bytes
// still hash to the reviewed digest, without decoding it or rehashing its
// blobs. Admission's own verification is tested in internal/store and write.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

type trioPacket struct{ dir, blob string }

// intakeTrio captures three packets with one blob each, accepts the first and
// rejects the second; the third stays pending.
func intakeTrio(t *testing.T) (store.Project, map[string]trioPacket) {
	t.Helper()
	p := testProject(t)
	inbox, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]model.PacketRef{}
	out := map[string]trioPacket{}
	for i, name := range []string{"accepted", "rejected", "pending"} {
		body := []byte("blob bytes of the " + name + " packet")
		event, err := model.EncodeEvent(testTask(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		ref, err := store.WriteIntake(context.Background(), p, store.IntakeRequest{CommandID: testID(i + 1000),
			Author: model.Actor{ID: "author"}, Events: []model.Event{event}, Blobs: []io.Reader{bytes.NewReader(body)}})
		if err != nil {
			t.Fatalf("control capture must succeed: %v", err)
		}
		refs[name] = ref
		dir := filepath.Join(inbox, string(ref.CommandID))
		out[name] = trioPacket{dir: dir, blob: filepath.Join(dir, "blobs", string(model.HashBytes(body)))}
	}
	reviewPacket(t, p, 1, refs["accepted"], "accepted")
	reviewPacket(t, p, 2, refs["rejected"], "rejected")
	return p, out
}

// tamper rewrites a published file in place, keeping its owner-only mode. A
// packet.json gets a forged author (a change of content, not of spelling); a
// blob gets one more byte.
func tamper(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(data, ' ')
	if filepath.Base(path) == "packet.json" {
		changed = bytes.ReplaceAll(data, []byte(`: "author"`), []byte(`: "forger"`))
		if bytes.Equal(changed, data) {
			t.Fatal("control tamper did not apply")
		}
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readsFail(t *testing.T, p store.Project, want string) {
	t.Helper()
	// Intake pending is a todo section (R19): todo is the read that presents it.
	_, err := ReadView(p, ViewRequest{View: "todo"})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("todo must refuse with %q, got %v", want, err)
	}
}

func TestReadIntakeGuaranteePerPacket(t *testing.T) {
	t.Run("control", func(t *testing.T) {
		p, _ := intakeTrio(t)
		intake := todoOf(t, p).IntakePending
		if len(intake) != 2 || intake[0].Disposition != "rejected" || intake[1].Disposition != "pending" ||
			intake[0].Packet == nil || intake[1].Packet == nil || intake[0].Review == nil ||
			intake[0].Packet.CommandID != testID(1001) || intake[1].Packet.CommandID != testID(1002) {
			t.Fatalf("todo must present the rejected packet with its disposition and the pending one, both decoded, and drop the accepted one: %+v", intake)
		}
	})
	// Presented packets: full verification, blobs included.
	t.Run("pending blob tampered", func(t *testing.T) {
		p, trio := intakeTrio(t)
		tamper(t, trio["pending"].blob)
		readsFail(t, p, "blob bytes do not match their identity")
	})
	t.Run("rejected blob tampered", func(t *testing.T) {
		p, trio := intakeTrio(t)
		tamper(t, trio["rejected"].blob)
		readsFail(t, p, "blob bytes do not match their identity")
	})
	t.Run("pending packet tampered", func(t *testing.T) {
		p, trio := intakeTrio(t)
		tamper(t, filepath.Join(trio["pending"].dir, "packet.json"))
		readsFail(t, p, "request digest does not match")
	})
	t.Run("rejected packet tampered", func(t *testing.T) {
		p, trio := intakeTrio(t)
		tamper(t, filepath.Join(trio["rejected"].dir, "packet.json"))
		readsFail(t, p, "no longer matches its reviewed bytes")
	})
	// Accepted packets: packet.json against the reviewed digest, as real entries.
	t.Run("accepted packet tampered", func(t *testing.T) {
		p, trio := intakeTrio(t)
		tamper(t, filepath.Join(trio["accepted"].dir, "packet.json"))
		readsFail(t, p, "no longer matches its reviewed bytes")
	})
	t.Run("accepted packet symlinked to its own bytes", func(t *testing.T) {
		p, trio := intakeTrio(t)
		path := filepath.Join(trio["accepted"].dir, "packet.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		elsewhere := filepath.Join(t.TempDir(), "packet.json")
		if err := os.WriteFile(elsewhere, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, path); err != nil {
			t.Fatal(err)
		}
		readsFail(t, p, "never a symlink")
	})
	t.Run("accepted packet readable by others", func(t *testing.T) {
		p, trio := intakeTrio(t)
		if err := os.Chmod(filepath.Join(trio["accepted"].dir, "packet.json"), 0o644); err != nil {
			t.Fatal(err)
		}
		readsFail(t, p, "insecure-permissions")
	})
	t.Run("accepted packet directory readable by others", func(t *testing.T) {
		p, trio := intakeTrio(t)
		if err := os.Chmod(trio["accepted"].dir, 0o755); err != nil {
			t.Fatal(err)
		}
		readsFail(t, p, "insecure-permissions")
	})
	// The stated blind spot: a read does not rehash an accepted packet's blobs.
	// Admission verified them before the review existed; the full read that
	// admission and reconciliation use still refuses the tampered packet.
	t.Run("accepted blob tampered is left to full verification", func(t *testing.T) {
		p, trio := intakeTrio(t)
		tamper(t, trio["accepted"].blob)
		intake := todoOf(t, p).IntakePending
		if len(intake) != 2 || intake[0].CommandID == testID(1000) || intake[1].CommandID == testID(1000) {
			t.Fatalf("todo must still drop the accepted packet, never present it: %+v", intake)
		}
		if _, err := store.ReadIntake(p, nil); err == nil || !strings.Contains(err.Error(), "blob bytes do not match their identity") {
			t.Fatalf("full verification must still refuse the tampered accepted packet, got %v", err)
		}
	})
}
