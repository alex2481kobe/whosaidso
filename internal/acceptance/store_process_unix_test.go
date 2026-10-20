//go:build darwin || linux

// Storage probes that need POSIX process control live here: a child stopped
// with SIGSTOP inside its admission, and a pipe filled to capacity through a
// non-blocking descriptor. Windows can do neither; the portable probes stay in
// store_process_test.go.
package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// Pause the real admitting process inside artifact validation
// under its lock, kill it, then recover a partial publication with the next CLI.
// The git fixture also tests root-relative pins below a repository's top level.
func TestStoreKilledAdmissionAndNestedGit(t *testing.T) {
	w := flowNew(t)
	repo := t.TempDir()
	w.root = filepath.Join(repo, "project")
	w.put("whosaidso.toml", []byte("id = 'flow/review'\nledger = '.whosaidso/events'\n"))
	w.put("evidence.json", []byte(`{"scope":"inside"}`))
	pvPut(t, repo, "evidence.json", []byte(`{"scope":"outside"}`))
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet")
	git("add", ".")
	git("commit", "--quiet", "-m", "nested fixture")
	claim := procClaim(w)
	pin := pvPin([]byte(`{"scope":"inside"}`), "evidence.json")
	pin.Kind, pin.Git = "git", &model.GitPin{ObjectFormat: git("rev-parse", "--show-object-format"), Commit: git("rev-parse", "HEAD"), Path: "evidence.json"}
	claim.Provenance.SourceRefs = []model.ArtifactRef{pin}
	w.mustAdmit(flowAgent, claim)
	before := w.ledger()
	claim.ID = w.id()
	args := procAdmission(w, w.capture(flowAgent, claim))
	shim := t.TempDir()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// Only the child gets this shim. Git still reads real committed objects.
	script := "#!/bin/sh\nkill -STOP \"$PPID\"\nprintf ready > \"$SHIM_MARKER\"\nexec \"$SHIM_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(shim, "shim-ready")
	cmd := procCommand(w, args...)
	cmd.Env = append(cmd.Env, "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"), "SHIM_MARKER="+marker, "SHIM_GIT="+realGit)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	procWait(t, func() bool { _, err := os.Stat(marker); return err == nil })
	lock, err := os.OpenFile(filepath.Join(w.root, ".whosaidso/events/.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK {
		t.Fatalf("control: interrupted admission must actually hold the lock, got %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !reflect.DeepEqual(before, w.ledger()) {
		t.Fatal("killed validation exposed a partial canonical transaction")
	}
	// A torn temporary models an interrupted file write, without racing a
	// sub-millisecond syscall window or adding production fault-injection hooks.
	partial := ".whosaidso/events/00000001-" + string(w.id()) + ".json.tmp"
	w.put(partial, []byte(`{"version":`))
	if _, err := w.cli(nil, args...); err != nil {
		t.Fatalf("next process must release the dead writer's lock and recover the partial file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.root, partial)); !os.IsNotExist(err) {
		t.Errorf("recovery retained its own partial publication: %v", err)
	}
	w.record(claim.ID)
}

// Losing the acknowledgement cannot roll back publication.
func TestStoreKilledAcknowledgement(t *testing.T) {
	w := flowNew(t)
	claim := procClaim(w)
	args := procAdmission(w, w.capture(flowAgent, claim))
	r, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer output.Close()
	// Fill to the OS's actual capacity; no payload-size or scheduling guess.
	fd := int(output.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := syscall.Write(fd, make([]byte, 4096)); err == syscall.EAGAIN {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	cmd := procCommand(w, args...)
	cmd.Stdout = output // deliberately undrained: acknowledgement blocks
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var published []byte
	procWait(t, func() bool {
		files, _ := filepath.Glob(filepath.Join(w.root, ".whosaidso/events/*-"+args[2]+".json"))
		if len(files) == 0 {
			return false
		}
		published, err = os.ReadFile(files[0])
		return err == nil
	})
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	got, err := w.cli(nil, args...)
	var a, b model.Bundle
	if err != nil || json.Unmarshal(got, &a) != nil || json.Unmarshal(published, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("retry must return the whole published bundle after SIGKILL, got %v", err)
	}
	w.record(claim.ID)
}
