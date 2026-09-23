package write

// Seal admission of a run's own outputs (R9's single run-directory form, bytes
// proven from the run directory or the seal's own blobs). Proof families and generic artifact gates are tested in
// the gate_*_test.go files.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/evidence"
	"datum/internal/model"
)

// runAdmitWorld is a proof world plus one started run whose seal the case builds.
func runAdmitWorld(t *testing.T) (*proofWorld, model.InvocationEnvelope, model.PacketRef) {
	t.Helper()
	w := newProofWorld(t, true)
	env := w.envelope(w.criterion)
	return w, env, w.f.capture(nil, &model.InvocationStart{Envelope: env})
}

func runSealWith(env model.InvocationEnvelope, outputs ...model.ArtifactRef) *model.InvocationSeal {
	seal := proofSealed(env, proofPass)
	seal.Envelope.OutputRefs = proofKnown(outputs)
	return seal
}

func runOwnPath(env model.InvocationEnvelope) string {
	return evidence.RunDir(env.InvocationID) + "/out/result.json"
}

// runOwnBody differs from the criterion's example, which admission already
// stored, so only this run's admission can put it in the store.
const runOwnBody = proofPass + "\n"

func runStored(root string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(evidence.DefaultArtifactDir), string(model.HashBytes([]byte(runOwnBody)))))
	return err == nil
}

func TestSealAdmissionProvesEachOutputIsThisRuns(t *testing.T) {
	t.Run("control: the output is in the run directory", func(t *testing.T) {
		w, env, start := runAdmitWorld(t)
		proofPut(t, w.f.project.Root, runOwnPath(env), runOwnBody)
		w.f.accept(start, w.f.capture(nil, runSealWith(env, proofPin(runOwnBody, runOwnPath(env)))))
		if !runStored(w.f.project.Root) {
			t.Fatal("admission must materialize the run's output into the store")
		}
	})
	t.Run("control: the run directory is gone but the seal captured the bytes", func(t *testing.T) {
		w, env, start := runAdmitWorld(t)
		w.f.accept(start, w.f.capture([][]byte{[]byte(runOwnBody)}, runSealWith(env, proofPin(runOwnBody, runOwnPath(env)))))
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
		{"bare contract path, bytes captured", "invalid-field", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			return []model.PacketRef{w.f.capture([][]byte{[]byte(proofPass)}, runSealWith(env, proofPin(proofPass, proofPath)))}
		}},
		{"bare path beside the run-dir path", "invalid-field", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			proofPut(w.f.t, w.f.project.Root, runOwnPath(env), proofPass)
			both := proofPin(proofPass, runOwnPath(env))
			both.Content.Locators = append(both.Content.Locators, model.Locator{Path: proofPath})
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, both))}
		}},
		{"another run's directory", "invalid-field", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			other := evidence.RunDir(w.f.id()) + "/out/result.json"
			proofPut(w.f.t, w.f.project.Root, other, proofPass)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofPin(proofPass, other)))}
		}},
		{"non-canonical spelling of the run directory", "invalid-field", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			odd := evidence.RunDir(env.InvocationID) + "//out/result.json"
			proofPut(w.f.t, w.f.project.Root, runOwnPath(env), proofPass)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofPin(proofPass, odd)))}
		}},
		{"same digest only in the content store (the criterion's example)", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			proofPut(w.f.t, w.f.project.Root, evidence.DefaultArtifactDir+"/"+string(model.HashBytes([]byte(proofPass))), proofPass)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofPin(proofPass, runOwnPath(env))))}
		}},
		{"same digest captured by a different packet of the set", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			other := w.f.capture([][]byte{[]byte(proofPass)}, w.f.task())
			return []model.PacketRef{other, w.f.capture(nil, runSealWith(env, proofPin(proofPass, runOwnPath(env))))}
		}},
		{"run directory reached through a symlink", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			root := w.f.project.Root
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(evidence.RunDir(env.InvocationID))), 0700); err != nil {
				w.f.t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "out"), filepath.Join(root, filepath.FromSlash(evidence.RunDir(env.InvocationID)), "out")); err != nil {
				w.f.t.Fatal(err)
			}
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofPin(proofPass, runOwnPath(env))))}
		}},
		{"run-dir bytes differ from the pin", "unavailable", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			proofPut(w.f.t, w.f.project.Root, runOwnPath(env), proofFail)
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, proofPin(proofPass, runOwnPath(env))))}
		}},
		{"a git pin is not a run output", "invalid-field", func(w *proofWorld, env model.InvocationEnvelope) []model.PacketRef {
			ref := model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: runOwnPath(env)}, Selector: model.Selector{Kind: "whole"}}
			return []model.PacketRef{w.f.capture(nil, runSealWith(env, ref))}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, env, start := runAdmitWorld(t)
			// The criterion's example sits at the bare path in every case.
			if _, err := os.Stat(filepath.Join(w.f.project.Root, proofPath)); err != nil {
				t.Fatalf("fixture: the example must be present: %v", err)
			}
			w.f.refuse(w.f.request(append([]model.PacketRef{start}, tc.build(w, env)...)...), tc.code)
		})
	}
}
