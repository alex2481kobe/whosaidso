// Independent storage/admission probes: real CLI processes, files, signals,
// replay and containment belong here; production fixes and proof policy do not.
// Process death is exercised, not power loss or a filesystem lying about fsync.
package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

func procClaim(w *flowWorld) *model.ClaimAssert {
	return &model.ClaimAssert{ID: w.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "publication preserves this claim", Falsifier: "the claim disappears on replay", Scope: w.scope, ExternalRefs: []model.ExternalReference{}}}
}

func procAdmission(w *flowWorld, packet model.ID) []string {
	// Writes print a one-line acknowledgement by default; --json is the full result this test decodes.
	return []string{"admit", "--command-id", string(w.id()), "--actor", "reviewer", "--outcome", "accepted", "--reason", "independent storage review", "--json", string(packet)}
}

func procCommand(w *flowWorld, args ...string) *exec.Cmd {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	w.t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, pvWhoSaidSo(w.t), args...)
	cmd.Dir, cmd.Env = w.root, append(os.Environ(), "HOME="+w.home)
	bindProjectHome(cmd)
	return cmd
}

func procWait(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("fixture did not reach its observable interruption boundary")
		}
		time.Sleep(time.Millisecond)
	}
}

// A published retry returns its existing
// result. The lookup must survive loss of the producer's machine-local inbox.
func TestStoreRetrySurvivesIntakeLoss(t *testing.T) {
	w := flowNew(t)
	args := procAdmission(w, w.capture(flowAgent, procClaim(w)))
	want, err := w.cli(nil, args...)
	if err != nil {
		t.Fatalf("control: initial admission: %v", err)
	}
	if got, err := w.cli(nil, args...); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("control: identical retry must return the same bundle: %v", err)
	}
	before := w.ledger()
	reads := map[string]map[string]any{}
	for _, verb := range []string{"show", "history", "todo"} { // the views replace state, now and context
		reads[verb] = w.readJSON(verb)
	}
	// Leave only whosaidso.toml and .whosaidso/events, with an empty machine inbox.
	if err := os.RemoveAll(w.home); err != nil {
		t.Fatal(err)
	}
	for verb, want := range reads {
		for i := 0; i < 2; i++ {
			if got := w.readJSON(verb); !reflect.DeepEqual(got, want) {
				t.Errorf("%s changed after deleting intake; canonical answers must replay from the ledger alone", verb)
			}
		}
	}
	if got, err := w.cli(nil, args...); err != nil || !bytes.Equal(got, want) {
		t.Errorf("expected the already-published bundle without local intake; got %q, %v; a moved/cloned project cannot recover an admission acknowledgement", got, err)
	}
	if !reflect.DeepEqual(before, w.ledger()) {
		t.Error("retry rewrote canonical bytes")
	}
}

// Real filesystem paths stay confined,
// including the admission lock, not merely the spelling in whosaidso.toml.
func TestStoreLockCannotCreateOutsideRoot(t *testing.T) {
	w := flowNew(t)
	w.mustAdmit(flowAgent, procClaim(w)) // passing ordinary-lock control
	before := w.ledger()
	lock := filepath.Join(w.root, ".whosaidso/events/.lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "escaped-lock")
	if err := os.Symlink(outside, lock); err != nil {
		t.Fatal(err)
	}
	args := procAdmission(w, w.capture(flowAgent, procClaim(w)))
	// ReadDir's dotfile omission is intentional; do not follow this dangling
	// lock through flowWorld.ledger while establishing the pre-admission bytes.
	_, err := w.cli(nil, args...)
	if err == nil {
		t.Error("expected refusal of an escaping lock symlink; admission succeeded, so its real lock path leaves the whosaidso root")
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Errorf("expected no external file; opening the lock created %s: %v; a confined admission wrote outside its project", outside, err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	w.put(".whosaidso/events/.lock", nil) // restore the fixture's lock to compare bundles
	if !reflect.DeepEqual(before, w.ledger()) {
		t.Error("expected an unchanged ledger after refusing an escaping lock; a new canonical bundle appeared")
	}
}

// Five processes overlap capture/admit/run;
// each admission result must occur once in one replayable, contiguous chain.
func TestStoreConcurrentProcesses(t *testing.T) {
	w := flowProofWorld(t)
	gate := make(chan struct{})
	results := make(chan error, 5)
	var claims []model.ID
	for i := 0; i < 4; i++ {
		claim := procClaim(w)
		claims = append(claims, claim.ID)
		body, err := model.Encode([]model.Event{recEncode(t, claim)})
		if err != nil {
			t.Fatal(err)
		}
		packet := w.id()
		// Writes print a one-line acknowledgement by default; --json is the full result this test decodes.
		capture := []string{"capture", "--json", "--actor", flowAgent, "--command-id", string(packet), "--events", "-"}
		admit := procAdmission(w, packet)
		go func() {
			<-gate
			first, err := w.cli(body, capture...)
			if err == nil {
				var retry []byte
				retry, err = w.cli(body, capture...)
				if err == nil && !bytes.Equal(first, retry) {
					err = fmt.Errorf("capture retry changed immutable packet identity")
				}
			}
			if err == nil {
				first, err = w.cli(nil, admit...)
			}
			if err == nil {
				var retry []byte
				retry, err = w.cli(nil, admit...)
				if err == nil && !bytes.Equal(first, retry) {
					err = fmt.Errorf("admission retry changed canonical bundle")
				}
			}
			results <- err
		}()
	}
	go func() {
		<-gate
		_, packets, err := w.run(pvProducer(pvPass))
		if err == nil {
			err = w.review("accepted", packets...)
		}
		results <- err
	}()
	close(gate)
	for i := 0; i < 5; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	p, err := store.Discover(w.root)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := store.ReadPrefix(p) // rejects forks, gaps and broken predecessors
	if err != nil {
		t.Fatal(err)
	}
	a, err := reduce.Replay(bundles)
	if err != nil {
		t.Fatal(err)
	}
	b, err := reduce.Replay(bundles)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("same ledger produced different replay state: %v", err)
	}
	for _, id := range claims {
		w.record(id) // every acknowledged claim is present, not just a valid prefix
	}
}

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
	script := "#!/bin/sh\nkill -STOP \"$PPID\"\nprintf ready > \"$ASTRA_MARKER\"\nexec \"$ASTRA_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(shim, "shim-ready")
	cmd := procCommand(w, args...)
	cmd.Env = append(cmd.Env, "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"), "ASTRA_MARKER="+marker, "ASTRA_GIT="+realGit)
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
