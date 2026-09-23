package reduce

// Persisting a snapshot and extending a restored one live here: which state
// fields are persisted, the version an image must match, map key order, and the owned tail
// fold. The value encoding is codec.go's; where images are stored and when
// one may be trusted is the store's, never this package's.
//
// A persisted snapshot is derived data. It is only ever a shortcut to the
// state Replay would compute from the same ledger prefix, and the store reuses
// one only after checking every byte of that prefix. Nothing here makes an
// image authoritative.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"datum/internal/model"
)

// snapshotFormat names the encoding. Change it with any change to codec.go or
// to the field list below, or to the event format an image carries: /2 is the
// R18.2 format, so an image of a pre-migration ledger is refused and rebuilt
// (and the old ledger refused) rather than answering from old events.
const snapshotFormat = "datum-snapshot/2"

// persisted names every state field an image carries, in encoding order. The
// transient ones (the candidate inventory and a dry run's refusal collector)
// belong to one Apply call and are never part of a state at rest.
func (s *state) persisted() []persistedField {
	return []persistedField{
		{"project", &s.project}, {"watermark", &s.watermark},
		{"records", &s.records}, {"current", &s.current}, {"closed", &s.closed},
		{"attempts", &s.attempts}, {"attemptOwner", &s.attemptOwner}, {"blockers", &s.blockers},
		{"invocations", &s.invocations}, {"criteria", &s.criteria}, {"reviews", &s.reviews},
		{"eventPackets", &s.eventPackets}, {"sources", &s.sources}, {"commands", &s.commands},
		{"events", &s.events},
		{"log.all", &s.log.all}, {"log.losses", &s.log.losses}, {"log.proofs", &s.log.proofs},
		{"log.decisionDisposals", &s.log.decisionDisposals}, {"log.supersessions", &s.log.supersessions},
		{"log.corrections", &s.log.corrections}, {"log.withdrawals", &s.log.withdrawals},
		{"log.disposals", &s.log.disposals},
		{"reverseRecord", &s.reverseRecord}, {"reverseCriterion", &s.reverseCriterion},
		{"reverseInvocation", &s.reverseInvocation}, {"reverseBlocker", &s.reverseBlocker},
	}
}

type persistedField struct {
	name string
	ptr  any
}

var transientFields = map[string]bool{"bundle": true, "proofRefusals": true}

// SnapshotVersion is what an image must match to be restored: the encoding,
// and a fingerprint of every type the persisted state reaches, so adding,
// removing, renaming or retyping any field refuses every older image. It does
// not cover a change to a reducer RULE that leaves the types alone; the store
// also binds an image to the binary that wrote it for that reason.
func SnapshotVersion() string { return snapshotVersion }

var snapshotVersion = snapshotFormat + "+" + shapeFingerprint(newState().persisted())

// shapeFingerprint hashes the names, kinds and fields of every type the listed
// fields reach, the encodable event types included.
func shapeFingerprint(fields []persistedField) string {
	var b strings.Builder
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		fmt.Fprintf(&b, "%s|%s|%s;", t.PkgPath(), t.Name(), t.Kind())
		if seen[t] {
			return
		}
		seen[t] = true
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(t.Elem())
		case reflect.Map:
			walk(t.Key())
			walk(t.Elem())
		case reflect.Struct:
			if t == codecTimeType {
				return
			}
			for i := 0; i < t.NumField(); i++ {
				fmt.Fprintf(&b, "%s:", t.Field(i).Name)
				walk(t.Field(i).Type)
			}
		case reflect.Interface:
			names := make([]string, 0, len(codecEvents))
			for name := range codecEvents {
				names = append(names, string(name))
			}
			sort.Strings(names)
			for _, name := range names {
				fmt.Fprintf(&b, "%s=", name)
				walk(codecEvents[model.EventType(name)])
			}
		}
	}
	for _, f := range fields {
		fmt.Fprintf(&b, "%s=", f.name)
		walk(reflect.TypeOf(f.ptr).Elem())
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// EncodeSnapshot writes the complete persisted state. One state always
// encodes to the same bytes. It refuses, rather than drops, anything the
// encoding cannot carry.
func EncodeSnapshot(s Snapshot) (data []byte, err error) {
	st := s.inner()
	if st.bundle != nil || st.proofRefusals != nil {
		return nil, errors.New("snapshot encode: a candidate's transient state is attached")
	}
	defer recoverCodec(&err)
	e := &encoder{}
	e.bytes([]byte(snapshotVersion))
	for _, f := range st.persisted() {
		e.value(reflect.ValueOf(f.ptr).Elem())
	}
	return e.buf, nil
}

// RestoreSnapshot decodes an image into state no one else holds, then folds
// tail through the same transitions Replay uses, in place: the state is
// exclusively owned, so it pays no copy per bundle. Any failure, in the image
// or in any tail bundle, returns the empty snapshot and never a partial one.
// The returned snapshot is immutable like every other.
func RestoreSnapshot(data []byte, tail []model.Bundle) (Snapshot, error) {
	st, err := decodeState(data)
	if err != nil {
		return Snapshot{}, err
	}
	for i := range tail {
		if err := st.apply(tail[i]); err != nil {
			return Snapshot{}, err
		}
	}
	return Snapshot{st: st}, nil
}

func decodeState(data []byte) (st *state, err error) {
	defer recoverCodec(&err)
	d := &decoder{data: data}
	if version := string(d.bytes()); version != snapshotVersion {
		return nil, fmt.Errorf("snapshot decode: image is %q, this build reads %q", version, snapshotVersion)
	}
	st = &state{}
	for _, f := range st.persisted() {
		d.value(reflect.ValueOf(f.ptr).Elem())
	}
	if d.pos != len(data) {
		return nil, fmt.Errorf("snapshot decode: %d trailing bytes", len(data)-d.pos)
	}
	// Every fold writes into these maps; a state without one is not a state.
	for _, f := range st.persisted() {
		if v := reflect.ValueOf(f.ptr).Elem(); v.Kind() == reflect.Map && v.IsNil() {
			return nil, fmt.Errorf("snapshot decode: %s is absent", f.name)
		}
	}
	return st, nil
}

// compareKeys orders map keys: strings, integers, and structs of them field by
// field. Every key type the state uses is one of these.
func compareKeys(a, b reflect.Value) int {
	switch a.Kind() {
	case reflect.String:
		switch x, y := a.String(), b.String(); {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch x, y := a.Int(), b.Int(); {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch x, y := a.Uint(), b.Uint(); {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if c := compareKeys(a.Field(i), b.Field(i)); c != 0 {
				return c
			}
		}
		return 0
	}
	codecFail("no ordering for map key %s", a.Type())
	return 0
}

func recoverCodec(err *error) {
	if r := recover(); r != nil {
		fault, ok := r.(codecFault)
		if !ok {
			panic(r)
		}
		*err = fmt.Errorf("snapshot codec: %w", fault.err)
	}
}
