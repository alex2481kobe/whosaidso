package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixedEntropy is a deterministic stand-in for crypto/rand so an id is
// reproducible in a test. Production passes crypto/rand.Reader.
type fixedEntropy struct{ b byte }

func (f *fixedEntropy) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = f.b
	}
	return len(p), nil
}

var testClock = time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)

func mustID(t *testing.T, seed byte) ID {
	t.Helper()
	id, err := NewID(testClock, &fixedEntropy{seed})
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if !ValidID(id) {
		t.Fatalf("NewID produced an invalid id: %q", id)
	}
	return id
}

func samplePacket(t *testing.T, commandID ID) Packet {
	t.Helper()
	return Packet{
		Version:       WireVersion,
		Project:       "example/example",
		CommandID:     commandID,
		RequestDigest: HashBytes([]byte("authored input")),
		Author:        Actor{ID: "agent-a"},
		CapturedAt:    testClock,
		Events: []Event{
			{Type: "task.create", Data: json.RawMessage(`{"intent":"build the wire boundary"}`)},
			{Type: "source.intake", Data: json.RawMessage(`{"speaker":"owner","order":1}`)},
		},
	}
}

// TestWireRoundTripAndIdentity is the wire format's proving test: identity is stable and
// separable, encoding is deterministic, and a bundle names itself correctly.
func TestWireRoundTripAndIdentity(t *testing.T) {
	packetID := mustID(t, 0x11)
	admissionID := mustID(t, 0x22)

	if packetID == admissionID {
		t.Fatal("different entropy must produce different ids")
	}
	// A fixed clock and fixed entropy must reproduce the same id exactly.
	if again := mustID(t, 0x11); again != packetID {
		t.Fatalf("id is not reproducible: %q then %q", packetID, again)
	}

	p := samplePacket(t, packetID)
	encoded, err := Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.HasSuffix(encoded, []byte("\n")) {
		t.Error("encoded output must end with exactly one newline")
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket: %v", err)
	}
	reencoded, err := Encode(decoded)
	if err != nil {
		t.Fatalf("re-Encode: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Errorf("round trip is not byte-identical:\n--- first ---\n%s\n--- second ---\n%s", encoded, reencoded)
	}

	// Event ORDER carries meaning and must survive.
	if len(decoded.Events) != 2 ||
		decoded.Events[0].Type != "task.create" ||
		decoded.Events[1].Type != "source.intake" {
		t.Errorf("event order was not preserved: %+v", decoded.Events)
	}

	b := Bundle{
		Version:       WireVersion,
		Project:       p.Project,
		Sequence:      1,
		CommandID:     admissionID,
		RequestDigest: HashBytes([]byte("admission input")),
		Admitter:      Actor{ID: "coordinator"},
		RecordedAt:    testClock,
		Packets:       []PacketRef{{CommandID: packetID, Digest: HashBytes(encoded)}},
		Events:        p.Events,
	}
	bundleBytes, err := Encode(b)
	if err != nil {
		t.Fatalf("Encode bundle: %v", err)
	}
	gotBundle, err := DecodeBundle(bundleBytes)
	if err != nil {
		t.Fatalf("DecodeBundle: %v", err)
	}
	if gotBundle.Predecessor != "" {
		t.Errorf("genesis bundle must have no predecessor, got %q", gotBundle.Predecessor)
	}

	name, err := BundleName(1, admissionID)
	if err != nil {
		t.Fatalf("BundleName: %v", err)
	}
	want := "00000001-" + string(admissionID) + ".json"
	if name != want {
		t.Errorf("bundle filename\n  got  %s\n  want %s", name, want)
	}

	// The transaction id is the ADMISSION's. Changing it must not disturb the
	// packet's identity - that separation is what makes retries well defined.
	if gotBundle.Packets[0].CommandID != packetID {
		t.Error("admission id leaked into the packet id")
	}
	if gotBundle.CommandID == gotBundle.Packets[0].CommandID {
		t.Error("bundle and packet must not share one identity")
	}
}

// TestEncodeNormalizesKeyOrderNotArrayOrder: object key order is presentation
// and must normalize away; array order is data and must not.
func TestEncodeNormalizesKeyOrderNotArrayOrder(t *testing.T) {
	a := []byte(`{"beta":1,"alpha":{"z":true,"a":false},"list":[1,2,3]}`)
	b := []byte(`{"alpha":{"a":false,"z":true},"list":[1,2,3],"beta":1}`)

	ea, err := encodeRaw(a)
	if err != nil {
		t.Fatalf("encode a: %v", err)
	}
	eb, err := encodeRaw(b)
	if err != nil {
		t.Fatalf("encode b: %v", err)
	}
	if !bytes.Equal(ea, eb) {
		t.Errorf("reordered keys must normalize identically:\n%s\nvs\n%s", ea, eb)
	}

	reordered, err := encodeRaw([]byte(`{"beta":1,"alpha":{"z":true,"a":false},"list":[3,2,1]}`))
	if err != nil {
		t.Fatalf("encode reordered array: %v", err)
	}
	if bytes.Equal(ea, reordered) {
		t.Error("reordering an array changed nothing, but array order is data")
	}
}

// TestEncodePreservesNumericTokens: a number is written back exactly as it
// arrived, so a digest over it stays stable.
func TestEncodePreservesNumericTokens(t *testing.T) {
	out, err := encodeRaw([]byte(`{"big":12345678901234567890,"exact":1.10,"exp":1e3}`))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, token := range []string{"12345678901234567890", "1.10", "1e3"} {
		if !strings.Contains(string(out), token) {
			t.Errorf("numeric token %q was reformatted; got:\n%s", token, out)
		}
	}
}

func encodeRaw(b []byte) ([]byte, error) {
	var v json.RawMessage = b
	return Encode(v)
}

func TestSameActorNeverMatchesTwoUnknowns(t *testing.T) {
	unknown := Actor{UnknownReason: "no --actor and no WHOSAIDSO_ACTOR"}
	if SameActor(unknown, unknown) {
		t.Error("two unknown actors must never count as the same actor - that would fake self-admission")
	}
	if !SameActor(Actor{ID: "coordinator"}, Actor{ID: "coordinator"}) {
		t.Error("identical known ids are the same actor")
	}
	if SameActor(Actor{ID: "agent-a"}, Actor{ID: "agent-b"}) {
		t.Error("different ids are different actors")
	}
}

func TestBundleNameRefusesFormatLimit(t *testing.T) {
	id := mustID(t, 0x33)
	if _, err := BundleName(99999999, id); err != nil {
		t.Errorf("the largest eight-digit sequence must be accepted: %v", err)
	}
	// An ambiguous filename is worse than a refusal.
	if _, err := BundleName(100000000, id); err == nil {
		t.Error("a sequence past eight digits must refuse rather than truncate")
	}
	if _, err := BundleName(0, id); err == nil {
		t.Error("sequence 0 must refuse; numbering starts at 1")
	}
}
