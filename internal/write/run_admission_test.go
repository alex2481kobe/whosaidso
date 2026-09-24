package write

// Seal admission of a run's own outputs (each output a name inside the run
// and a content pin, its bytes proven from the seal's own captured blobs). Proof
// families and generic artifact gates are tested in the gate_*_test.go files.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// runAdmitWorld is a proof world plus one started run whose seal the case builds.
func runAdmitWorld(t *testing.T) (*proofWorld, model.InvocationEnvelope, model.PacketRef) {
	t.Helper()
	w := newProofWorld(t, true)
	env := w.envelope(w.criterion)
	return w, env, w.f.capture(nil, &model.InvocationStart{Envelope: env})
}

func runSealWith(env model.InvocationEnvelope, outputs ...model.RunOutput) *model.InvocationSeal {
	seal := proofSealed(env, proofPass)
	seal.Envelope.Outputs = proofKnown(outputs)
	return seal
}

// runOwnBody differs from the criterion's example, which admission already
// stored, so only this run's admission can put it in the store.
const runOwnBody = proofPass + "\n"

func runStored(root string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(evidence.DefaultArtifactDir), string(model.HashBytes([]byte(runOwnBody)))))
	return err == nil
}

func TestSealAdmissionProvesEachOutputIsThisRuns(t *testing.T) {
	t.Run("control: the seal's own packet captured the bytes", func(t *testing.T) {
		w, env, start := runAdmitWorld(t)
		w.f.accept(start, w.f.capture([][]byte{[]byte(runOwnBody)}, runSealWith(env, proofOutput(runOwnBody, proofPath))))
		if !runStored(w.f.project.Root) {
			t.Fatal("admission must materialize the captured output into the store")
		}
	})

	for _, tc := range []struct {
		name string
		code string
		// build returns the packets to admit together with the start.
		build func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef
	}{
		{"same bytes at the path the output's name spells", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			proofPut(w.f.t, w.f.project.Root, "stdout", runOwnBody)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofOutput(runOwnBody, "stdout")))}
		}},
		{"same bytes in an old-style run directory", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			proofPut(w.f.t, w.f.project.Root, evidence.DefaultArtifactDir+"/runs/"+string(env.InvocationID)+"/"+proofPath, runOwnBody)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofOutput(runOwnBody, proofPath)))}
		}},
		{"same digest only in the content store (the criterion's example)", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			proofPut(w.f.t, w.f.project.Root, evidence.DefaultArtifactDir+"/"+string(model.HashBytes([]byte(proofPass))), proofPass)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofOutput(proofPass, proofPath)))}
		}},
		{"same digest captured by a different packet of the set", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			other := w.f.capture([][]byte{[]byte(runOwnBody)}, w.f.task())
			return []model.PacketRef{other, w.f.capture(nil, runSealWith(env, proofOutput(runOwnBody, proofPath)))}
		}},
		{"same bytes only in write.Run's staging, outside the project", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			proofPut(w.f.t, runTestStaging(w.f.t, w.f.project, env.InvocationID), proofPath, runOwnBody)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofOutput(runOwnBody, proofPath)))}
		}},
		{"the captured blob was altered after capture", "intake-corrupt", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			seal := w.f.capture([][]byte{[]byte(runOwnBody)}, runSealWith(env, proofOutput(runOwnBody, proofPath)))
			inbox, err := store.IntakeDir(w.f.project)
			if err != nil {
				w.f.t.Fatal(err)
			}
			blob := filepath.Join(inbox, string(seal.CommandID), "blobs", string(model.HashBytes([]byte(runOwnBody))))
			if err := os.Chmod(blob, 0600); err != nil {
				w.f.t.Fatal(err)
			}
			if err := os.WriteFile(blob, []byte(strings.Replace(runOwnBody, "0.0100", "0.0900", 1)), 0400); err != nil {
				w.f.t.Fatal(err)
			}
			return []model.PacketRef{seal}
		}},
		{"the output pins the captured digest with another length", "conflict", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			out := proofOutput(runOwnBody, proofPath)
			out.Length++
			return []model.PacketRef{w.f.capture([][]byte{[]byte(runOwnBody)}, runSealWith(env, out))}
		}},
		{"captured bytes differ from the pin", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			return []model.PacketRef{w.f.capture([][]byte{[]byte(proofFail)}, runSealWith(env, proofOutput(runOwnBody, proofPath)))}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, env, start := runAdmitWorld(t)
			// The criterion's example sits at the bare path in every case.
			if _, err := os.Stat(filepath.Join(w.f.project.Root, proofPath)); err != nil {
				t.Fatalf("fixture: the example must be present: %v", err)
			}
			w.f.refuse(w.f.request(append([]model.PacketRef{start}, tc.build(w, env)...)...), tc.code)
			if runStored(w.f.project.Root) {
				t.Fatal("a refused seal published its output")
			}
		})
	}
}
