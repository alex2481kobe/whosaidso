package model

import (
	"fmt"
	"strings"
	"testing"
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
