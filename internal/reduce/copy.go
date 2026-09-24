package reduce

import (
	"reflect"
	"sync"
)

// A snapshot is documented as an immutable projection of a ledger prefix, and
// every accessor here hands a caller a value out of the reduced state. Those
// values are structs, but several of them carry slices and pointers, so
// returning one by value shares the memory underneath it.
//
// state.clone says "values in these maps are replaced, never mutated in place,
// so a shallow copy of each map is a real fork of the state". That is true
// about the maps and false about the graph they reach. A Closure holds two
// witness slices; an Attempt holds a *Terminal. Copying the map copies the
// slice header and the pointer, so two forks that the code calls independent
// still write to the same backing array. An acceptance fixture caught it three ways:
// mutating a closure, a task spec and a terminal returned by a LATER snapshot
// each changed an EARLIER one from CLOSED to BLOCKED, with no admitted event.
//
// A snapshot that changes without an event is the worst thing this package
// could do, because every query above it would be correctly computed from
// corrupted input, and the ledger would still read clean.
//
// The copy happens on the way OUT rather than in clone, for a measured reason:
// clone runs once per admitted bundle, so deep copying there costs the whole
// state on every event and turns replay quadratic. Reads pay only for what they
// actually read.
//
// Measured on 250 tasks, 500 events, darwin/arm64:
//
//	Replay      31.0ms/op        write path, unchanged
//	Tasks        970us/op   416KB, 5007 allocs
//	TasksRaw     787us/op    60KB, 1000 allocs   same read, no copy
//
// So a full read of 250 tasks pays about 184us and 355KB for the guarantee,
// around 0.7us per task. The write path is not merely believed to be untouched:
// BenchmarkReplay was run with both copy functions replaced by a panic and it
// completed, so no copy is reachable from Apply.
//
// It is reflective rather than 44 hand-written copy methods because the hazard
// is not today's fields, it is the field someone adds next year to TaskSpec.
// A hand-written copier that nobody updated is the same defect wearing a hat: a
// function that looks right and answers the question its author had. Reflection
// walks whatever the struct actually holds. TestDeepCopyLeavesNoAliasedField
// checks it against every escaping type by reflection, so a new type is covered
// the day it is declared.
//
// It assumes the reduced state is acyclic, which it is: records point at each
// other with RecordRef values, never with pointers.

// deepCopy returns a value sharing no mutable memory with in.
func deepCopy[T any](in T) T {
	if !needsCopy(reflect.TypeOf(in)) {
		return in
	}
	v := reflect.ValueOf(&in).Elem()
	deepCopyValue(v)
	return in
}

// deepCopySlice copies a slice and every value reachable from it. Accessors
// that build a fresh slice still need this, since the ELEMENTS they append are
// the stored ones.
//
// It ALWAYS allocates a new backing array, even for an element type that needs
// no deep copy. Returning the input for a scalar element type would be a
// correct-looking shortcut that hands a caller the stored slice, and callers
// here sort what they get back: sortedReferrers would have sorted the reverse
// index in place. Slice elements are addressable through the slice's data
// pointer, so a non-addressable slice Value is still fine to walk.
func deepCopySlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	var zero T
	if !needsCopy(reflect.TypeOf(&zero).Elem()) {
		return out
	}
	v := reflect.ValueOf(out)
	for i := 0; i < v.Len(); i++ {
		deepCopyValue(v.Index(i))
	}
	return out
}

// deepCopyValue replaces every reference reachable from v, in place. v must be
// addressable.
func deepCopyValue(v reflect.Value) {
	if !needsCopy(v.Type()) {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		nv := reflect.New(v.Type().Elem())
		nv.Elem().Set(v.Elem())
		deepCopyValue(nv.Elem())
		v.Set(nv)
	case reflect.Slice:
		if v.IsNil() {
			return
		}
		ns := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(ns, v)
		for i := 0; i < ns.Len(); i++ {
			deepCopyValue(ns.Index(i))
		}
		v.Set(ns)
	case reflect.Map:
		if v.IsNil() {
			return
		}
		t := v.Type()
		nm := reflect.MakeMapWithSize(t, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			k := reflect.New(t.Key()).Elem()
			k.Set(iter.Key())
			deepCopyValue(k)
			val := reflect.New(t.Elem()).Elem()
			val.Set(iter.Value())
			deepCopyValue(val)
			nm.SetMapIndex(k, val)
		}
		v.Set(nm)
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		inner := reflect.New(v.Elem().Type()).Elem()
		inner.Set(v.Elem())
		deepCopyValue(inner)
		v.Set(inner)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			// An unexported reference field cannot be copied through
			// reflection and would stay aliased in silence. No escaping type
			// has one, and TestNoEscapingTypeHidesAReference keeps it that way.
			if !f.CanSet() {
				continue
			}
			deepCopyValue(f)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			deepCopyValue(v.Index(i))
		}
	}
}

var copyNeeded sync.Map // reflect.Type -> bool

// needsCopy reports whether t can reach any reference at all. A type of plain
// scalars is returned untouched, which is most of what the hot accessors carry.
func needsCopy(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if v, ok := copyNeeded.Load(t); ok {
		return v.(bool)
	}
	r := reaches(t, map[reflect.Type]bool{})
	copyNeeded.Store(t, r)
	return r
}

func reaches(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		// A type reached through itself adds no NEW reason to copy; whichever
		// field led here already decided it.
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		return true
	case reflect.Array:
		return reaches(t.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if reaches(t.Field(i).Type, seen) {
				return true
			}
		}
	}
	return false
}
