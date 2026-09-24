package write

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

type admissionFixture struct {
	t       *testing.T
	project store.Project
	next    int
	author  model.Actor
}

func newAdmissionFixture(t *testing.T) *admissionFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	return &admissionFixture{t: t, project: store.Project{ID: "test/admission", Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}, author: model.Actor{ID: "agent-c2"}}
}

func (f *admissionFixture) id() model.ID {
	f.next++
	return model.ID(fmt.Sprintf("%026d", f.next))
}

func (f *admissionFixture) task() *model.TaskCreate {
	return &model.TaskCreate{
		ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.TaskSpec{
			Intent: "verify admission", Subject: "a test task",
			Scope:    model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this test", Limitations: "no production evidence"},
			NonGoals: []string{"production writes"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: f.id(), Revision: 1, Criterion: "the gate preserves the contract"}},
			ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: f.author,
		},
	}
}

func (f *admissionFixture) ref(id model.ID, revision model.Revision) model.RecordRef {
	return model.RecordRef{Project: f.project.ID, RecordID: id, Revision: revision}
}

func admissionTestEvent(t *testing.T, event model.TypedEvent) model.Event {
	t.Helper()
	raw, err := model.EncodeEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *admissionFixture) capture(blobs [][]byte, events ...model.TypedEvent) model.PacketRef {
	f.t.Helper()
	raw := make([]model.Event, len(events))
	for i, event := range events {
		raw[i] = admissionTestEvent(f.t, event)
	}
	readers := make([]io.Reader, len(blobs))
	for i, blob := range blobs {
		readers[i] = bytes.NewReader(blob)
	}
	ref, err := store.WriteIntake(context.Background(), f.project, store.IntakeRequest{CommandID: f.id(), Author: f.author, Events: raw, Blobs: readers})
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}

func (f *admissionFixture) request(refs ...model.PacketRef) AdmitRequest {
	ids := make([]model.ID, len(refs))
	for i, ref := range refs {
		ids[i] = ref.CommandID
	}
	return AdmitRequest{CommandID: f.id(), PacketIDs: ids, Admitter: model.Actor{ID: "coordinator"}, Outcome: "accepted", Reason: "checked against this test's contract"}
}

func (f *admissionFixture) accept(refs ...model.PacketRef) model.Bundle {
	f.t.Helper()
	b, err := Admit(context.Background(), f.project, f.request(refs...))
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *admissionFixture) snapshot() reduce.Snapshot {
	f.t.Helper()
	prefix, err := store.ReadPrefix(f.project)
	if err != nil {
		f.t.Fatal(err)
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

func (f *admissionFixture) goodControl() *model.TaskCreate {
	f.t.Helper()
	task := f.task()
	f.accept(f.capture(nil, task))
	if projected, ok := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID}); !ok || projected.Status != reduce.StatusReady {
		f.t.Fatalf("good control is not READY: %+v", projected)
	}
	return task
}

func admissionErrorCode(err error) string {
	var conflict *reduce.Conflict
	if errors.As(err, &conflict) {
		return conflict.Code()
	}
	var fault *model.Fault
	if errors.As(err, &fault) {
		return fault.Code
	}
	return ""
}

func (f *admissionFixture) refuse(request AdmitRequest, code string) {
	f.t.Helper()
	before := f.snapshot().Watermark()
	_, err := Admit(context.Background(), f.project, request)
	if err == nil || admissionErrorCode(err) != code {
		f.t.Fatalf("wanted %s, got %v", code, err)
	}
	if after := f.snapshot().Watermark(); after != before {
		f.t.Fatalf("refusal published: before %+v, after %+v", before, after)
	}
}

func admissionContent(data []byte) model.ArtifactRef {
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(data), Length: uint64(len(data)), MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
}

func TestAdmissionCaptureForwardReferenceReplayAndRetry(t *testing.T) {
	f := newAdmissionFixture(t)
	producer, consumer := f.task(), f.task()
	consumer.Spec.ContextRefs = []model.RecordRef{f.ref(producer.ID, 1)}
	body := []byte("owner source, exact bytes\n")
	source := &model.SourceIntake{SourceID: f.id(), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), SourceRef: admissionContent(body), Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{f.ref(producer.ID, 1)}}
	consumer.Provenance.SourceRefs = []model.ArtifactRef{source.SourceRef}
	first := f.capture(nil, consumer)
	second := f.capture([][]byte{body}, producer, source)
	if got := f.snapshot().Watermark().Sequence; got != 0 {
		t.Fatalf("capture wrote canonical state: %d", got)
	}
	request := f.request(first, second)
	request.Admitter = f.author
	bundle, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Sequence != 1 || len(bundle.Events) != 4 {
		t.Fatalf("not a single complete publication: %+v", bundle)
	}
	packets, err := store.ReadIntake(f.project, []model.ID{second.CommandID, first.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	expected := append(append([]model.Event{}, packets[0].Events...), packets[1].Events...)
	if !reflect.DeepEqual(bundle.Events[:3], expected) {
		t.Fatal("admission changed authored event data or packet event order")
	}
	review, err := model.DecodeEvent(bundle.Events[3])
	if err != nil || review.(*model.ReviewAdmit).Reason != request.Reason || strings.Contains(string(bundle.Events[3].Data), "self_admission") {
		t.Fatalf("C39: self-admission is computed, never stored or written as prose: %v, %v", review, err)
	}
	for _, ref := range []model.PacketRef{first, second} {
		if got := review.(*model.ReviewAdmit).Authors[ref.CommandID]; got != f.author {
			t.Fatalf("packet author was not recorded for %s: %+v", ref.CommandID, got)
		}
		projected, ok := f.snapshot().Review(reduce.ReviewKey{Project: f.project.ID, CommandID: ref.CommandID})
		if !ok || projected.SelfAdmission != model.SelfAdmissionTrue {
			t.Fatalf("self admission is not visible for %s: %+v", ref.CommandID, projected)
		}
	}
	if len(f.snapshot().Records()) != 2 || len(f.snapshot().Sources()) != 1 {
		t.Fatal("the admitted state did not replay")
	}
	resolved, err := evidence.NewResolver(f.project.Root).Resolve(context.Background(), source.SourceRef)
	if err != nil || !bytes.Equal(resolved.Bytes, body) || resolved.Origin != evidence.OriginArtifactStore {
		t.Fatalf("digest-only source not preserved: %+v, %v", resolved, err)
	}
	request.PacketIDs = []model.ID{second.CommandID, first.CommandID, first.CommandID}
	retry, err := Admit(context.Background(), f.project, request)
	if err != nil || retry.CommandID != bundle.CommandID || f.snapshot().Watermark().Sequence != 1 {
		t.Fatalf("set retry was not idempotent: %+v, %v", retry, err)
	}
}

func TestAdmissionStructuredSelfAdmission(t *testing.T) {
	known := model.Actor{ID: "reviewer"}
	unknown := model.Actor{UnknownReason: "not recorded"}
	for _, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		for _, admitter := range []model.Actor{known, unknown} {
			t.Run(outcome+"/"+admitter.ID+admitter.UnknownReason, func(t *testing.T) {
				f := newAdmissionFixture(t)
				authors := []model.Actor{known, {ID: "other"}, unknown, {UnknownReason: "different missing identity"}, {ID: "Reviewer"}}
				refs := make([]model.PacketRef, len(authors))
				want := []model.SelfAdmissionState{model.SelfAdmissionTrue, model.SelfAdmissionFalse, model.SelfAdmissionUnknown, model.SelfAdmissionUnknown, model.SelfAdmissionFalse}
				for i, author := range authors {
					f.author = author
					refs[i] = f.capture(nil, f.task())
					if admitter == unknown {
						want[i] = model.SelfAdmissionUnknown
					}
				}
				request := f.request(refs...)
				request.Admitter, request.Outcome = admitter, outcome
				// User-authored text can contain a misleading self-admission sentence.
				request.Reason = " \tMy exact words: café.\r\nSelf-admitted: true.\n  "
				// The input order must not associate a comparison with the wrong packet.
				for i, j := 0, len(request.PacketIDs)-1; i < j; i, j = i+1, j-1 {
					request.PacketIDs[i], request.PacketIDs[j] = request.PacketIDs[j], request.PacketIDs[i]
				}
				bundle, err := Admit(context.Background(), f.project, request)
				if err != nil {
					t.Fatalf("self-admission and unknown identity must remain permitted: %v", err)
				}
				raw, err := model.DecodeEvent(bundle.Events[len(bundle.Events)-1])
				if err != nil {
					t.Fatal(err)
				}
				review := raw.(*model.ReviewAdmit)
				// C39: no stored comparison; the recorded author is the fact.
				if strings.Contains(string(bundle.Events[len(bundle.Events)-1].Data), "self_admission") || len(review.Authors) != len(refs) {
					t.Fatalf("admission must record every author and store no comparison: %+v", review)
				}
				for i, ref := range refs {
					if got := review.Authors[ref.CommandID]; got != authors[i] {
						t.Errorf("packet %s author = %+v, want %+v", ref.CommandID, got, authors[i])
					}
					projected, ok := f.snapshot().Review(reduce.ReviewKey{Project: f.project.ID, CommandID: ref.CommandID})
					if !ok || projected.Packet != ref || projected.SelfAdmission != want[i] || projected.Outcome != outcome {
						t.Errorf("wrong admitted fact for packet %s: %+v", ref.CommandID, projected)
					}
				}
				if review.Reason != request.Reason {
					t.Fatalf("admitter words changed or a suffix was appended:\ngot  %q\nwant %q", review.Reason, request.Reason)
				}
				if got := len(f.snapshot().Records()); (outcome == "accepted" && got != len(refs)) || (outcome != "accepted" && got != 0) {
					t.Fatalf("wrong publication for %s: %d records", outcome, got)
				}
				retry, err := Admit(context.Background(), f.project, request)
				if err != nil || retry.CommandID != bundle.CommandID || retry.Sequence != bundle.Sequence || f.snapshot().Watermark().Sequence != 1 {
					t.Fatalf("retry did not return the same admission: %v", err)
				}
				// Stored JSON has indentation; compare decoded facts, not raw spacing.
				retried, err := model.DecodeEvent(retry.Events[len(retry.Events)-1])
				if err != nil || !reflect.DeepEqual(retried, review) {
					t.Fatalf("retry changed the recorded review: %v", err)
				}
			})
		}
	}
}

func TestAdmissionStaleRevisionAndConcurrentWriters(t *testing.T) {
	f := newAdmissionFixture(t)
	task := f.goodControl()
	amend := &model.TaskAmend{Provenance: task.Provenance, Target: f.ref(task.ID, 1), Replacement: task.Spec}
	first, second := f.capture(nil, amend), f.capture(nil, amend)
	requests := []AdmitRequest{f.request(first), f.request(second)}
	errorsOut := make(chan error, 2)
	start := make(chan struct{})
	var writers sync.WaitGroup
	for _, request := range requests {
		writers.Add(1)
		go func(r AdmitRequest) {
			defer writers.Done()
			<-start
			_, err := Admit(context.Background(), f.project, r)
			errorsOut <- err
		}(request)
	}
	close(start)
	writers.Wait()
	close(errorsOut)
	success, conflict := 0, 0
	for err := range errorsOut {
		if err == nil {
			success++
		} else if admissionErrorCode(err) == "revision-conflict" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || f.snapshot().Watermark().Sequence != 2 {
		t.Fatalf("two pre-lock proposals bypassed revision checking: successes %d, conflicts %d", success, conflict)
	}
	f.refuse(f.request(f.capture(nil, amend)), "revision-conflict")
}

func TestAdmissionMixedContentRetryRefused(t *testing.T) {
	f := newAdmissionFixture(t)
	f.goodControl()
	packet := f.capture(nil, f.task())
	request := f.request(packet)
	if _, err := Admit(context.Background(), f.project, request); err != nil {
		t.Fatal(err)
	}
	if _, err := Admit(context.Background(), f.project, request); err != nil {
		t.Fatalf("identical control retry failed: %v", err)
	}
	changed := request
	changed.Reason = "different judgment"
	f.refuse(changed, "conflict")
	changed = request
	changed.PacketIDs = append(append([]model.ID{}, request.PacketIDs...), f.capture(nil, f.task()).CommandID)
	f.refuse(changed, "conflict")
	changed = request
	changed.Admitter.ID = "different reviewer"
	f.refuse(changed, "conflict")
	changed = request
	changed.Outcome = "rejected"
	f.refuse(changed, "conflict")
	f.refuse(f.request(packet), "conflict")
}

// The published bundle, not intake, answers a retry. A different valid request
// copied under an admitted packet id changes nothing a retry returns: the
// ledger's bound ref still names the original bytes. The swapped packet is no
// route to a new admission either, because its id already has a disposition.
// (This test once required the swap to refuse the identical retry; the ledger
// is authoritative, so an identical retry now gets the published answer.)
func TestAdmissionRetryAnsweredFromLedgerNotIntake(t *testing.T) {
	f := newAdmissionFixture(t)
	f.goodControl()
	original := f.capture(nil, f.task())
	request := f.request(original)
	want, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Admit(context.Background(), f.project, request); err != nil {
		t.Fatalf("unchanged packet retry failed: %v", err)
	}
	changed := f.capture(nil, f.task())
	packets, err := store.ReadIntake(f.project, []model.ID{changed.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	// Intake request identity excludes its clerical command id, so this is a
	// packet that verifies under the original id with different content.
	packets[0].CommandID = original.CommandID
	encoded, err := model.Encode(packets[0])
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.IntakeDir(f.project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, string(original.CommandID), "packet.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if verified, err := store.ReadVerifiedIntake(f.project, []model.ID{original.CommandID}); err != nil || verified[0].Ref == want.Packets[0] {
		t.Fatalf("fixture must leave different verifiable bytes under the admitted id: %v", err)
	}
	got, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatalf("identical retry after intake changed: %v", err)
	}
	a, errA := model.Encode(got)
	b, errB := model.Encode(want)
	if errA != nil || errB != nil || string(a) != string(b) {
		t.Fatalf("retry returned a different bundle:\n%s\nwant\n%s", a, b)
	}
	changedReason := request
	changedReason.Reason = "a different judgment"
	f.refuse(changedReason, "conflict")
	f.refuse(f.request(original), "conflict")
}

func TestAdmissionPacketProjectVerified(t *testing.T) {
	f := newAdmissionFixture(t)
	f.goodControl()
	packet := f.capture(nil, f.task())
	packets, err := store.ReadIntake(f.project, []model.ID{packet.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	packets[0].Project = "another/project"
	encoded, err := model.Encode(packets[0])
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.IntakeDir(f.project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, string(packet.CommandID), "packet.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	f.refuse(f.request(packet), "intake-corrupt")
}

func TestAdmissionRejectionAndCorrectionRemainVisible(t *testing.T) {
	for _, outcome := range []string{"rejected", "correction-requested"} {
		t.Run(outcome, func(t *testing.T) {
			f := newAdmissionFixture(t)
			f.goodControl()
			candidate := f.task()
			candidate.Spec.ContextRefs = []model.RecordRef{f.ref(f.id(), 1)}
			packet := f.capture(nil, candidate)
			request := f.request(packet)
			request.Outcome, request.Reason = outcome, "the referenced task has not been established"
			bundle, err := Admit(context.Background(), f.project, request)
			if err != nil {
				t.Fatal(err)
			}
			if len(bundle.Events) != 1 || bundle.Events[0].Type != "review.admit" {
				t.Fatal("nonaccepted candidates entered the canonical bundle")
			}
			snapshot := f.snapshot()
			if _, found := snapshot.Record(f.ref(candidate.ID, 1)); found {
				t.Fatal("nonaccepted candidate became canonical")
			}
			review, found := snapshot.Review(reduce.ReviewKey{Project: f.project.ID, CommandID: packet.CommandID})
			if !found || review.Outcome != outcome || !strings.Contains(review.Reason, request.Reason) || review.Packet != packet {
				t.Fatalf("review is not attributable to the exact packet: %+v", review)
			}
			pending, err := store.ReadIntake(f.project, []model.ID{packet.CommandID})
			if err != nil || len(pending) != 1 {
				t.Fatalf("review hid intake: %v", err)
			}
		})
	}
}

func TestAdmissionMissingAndCorruptSupport(t *testing.T) {
	for _, mutation := range []string{"missing", "corrupt-intake", "corrupt-artifact", "wrong-length"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			f.goodControl()
			body := []byte("real source\n")
			source := &model.SourceIntake{SourceID: f.id(), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), SourceRef: admissionContent(body), Speaker: f.author, Referents: []model.RecordRef{}}
			blobs := [][]byte{body}
			if mutation == "missing" {
				blobs = nil
			}
			if mutation == "wrong-length" {
				source.Length++
				source.SourceRef.Content.Length++
			}
			// Capture itself now refuses a source.intake whose exact bytes are not
			// among its blobs, so these two never reach admission. They once did
			// only because capture through the store skipped the rule.
			if mutation == "missing" || mutation == "wrong-length" {
				raw := admissionTestEvent(t, source)
				readers := []io.Reader{}
				for _, blob := range blobs {
					readers = append(readers, bytes.NewReader(blob))
				}
				_, err := store.WriteIntake(context.Background(), f.project, store.IntakeRequest{CommandID: f.id(), Author: f.author, Events: []model.Event{raw}, Blobs: readers})
				if admissionErrorCode(err) != "source-not-captured" {
					t.Fatalf("wanted capture to refuse with source-not-captured, got %v", err)
				}
				if pending, err := store.ReadIntake(f.project, nil); err != nil || len(pending) != 1 {
					t.Fatalf("refused capture left intake: %d, %v", len(pending), err)
				}
				return
			}
			packet := f.capture(blobs, source)
			code := "unavailable"
			if mutation == "corrupt-intake" {
				inbox, err := store.IntakeDir(f.project)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(inbox, string(packet.CommandID), "blobs", string(source.OriginalDigest)), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				code = "intake-corrupt"
			}
			if mutation == "corrupt-artifact" {
				dir := filepath.Join(f.project.Root, evidence.DefaultArtifactDir)
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, string(source.OriginalDigest)), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				code = "conflict"
			}
			if mutation == "wrong-length" {
				code = "conflict"
			}
			f.refuse(f.request(packet), code)
		})
	}
}

func TestAdmissionLocatorMaterializedWithoutPayloadRewrite(t *testing.T) {
	f := newAdmissionFixture(t)
	body := []byte("source held in a removable producer path")
	path := filepath.Join(f.project.Root, "producer.txt")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	source := &model.SourceIntake{SourceID: f.id(), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), SourceRef: admissionContent(body), Speaker: f.author, Referents: []model.RecordRef{}}
	source.SourceRef.Content.Locators = []model.Locator{{Path: "producer.txt"}}
	// Capture must now carry the source's bytes; the locator stays authored.
	packet := f.capture([][]byte{body}, source)
	f.accept(packet)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	resolved, err := evidence.NewResolver(f.project.Root).Resolve(context.Background(), source.SourceRef)
	if err != nil || !bytes.Equal(body, resolved.Bytes) || resolved.Origin != evidence.OriginArtifactStore {
		t.Fatalf("source disappeared with its locator: %+v, %v", resolved, err)
	}
	if got := f.snapshot().Sources()[0].Intake.SourceRef.Content.Locators; !reflect.DeepEqual(got, source.SourceRef.Content.Locators) {
		t.Fatal("admission rewrote the authored locator")
	}
}

func TestAdmissionClaimArtifacts(t *testing.T) {
	for _, location := range []string{"provenance", "external"} {
		for _, scenario := range []string{"intake", "locator", "missing", "wrong-length", "missing-selector", "traversal", "absolute-path", "symlink-store"} {
			t.Run(location+"/"+scenario, func(t *testing.T) {
				f := newAdmissionFixture(t)
				claim := f.claim()
				body := []byte(`{"finding":"needs measurement"}`)
				ref := admissionContent(body)
				ref.Content.MediaType = "application/json"
				ref.Selector = model.Selector{Kind: "json-pointer", Pointer: "/finding"}
				blobs := [][]byte{body}
				code := ""
				switch scenario {
				case "locator":
					blobs = nil
					ref.Content.Locators = []model.Locator{{Path: "finding.json"}}
					if err := os.WriteFile(filepath.Join(f.project.Root, "finding.json"), body, 0600); err != nil {
						t.Fatal(err)
					}
				case "missing":
					blobs, code = nil, "unavailable"
				case "wrong-length":
					ref.Content.Length++
					code = "conflict"
				case "missing-selector":
					ref.Selector.Pointer = "/absent"
					code = "unavailable"
				case "traversal":
					ref.Content.Locators = []model.Locator{{Path: "../finding.json"}}
					code = "invalid-field"
				case "absolute-path":
					ref.Content.Locators = []model.Locator{{Path: filepath.Join(f.project.Root, "finding.json")}}
					code = "invalid-field"
				case "symlink-store":
					dir := filepath.Join(f.project.Root, evidence.DefaultArtifactDir)
					if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(t.TempDir(), dir); err != nil {
						t.Fatal(err)
					}
					code = "invalid-field"
				}
				if location == "provenance" {
					claim.Provenance.SourceRefs = []model.ArtifactRef{ref}
				} else {
					claim.Spec.ExternalRefs = []model.ExternalReference{{Tag: "VERIFIED", Citation: "reported finding", SourceRef: &ref}}
				}
				if scenario == "traversal" || scenario == "absolute-path" {
					// Capture also rejects unsafe paths; feed untrusted bytes
					// directly to verify admission repeats that check.
					data, err := json.Marshal(claim)
					if err != nil {
						t.Fatal(err)
					}
					packets, err := gatePackets(f.project.ID, reduce.Snapshot{}, []model.Packet{{Project: f.project.ID, CommandID: f.id(), Author: f.author, Events: []model.Event{{Type: claim.EventType(), Data: data}}}})
					if admissionErrorCode(err) != code || len(packets) != 0 {
						t.Fatalf("unsafe claim artifact path escaped admission: %v, %v", packets, err)
					}
					return
				}
				packet := f.capture(blobs, claim)
				if code != "" {
					f.refuse(f.request(packet), code)
					return
				}
				stored, err := store.ReadIntake(f.project, []model.ID{packet.CommandID})
				if err != nil {
					t.Fatal(err)
				}
				bundle := f.accept(packet)
				if !reflect.DeepEqual(bundle.Events[0], stored[0].Events[0]) {
					t.Fatal("artifact handling rewrote the authored claim")
				}
				if scenario == "locator" {
					if err := os.Remove(filepath.Join(f.project.Root, "finding.json")); err != nil {
						t.Fatal(err)
					}
				}
				resolved, err := evidence.NewResolver(f.project.Root).Resolve(context.Background(), ref)
				if err != nil || !bytes.Equal(resolved.Bytes, body) || resolved.Origin != evidence.OriginArtifactStore {
					t.Fatalf("claim artifact was not preserved: %+v, %v", resolved, err)
				}
				got, ok := f.snapshot().ClaimAt(f.ref(claim.ID, 1))
				if !ok || got.Status != reduce.StatusUnmeasured {
					t.Fatalf("a source artifact became an observation: %+v", got)
				}
			})
		}
	}
}
