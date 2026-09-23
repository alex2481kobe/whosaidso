package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

func readVerifyProject(t *testing.T) store.Project {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	return store.Project{ID: laneEReduceProject, Root: root, Ledger: filepath.Join(root, "record", "events")}
}

// Publish through the real store and validate the sealed prefix with Replay.
// The query supports the full event vocabulary, including events not yet enabled
// by the first CLI write gate, just as internal/query's own fixtures do.
func readVerifyAppend(t *testing.T, p store.Project, n int, packets []model.PacketRef, events ...model.TypedEvent) reduce.Snapshot {
	t.Helper()
	raw := make([]model.Event, 0, len(events))
	for _, event := range events {
		encoded, err := model.EncodeEvent(event)
		if err != nil {
			t.Fatalf("control %s must pass the strict event codec: %v", event.EventType(), err)
		}
		raw = append(raw, encoded)
	}
	_, err := store.Transact(context.Background(), p, laneEReduceID(n), model.HashBytes([]byte(fmt.Sprint(n))),
		func([]model.Bundle) (model.Bundle, error) {
			return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: packets, Events: raw}, nil
		})
	if err != nil {
		t.Fatalf("control strict bundle publication must succeed: %v", err)
	}
	prefix, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatalf("control published prefix must be readable: %v", err)
	}
	s, err := reduce.Replay(prefix)
	if err != nil {
		t.Fatalf("control published events must be admitted by Replay: %v", err)
	}
	return s
}

func readVerifyAnswer(t *testing.T, p store.Project, command string, id model.ID) query.Answer {
	t.Helper()
	a, err := query.Read(p, query.Request{Command: command, ID: id})
	if err != nil {
		t.Fatalf("%s %s must return an answer for the accepted fixture: %v", command, id, err)
	}
	return a
}

func readVerifyReady(t *testing.T, p store.Project) {
	t.Helper()
	readVerifyAppend(t, p, 100, []model.PacketRef{}, laneEReduceCreate(1, laneEReduceSpec(1)))
	a := readVerifyAnswer(t, p, "show", laneEReduceID(1))
	if a.Result != "KNOWN" || a.Watermark.Sequence != 1 || len(a.Records) != 1 || a.Records[0].Task.Status != reduce.StatusReady {
		t.Fatalf("control must read one admitted READY task at watermark 1, got %+v", a)
	}
}

// readVerifyAdmitted captures each group as a real intake packet, then
// publishes the groups with the accepted review write.Admit records: packet
// authors, the capture stamps intake assigned, and each event's packet.
// review round-2 consolidation, step 1.
func readVerifyAdmitted(t *testing.T, p store.Project, n int, groups ...model.Packet) reduce.Snapshot {
	t.Helper()
	ids := make([]model.ID, 0, len(groups))
	for _, g := range groups {
		if _, err := store.WriteIntake(context.Background(), p, store.IntakeRequest{CommandID: g.CommandID, Author: g.Author, Events: g.Events}); err != nil {
			t.Fatalf("control capture of packet %s must succeed: %v", g.CommandID, err)
		}
		ids = append(ids, g.CommandID)
	}
	verified, err := store.ReadVerifiedIntake(p, ids)
	if err != nil || len(verified) != len(ids) {
		t.Fatalf("control captured packets must read back verified: %v", err)
	}
	packets, refs := make([]model.Packet, len(verified)), make([]model.PacketRef, len(verified))
	for i, v := range verified {
		packets[i], refs[i] = v.Packet, v.Ref
	}
	var events []model.TypedEvent
	for _, raw := range laneEReduceReview(t, model.Actor{ID: "reviewer"}, refs, packets) {
		e, err := model.DecodeEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	return readVerifyAppend(t, p, n, refs, events...)
}

// readVerifyAfter is a wall-clock instant strictly after t: the store stamps
// real capture and recording times, so the run's times must follow them.
func readVerifyAfter(t time.Time) time.Time {
	now := time.Now().UTC()
	for !now.After(t) {
		now = time.Now().UTC()
	}
	return now
}

func readVerifyTypes(a query.Answer) []model.EventType {
	types := make([]model.EventType, 0, len(a.History))
	for _, event := range a.History {
		types = append(types, event.Event.Type)
	}
	return types
}

func TestReadVerifyClaimHistoryIncludesReceiptsExplicitlyNamingItsCriterion(t *testing.T) {
	p := readVerifyProject(t)
	events, env, proof := outsideProofFixture()
	// The criterion is admitted a bundle before the run; the run starts after
	// that bundle is recorded, and each packet is captured after what it holds.
	var records, starts []model.TypedEvent
	for _, e := range events {
		if _, ok := e.(*model.TaskStart); ok {
			starts = append(starts, e)
		} else {
			records = append(records, e)
		}
	}
	readVerifyAdmitted(t, p, 100, laneEReducePacket(t, 2201, "lane-e", time.Time{}, records...), laneEReducePacket(t, 2202, "runner", time.Time{}, starts...))
	prefix, err := store.ReadPrefix(p)
	if err != nil || len(prefix) == 0 {
		t.Fatalf("control criterion bundle must be published: %v", err)
	}
	env.StartedAt = readVerifyAfter(prefix[len(prefix)-1].RecordedAt)
	seal := outsideProofSeal(env)
	seal.Envelope.ObservedAt = laneEEvidenceKnown(readVerifyAfter(env.StartedAt))
	s := readVerifyAdmitted(t, p, 101, laneEReducePacket(t, 2203, "runner", time.Time{}, &model.InvocationStart{Envelope: env}),
		laneEReducePacket(t, 2204, "runner", time.Time{}, seal), laneEReducePacket(t, 2205, "reviewer", time.Time{}, proof))
	claim, ok := s.ClaimAt(proof.Claim)
	if !ok || claim.Status != reduce.StatusProven || len(claim.Observations) != 1 {
		t.Fatalf("control admitted start, seal and proof must establish a PROVEN claim with one observation, got %+v", claim)
	}
	// Both receipts are already selected when the question is about the owning
	// task or the explicitly named instrument. No artifact resolution is needed.
	for _, id := range []model.ID{laneEReduceID(1), env.InstrumentRef.RecordID} {
		a := readVerifyAnswer(t, p, "history", id)
		if !strings.Contains(fmt.Sprint(readVerifyTypes(a)), "invocation.start invocation.seal") || a.Watermark.Sequence != s.Watermark().Sequence {
			t.Fatalf("control task/instrument history must include both admitted receipts at the ledger head %d, got %v at %d", s.Watermark().Sequence, readVerifyTypes(a), a.Watermark.Sequence)
		}
	}
	refs := s.CriterionReferrers(*env.CriterionRef.Value)
	if len(refs) != 3 {
		t.Fatalf("control criterion index must contain start, seal and proof references, got %+v", refs)
	}
	a := readVerifyAnswer(t, p, "history", proof.Claim.RecordID)
	want := []model.EventType{"claim.assert", "criterion.fix", "invocation.start", "invocation.seal", "proof.admit"}
	if got := readVerifyTypes(a); !reflect.DeepEqual(got, want) {
		t.Fatalf("claim history at watermark %d returned %v, want %v. Both receipts explicitly name this exact claim revision in envelope.criterion_ref.value.claim, and Replay indexes them. historyOrigins consults only RecordReferrers, so it hides the measurement receipts while retaining the proof that cites them; following this explicit nested reference requires no recursive traversal", a.Watermark.Sequence, got, want)
	}
}

func TestReadVerifyPendingCannotIgnoreAReplayedReviewMissingFromEnvelopePackets(t *testing.T) {
	for _, outcome := range []string{"rejected", "correction-requested", "accepted"} {
		t.Run(outcome, func(t *testing.T) {
			p := readVerifyProject(t)
			readVerifyReady(t, p)
			capture := func(n int) model.PacketRef {
				t.Helper()
				event, err := model.EncodeEvent(laneEReduceCreate(n, laneEReduceSpec(1)))
				if err != nil {
					t.Fatal(err)
				}
				ref, err := store.WriteIntake(context.Background(), p, store.IntakeRequest{
					CommandID: laneEReduceID(n + 1000), Author: model.Actor{ID: "author"}, Events: []model.Event{event}})
				if err != nil {
					t.Fatalf("control capture must succeed: %v", err)
				}
				return ref
			}
			review := func(ref model.PacketRef) *model.ReviewAdmit {
				return &model.ReviewAdmit{Packets: []model.PacketRef{ref}, Outcome: outcome,
					Actor: model.Actor{ID: "reviewer"}, Reason: "explicit canonical disposition"}
			}
			control := capture(2)
			readVerifyAppend(t, p, 101, []model.PacketRef{control}, review(control))
			a := readVerifyAnswer(t, p, "intake pending", "")
			if outcome == "accepted" {
				if len(a.Intake) != 0 {
					t.Fatalf("control accepted packet must leave pending, got %+v", a.Intake)
				}
			} else if len(a.Intake) != 1 || a.Intake[0].Disposition != outcome {
				t.Fatalf("control matching envelope and event must show %s, got %+v", outcome, a.Intake)
			}

			target := capture(3)
			s := readVerifyAppend(t, p, 102, []model.PacketRef{}, review(target))
			r, ok := s.Review(reduce.ReviewKey{Project: p.ID, CommandID: target.CommandID})
			if !ok || r.Outcome != outcome || r.Origin.Sequence != 3 {
				t.Fatalf("control canonical review must replay as %s at sequence 3, got %+v", outcome, r)
			}
			// A read may refuse inconsistent storage. It must not return KNOWN
			// while silently discarding an event its own snapshot admitted.
			a, err := query.Read(p, query.Request{Command: "intake pending"})
			if err != nil {
				return
			}
			for _, packet := range a.Intake {
				if packet.CommandID == target.CommandID && (outcome == "accepted" || packet.Disposition != outcome || packet.Review == nil) {
					t.Errorf("pending at watermark %d reports packet %s as %s with review %+v, but the same prefix replays review.admit=%s. Want that disposition (or exclusion for accepted), or an explicit inconsistent-ledger error. A canonical review cannot depend on its packet ID also appearing in bundle.Packets", a.Watermark.Sequence, target.CommandID, packet.Disposition, packet.Review, outcome)
				}
			}
			if outcome == "accepted" {
				return
			}
			dir, err := store.IntakeDir(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(dir, string(target.CommandID))); err != nil {
				t.Fatal(err)
			}
			a, err = query.Read(p, query.Request{Command: "intake pending"})
			if err != nil {
				return
			}
			for _, packet := range a.Intake {
				if packet.CommandID == target.CommandID && packet.Disposition == outcome && packet.Unavailable != nil && packet.Unavailable.State == "UNKNOWN" {
					return
				}
			}
			t.Errorf("after removing only local bytes, canonical %s packet %s disappeared from the successful answer at watermark %d. Want the review retained with UNKNOWN local bytes; the ledger still contains its review.admit event", outcome, target.CommandID, a.Watermark.Sequence)
		})
	}
}

func TestReadVerifyAuthoredValuesSurviveTextFromTheAdmittedLedger(t *testing.T) {
	p := readVerifyProject(t)
	readVerifyReady(t, p)
	// Unlike a fabricated Answer, these strings must pass the strict event and
	// bundle codecs, Replay, and query selection before either renderer sees them.
	values := []string{
		"quoted \"field\": [0] / \\ path\n\t\r\x00\x1b[2J",
		strings.Repeat("measurement λ / 漢字 / 🐋; ", 5000) + "END-OF-AUTHORED-VALUE",
		"literal \\u003c is different from < & >; actual separator \u2028; trailing spaces   ",
	}
	for i, value := range values {
		spec := laneEReduceSpec(1)
		spec.Intent = value
		spec.NextActor = model.Actor{UnknownReason: "author, reviewer and holder cannot assign the next actor"}
		readVerifyAppend(t, p, 101+i, []model.PacketRef{}, laneEReduceCreate(2+i, spec))
	}
	// "open tasks" is `datum show` kept to TASK records not CLOSED: the
	// selection `datum task todo` made before its R12 removal.
	answer := func(command string) query.Answer {
		if command != "open tasks" {
			return readVerifyAnswer(t, p, command, "")
		}
		a := readVerifyAnswer(t, p, "show", "")
		open := []query.Record{}
		for _, r := range a.Records {
			if r.Fact.Kind == model.Task && r.Task != nil && r.Task.Status != reduce.StatusClosed {
				open = append(open, r)
			}
		}
		if len(open) != len(values)+1 {
			t.Fatalf("control: show must hold %d open tasks at watermark %d, got %d", len(values)+1, a.Watermark.Sequence, len(open))
		}
		a.Records = open
		return a
	}
	for _, command := range []string{"show", "history", "open tasks"} {
		a := answer(command)
		var exported, rendered bytes.Buffer
		if err := query.RenderJSON(&exported, a); err != nil {
			t.Fatal(err)
		}
		if err := query.RenderText(&rendered, a); err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			literal, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(exported.Bytes(), literal) || !bytes.Contains(rendered.Bytes(), literal) {
				t.Errorf("%s must retain the complete authored JSON string in both formats at watermark %d; missing literal of %d bytes (truncation or escape changes would change the answer)", command, a.Watermark.Sequence, len(literal))
			}
		}
		if a.Watermark.Sequence != 4 || !strings.Contains(rendered.String(), `"sequence": 4`) {
			t.Fatalf("%s must retain watermark 4 in both formats, got %+v", command, a.Watermark)
		}
		for repetition := 0; repetition < 5; repetition++ {
			var again bytes.Buffer
			if err := query.RenderText(&again, answer(command)); err != nil || !bytes.Equal(again.Bytes(), rendered.Bytes()) {
				t.Fatalf("%s changed across reads of one unchanged ledger: %v", command, err)
			}
		}
	}
}

func TestReadVerifyLaterAdmissionDoesNotDispositionAnEarlierPrefix(t *testing.T) {
	p := readVerifyProject(t)
	readVerifyReady(t, p)
	create := laneEReduceCreate(2, laneEReduceSpec(1))
	create.Provenance.SourceRefs = []model.ArtifactRef{}
	event, err := model.EncodeEvent(create)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.WriteIntake(context.Background(), p, store.IntakeRequest{
		CommandID: laneEReduceID(1002), Author: create.Provenance.Author, Events: []model.Event{event}})
	if err != nil {
		t.Fatal(err)
	}
	control := readVerifyAnswer(t, p, "intake pending", "")
	if control.Watermark.Sequence != 1 || len(control.Intake) != 1 || control.Intake[0].Disposition != "pending" {
		t.Fatalf("control packet must be pending before admission at watermark 1, got %+v", control)
	}
	// Freeze only the ledger. Both projects retain the same logical identity
	// and therefore read the same later intake inventory. This exercises the
	// old-prefix/new-intake combination without a probabilistic timing race.
	frozen := p
	frozen.Ledger = t.TempDir()
	entries, err := os.ReadDir(p.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Ledger, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(frozen.Ledger, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = write.Admit(context.Background(), p, write.AdmitRequest{CommandID: laneEReduceID(200),
		PacketIDs: []model.ID{ref.CommandID}, Admitter: model.Actor{ID: "reviewer"}, Outcome: "accepted", Reason: "accept the control task"})
	if err != nil {
		t.Fatalf("control packet must pass the real admission gate: %v", err)
	}
	later := readVerifyAnswer(t, p, "intake pending", "")
	if later.Watermark.Sequence != 2 || len(later.Intake) != 0 {
		t.Fatalf("later prefix must exclude the accepted packet at watermark 2, got %+v", later)
	}
	earlier := readVerifyAnswer(t, frozen, "intake pending", "")
	if !reflect.DeepEqual(earlier, control) {
		t.Fatalf("later admission changed the old prefix's answer: before %+v, after %+v. Intake dispositions must come from the selected watermark", control, earlier)
	}
}
