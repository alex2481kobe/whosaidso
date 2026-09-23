package store

// Tests for the read-side intake inventory (IntakeIDs) and the reviewed-packet
// digest check. Full packet verification is tested in intake_test.go.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"datum/internal/model"
)

func TestIntakeIDsListsOnlyPublishedPackets(t *testing.T) {
	p := intakeProject(t)
	if ids, err := IntakeIDs(p); err != nil || len(ids) != 0 || ids == nil {
		t.Fatalf("a missing inbox must list as empty, got %v, %v", ids, err)
	}
	capturedControl(t, p, commandID(2), "second")
	capturedControl(t, p, commandID(1), "first")
	inbox := filepath.Dir(packetDir(t, p, commandID(1)))
	// An unfinished writer's temporary is never a candidate.
	if err := os.Mkdir(filepath.Join(inbox, string(commandID(3))+"-123.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	ids, err := IntakeIDs(p)
	if err != nil || !reflect.DeepEqual(ids, []model.ID{commandID(1), commandID(2)}) {
		t.Fatalf("control: expected both packets in command-id order and no temporary, got %v, %v", ids, err)
	}
	for name, create := range map[string]func(string) error{
		"stray file":         func(path string) error { return os.WriteFile(path, nil, 0o600) },
		"non-ULID directory": func(path string) error { return os.Mkdir(path, 0o700) },
	} {
		stray := filepath.Join(inbox, "stray")
		if name == "stray file" {
			stray = filepath.Join(inbox, string(commandID(4)))
		}
		if err := create(stray); err != nil {
			t.Fatal(err)
		}
		if _, err := IntakeIDs(p); err == nil || !strings.Contains(err.Error(), "unexpected published intake entry") {
			t.Errorf("%s: the inventory must be refused as intake-corrupt, got %v", name, err)
		}
		if err := os.Remove(stray); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReviewedPacketDigestIsTheExactPacketBytes(t *testing.T) {
	p := intakeProject(t)
	ref := capturedControl(t, p, commandID(1), "source")
	if digest, err := ReviewedPacketDigest(p, ref.CommandID); err != nil || digest != ref.Digest {
		t.Fatalf("control: expected the acknowledged digest %s, got %s, %v", ref.Digest, digest, err)
	}
	if _, err := ReviewedPacketDigest(p, "not-a-ulid"); err == nil || !strings.Contains(err.Error(), "not a ULID") {
		t.Fatalf("an invalid id must never become a path, got %v", err)
	}
	if _, err := ReviewedPacketDigest(p, commandID(2)); err == nil || !strings.Contains(err.Error(), "intake-not-found") {
		t.Fatalf("an absent packet must be reported absent, got %v", err)
	}
}
