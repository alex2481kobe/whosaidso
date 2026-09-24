package reduce

// Tests for persisted snapshots (restore.go, codec.go): complete field
// coverage, whole-state equality with Replay at every split of several
// ledgers, a pinned golden encoding, damaged images, and immutability.

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
)

// restoreFixtures are ledgers that between them reach every event kind the
// reducer folds: tasks, attempts, holds, closures, sources, criteria, runs,
// proofs, rejected families, supersessions, corrections and disposals.
func restoreFixtures(t *testing.T) map[string][]model.Bundle {
	t.Helper()
	supersede, _, _, _ := supersedeLedger(t)
	revised, _, _, _ := revisedLedger(t)
	family, _, _, _ := familyLedger(t, true)
	rejected, _, env := rejectedLedgerBase(t)
	rejectRun(t, rejected, env, 1)
	// Amendments, holds, clearances and a takeover over the golden tasks.
	tasks := goldenLedger(t)
	tskb, tskc, tskd := ref(newID("TSKB"), 1), ref(newID("TSKC"), 1), ref(newID("TSKD"), 1)
	tasks.add(t, &model.TaskAmend{Provenance: provenance("lane-b"), Target: tskb, ExpectedRevision: 1, Replacement: taskSpec(withIntent("amended"))})
	tasks.add(t, &model.BlockerHold{Task: tskd, BlockerID: newID("HDD1"), Reason: model.BlockerPrerequisite, Actor: model.Actor{ID: "owner"}, Criterion: "the owner rules"})
	tasks.add(t, &model.BlockerClear{Task: tskd, BlockerID: newID("HDD1"), HoldRef: model.BlockerRef{Task: tskd, BlockerID: newID("HDD1")}, ResolvingWitness: blobRef("ruling")})
	tasks.add(t, &model.TaskTakeover{Task: tskc, Actor: model.Actor{ID: "lane-b"}, AttemptID: newID("ATTX"), PriorAttemptID: newID("ATTC"), StoppedConfirmationRef: blobRef("stopped")})
	// Revisions of a proven claim, a decision and an instrument.
	proof := proofLedger(t, true)
	proof.add(t, &model.ClaimRevise{Target: ref(newID("CMA1"), 1), ExpectedRevision: 1, Provenance: provenance("lane-a"), Replacement: claimSpec()})
	proof.add(t, &model.DecisionOpen{ID: newID("DCSA"), Provenance: provenance("author"), Spec: decisionSpec()})
	proof.add(t, &model.DecisionRevise{Target: ref(newID("DCSA"), 1), ExpectedRevision: 1, Replacement: decisionSpec(), Provenance: provenance("author")})
	config := configLedger(t)
	instrument := proofInstrument()
	instrument.ConfigSurface = []string{"samples", "mode"}
	config.add(t, &model.InstrumentRevise{Target: ref(newID("HNSS"), 1), ExpectedRevision: 1, Provenance: provenance("lane-a"), Replacement: instrument})
	// Sub-second ledger times, so a codec that rounds them cannot pass.
	for i := range tasks.out {
		tasks.out[i].RecordedAt = tasks.out[i].RecordedAt.Add(time.Duration(i+1) * 123457 * time.Nanosecond)
	}
	return map[string][]model.Bundle{
		"golden":    goldenLedger(t).bundles(),
		"tasks":     tasks.bundles(),
		"proof":     proof.bundles(),
		"losses":    lossLedger(t).bundles(),
		"supersede": supersede.bundles(),
		"revised":   revised.bundles(),
		"family":    family.bundles(),
		"rejected":  rejected.bundles(),
		"config":    config.bundles(),
	}
}

func mustEncode(t *testing.T, s Snapshot) []byte {
	t.Helper()
	data, err := EncodeSnapshot(s)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return data
}

func TestPersistedFieldsCoverTheWholeState(t *testing.T) {
	named := map[string]bool{}
	for _, f := range newState().persisted() {
		if named[f.name] {
			t.Fatalf("%s is persisted twice", f.name)
		}
		named[f.name] = true
	}
	st := reflect.TypeOf(state{})
	for i := 0; i < st.NumField(); i++ {
		name := st.Field(i).Name
		if name == "log" {
			lt := st.Field(i).Type
			for j := 0; j < lt.NumField(); j++ {
				if !named["log."+lt.Field(j).Name] {
					t.Errorf("event log list %s is neither persisted nor transient", lt.Field(j).Name)
				}
			}
			continue
		}
		if !named[name] && !transientFields[name] {
			t.Errorf("state field %s is neither persisted nor transient; a restored state would lose it", name)
		}
	}
}

func TestRestoreAtEverySplitEqualsReplay(t *testing.T) {
	kinds := map[model.EventType]bool{}
	for name, bundles := range restoreFixtures(t) {
		want := mustReplay(t, bundles)
		for _, b := range bundles {
			for _, e := range b.Events {
				kinds[e.Type] = true
			}
		}
		for k := 0; k <= len(bundles); k++ {
			image := mustEncode(t, mustReplay(t, bundles[:k]))
			got, err := RestoreSnapshot(image, bundles[k:])
			if err != nil {
				t.Fatalf("%s split %d: %v", name, k, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: restoring %d bundles and folding %d more differs from Replay", name, k, len(bundles)-k)
			}
			if again := mustEncode(t, got); string(again) != string(mustEncode(t, want)) {
				t.Fatalf("%s split %d: equal states encoded to different bytes", name, k)
			}
		}
	}
	if len(kinds) != len(codecEvents) {
		t.Errorf("the fixtures reach only %d event kinds; the parity claim needs every one: %v", len(kinds), kinds)
	}
}

// The golden ledger's image is pinned. A change that moves it must also move
// snapshotFormat or the state's types, so older images are refused.
const goldenImageSHA256 = "717a35d6b45956491e71f6a125ffb905bc48d995e1da4921ac152d6f80ed0f69"

func TestGoldenImageIsPinned(t *testing.T) {
	image := mustEncode(t, mustReplay(t, goldenLedger(t).bundles()))
	sum := sha256.Sum256(image)
	if got := hex.EncodeToString(sum[:]); got != goldenImageSHA256 {
		t.Fatalf("the golden image moved to %s (version %s); if the encoding changed on purpose, change snapshotFormat and repin", got, SnapshotVersion())
	}
	if !strings.HasPrefix(SnapshotVersion(), snapshotFormat+"+") {
		t.Fatalf("version %q does not name the format", SnapshotVersion())
	}
}

func TestDamagedImagesAreRefusedNeverPartial(t *testing.T) {
	bundles := proofLedger(t, true).bundles()
	image := mustEncode(t, mustReplay(t, bundles))
	if _, err := RestoreSnapshot(image, nil); err != nil {
		t.Fatalf("control: the intact image restores: %v", err)
	}
	for n := 0; n < len(image); n++ {
		if s, err := RestoreSnapshot(image[:n], nil); err == nil || s.Watermark().Sequence != 0 {
			t.Fatalf("an image cut at %d of %d bytes restored", n, len(image))
		}
	}
	if _, err := RestoreSnapshot(append(append([]byte{}, image...), 0), nil); err == nil {
		t.Fatal("trailing bytes restored")
	}
	other := []byte(strings.Replace(string(image), snapshotFormat, "whosaidso-snapshot/0", 1))
	if _, err := RestoreSnapshot(other, nil); err == nil {
		t.Fatal("an image of another version restored")
	}
	// A flipped byte may decode to a different state (the store's checksum is
	// what catches that); it must never panic.
	for i := range image {
		flipped := append([]byte{}, image...)
		flipped[i] ^= 0x55
		_, _ = RestoreSnapshot(flipped, nil)
	}
}

func TestARefusedTailReturnsNoPartialState(t *testing.T) {
	bundles := goldenLedger(t).bundles()
	image := mustEncode(t, mustReplay(t, bundles[:4]))
	bad := bundles[6] // skips 5 and 6: a gap the envelope refuses
	s, err := RestoreSnapshot(image, []model.Bundle{bundles[4], bad})
	if err == nil {
		t.Fatal("a tail with a gap was folded")
	}
	if !reflect.DeepEqual(s, Snapshot{}) {
		t.Fatalf("a refused tail returned a partial state at sequence %d", s.Watermark().Sequence)
	}
}

func TestRestoredSnapshotsShareNothing(t *testing.T) {
	bundles := goldenLedger(t).bundles()
	image := mustEncode(t, mustReplay(t, bundles[:8]))
	keep := append([]byte{}, image...)
	a, err := RestoreSnapshot(image, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RestoreSnapshot(image, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := mustEncode(t, a)
	// Folding onto one restored snapshot changes neither it nor its sibling.
	next := a
	for _, bundle := range bundles[8:] {
		if next, err = Apply(next, bundle); err != nil {
			t.Fatal(err)
		}
	}
	if string(mustEncode(t, a)) != string(before) || string(mustEncode(t, b)) != string(before) {
		t.Fatal("Apply onto a restored snapshot changed it or a sibling restored from the same image")
	}
	if !reflect.DeepEqual(next, mustReplay(t, bundles)) {
		t.Fatal("Apply onto a restored snapshot differs from Replay")
	}
	// The image is not retained: overwriting it changes no restored state.
	for i := range image {
		image[i] = 0
	}
	if string(mustEncode(t, a)) != string(before) || string(keep) != string(before) {
		t.Fatal("a restored snapshot aliases the image it was decoded from")
	}
}

func TestEncodeRefusesWhatItCannotCarry(t *testing.T) {
	st := newState()
	st.watermark.RecordedAt = time.Date(2026, 9, 23, 0, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	if _, err := EncodeSnapshot(Snapshot{st: st}); err == nil {
		t.Fatal("a non-UTC time was encoded")
	}
	st = newState()
	st.bundle = &bundleFacts{}
	if _, err := EncodeSnapshot(Snapshot{st: st}); err == nil {
		t.Fatal("a candidate's transient inventory was encoded")
	}
	if _, err := EncodeSnapshot(Snapshot{}); err != nil {
		t.Fatalf("control: the empty snapshot encodes: %v", err)
	}
}

// codecSample reaches every kind the codec writes, including ones today's
// state does not hold, so no branch is only believed to work.
type codecSample struct {
	Raw, EmptyRaw, NilRaw []byte
	List, EmptyList       []string
	Map, NilMap           map[Origin]int
	Ptr, NilPtr           *Origin
	At, Zero              time.Time
	Neg                   int
	Small                 uint8
	Flag                  bool
	Pair                  [2]uint64
	Event, NilEvent       model.TypedEvent
}

func TestCodecRoundTripsEveryKindWithoutAliasing(t *testing.T) {
	in := codecSample{Raw: []byte("raw"), EmptyRaw: []byte{}, List: []string{"a", ""}, EmptyList: []string{},
		Map: map[Origin]int{{Sequence: 2}: 1, {Sequence: 1, EventIndex: 3}: -4}, Ptr: &Origin{Sequence: 9},
		At: time.Date(2026, 9, 23, 1, 2, 3, 456789012, time.UTC), Neg: -12, Small: 200, Flag: true, Pair: [2]uint64{1, 1 << 60},
		Event: &model.TaskStart{Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"), Actor: model.Actor{ID: "lane"}}}
	e := &encoder{}
	e.value(reflect.ValueOf(in))
	data := append([]byte{}, e.buf...)
	var out codecSample
	d := &decoder{data: e.buf}
	d.value(reflect.ValueOf(&out).Elem())
	if d.pos != len(e.buf) || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip differs:\n in %+v\nout %+v", in, out)
	}
	for i := range e.buf {
		e.buf[i] = 0
	}
	if string(out.Raw) != "raw" || out.List[0] != "a" {
		t.Fatal("a decoded value aliases the image it came from")
	}
	again := &encoder{}
	again.value(reflect.ValueOf(out))
	if string(again.buf) != string(data) {
		t.Fatal("a decoded value re-encodes to different bytes")
	}
}

func TestFingerprintMovesWithAnyTypeChange(t *testing.T) {
	// Anonymous types, so only the shape can tell them apart, not a type name.
	base := shapeFingerprint([]persistedField{{"f", &struct{ X []struct{ A uint64 } }{}}})
	if base != shapeFingerprint([]persistedField{{"f", &struct{ X []struct{ A uint64 } }{}}}) {
		t.Fatal("control: one shape fingerprints the same twice")
	}
	for name, other := range map[string]persistedField{
		"renamed field":        {"f", &struct{ Y []struct{ A uint64 } }{}},
		"renamed nested field": {"f", &struct{ X []struct{ B uint64 } }{}},
		"retyped nested field": {"f", &struct{ X []struct{ A string } }{}},
		"added nested field":   {"f", &struct{ X []struct{ A, B uint64 } }{}},
		"renamed state field":  {"g", &struct{ X []struct{ A uint64 } }{}},
	} {
		if shapeFingerprint([]persistedField{other}) == base {
			t.Errorf("a %s kept the fingerprint, so an older image would restore into it", name)
		}
	}
}
