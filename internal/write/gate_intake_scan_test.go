package write

// Tests for the admission's one shared intake inventory (pendingIntake): every
// retained packet and blob is still verified, a corrupt blob in any packet
// refuses a proof admission exactly as a direct verified read refuses, the
// inventory is read once per admission however many proofs or UNKNOWN seals
// ask, only when one asks, and never reused by a later command. Pending-intake
// rules themselves are tested in gate_family_test.go and reconcile_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// countScans wraps the inventory read for the rest of the test.
func countScans(t *testing.T) *int {
	t.Helper()
	n := 0
	real := scanIntake
	scanIntake = func(project store.Project) ([]model.Packet, error) {
		n++
		return real(project)
	}
	t.Cleanup(func() { scanIntake = real })
	return &n
}

// threeProofs admits three claims' criteria and one passing run each, then
// captures one packet carrying a proof for each criterion.
func threeProofs(t *testing.T) (*proofWorld, model.PacketRef) {
	w, proofs := threeProofEvents(t)
	return w, w.f.capture(nil, proofs...)
}

func threeProofEvents(t *testing.T) (*proofWorld, []model.TypedEvent) {
	t.Helper()
	w := newProofWorld(t, true)
	proofs := []model.TypedEvent{}
	for i := 0; i < 3; i++ {
		criterion := w.criterion
		if i > 0 {
			claim := w.f.claim()
			w.f.accept(w.f.capture(nil, claim))
			criterion = w.fix(w.f.ref(claim.ID, 1))
		}
		pass, start, seal := w.run(criterion, proofPass)
		w.f.accept(start, seal)
		proofs = append(proofs, w.proof(criterion, map[model.InvocationRef]string{pass: "supports"}))
	}
	return w, proofs
}

func TestPendingIntakeReadOncePerAdmission(t *testing.T) {
	t.Run("three-proofs-one-scan", func(t *testing.T) {
		w, proofs := threeProofs(t)
		scans := countScans(t)
		w.f.accept(proofs)
		if *scans != 1 {
			t.Fatalf("three proofs in one admission read intake %d times, want 1", *scans)
		}
	})
	t.Run("no-proof-no-scan", func(t *testing.T) {
		f := newAdmissionFixture(t)
		scans := countScans(t)
		f.accept(f.capture(nil, f.task()))
		if *scans != 0 {
			t.Fatalf("an admission with no proof or UNKNOWN seal read intake %d times", *scans)
		}
	})
	t.Run("never-across-commands", func(t *testing.T) {
		w, proofs := threeProofs(t)
		scans := countScans(t)
		// A dry run, then the admission itself: each command reads intake afresh.
		if check, err := CheckAdmission(context.Background(), w.f.project, []model.ID{proofs.CommandID}, nil, model.Actor{ID: "coordinator"}); err != nil || len(check.Refusals) != 0 || *scans != 1 {
			t.Fatalf("control dry run: %+v, %v, scans %d", check, err, *scans)
		}
		before := *scans
		w.f.accept(proofs)
		if *scans != before+1 {
			t.Fatalf("a second admission reused intake read by the first: scans %d then %d", before, *scans)
		}
	})
	t.Run("unknown-seal-and-proof-share-one-scan", func(t *testing.T) {
		w, pass, dead, _ := deadRun(t)
		seal, err := Reconcile(context.Background(), w.f.project, ReconcileRequest{Author: w.f.author, InvocationID: dead.InvocationID, Reason: "runner host lost power"})
		if err != nil {
			t.Fatal(err)
		}
		proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports", dead: "inconclusive"}))
		scans := countScans(t)
		w.f.accept(seal, proof)
		if *scans != 1 || w.status(t) != reduce.StatusProven {
			t.Fatalf("an UNKNOWN seal and a proof in one admission: scans %d, status %s", *scans, w.status(t))
		}
	})
	t.Run("dry-run-members-share-one-scan", func(t *testing.T) {
		w, proofs := threeProofs(t)
		scans := countScans(t)
		check, err := CheckAdmission(context.Background(), w.f.project, []model.ID{proofs.CommandID}, nil, model.Actor{ID: "coordinator"})
		if err != nil || len(check.Refusals) != 0 || len(check.Members) != 3 {
			t.Fatalf("control dry run: %+v, %v", check, err)
		}
		if *scans != 1 {
			t.Fatalf("a dry run of three proofs read intake %d times, want 1", *scans)
		}
	})
}

// Every packet in the inbox that holds a blob is corrupted in turn: a proof
// admission of one proof and of three must each refuse with exactly the error
// a direct verified read of the whole inbox gives, and publish nothing.
func TestProofAdmissionRefusesCorruptBlobAnywhereInIntake(t *testing.T) {
	w, events := threeProofEvents(t)
	proofs, single := w.f.capture(nil, events...), w.f.capture(nil, events[0])
	// An unrelated packet nobody has reviewed also sits in the inbox.
	w.f.capture([][]byte{[]byte("unreviewed bytes")}, w.f.task())
	ctx := context.Background()
	inbox, err := store.IntakeDir(w.f.project)
	if err != nil {
		t.Fatal(err)
	}
	if check, err := CheckAdmission(ctx, w.f.project, []model.ID{proofs.CommandID}, nil, model.Actor{ID: "coordinator"}); err != nil || len(check.Refusals) != 0 {
		t.Fatalf("control: the intact inbox must pass: %+v, %v", check, err)
	}
	packets, err := store.ReadVerifiedIntake(w.f.project, nil)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := 0
	for _, packet := range packets {
		for _, blob := range packet.Blobs {
			path := filepath.Join(inbox, string(packet.Ref.CommandID), "blobs", string(blob.SHA256))
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Same length reaches the blob's own hash; a longer blob also
			// changes the inventory the request digest covers.
			flipped := append([]byte(nil), original...)
			flipped[0] ^= 1
			for _, corrupt := range [][]byte{flipped, append([]byte("corrupt "), original...)} {
				if err := os.WriteFile(path, corrupt, 0600); err != nil {
					t.Fatal(err)
				}
				_, want := store.ReadIntake(w.f.project, nil)
				if want == nil {
					t.Fatalf("control: the direct read did not see the corrupt blob in %s", packet.Ref.CommandID)
				}
				for _, ref := range []model.PacketRef{single, proofs} {
					if ref.CommandID == packet.Ref.CommandID {
						continue // the admission's own packet fails its own read first
					}
					head := w.f.snapshot().Watermark()
					_, got := Admit(ctx, w.f.project, w.f.request(ref))
					if got == nil || got.Error() != want.Error() || admissionErrorCode(got) != "intake-corrupt" {
						t.Fatalf("corrupt blob in %s: admission of %s refused with %v, want %v", packet.Ref.CommandID, ref.CommandID, got, want)
					}
					if w.f.snapshot().Watermark() != head {
						t.Fatal("a refused admission published")
					}
				}
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			corrupted++
		}
	}
	if corrupted < 4 {
		t.Fatalf("control: expected runs and the unreviewed packet to hold blobs, corrupted %d", corrupted)
	}
	w.f.accept(proofs)
}
