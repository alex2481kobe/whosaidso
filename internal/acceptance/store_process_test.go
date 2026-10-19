// Independent storage/admission probes: real CLI processes, files, signals,
// replay and containment belong here; production fixes and proof policy do not.
// Process death is exercised, not power loss or a filesystem lying about fsync.
package acceptance_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
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
	cmd.Dir, cmd.Env = w.root, append(os.Environ(), "HOME="+w.home, "USERPROFILE="+w.home)
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
