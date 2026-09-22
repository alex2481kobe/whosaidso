package model

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecodeBundleWritableSequence(t *testing.T) {
	const id ID = "01K5V8Q2220000000000000000"
	for _, tc := range []struct {
		sequence uint64
		bound    string
	}{
		{0, "starts at 1"}, {1, ""}, {99999999, ""},
		{100000000, "exceeds"}, {^uint64(0), "exceeds"},
	} {
		t.Run(fmt.Sprint(tc.sequence), func(t *testing.T) {
			pred := ""
			if tc.sequence > 1 {
				pred = `,"predecessor":"01K5V8Q1110000000000000000"`
			}
			raw := fmt.Sprintf(`{"version":1,"project":"p","sequence":%d,"command_id":%q,
				"request_digest":%q,"admitter":{"id":"a"},"recorded_at":"2026-09-22T00:00:00Z",
				"packets":[],"events":[{"type":"task.create","data":{}}]%s}`,
				tc.sequence, id, HashBytes([]byte("request")), pred)
			_, decodeErr := DecodeBundle([]byte(raw))
			_, nameErr := BundleName(tc.sequence, id)
			for site, err := range map[string]error{"decode": decodeErr, "name": nameErr} {
				if tc.bound == "" {
					if err != nil {
						t.Errorf("%s rejected writable sequence: %v", site, err)
					}
					continue
				}
				f, ok := err.(*Fault)
				if !ok || f.Code != "invalid-field" || f.Path != "bundle.sequence" || !strings.Contains(f.Detail, tc.bound) {
					t.Errorf("%s must identify crossed bound %q at bundle.sequence: %v", site, tc.bound, err)
				}
			}
		})
	}
}

// validPacketJSON builds a packet whose only defect is whatever the caller
// injects. Every refusal case below starts from bytes that are known to pass.
func validPacketJSON(inject string) string {
	d := string(HashBytes([]byte("authored input")))
	body := `"version":1,"project":"datum/datum",` +
		`"command_id":"01K5V8Q1110000000000000000","request_digest":"` + d + `",` +
		`"author":{"id":"lane-a"},"captured_at":"2026-09-22T01:02:03Z",` +
		`"events":[{"type":"task.create","data":{"intent":"x"}}]`
	if inject != "" {
		body = inject + "," + body
	}
	return "{" + body + "}"
}

// TestStrictDecodeRefuses is the negative half of the wire boundary. It opens
// with a GOOD CONTROL on purpose: a decoder that rejected everything would pass
// every refusal below while being completely broken, so the control is what
// makes the rest of the table mean anything.
func TestStrictDecodeRefuses(t *testing.T) {
	if _, err := DecodePacket([]byte(validPacketJSON(""))); err != nil {
		t.Fatalf("good control must pass, or every refusal here is meaningless: %v", err)
	}

	d := string(HashBytes([]byte("authored input")))
	cases := []struct {
		name string
		body string
		code string
	}{
		{"duplicate key", validPacketJSON(`"version":1`), "invalid-json"},
		{"unknown field", validPacketJSON(`"surprise":true`), "invalid-field"},
		{"trailing content", validPacketJSON("") + `{"more":1}`, "invalid-json"},
		{"wrong version", strings.Replace(validPacketJSON(""), `"version":1`, `"version":99`, 1), "unknown-version"},
		{"empty events", strings.Replace(validPacketJSON(""),
			`"events":[{"type":"task.create","data":{"intent":"x"}}]`, `"events":[]`, 1), "invalid-field"},
		{"empty event type", strings.Replace(validPacketJSON(""), `"type":"task.create"`, `"type":""`, 1), "unknown-event"},
		{"event data is not an object", strings.Replace(validPacketJSON(""),
			`"data":{"intent":"x"}`, `"data":[1,2]`, 1), "invalid-field"},
		{"actor is both known and unknown", strings.Replace(validPacketJSON(""),
			`"author":{"id":"lane-a"}`, `"author":{"id":"lane-a","unknown_reason":"r"}`, 1), "invalid-field"},
		{"actor is neither", strings.Replace(validPacketJSON(""),
			`"author":{"id":"lane-a"}`, `"author":{}`, 1), "invalid-field"},
		{"lowercase id", strings.Replace(validPacketJSON(""),
			`"01K5V8Q1110000000000000000"`, `"01k5v8q1110000000000000000"`, 1), "invalid-field"},
		{"uppercase digest", strings.Replace(validPacketJSON(""), d, strings.ToUpper(d), 1), "invalid-field"},
		{"empty project", strings.Replace(validPacketJSON(""), `"project":"datum/datum"`, `"project":""`, 1), "invalid-field"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodePacket([]byte(c.body))
			if err == nil {
				t.Fatalf("accepted what it must refuse")
			}
			f, ok := err.(*Fault)
			if !ok {
				t.Fatalf("refusal is not a Fault: %T %v", err, err)
			}
			if f.Code != c.code {
				t.Errorf("fault code\n  got  %s\n  want %s\n  (%v)", f.Code, c.code, err)
			}
		})
	}
}

// TestBundleChainRules: only the genesis bundle stands alone, so a gap in the
// chain is detectable rather than invisible.
// CLI inputs reach the same exact-key refusal as packets: an alias must not
// let encoding/json pick one of two authored meanings.
func TestStrictCallerInputDecoders(t *testing.T) {
	if _, err := DecodeEvents([]byte(`[{"type":"claim.assert","data":{}}]`)); err != nil {
		t.Fatalf("control event array: %v", err)
	}
	for _, in := range []string{`[{"type":"claim.revise","TYPE":"claim.assert","data":{}}]`, `[{"TYPE":"claim.revise","type":"claim.assert","data":{}}]`, `[{"type":"a","type":"b","data":{}}]`, `{"type":"a","data":{}}`, "[{\"type\":\"a-\xff\",\"data\":{}}]", `[] []`} {
		if _, err := DecodeEvents([]byte(in)); err == nil {
			t.Errorf("event array accepted: %s", in)
		}
	}
	ref := `[{"kind":"content","content":{"sha256":"` + string(HashBytes([]byte("x"))) + `","length":1,"media_type":"text/plain","locators":[{"path":"a"}]},"selector":{"kind":"json-pointer","pointer":"/a"}}]`
	if refs, err := DecodeArtifactRefs([]byte(ref), "refs"); err != nil || refs[0].Selector.Pointer != "/a" {
		t.Fatalf("control refs: %+v, %v", refs, err)
	}
	for _, in := range []string{strings.Replace(ref, `"pointer":"/a"`, `"pointer":"/a","POINTER":"/b"`, 1), strings.Replace(ref, `"pointer":"/a"`, `"POINTER":"/b","pointer":"/a"`, 1), strings.Replace(ref, `"length"`, `"LENGTH"`, 1), `null`, ref + ` []`} {
		if _, err := DecodeArtifactRefs([]byte(in), "refs"); err == nil {
			t.Errorf("artifact refs accepted: %s", in)
		}
	}
}

func TestBundleChainRules(t *testing.T) {
	d := string(HashBytes([]byte("admission")))
	mk := func(seq string, pred string) string {
		p := ""
		if pred != "" {
			p = `"predecessor":"` + pred + `",`
		}
		return `{"version":1,"project":"p","sequence":` + seq + `,` +
			`"command_id":"01K5V8Q2220000000000000000",` + p +
			`"request_digest":"` + d + `","admitter":{"id":"coordinator"},` +
			`"recorded_at":"2026-09-22T01:02:03Z","packets":[],` +
			`"events":[{"type":"task.create","data":{}}]}`
	}
	if _, err := DecodeBundle([]byte(mk("1", ""))); err != nil {
		t.Fatalf("genesis bundle with no predecessor must pass: %v", err)
	}
	if _, err := DecodeBundle([]byte(mk("2", "01K5V8Q1110000000000000000"))); err != nil {
		t.Fatalf("a later bundle naming its parent must pass: %v", err)
	}
	for name, body := range map[string]string{
		"genesis with a predecessor": mk("1", "01K5V8Q1110000000000000000"),
		"later with no predecessor":  mk("2", ""),
		"sequence zero":              mk("0", ""),
	} {
		if _, err := DecodeBundle([]byte(body)); err == nil {
			t.Errorf("accepted what it must refuse: %s", name)
		}
	}
}

// TestArtifactRefShape: the tag picks the pin, and a selector cannot contradict
// itself. U01 checks shape only; U07 verifies the actual bytes.
func TestArtifactRefShape(t *testing.T) {
	sha1Commit := strings.Repeat("a", 40)
	good := ArtifactRef{
		Kind:     "git",
		Git:      &GitPin{ObjectFormat: "sha1", Commit: sha1Commit, Path: "internal/model/wire.go"},
		Selector: Selector{Kind: "whole"},
	}
	if err := ValidateArtifactRef(good, "ref"); err != nil {
		t.Fatalf("good control must pass: %v", err)
	}
	// The empty JSON pointer is the document root and is legal.
	rooted := good
	rooted.Selector = Selector{Kind: "json-pointer", Pointer: ""}
	if err := ValidateArtifactRef(rooted, "ref"); err != nil {
		t.Errorf("empty json-pointer is the root and must be legal: %v", err)
	}

	bad := map[string]ArtifactRef{
		"git kind without a git pin": {Kind: "git", Selector: Selector{Kind: "whole"}},
		"content kind without one":   {Kind: "content", Selector: Selector{Kind: "whole"}},
		"unknown kind":               {Kind: "magic", Selector: Selector{Kind: "whole"}},
		"sha1 length under sha256":   {Kind: "git", Git: &GitPin{ObjectFormat: "sha256", Commit: sha1Commit, Path: "x"}, Selector: Selector{Kind: "whole"}},
		"unknown object format":      {Kind: "git", Git: &GitPin{ObjectFormat: "md5", Commit: sha1Commit, Path: "x"}, Selector: Selector{Kind: "whole"}},
		"whole with a pointer":       {Kind: "git", Git: good.Git, Selector: Selector{Kind: "json-pointer", Pointer: "/a"}},
		"unknown selector":           {Kind: "git", Git: good.Git, Selector: Selector{Kind: "regex"}},
	}
	// "whole with a pointer" needs the whole-kind spelling to be the defect:
	bad["whole with a pointer"] = ArtifactRef{Kind: "git", Git: good.Git, Selector: Selector{Kind: "whole", Pointer: "/a"}}

	for name, ref := range bad {
		if err := ValidateArtifactRef(ref, "ref"); err == nil {
			t.Errorf("accepted what it must refuse: %s", name)
		}
	}
}

// utcSeal is a sealed invocation whose two timestamps, one of them nested in a
// pointer, are the same instants spelled in zone.
func utcSeal(zone *time.Location) *InvocationSeal {
	seal := schemaEvents()[9].(*InvocationSeal)
	seal.Envelope.StartedAt = seal.Envelope.StartedAt.In(zone)
	seal.Envelope.ObservedAt = schemaKnown(seal.Envelope.StartedAt.Add(time.Second))
	return seal
}

// Ruling R8.4, encode half: an in-process timestamp in any zone is written as
// UTC, so one instant has one spelling, one byte string and one digest.
func TestEncodeWritesOneInstantAsOneSpelling(t *testing.T) {
	var first []byte
	for i, zone := range []*time.Location{time.UTC, time.FixedZone("plus", 37*60), time.FixedZone("minus", -5*3600)} {
		seal := utcSeal(zone)
		event, err := EncodeEvent(seal)
		if err != nil {
			t.Fatalf("%s: %v", zone, err)
		}
		if seal.Envelope.StartedAt.Location() != zone || seal.Envelope.ObservedAt.Value.Location() != zone {
			t.Errorf("%s: EncodeEvent rewrote the caller's value instead of a private copy", zone)
		}
		packet, err := Encode(Packet{Version: WireVersion, Project: schemaProject, CommandID: schemaID(20),
			RequestDigest: HashBytes([]byte("r")), Author: Actor{ID: "lane-a"},
			CapturedAt: seal.Envelope.StartedAt, Events: []Event{event}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = packet
		} else if !bytes.Equal(packet, first) || HashBytes(packet) != HashBytes(first) {
			t.Errorf("one instant in zone %s encoded differently:\n%s\nvs\n%s", zone, packet, first)
		}
	}
	for _, want := range []string{`"started_at": "2026-09-21T12:00:00Z"`, `"observed_at"`, `"2026-09-21T12:00:01Z"`} {
		if !bytes.Contains(first, []byte(want)) {
			t.Errorf("encoded packet lacks %s", want)
		}
	}
}

// Ruling R8.4, decode half: any other offset is refused, not normalised, at
// every depth, naming the field. Stored bytes are exactly what decode returns.
func TestDecodeRefusesANonUTCTimestampAtAnyDepth(t *testing.T) {
	event, err := EncodeEvent(utcSeal(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := DecodeEvent(event); err != nil ||
		decoded.(*InvocationSeal).Envelope.StartedAt.Location() != time.UTC {
		t.Fatalf("control: a UTC event must decode as UTC: %v", err)
	}
	packet := validPacketJSON("")
	if _, err := DecodePacket([]byte(packet)); err != nil {
		t.Fatalf("control: %v", err)
	}
	cases := []struct{ name, from, to, path string }{
		{"top-level +01:00", `"2026-09-22T01:02:03Z"`, `"2026-09-22T02:02:03+01:00"`, "packet.captured_at"},
		{"top-level +00:00", `"2026-09-22T01:02:03Z"`, `"2026-09-22T01:02:03+00:00"`, "packet.captured_at"},
		{"nested", `"started_at":"2026-09-21T12:00:00Z"`, `"started_at":"2026-09-21T12:37:00+00:37"`, "event.data.envelope.started_at"},
		{"nested in a pointer", `"value":"2026-09-21T12:00:01Z"`, `"value":"2026-09-21T07:00:01-05:00"`, "event.data.envelope.observed_at.value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			if strings.HasPrefix(c.path, "packet") {
				_, err = DecodePacket([]byte(strings.Replace(packet, c.from, c.to, 1)))
			} else {
				if !bytes.Contains(event.Data, []byte(c.from)) {
					t.Fatalf("fixture lacks %s: %s", c.from, event.Data)
				}
				_, err = DecodeEvent(Event{Type: event.Type, Data: bytes.Replace(event.Data, []byte(c.from), []byte(c.to), 1)})
			}
			if f, ok := err.(*Fault); !ok || f.Code != "invalid-field" || f.Path != c.path {
				t.Fatalf("want invalid-field at %s, got %v", c.path, err)
			}
		})
	}
}

// The refusal breaks no history: Datum's own committed ledger decodes whole.
func TestCommittedLedgerIsAlreadyUTC(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "record", "events", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("committed ledger not found: %v", err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := DecodeBundle(data)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for i, event := range bundle.Events {
			if _, err := DecodeEvent(event); err != nil {
				t.Errorf("%s event %d: %v", path, i, err)
			}
		}
	}
}
