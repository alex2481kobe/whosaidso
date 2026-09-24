package reduce

// The value encoding a persisted snapshot uses, walked by reflection over the
// state's own types. Which state fields are persisted, the version stamp and
// restoring lives in restore.go; nothing here knows a reducer rule.
//
// It is reflective for the reason copy.go gives: the hazard is the field
// someone adds next year. A hand-written encoder nobody updated would restore a
// state missing that field and answer from it. Reflection walks what the types
// actually hold, and the shape fingerprint in restore.go makes any change to
// those types refuse every image written before it.
//
// Nil and empty are distinct (a nil slice and an empty one render differently
// in JSON answers), maps are written in sorted key order so one state always
// encodes to the same bytes, and a time must be UTC and is restored as
// the same UTC instant.

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"sort"
	"time"

	"whosaidso/internal/model"
)

var codecTimeType = reflect.TypeOf(time.Time{})
var typedEventType = reflect.TypeOf((*model.TypedEvent)(nil)).Elem()

// codecEvents is the closed event set the encoding can name. An event type
// outside it refuses to encode, so a new event kind cannot be persisted as
// something else; the snapshot is simply not cached until it is added here.
var codecEvents = func() map[model.EventType]reflect.Type {
	out := map[model.EventType]reflect.Type{}
	for _, e := range []model.TypedEvent{
		&model.TaskCreate{}, &model.TaskAmend{}, &model.TaskStart{}, &model.TaskTakeover{},
		&model.AttemptTerminal{}, &model.TaskClose{}, &model.BlockerHold{}, &model.BlockerClear{},
		&model.InvocationStart{}, &model.InvocationSeal{}, &model.SourceIntake{}, &model.ClaimAssert{},
		&model.ClaimRevise{}, &model.CriterionFix{}, &model.ProofAdmit{}, &model.DecisionOpen{},
		&model.DecisionRevise{}, &model.DecisionDispose{}, &model.Supersede{}, &model.Correction{},
		&model.InstrumentDeclare{}, &model.InstrumentRevise{}, &model.TrustWithdraw{},
		&model.ReviewAdmit{}, &model.ArtifactDispose{},
	} {
		out[e.EventType()] = reflect.TypeOf(e).Elem()
	}
	return out
}()

// codecFault is the one panic the walkers raise; restore and encode recover
// it, and only it, into an error.
type codecFault struct{ err error }

func codecFail(format string, args ...any) { panic(codecFault{fmt.Errorf(format, args...)}) }

type encoder struct{ buf []byte }

func (e *encoder) uint(n uint64) { e.buf = binary.AppendUvarint(e.buf, n) }
func (e *encoder) int(n int64)   { e.buf = binary.AppendVarint(e.buf, n) }
func (e *encoder) bytes(b []byte) {
	e.uint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

// value writes v. Nil pointers, slices, maps and interfaces are 0; a present
// one is 1, or its length plus one.
func (e *encoder) value(v reflect.Value) {
	t := v.Type()
	if t == codecTimeType {
		at := v.Interface().(time.Time)
		if at.Location() != time.UTC {
			codecFail("reduced state holds a non-UTC time %s", at)
		}
		e.int(at.Unix())
		e.uint(uint64(at.Nanosecond()))
		return
	}
	switch t.Kind() {
	case reflect.String:
		e.uint(uint64(v.Len()))
		e.buf = append(e.buf, v.String()...)
	case reflect.Bool:
		if v.Bool() {
			e.uint(1)
		} else {
			e.uint(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.int(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.uint(v.Uint())
	case reflect.Pointer:
		if v.IsNil() {
			e.uint(0)
			return
		}
		e.uint(1)
		e.value(v.Elem())
	case reflect.Interface:
		if t != typedEventType {
			codecFail("no encoding for interface %s", t)
		}
		if v.IsNil() {
			e.uint(0)
			return
		}
		event := v.Interface().(model.TypedEvent)
		concrete, ok := codecEvents[event.EventType()]
		if !ok || v.Elem().Kind() != reflect.Pointer || v.Elem().Type().Elem() != concrete || v.Elem().IsNil() {
			codecFail("event %T is outside the encodable set", event)
		}
		e.uint(1)
		e.bytes([]byte(event.EventType()))
		e.value(v.Elem().Elem())
	case reflect.Slice:
		if v.IsNil() {
			e.uint(0)
			return
		}
		e.uint(uint64(v.Len()) + 1)
		if t.Elem().Kind() == reflect.Uint8 {
			e.buf = append(e.buf, v.Bytes()...)
			return
		}
		for i := 0; i < v.Len(); i++ {
			e.value(v.Index(i))
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			e.value(v.Index(i))
		}
	case reflect.Map:
		if v.IsNil() {
			e.uint(0)
			return
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return compareKeys(keys[i], keys[j]) < 0 })
		e.uint(uint64(len(keys)) + 1)
		for _, k := range keys {
			e.value(k)
			e.value(v.MapIndex(k))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			e.value(v.Field(i))
		}
	default:
		codecFail("no encoding for %s", t)
	}
}

type decoder struct {
	data []byte
	pos  int
}

func (d *decoder) uint() uint64 {
	n, size := binary.Uvarint(d.data[d.pos:])
	if size <= 0 {
		codecFail("truncated or overlong integer at byte %d", d.pos)
	}
	d.pos += size
	return n
}

func (d *decoder) int() int64 {
	n, size := binary.Varint(d.data[d.pos:])
	if size <= 0 {
		codecFail("truncated or overlong integer at byte %d", d.pos)
	}
	d.pos += size
	return n
}

// length reads a count and refuses one the remaining input cannot hold, so a
// damaged image cannot ask for an enormous allocation.
func (d *decoder) length(n uint64) int {
	if n > uint64(len(d.data)-d.pos) {
		codecFail("length %d exceeds the %d bytes left", n, len(d.data)-d.pos)
	}
	return int(n)
}

func (d *decoder) bytes() []byte {
	n := d.length(d.uint())
	out := d.data[d.pos : d.pos+n]
	d.pos += n
	return out
}

func (d *decoder) value(v reflect.Value) {
	t := v.Type()
	if t == codecTimeType {
		sec, nsec := d.int(), d.uint()
		if nsec >= 1e9 {
			codecFail("nanoseconds %d out of range", nsec)
		}
		v.Set(reflect.ValueOf(time.Unix(sec, int64(nsec)).UTC()))
		return
	}
	switch t.Kind() {
	case reflect.String:
		v.SetString(string(d.bytes()))
	case reflect.Bool:
		switch d.uint() {
		case 0:
			v.SetBool(false)
		case 1:
			v.SetBool(true)
		default:
			codecFail("invalid bool")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := d.int()
		if v.OverflowInt(n) {
			codecFail("%d overflows %s", n, t)
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := d.uint()
		if v.OverflowUint(n) {
			codecFail("%d overflows %s", n, t)
		}
		v.SetUint(n)
	case reflect.Pointer:
		switch d.uint() {
		case 0:
			return
		case 1:
			v.Set(reflect.New(t.Elem()))
			d.value(v.Elem())
		default:
			codecFail("invalid pointer tag")
		}
	case reflect.Interface:
		if t != typedEventType {
			codecFail("no decoding for interface %s", t)
		}
		switch d.uint() {
		case 0:
			return
		case 1:
		default:
			codecFail("invalid event tag")
		}
		name := model.EventType(d.bytes())
		concrete, ok := codecEvents[name]
		if !ok {
			codecFail("unknown event type %q", name)
		}
		event := reflect.New(concrete)
		d.value(event.Elem())
		v.Set(event)
	case reflect.Slice:
		n := d.uint()
		if n == 0 {
			return
		}
		count := d.length(n - 1)
		if t.Elem().Kind() == reflect.Uint8 {
			v.SetBytes(append(make([]byte, 0, count), d.data[d.pos:d.pos+count]...))
			d.pos += count
			return
		}
		v.Set(reflect.MakeSlice(t, count, count))
		for i := 0; i < count; i++ {
			d.value(v.Index(i))
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			d.value(v.Index(i))
		}
	case reflect.Map:
		n := d.uint()
		if n == 0 {
			return
		}
		count := d.length(n - 1)
		v.Set(reflect.MakeMapWithSize(t, count))
		for i := 0; i < count; i++ {
			k, val := reflect.New(t.Key()).Elem(), reflect.New(t.Elem()).Elem()
			d.value(k)
			d.value(val)
			if v.MapIndex(k).IsValid() {
				codecFail("duplicate map key in %s", t)
			}
			v.SetMapIndex(k, val)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			d.value(v.Field(i))
		}
	default:
		codecFail("no decoding for %s", t)
	}
}
