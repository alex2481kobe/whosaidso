package model

// Strict packet and bundle decoding lives here.
// Deterministic encoding lives in encoding.go; shared field checks live in validation.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"
)

// ---- strict decoding -----------------------------------------------------

// envelopeKeys lists the exact JSON names each envelope accepts. Go's decoder
// matches case-insensitively, so "Version" lands in Version and neither
// DisallowUnknownFields nor a duplicate-key check notices. An alias that
// overwrites a field is indistinguishable from the real one downstream.
// Found by lane E.
var envelopeKeys = map[string]map[string]bool{
	"packet": {"version": true, "project": true, "command_id": true,
		"request_digest": true, "author": true, "captured_at": true, "events": true},
	"bundle": {"version": true, "project": true, "sequence": true, "command_id": true,
		"predecessor": true, "request_digest": true, "admitter": true,
		"recorded_at": true, "packets": true, "events": true},
}

func strictUnmarshal(b []byte, into any, what string) error {
	// The DECODER accepted invalid UTF-8 even once the encoder refused it: the
	// same collision, entering from the other side. Found by lane E.
	if !utf8.Valid(b) {
		return fault("invalid-json", what, "input contains invalid UTF-8")
	}
	// Three passes on purpose: the ordered parse catches duplicate keys and
	// trailing content; the exact-name check catches case aliases; the struct
	// decoder catches everything else.
	tree, err := parseOrdered(b)
	if err != nil {
		return err
	}
	if allowed, ok := envelopeKeys[what]; ok {
		top, isObject := tree.([]member)
		if !isObject {
			return fault("invalid-json", what, "top-level value is not an object")
		}
		for _, m := range top {
			if !allowed[m.key] {
				return fault("invalid-field", what+"."+m.key,
					"unknown field, or a case variant of a known one")
			}
			// The envelope check stopped at the top level, so a case alias
			// INSIDE an event still overwrote its type. Found by lane E.
			if m.key != "events" {
				continue
			}
			list, isArray := m.value.([]any)
			if !isArray {
				return fault("invalid-field", what+".events", "events is not an array")
			}
			for i, item := range list {
				obj, isObject := item.([]member)
				if !isObject {
					return fault("invalid-field", fmt.Sprintf("%s.events[%d]", what, i), "event is not an object")
				}
				for _, em := range obj {
					if !eventKeys[em.key] {
						return fault("invalid-field",
							fmt.Sprintf("%s.events[%d].%s", what, i, em.key),
							"unknown field, or a case variant of a known one")
					}
				}
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fault("invalid-field", what, err.Error())
	}
	return refuseNonUTC(reflect.ValueOf(into), what)
}

// ---- UTC-only timestamps (ruling R8.4) -------------------------------------

var timeType = reflect.TypeOf(time.Time{})

// refuseNonUTC is the decode half of R8.4: the wire carries only UTC. One
// instant spelled in two zones was two byte strings and two digests, and a
// decoded private location could be reassigned through Time.Location() in
// every snapshot at once. Like ValidID refusing lowercase rather than upcasing,
// decoding REFUSES any other offset (+00:00 included), so the stored bytes are
// exactly what decode returns. It reaches every nested timestamp, not only the
// envelope's own.
func refuseNonUTC(v reflect.Value, path string) error {
	return walkWire(v, path, 0, func(v reflect.Value, at string) error {
		if v.Type() == timeType && v.CanInterface() && v.Interface().(time.Time).Location() != time.UTC {
			return fault("invalid-field", at, "timestamp must be UTC, spelled Z (ruling R8.4)")
		}
		return nil
	})
}

// utcBytes is the encode half: it re-encodes raw, marshalled from a value of
// type t, with every timestamp normalised to UTC. It works on a private copy
// decoded from the bytes, so the caller's value is never rewritten. A timestamp
// it cannot set (inside a map value) stays as it was and refuseNonUTC then
// refuses it: loud, never silent.
func utcBytes(raw []byte, t reflect.Type) ([]byte, error) {
	private := reflect.New(t)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(private.Interface()); err != nil {
		return nil, fault("invalid-field", "event.data", err.Error())
	}
	_ = walkWire(private, "", 0, func(v reflect.Value, _ string) error {
		if v.Type() == timeType && v.CanSet() {
			v.Set(reflect.ValueOf(v.Interface().(time.Time).UTC()))
		}
		return nil
	})
	return json.Marshal(private.Elem().Interface())
}

// DecodePacket parses intake bytes, refusing anything it cannot fully account for.
func DecodePacket(b []byte) (Packet, error) {
	var p Packet
	if err := strictUnmarshal(b, &p, "packet"); err != nil {
		return Packet{}, err
	}
	if p.Version != WireVersion {
		return Packet{}, fault("unknown-version", "packet.version",
			fmt.Sprintf("wire version %d, expected %d", p.Version, WireVersion))
	}
	if p.Project == "" {
		return Packet{}, fault("invalid-field", "packet.project", "empty project id")
	}
	if !ValidID(p.CommandID) {
		return Packet{}, fault("invalid-field", "packet.command_id", "not a ULID")
	}
	if !ValidDigest(p.RequestDigest) {
		return Packet{}, fault("invalid-field", "packet.request_digest", "not lowercase sha-256 hex")
	}
	if f := validActor(p.Author, "packet.author"); f != nil {
		return Packet{}, f
	}
	if err := validEvents(p.Events, "packet.events"); err != nil {
		return Packet{}, err
	}
	return p, nil
}

// DecodeBundle parses ledger bytes. A bundle that fails here is never repaired
// by re-encoding it; admitted bytes are what they are.
func DecodeBundle(b []byte) (Bundle, error) {
	var bd Bundle
	if err := strictUnmarshal(b, &bd, "bundle"); err != nil {
		return Bundle{}, err
	}
	if bd.Version != WireVersion {
		return Bundle{}, fault("unknown-version", "bundle.version",
			fmt.Sprintf("wire version %d, expected %d", bd.Version, WireVersion))
	}
	if bd.Project == "" {
		return Bundle{}, fault("invalid-field", "bundle.project", "empty project id")
	}
	// An admitted sequence must have a writable name; keep both bounds owned
	// by BundleName rather than maintaining a second definition here.
	if _, err := BundleName(bd.Sequence, bd.CommandID); err != nil {
		return Bundle{}, err
	}
	// Only the genesis bundle may stand alone; every other one names its parent,
	// so a gap in the chain is detectable rather than invisible.
	if bd.Sequence == 1 {
		if bd.Predecessor != "" {
			return Bundle{}, fault("ledger-discontinuity", "bundle.predecessor", "sequence 1 cannot have a predecessor")
		}
	} else if !ValidID(bd.Predecessor) {
		return Bundle{}, fault("ledger-discontinuity", "bundle.predecessor", "missing or malformed predecessor")
	}
	if !ValidDigest(bd.RequestDigest) {
		return Bundle{}, fault("invalid-field", "bundle.request_digest", "not lowercase sha-256 hex")
	}
	if f := validActor(bd.Admitter, "bundle.admitter"); f != nil {
		return Bundle{}, f
	}
	for i, pr := range bd.Packets {
		at := fmt.Sprintf("bundle.packets[%d]", i)
		if !ValidID(pr.CommandID) {
			return Bundle{}, fault("invalid-field", at+".command_id", "not a ULID")
		}
		if !ValidDigest(pr.Digest) {
			return Bundle{}, fault("invalid-field", at+".digest", "not lowercase sha-256 hex")
		}
	}
	if err := validEvents(bd.Events, "bundle.events"); err != nil {
		return Bundle{}, err
	}
	return bd, nil
}

// eventKeys is the exact spelling an event object accepts. The envelope check
// stops at the top level, so a case alias INSIDE an event slipped through and
// overwrote its type. Found by lane E.
var eventKeys = map[string]bool{"type": true, "data": true}

func validEvents(events []Event, path string) error {
	if len(events) == 0 {
		return fault("invalid-field", path, "no events; an empty write is not a fact")
	}
	for i, e := range events {
		if e.Type == "" {
			return faultAt("unknown-event", path, i, "empty event type")
		}
		// Shape only. Whether this type is in the closed set, and whether its
		// fields make sense, is U02's job.
		trimmed := bytes.TrimSpace(e.Data)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return faultAt("invalid-field", path, i, "event data must be a JSON object")
		}
		if _, err := parseOrdered(trimmed); err != nil {
			return err
		}
	}
	return nil
}
