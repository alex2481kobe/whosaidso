package reduce

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"whosaidso/internal/model"
)

// The two tests here protect the deep copy from rotting rather than checking
// today's fields. An acceptance fixture proves the BEHAVIOUR from
// outside: mutating a value from a later snapshot must not change an earlier
// one. These prove the STRUCTURE, so a type added next year is covered the day
// it is declared and nobody has to remember this file exists.

// escapingTypes is every struct type reachable from the return of an exported
// Snapshot method: exactly the values that leave this package.
func escapingTypes(t *testing.T) map[reflect.Type]string {
	t.Helper()
	st := reflect.TypeOf(Snapshot{})
	out := map[reflect.Type]string{}
	var walk func(reflect.Type, string)
	walk = func(ty reflect.Type, path string) {
		switch ty.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			walk(ty.Elem(), path+"[]")
			return
		case reflect.Struct:
			if _, seen := out[ty]; seen {
				return
			}
			out[ty] = path
			for i := 0; i < ty.NumField(); i++ {
				f := ty.Field(i)
				walk(f.Type, path+"."+f.Name)
			}
		}
	}
	for i := 0; i < st.NumMethod(); i++ {
		m := st.Method(i)
		for j := 0; j < m.Type.NumOut(); j++ {
			walk(m.Type.Out(j), m.Name)
		}
	}
	if len(out) < 20 {
		t.Fatalf("only %d escaping types found, so this test is not looking at the API it claims to", len(out))
	}
	return out
}

// immutableByContract is the stated exemption list, and it is deliberately
// tiny. These types DO hide references that deepCopy leaves shared.
//
// The first version of this comment claimed "nothing can mutate them through
// their public API, so a shared address can never become a shared change".
// That is false, and an outside reviewer proved it with
// TestCopySnapshotTimeLocationCannotRewriteAdmittedTimestamp. time.Time.Location
// is exported and returns a *time.Location, and `*loc = *other` is ordinary Go
// needing no unsafe and no reflection. Assigning through it changes how an
// admitted timestamp renders in every snapshot at once, with no event behind
// the change.
//
// So the accurate claim is narrower: these types have no exported method that
// mutates the receiver, and the only way to change one is to assign through a
// pointer the caller had to go out of its way to obtain.
//
// The UTC-only wire closes the reachable case. The wire carries only UTC: EncodeEvent
// writes every timestamp as UTC and decoding refuses any other offset, so an
// admitted snapshot can no longer hold a private location - none can be
// admitted. TestInvocationTimestampIsUTCThroughLedgerPublication and
// TestInvocationTimestampIsUTCThroughApplicationAdmission prove it through the
// real ledger and intake paths. The one location left is time.UTC itself,
// shared by the whole process; assigning through it corrupts every time value
// in the program, not one snapshot, and no copy policy could contain that.
//
// A snapshot built by hand, bypassing decode, can still hold a private
// location; the copy does not detach it. TestDetachedLocationCandidateCost
// keeps the measured price of detaching (one allocation per timestamp) should
// that ever become reachable again. Production copy behavior is unchanged.
//
// Adding to this list is a claim about what a caller can do to a type. Make it
// on purpose, in writing, with its price stated, or do not make it.
var immutableByContract = map[string]bool{
	"time.Time":     true,
	"time.Location": true,
}

// TestNoEscapingTypeHidesAReference is what makes the reflective copy sound.
// deepCopyValue skips a field it cannot set, and an unexported slice or pointer
// would therefore stay ALIASED while the copy reported success. No escaping
// type has one today. If this ever fails, the new field is silently shared
// between snapshots and the copy must grow a hand-written case for it.
func TestNoEscapingTypeHidesAReference(t *testing.T) {
	for ty, path := range escapingTypes(t) {
		if immutableByContract[ty.String()] {
			continue
		}
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			if f.IsExported() {
				continue
			}
			switch f.Type.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
				t.Errorf("%s.%s (reached by %s) is an unexported %s, which deepCopy cannot replace, so it stays shared between snapshots",
					ty, f.Name, path, f.Type.Kind())
			}
		}
	}
}

// TestDeepCopySharesNoMemoryWithItsInput fills every reference in each escaping
// type, copies it, and asserts the two values have no pointer, slice or map in
// common. A shared address is the defect itself, not a proxy for it.
func TestDeepCopySharesNoMemoryWithItsInput(t *testing.T) {
	for ty := range escapingTypes(t) {
		original := reflect.New(ty).Elem()
		fillRefs(original, 6)

		copied := reflect.New(ty).Elem()
		copied.Set(original)
		deepCopyValue(copied)

		before, after := map[uintptr]string{}, map[uintptr]string{}
		addrs(original, before, "", 8)
		addrs(copied, after, "", 8)
		if len(before) == 0 {
			continue // a type of plain scalars has nothing to share
		}
		for addr, path := range before {
			if other, shared := after[addr]; shared {
				t.Errorf("%s still shares memory after copying: original%s and copy%s are the same address", ty, path, other)
			}
		}
	}
}

// fillRefs allocates every reference reachable from v so the disjointness check
// has something to compare. Depth is bounded because a type can reach itself.
func fillRefs(v reflect.Value, depth int) {
	if depth <= 0 || !v.CanSet() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fillRefs(v.Elem(), depth-1)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillRefs(s.Index(0), depth-1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		val := reflect.New(v.Type().Elem()).Elem()
		fillRefs(val, depth-1)
		m.SetMapIndex(reflect.New(v.Type().Key()).Elem(), val)
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			fillRefs(v.Field(i), depth-1)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillRefs(v.Index(i), depth-1)
		}
	}
}

// addrs collects the address behind every reference reachable from v.
func addrs(v reflect.Value, out map[uintptr]string, path string, depth int) {
	if depth <= 0 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Map:
		if v.IsNil() {
			return
		}
		out[v.Pointer()] = path
		if v.Kind() == reflect.Pointer {
			addrs(v.Elem(), out, path+"*", depth-1)
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			addrs(iter.Value(), out, path+"[k]", depth-1)
		}
	case reflect.Slice:
		if v.IsNil() || v.Len() == 0 {
			return
		}
		out[v.Pointer()] = path
		for i := 0; i < v.Len(); i++ {
			addrs(v.Index(i), out, fmt.Sprintf("%s[%d]", path, i), depth-1)
		}
	case reflect.Interface:
		if !v.IsNil() {
			addrs(v.Elem(), out, path+".(dyn)", depth-1)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			// An unexported field is unreadable here, and unreadable is why
			// TestNoEscapingTypeHidesAReference exists instead.
			if v.Field(i).CanInterface() {
				addrs(v.Field(i), out, path+"."+v.Type().Field(i).Name, depth-1)
			}
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			addrs(v.Index(i), out, fmt.Sprintf("%s[%d]", path, i), depth-1)
		}
	}
}

// ---- cost ----------------------------------------------------------------
//
// The copy is on the read path rather than in clone for a reason that is only
// worth anything if it is measured. BenchmarkReplay must not move: it is the
// write path and it does not copy. BenchmarkTasks against benchmarkTasksRaw is
// what a caller pays for the guarantee.

func benchLedger(b *testing.B, tasks int) []model.Bundle {
	b.Helper()
	var out []model.Bundle
	var prev model.ID
	seq := uint64(0)
	emit := func(events ...model.TypedEvent) {
		seq++
		raw := make([]model.Event, 0, len(events))
		for _, e := range events {
			enc, err := model.EncodeEvent(e)
			if err != nil {
				b.Fatalf("fixture event %s does not encode: %v", e.EventType(), err)
			}
			raw = append(raw, enc)
		}
		bd := model.Bundle{
			Version: model.WireVersion, Project: testProject, Sequence: seq,
			CommandID: newID(fmt.Sprintf("CMD%d", seq)), Predecessor: prev,
			RequestDigest: newDigest(fmt.Sprintf("request-%d", seq)),
			Admitter:      model.Actor{ID: "coordinator"},
			RecordedAt:    baseTime.Add(time.Duration(seq) * time.Minute),
			Packets:       []model.PacketRef{{CommandID: newID(fmt.Sprintf("PKT%d", seq)), Digest: newDigest(fmt.Sprintf("packet-%d", seq))}},
			Events:        raw,
		}
		prev = bd.CommandID
		out = append(out, bd)
	}
	for i := 0; i < tasks; i++ {
		id := newID(fmt.Sprintf("TK%03d", i))
		emit(&model.TaskCreate{Provenance: provenance("agent-a"), ID: id, Spec: taskSpec()})
		emit(&model.TaskStart{Task: ref(id, 1), Actor: model.Actor{ID: "agent-a"}, AttemptID: newID(fmt.Sprintf("AT%03d", i))})
	}
	return out
}

func BenchmarkReplay(b *testing.B) {
	bundles := benchLedger(b, 250)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Replay(bundles); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTasks(b *testing.B) {
	s, err := Replay(benchLedger(b, 250))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Tasks()
	}
}

// BenchmarkTasksRaw is the same read without the copy: the price of the
// guarantee is the difference between this and BenchmarkTasks.
func BenchmarkTasksRaw(b *testing.B) {
	s, err := Replay(benchLedger(b, 250))
	if err != nil {
		b.Fatal(err)
	}
	st := s.inner()
	ids := make([]Ident, 0)
	for who := range st.current {
		ids = append(ids, who)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, who := range ids {
			_, _ = st.task(who)
		}
	}
}

// TestSliceAccessorsCannotBeMutatedThroughTheirResult is here because a
// mutation survived without it. deepCopySlice originally returned its input
// unchanged whenever the ELEMENT type needed no deep copy, which reads like a
// harmless fast path and is not one: sortedReferrers sorts what it is given, so
// the reverse index would have been reordered in place by a read. Every test in
// the package still passed, because the three aliasing fixtures all go through
// deepCopy rather than deepCopySlice.
//
// It asks one question of every slice-returning accessor, by reflection so a
// new accessor is covered without being listed: if a caller wrecks what you
// returned, does the next caller still get the truth?
func TestSliceAccessorsCannotBeMutatedThroughTheirResult(t *testing.T) {
	snap := mustReplay(t, goldenLedger(t).bundles())
	sv := reflect.ValueOf(snap)

	var anyRef model.RecordRef
	if recs := snap.Records(); len(recs) > 0 {
		anyRef = asRef(recs[0].Key)
	}

	checked := 0
	for i := 0; i < sv.NumMethod(); i++ {
		m := sv.Type().Method(i)
		if m.Type.NumOut() != 1 || m.Type.Out(0).Kind() != reflect.Slice {
			continue
		}
		args, ok := benignArgs(m.Type, anyRef)
		if !ok {
			continue
		}

		first := sv.Method(i).Call(args)[0]
		if first.Len() == 0 {
			continue // nothing returned means nothing to alias
		}
		before := fingerprint(t, first.Interface())

		// Wreck it the two ways a caller plausibly would: reorder, then blank.
		for a, b := 0, first.Len()-1; a < b; a, b = a+1, b-1 {
			x, y := reflect.New(first.Type().Elem()).Elem(), reflect.New(first.Type().Elem()).Elem()
			x.Set(first.Index(a))
			y.Set(first.Index(b))
			first.Index(a).Set(y)
			first.Index(b).Set(x)
		}
		first.Index(0).Set(reflect.Zero(first.Type().Elem()))
		// Reordering and blanking only touch the slice this call returned.
		// An element whose own slices are still shared survives that, so
		// wreck each element's CONTENTS in place as well.
		for k := 0; k < first.Len(); k++ {
			wreckInPlace(first.Index(k), 6)
		}

		again := fingerprint(t, sv.Method(i).Call(args)[0].Interface())
		if again != before {
			t.Errorf("Snapshot.%s hands out the stored slice: wrecking the first result changed what the second call answered\n first: %s\nsecond: %s", m.Name, before, again)
		}
		checked++
	}
	if checked < 4 {
		t.Fatalf("only %d slice accessors were exercised, so this test is not covering the API it claims to", checked)
	}
}

// wreckInPlace zeroes what every reachable reference POINTS AT, without
// replacing the reference. Overwriting the field would only prove the struct
// was copied; writing through it is what proves the memory underneath is not
// shared with the snapshot.
func wreckInPlace(v reflect.Value, depth int) {
	if depth <= 0 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		wreckInPlace(v.Elem(), depth-1)
		if v.Elem().CanSet() {
			v.Elem().Set(reflect.Zero(v.Type().Elem()))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			wreckInPlace(v.Index(i), depth-1)
			v.Index(i).Set(reflect.Zero(v.Type().Elem()))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				wreckInPlace(v.Field(i), depth-1)
			}
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			wreckInPlace(v.Index(i), depth-1)
		}
	}
}

// fingerprint renders a result by VALUE. %+v prints a pointer as its address,
// and the copy allocates a new address every call, so a %+v comparison would
// report a difference on every correct read and never on an incorrect one: a
// true statement about identity where the question was about content.
func fingerprint(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("fixture cannot fingerprint %T: %v", v, err)
	}
	return string(b)
}

// benignArgs supplies arguments for the accessors that take them. An accessor
// whose arguments cannot be guessed is skipped rather than guessed at.
func benignArgs(ft reflect.Type, ref model.RecordRef) ([]reflect.Value, bool) {
	args := make([]reflect.Value, 0, ft.NumIn()-1)
	for i := 1; i < ft.NumIn(); i++ {
		if ft.IsVariadic() && i == ft.NumIn()-1 {
			break
		}
		switch ft.In(i) {
		case reflect.TypeOf(model.RecordRef{}):
			args = append(args, reflect.ValueOf(ref))
		case reflect.TypeOf(Ident{}):
			args = append(args, reflect.ValueOf(Ident{Project: ref.Project, ID: ref.RecordID}))
		case reflect.TypeOf(model.CriterionRef{}):
			args = append(args, reflect.ValueOf(model.CriterionRef{}))
		case reflect.TypeOf(model.InvocationRef{}):
			args = append(args, reflect.ValueOf(model.InvocationRef{}))
		default:
			return nil, false
		}
	}
	return args, true
}
