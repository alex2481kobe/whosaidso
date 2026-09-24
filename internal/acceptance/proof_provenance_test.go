// Independent proof-path probes belong here: output provenance, execution
// comparability, and portable captured evidence. Production fixes and ledger
// inventory assertions do not. Every refusal starts with a passing proof.
package acceptance_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func proofExampleWorld(t *testing.T, example string) *pvWorld {
	t.Helper()
	w := pvNew(t)
	pvPut(t, w.p.Root, "out/result.json", []byte(example))
	w.fix("out/result.json", []byte(example))
	return w
}

func requireProven(t *testing.T, w *pvWorld, members map[model.ID]string) {
	t.Helper()
	err := w.prove(members)
	if got := w.status(); err != nil || got != reduce.StatusProven {
		t.Fatalf("control: expected a satisfied, complete family to prove; got %s, %v", got, err)
	}
}

// OWNER-RULINGS-DATUM.md:181-183 (R9): the contract path names an output of
// EACH RUN ITSELF. A run declaring that name without producing it must not
// borrow the shared example at the bare path, nor the digest store's copy
// when that file has vanished. Neither observes this run's output. (The
// bare-alias-first route, two locators on one output, is no longer
// expressible: an output has one name and no locator.)
func TestProofBareContractPathCannotBorrowAnExample(t *testing.T) {
	pvOwnOutputControl(t, []byte(pvFail))
	for _, route := range []string{"shared-example", "store-only"} {
		t.Run(route, func(t *testing.T) {
			w := proofExampleWorld(t, pvPass)
			if route == "store-only" {
				if err := os.Remove(filepath.Join(w.p.Root, "out/result.json")); err != nil {
					t.Fatal(err)
				}
			}
			id, sealErr := w.run(true, func(id model.ID) []model.RunOutput {
				// Declared, never produced by this run.
				return []model.RunOutput{pvOutput([]byte(pvPass), "out/result.json")}
			})
			var proofErr error
			if sealErr == nil {
				proofErr = w.prove(map[model.ID]string{id: "supports"})
			}
			if got := w.status(); got == reduce.StatusProven || sealErr == nil && proofErr == nil {
				t.Errorf("expected refusal: run %s produced no out/result.json; got seal=%v, proof=%v, status=%s. R9 binds the reading to this run; a passing example or cached copy cannot establish an observation", id, sealErr, proofErr, got)
			}
		})
	}
}

// DATUM-CONTRACT.md:513: execution_source_identity makes a different
// machine a different condition, never a silently comparable one. Observe
// drops that identity, and comparable checks only the two producer maps.
func TestProofDifferentMachinesAreNotComparable(t *testing.T) {
	for _, different := range []bool{false, true} {
		name := "control-same-machine"
		if different {
			name = "different-machines"
		}
		t.Run(name, func(t *testing.T) {
			w := proofExampleWorld(t, pvFail)
			members := map[model.ID]string{}
			for i := 0; i < 2; i++ {
				id := w.id()
				env := w.start(id, true)
				machine := recID(900)
				if different && i == 1 {
					machine = recID(901)
				}
				env.ExecutionSourceIdentity.MachineID = recKnown(machine)
				// Coordinator decision 2026-09-23: only a known equal HEAD with clean checkouts (or equal pins) establishes equal source.
				env.ExecutionSourceIdentity.Head, env.ExecutionSourceIdentity.Dirty = recKnown(model.GitHead{ObjectFormat: "sha1", Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}), recKnown(false)
				w.mustAdmit(w.lane, &model.InvocationStart{Envelope: env}, w.seal(env, w.produce(id, []byte(pvPass), "out/result.json")))
				members[id] = "supports"
			}
			if !different {
				requireProven(t, w, members)
				return
			}
			err := w.prove(members)
			if got := w.status(); err == nil || got == reduce.StatusProven {
				t.Errorf("expected refusal of support from different recorded machine IDs; got proof=%v, status=%s. Identical producer maps do not establish matching execution conditions (contract:513)", err, got)
			}
		})
	}
}

// DATUM-CONTRACT.md:505-520: intake survives deletion of the lane worktree;
// a coordinator admits the copied directory, including a worker's captured
// outputs. R9 still binds each output to its run. Requiring the original run
// directory after capturing its exact bytes makes this valid flow impossible.
func TestProofCapturedRunSurvivesProducerCheckoutRemoval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the producer is a POSIX shell script")
	}
	for _, removeProducer := range []bool{false, true} {
		name := "control-producer-present"
		if removeProducer {
			name = "only-durable-intake-survives"
		}
		t.Run(name, func(t *testing.T) {
			w := proofExampleWorld(t, pvFail)
			env, packets, err := w.cliRun(pvProducer(pvPass))
			if err != nil || env.Outputs.Value == nil {
				t.Fatalf("control: a real whosaidso run must durably capture its passing output: %v", err)
			}
			if removeProducer {
				// The coordinator retains the project ledger and receives intake;
				// the producer's script and staging are not transported.
				if err := os.Remove(filepath.Join(w.p.Root, "tools", "run.sh")); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.cliAdmit(packets...); err != nil {
				t.Fatalf("expected admission from durable captured blobs; got %v", err)
			}
			// The exact run output remains readable after admission. A missing
			// observation, changed bytes, or the failing example cannot explain
			// a refusal here.
			for _, out := range *env.Outputs.Value {
				if _, err := evidence.NewResolver(w.p.Root).Resolve(context.Background(), out.Ref()); err != nil {
					t.Fatalf("captured output must still resolve by its exact pin: %v", err)
				}
			}
			err = w.prove(map[model.ID]string{env.InvocationID: "supports"})
			if got := w.status(); err != nil || got != reduce.StatusProven {
				t.Errorf("expected PROVEN from the real run's intact captured bytes, independent of the producer checkout; got status=%s, proof=%v. The run-directory noStore branch refuses durable evidence that admission has preserved, breaking the worktree/worker flow (contract:505-520)", got, err)
			}
		})
	}
}
