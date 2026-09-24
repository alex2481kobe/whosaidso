// Independent round-two probes belong here: alternate entrances to the recent
// identity, capture, retry, READY and config rules. Production fixes, new policy,
// ledger inventory assertions and changes to other reviewers' tests do not.
package acceptance_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

// Contract:99,161-163,362-369: matching execution conditions include the
// source actually read. Empty source-pin lists do not establish equal source.
func TestAstraRound2ChangedExecutionSourceCannotCompare(t *testing.T) {
	for _, change := range []string{"control", "committed", "dirty"} {
		t.Run(change, func(t *testing.T) {
			w := astraProofWorld(t, pvFail)
			script := strings.Replace(pvProducer(pvPass), "printf '%s' '"+pvPass+"'", "cat measurement.json", 1)
			pvPut(t, w.p.Root, "tools/run.sh", []byte(script))
			pvPut(t, w.p.Root, "measurement.json", []byte(pvPass))
			pvPut(t, w.p.Root, ".gitignore", []byte(".whosaidso/\n"))
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", w.p.Root, "-c", "user.name=review", "-c", "user.email=review@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git fixture: %v: %s", err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "--quiet")
			git("add", ".")
			git("commit", "--quiet", "-m", "first source")
			members := map[model.ID]string{}
			var first model.ExecutionIdentity
			for i := 0; i < 2; i++ {
				if i == 1 && change != "control" {
					pvPut(t, w.p.Root, "measurement.json", []byte(strings.Replace(pvPass, "0.0100", "0.0150", 1)))
					if change == "committed" {
						git("add", "measurement.json")
						git("commit", "--quiet", "-m", "different measured source")
					}
				}
				env, packets, err := w.cliRun(script)
				if err != nil {
					t.Fatalf("real producer must complete: %v", err)
				}
				identity := env.ExecutionSourceIdentity
				if identity.Head.Value == nil || identity.Head.Value.Commit != git("rev-parse", "HEAD") || identity.MachineID.Value == nil {
					t.Fatalf("fixture needs observed HEAD and machine: %+v", identity)
				}
				if i == 0 {
					first = identity
				} else {
					if *first.MachineID.Value != *identity.MachineID.Value {
						t.Fatal("control: fresh CLI processes must retain the same machine identity")
					}
					if change == "committed" && *first.Head.Value == *identity.Head.Value {
						t.Fatal("fixture must execute different committed source")
					}
				}
				if err := w.cliAdmit(packets...); err != nil {
					t.Fatalf("honest observations must admit: %v", err)
				}
				members[env.InvocationID] = "supports"
			}
			if change == "control" {
				astraProven(t, w, members)
				return
			}
			if err := w.prove(members); err == nil || w.status() == reduce.StatusProven {
				t.Errorf("expected incomparable source conditions; got proof=%v, status=%s after %s source changed. The producer read different bytes, but comparison ignores HEAD/dirty and treats two empty source lists as equality (contract:99,161-163)", err, w.status(), change)
			}
		})
	}
}

// Contract:120,144: every acknowledged source capture must save its bytes,
// including callers of the same exported writer used by the CLI.
func TestAstraRound2SourceCaptureCannotBypassDurability(t *testing.T) {
	for _, route := range []string{"control", "events", "build-events"} {
		t.Run(route, func(t *testing.T) {
			w := pvNew(t)
			body := []byte(`{"message":"words that must survive capture"}`)
			pin := pvPin(body, "missing-source.json")
			source := &model.SourceIntake{SourceID: w.id(), OriginalDigest: pin.Content.SHA256, Length: pin.Content.Length,
				SourceRef: pin, Speaker: w.lane, Referents: []model.RecordRef{w.claim}}
			events := []model.Event{recEncode(t, source)}
			r := store.IntakeRequest{CommandID: w.id(), Author: w.lane, Events: events}
			if route == "control" {
				r.Blobs = []io.Reader{bytes.NewReader(body)}
			} else if route == "build-events" {
				r.Events = nil
				r.BuildEvents = func([]store.CapturedBlob) ([]model.Event, error) { return events, nil }
			}
			packet, err := store.WriteIntake(context.Background(), w.p, r)
			if route == "control" {
				if err != nil {
					t.Fatal(err)
				}
				if err := w.cliAdmit(packet.CommandID); err != nil {
					t.Fatalf("control: captured bytes must survive without their original locator: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected refusal before acknowledging unsaved source; got packet %s, later admission=%s. The public intake writer bypasses the CLI-only source check and promises capture with no original bytes (contract:120,144)", packet.CommandID, recCode(w.review("accepted", packet.CommandID)))
			}
		})
	}
}

// Contract:52-53: an acceptance hold prevents dispatch when no attempt is live.
// Changing the verb to takeover must not reopen a terminal attempt's blocked task.
func TestAstraRound2TakeoverCannotBypassReady(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "awaiting-acceptance"}[blocked], func(t *testing.T) {
			w := flowNew(t)
			task := w.task("work only after acceptance")
			if blocked {
				packet, err := w.handback(task.attempt, "stopped")
				if err != nil {
					t.Fatal(err)
				}
				if err := w.review("accepted", packet); err != nil {
					t.Fatalf("control: terminate the prior attempt: %v", err)
				}
				w.mustAdmit(flowLane, &model.BlockerHold{Task: task.ref, BlockerID: w.id(), Reason: model.BlockerAwaitingAcceptance,
					Actor: model.Actor{ID: flowLane}, Criterion: "owner accepts before any further work"})
				start := &model.TaskStart{Task: task.ref, Actor: model.Actor{ID: flowLane}, AttemptID: w.id()}
				if err := w.review("accepted", w.capture(flowLane, start)); err == nil {
					t.Fatal("control: ordinary start must refuse the unresolved hold")
				}
			}
			body := []byte(`{"stopped":true}`)
			w.put("stopped.json", body)
			event := &model.TaskTakeover{Task: task.ref, Actor: model.Actor{ID: flowLane}, AttemptID: w.id(),
				PriorAttemptID: task.attempt, StoppedConfirmationRef: pvPin(body, "stopped.json")}
			err := w.review("accepted", w.capture(flowLane, event))
			if !blocked && err != nil {
				t.Fatalf("control: a takeover with no outstanding hold must admit: %v", err)
			}
			if blocked && err == nil {
				t.Error("expected refusal while acceptance is owed; takeover admitted a new attempt after the prior attempt ended. READY/BLOCKED must govern dispatch through both verbs (contract:52-53)")
			}
		})
	}
}

// Contract:684: a published admission answers an identical retry without
// requiring access to this machine's intake. Permissions do not change content.
func TestAstraRound2RetryDoesNotNeedReadableIntake(t *testing.T) {
	w := pvNew(t)
	r := write.AdmitRequest{CommandID: w.id(), PacketIDs: []model.ID{w.capture(w.lane, &model.InvocationStart{Envelope: w.start(w.id(), false)})},
		Admitter: w.lane, Outcome: "accepted", Reason: "retry the same review"}
	want, err := write.Admit(context.Background(), w.p, r)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := model.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	same := func(got model.Bundle) bool {
		b, err := model.Encode(got)
		return err == nil && bytes.Equal(b, wantBytes)
	}
	if got, err := write.Admit(context.Background(), w.p, r); err != nil || !same(got) {
		t.Fatalf("control: readable-intake retry: %v", err)
	}
	inbox, err := store.IntakeDir(w.p)
	if err != nil {
		t.Fatal(err)
	}
	packet := filepath.Join(inbox, string(r.PacketIDs[0]), "packet.json")
	if err := os.Chmod(packet, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(packet, 0600) })
	if _, err := os.ReadFile(packet); !os.IsPermission(err) {
		t.Skipf("fixture needs enforced file permissions: %v", err)
	}
	if got, err := write.Admit(context.Background(), w.p, r); err != nil || !same(got) {
		t.Errorf("expected the identical published result, got %v. Retry rereads unchanged but inaccessible intake; losing its permissions must not turn an acknowledged ledger fact into a failed command (contract:684)", err)
	}
	if err := os.Chmod(packet, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := write.Admit(context.Background(), w.p, r); err != nil || !same(got) {
		t.Fatalf("control: restoring only permission must restore the same retry: %v", err)
	}
}

// Contract:584-585,603-616: config names belong to the exact instrument
// revision; replay must reject the same invalid relationship admission rejects.
func TestAstraRound2ReplayChecksInstrumentConfigNames(t *testing.T) {
	w := pvNew(t)
	env := w.start(w.id(), false)
	w.mustAdmit(w.lane, &model.InvocationStart{Envelope: env}, w.seal(env, []model.ArtifactRef{}...))
	badEnv := w.start(w.id(), false)
	bad := w.seal(badEnv, []model.ArtifactRef{}...)
	bad.Envelope.ConfigEffective = recKnown(map[string]model.Availability[model.Scalar]{"undeclared-camera": recKnown(laneEEvidenceNumber("1"))})
	if err := w.admit(w.lane, &model.InvocationStart{Envelope: badEnv}, bad); recCode(err) != "invalid-field" || !strings.Contains(err.Error(), "config_effective") {
		t.Fatalf("control: admission must refuse the undeclared effective knob: %v", err)
	}
	prefix, err := store.ReadPrefix(w.p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reduce.Replay(prefix); err != nil {
		t.Fatalf("control: admitted config replays: %v", err)
	}
	for i := range prefix {
		for j, raw := range prefix[i].Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				t.Fatal(err)
			}
			if seal, ok := event.(*model.InvocationSeal); ok {
				*seal.Envelope.ConfigEffective.Value = map[string]model.Availability[model.Scalar]{"undeclared-camera": recKnown(laneEEvidenceNumber("1"))}
				prefix[i].Events[j] = recEncode(t, seal)
			}
		}
	}
	if _, err := reduce.Replay(prefix); err == nil {
		t.Error("expected replay to refuse an effective config name absent from the instrument; got a valid snapshot. The new admission-only check leaves the invalid relationship expressible in canonical history (contract:584-585,603-616)")
	}
}
