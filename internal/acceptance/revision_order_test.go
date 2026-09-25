// Revision-order counterexamples and controls belong here; production repairs
// and unrelated acceptance checks do not. All filesystem fixtures use TempDir.
package acceptance_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

func revisionOrderNew(t *testing.T) *gateVerifyFixture {
	t.Helper()
	t.Setenv(store.HomeEnv, filepath.Join(t.TempDir(), "revision-order-home"))
	t.Setenv(store.NoCacheEnv, "1")
	root := t.TempDir()
	return &gateVerifyFixture{t: t, p: store.Project{ID: recProject, Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}, n: 400}
}

// Probe preserves the actual captured packets, including their boundaries and
// authors, in a separately constructed replay bundle. groups is the valid
// replay order; capture permutes capture-time IDs, not authored event order.
func revisionOrderProbe(f *gateVerifyFixture, actor model.Actor, groups [][]model.TypedEvent, capture, replayCode, gateCode string) {
	t := f.t
	t.Helper()
	prefix, err := store.ReadPrefix(f.p)
	if err != nil {
		t.Fatal(err)
	}
	packets := make([]model.Packet, len(groups))
	refs := make([]model.PacketRef, len(groups))
	ids := []model.ID{}
	for _, digit := range capture {
		i := int(digit - '0')
		raw := []model.Event{}
		for _, event := range groups[i] {
			raw = append(raw, recEncode(t, event))
		}
		refs[i] = f.capture(actor, raw...)
		got, err := store.ReadVerifiedIntake(f.p, []model.ID{refs[i].CommandID})
		if err != nil {
			t.Fatal(err)
		}
		packets[i] = got[0].Packet
		ids = append(ids, refs[i].CommandID)
	}
	b := model.Bundle{Version: model.WireVersion, Project: f.p.ID, Sequence: uint64(len(prefix) + 1), CommandID: f.id(), RequestDigest: model.HashBytes([]byte("revision-order-replay")), Admitter: actor, RecordedAt: time.Now().UTC(), Packets: refs}
	if len(prefix) > 0 {
		b.Predecessor = prefix[len(prefix)-1].CommandID
	}
	b.Events = reduceReview(t, actor, refs, packets)
	if _, err := reduce.Replay(append(append([]model.Bundle{}, prefix...), b)); holdIdentityCode(err) != replayCode {
		t.Fatalf("packet-preserving replay: want %q, got %v", replayCode, err)
	}
	before := gateVerifyLedger(t, f.p)
	// The same captured set is checked in both request orders as well.
	for pass := 0; pass < 2; pass++ {
		check, err := write.CheckAdmission(context.Background(), f.p, ids, nil, actor)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if len(check.Refusals) > 0 {
			got = holdIdentityCode(check.Refusals[0].Err)
		}
		if got != gateCode {
			t.Errorf("check admission: replay=%q, want %q, got %+v", replayCode, gateCode, check.Refusals)
		}
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
	}
	if !reflect.DeepEqual(before, gateVerifyLedger(t, f.p)) {
		t.Fatal("dry run wrote the ledger")
	}
	admitted, err := write.Admit(context.Background(), f.p, write.AdmitRequest{CommandID: f.id(), PacketIDs: ids, Admitter: actor, Outcome: "accepted", Reason: "ordering verification"})
	if holdIdentityCode(err) != gateCode {
		t.Errorf("admission: replay=%q, want %q, got %v", replayCode, gateCode, err)
	}
	if err != nil {
		if !reflect.DeepEqual(before, gateVerifyLedger(t, f.p)) {
			t.Error("refusal wrote the ledger")
		}
		return
	}
	if _, err := reduce.Replay(append(prefix, admitted)); err != nil {
		t.Errorf("admitted bundle cannot replay: %v", err)
	}
	if !reflect.DeepEqual(admitted.Events[:len(admitted.Events)-1], b.Events[:len(b.Events)-1]) {
		t.Error("bundle event order differs from the required capture-independent order")
	}
}

func revisionOrderRevision(f *gateVerifyFixture, actor model.Actor, kind string) (model.RecordRef, model.TypedEvent) {
	f.t.Helper()
	prov := model.Provenance{SourceRefs: []model.ArtifactRef{}}
	ref := model.RecordRef{Project: f.p.ID, RecordID: f.id(), Revision: 1}
	var create, revise model.TypedEvent
	switch kind {
	case "task":
		spec := reduceSpec(1)
		create, revise = &model.TaskCreate{ID: ref.RecordID, Provenance: prov, Spec: spec}, &model.TaskAmend{Target: ref, Provenance: prov, Replacement: spec}
	case "claim":
		spec := f.claim(actor).Spec
		create, revise = &model.ClaimAssert{ID: ref.RecordID, Provenance: prov, Spec: spec}, &model.ClaimRevise{Target: ref, Provenance: prov, Replacement: spec}
	case "decision":
		spec := recDecisionSpec()
		spec.Scope = reduceScope()
		create, revise = &model.DecisionOpen{ID: ref.RecordID, Provenance: prov, Spec: spec}, &model.DecisionRevise{Target: ref, Provenance: prov, Replacement: spec}
	case "instrument":
		body := []byte(`{"tool":"revision-order"}`)
		gateVerifyPut(f.t, filepath.Join(f.p.Root, "revision-order-tool.json"), body)
		spec := recInstrumentSpec()
		spec.ImplementationRef = gateVerifyContent(body, "revision-order-tool.json")
		spec.Validation = recUnknown[model.InstrumentValidation]("not validated")
		create, revise = &model.InstrumentDeclare{ID: ref.RecordID, Provenance: prov, Spec: spec}, &model.InstrumentRevise{Target: ref, Provenance: prov, Replacement: spec}
	}
	if _, err := f.admit(actor, actor, create); err != nil {
		f.t.Fatal(err)
	}
	return ref, revise
}

// revisionOrderAsAdmitted renumbers groups into the order admission applies
// them (dependencies first, otherwise capture order), keeping the same capture
// order, so replay, check admission and admission judge the same sequence.
func revisionOrderAsAdmitted(groups [][]model.TypedEvent, capture, admitted string) ([][]model.TypedEvent, string) {
	renumbered := make([][]model.TypedEvent, len(groups))
	position := map[rune]rune{}
	for k, digit := range admitted {
		renumbered[k] = groups[digit-'0']
		position[digit] = rune('0' + k)
	}
	out := []rune{}
	for _, digit := range capture {
		out = append(out, position[digit])
	}
	return renumbered, string(out)
}

func revisionOrderTask(f *gateVerifyFixture, refs ...model.RecordRef) *model.TaskCreate {
	spec := reduceSpec(1)
	spec.ContextRefs = refs
	return &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: spec}
}

// Every case has a merged-packet and already-admitted control. Splitting the
// exact same valid events must not invent an impossible dependency cycle.
func TestRevisionOrderHistoricalReferencesFalseCycle(t *testing.T) {
	for _, kind := range []string{"task", "claim", "instrument", "decision"} {
		for _, use := range []string{"context", "supersede", "correction"} {
			for _, shape := range []string{"merged", "already-admitted", "01", "10"} {
				t.Run(kind+"/"+use+"/"+shape, func(t *testing.T) {
					f := revisionOrderNew(t)
					actor := model.Actor{ID: "revision-order"}
					r1, revise := revisionOrderRevision(f, actor, kind)
					r2 := r1
					r2.Revision = 2
					var event model.TypedEvent = revisionOrderTask(f, r1, r2)
					switch use {
					case "supersede":
						event = &model.Supersede{Prior: r1, Replacement: r2, Reason: "the admitted revision is replaced by its correction"}
					case "correction":
						body := []byte(`{"correction":"both revisions are affected"}`)
						gateVerifyPut(t, filepath.Join(f.p.Root, "revision-order-correction.json"), body)
						event = &model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: &r1}, AffectedRevisions: []model.RecordRef{r1, r2}, Reason: "record both affected revisions", CorrectiveRef: gateVerifyContent(body, "revision-order-correction.json")}
					}
					groups, capture := [][]model.TypedEvent{{revise}, {event}}, shape
					if shape == "merged" {
						groups, capture = [][]model.TypedEvent{{revise, event}}, "0"
					}
					if shape == "already-admitted" {
						if _, err := f.admit(actor, actor, revise); err != nil {
							t.Fatal(err)
						}
						groups, capture = [][]model.TypedEvent{{event}}, "0"
					}
					revisionOrderProbe(f, actor, groups, capture, "", "")
				})
			}
		}
	}
}

// Proposals naming a superseded revision are stale by design (optimistic concurrency); the gate does not reorder packets to rescue them.
// Admission applies packets in capture order after their dependencies, so a
// second amendment captured before the r2 hold makes the hold stale, and a
// packet naming claim r1 may land after the revision to r2.
func TestRevisionOrderControls(t *testing.T) {
	// Admission order for each capture order: dependencies first, otherwise
	// capture order. In the amend chain the hold (1) and the second amendment
	// (2) wait for the first amendment (0); with two claim revisions the r2
	// referrer (2) waits for the revision (1).
	admittedOrder := map[string]map[string]string{
		"amend-chain":         {"012": "012", "021": "021", "102": "012", "120": "012", "201": "021", "210": "021"},
		"two-claim-revisions": {"012": "012", "021": "012", "102": "102", "120": "120", "201": "012", "210": "120"},
	}
	for _, order := range []string{"012", "021", "102", "120", "201", "210"} {
		for _, shape := range []string{"amend-chain", "two-claim-revisions"} {
			t.Run(shape+"/"+order, func(t *testing.T) {
				f := revisionOrderNew(t)
				actor := model.Actor{ID: "revision-order"}
				kind := "task"
				if shape == "two-claim-revisions" {
					kind = "claim"
				}
				r1, revise := revisionOrderRevision(f, actor, kind)
				r2 := r1
				r2.Revision = 2
				groups := [][]model.TypedEvent{{revisionOrderTask(f, r1)}, {revise}, {revisionOrderTask(f, r2)}}
				if shape == "amend-chain" {
					a2 := *revise.(*model.TaskAmend)
					a2.Target = r2
					hold := &model.BlockerHold{Task: r2, BlockerID: f.id(), Reason: model.BlockerResume, Actor: actor, Criterion: "resume approved"}
					groups = [][]model.TypedEvent{{revise}, {hold}, {&a2}}
				}
				groups, capture := revisionOrderAsAdmitted(groups, order, admittedOrder[shape][order])
				want := ""
				if shape == "amend-chain" && admittedOrder[shape][order] != "012" {
					// The second amendment went first, so the hold names a superseded revision.
					want = "revision-conflict"
				}
				revisionOrderProbe(f, actor, groups, capture, want, want)
			})
		}
	}
	t.Run("author-order-is-preserved", func(t *testing.T) {
		f := revisionOrderNew(t)
		actor := model.Actor{ID: "revision-order"}
		r1, revise := revisionOrderRevision(f, actor, "task")
		hold := &model.BlockerHold{Task: r1, BlockerID: f.id(), Reason: model.BlockerResume, Actor: actor, Criterion: "resume approved"}
		revisionOrderProbe(f, actor, [][]model.TypedEvent{{revise, hold}}, "0", "revision-conflict", "revision-conflict")
	})
}

// Proposals naming a superseded revision are stale by design (optimistic concurrency); the gate does not reorder packets to rescue them.
// A proof captured after its claim's revision still judges a valid historical
// claim revision and lands after it; one captured after its criterion's fix no
// longer judges the current criterion and is refused.
func TestRevisionOrderProof(t *testing.T) {
	for _, mode := range []string{"claim", "claim-with-new-context", "criterion-2", "criterion-7"} {
		for _, shape := range []string{"merged", "already-admitted", "01", "10"} {
			t.Run(mode+"/"+shape, func(t *testing.T) {
				t.Setenv(store.HomeEnv, filepath.Join(t.TempDir(), "revision-order-home"))
				t.Setenv(store.NoCacheEnv, "1")
				w := pvOwnOutputControl(t, []byte(pvPass))
				s := w.snapshot()
				inv := s.Invocations()[0]
				proof := w.proof(map[model.ID]string{inv.Key.InvocationID: "supports"})
				f := &gateVerifyFixture{t: t, p: w.p, n: w.n}
				record, _ := s.Record(w.claim)
				var revise model.TypedEvent = &model.ClaimRevise{Target: w.claim, Provenance: record.Provenance, Replacement: *record.Claim}
				groups := [][]model.TypedEvent{{proof}, {revise}}
				if mode == "claim-with-new-context" {
					r2 := w.claim
					r2.Revision = 2
					groups = [][]model.TypedEvent{{revise}, {proof, revisionOrderTask(f, r2)}}
				}
				if mode == "criterion-2" || mode == "criterion-7" {
					criterion, _ := s.Criterion(w.criterion)
					fix := criterion.Fix
					fix.Revision = 2
					if mode == "criterion-7" {
						fix.Revision = 7
					}
					groups = [][]model.TypedEvent{{proof}, {&fix}}
				}
				capture, want := shape, ""
				if shape == "merged" {
					groups, capture = [][]model.TypedEvent{append(groups[0], groups[1]...)}, "0"
				}
				if shape == "already-admitted" {
					revision, remaining := groups[1], groups[0]
					if mode == "claim-with-new-context" {
						revision, remaining = groups[0], groups[1]
					}
					if _, err := f.admit(w.agent, w.agent, revision...); err != nil {
						t.Fatal(err)
					}
					groups, capture = [][]model.TypedEvent{remaining}, "0"
					if mode == "criterion-2" || mode == "criterion-7" {
						want = "invalid-transition"
					}
				}
				if shape == "10" && mode != "claim-with-new-context" {
					// No dependency between proof and revision: capture order stands.
					groups, capture = revisionOrderAsAdmitted(groups, capture, "10")
					if mode == "criterion-2" || mode == "criterion-7" {
						want = "invalid-transition"
					}
				}
				revisionOrderProbe(f, w.agent, groups, capture, want, want)
			})
		}
	}
}
