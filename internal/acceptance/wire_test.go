// Lane E's independent U01 attacks. This is the only Go file owned by this
// lane; helpers stay here to preserve the ownership boundary. Event semantics,
// resolved git/content agreement (U07), and ledger history (U04) are not U01.
package acceptance_test

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"whosaidso/internal/model"
)

const wirePacketID = "01K5V8Q1110000000000000000"
const wireAdmissionID = "01K5V8Q2220000000000000000"
const wireDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Handwritten controls do not depend on Encode agreeing with Decode.
const wirePacket = `{
  "version":1,"project":"datum/acceptance",
  "command_id":"` + wirePacketID + `","request_digest":"` + wireDigest + `",
  "author":{"id":"lane-e"},"captured_at":"2026-09-21T12:34:56Z",
  "events":[{"type":"task.create","data":{}}]
}`
const wireBundle = `{
  "version":1,"project":"datum/acceptance","sequence":1,
  "command_id":"` + wireAdmissionID + `","request_digest":"` + wireDigest + `",
  "admitter":{"id":"coordinator"},"recorded_at":"2026-09-21T12:34:56Z",
  "packets":[{"command_id":"` + wirePacketID + `","digest":"` + wireDigest + `"}],
  "events":[{"type":"task.create","data":{}}]
}`

func wireDecode(b []byte, bundle bool) error {
	if bundle {
		_, err := model.DecodeBundle(b)
		return err
	}
	_, err := model.DecodePacket(b)
	return err
}

func wireSource(b []byte) (model.ArtifactRef, error) {
	p, err := model.DecodePacket(b)
	if err != nil {
		return model.ArtifactRef{}, err
	}
	if len(p.Events) != 1 {
		return model.ArtifactRef{}, fmt.Errorf("fixture needs one event, got %d", len(p.Events))
	}
	var data struct {
		Source model.ArtifactRef `json:"source"`
	}
	err = json.Unmarshal(p.Events[0].Data, &data)
	return data.Source, err
}

func TestWireGoodPacketAndBundlePreserveIndependentIdentitiesAndEventOrder(t *testing.T) {
	p, err := model.DecodePacket([]byte(wirePacket))
	if err != nil {
		t.Fatalf("handwritten valid packet refused: %v", err)
	}
	p.Events = append(p.Events, model.Event{Type: "source.intake", Data: json.RawMessage(`{"order":2,"source":{"kind":"content","content":{"sha256":"` + wireDigest + `","length":2,"media_type":"application/json","locators":[{"path":"evidence.json"}]},"selector":{"kind":"json-pointer","pointer":""}}}`)})
	encoded, err := model.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := model.DecodePacket(encoded)
	if err != nil {
		t.Fatalf("encoded good packet refused: %v", err)
	}
	if got.CommandID != wirePacketID || got.Author != p.Author || got.RequestDigest != p.RequestDigest || got.Project != p.Project || !got.CapturedAt.Equal(p.CapturedAt) {
		t.Fatalf("good packet identity/metadata changed: %+v", got)
	}
	if len(got.Events) != len(p.Events) {
		t.Fatalf("ordered event count changed: %+v", got.Events)
	}
	for i, event := range got.Events {
		var actual, expected any
		if err := json.Unmarshal(event.Data, &actual); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(p.Events[i].Data, &expected); err != nil {
			t.Fatal(err)
		}
		if event.Type != p.Events[i].Type || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("event %d type/payload changed: %+v", i, event)
		}
	}
	again, err := model.Encode(got)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("valid packet round trip changed exact bytes: %v\n%s\n%s", err, encoded, again)
	}
	reversed := got
	reversed.Events = []model.Event{got.Events[1], got.Events[0]}
	reordered, err := model.Encode(reversed)
	if err != nil || bytes.Equal(encoded, reordered) || model.HashBytes(encoded) == model.HashBytes(reordered) {
		t.Fatalf("reversing packet events erased their causal order: %v", err)
	}
	b, err := model.DecodeBundle([]byte(wireBundle))
	if err != nil {
		t.Fatalf("handwritten valid genesis bundle refused: %v", err)
	}
	b.Packets[0].Digest, b.Events = model.HashBytes(encoded), got.Events
	for _, admission := range []model.ID{wireAdmissionID, "01K5V8Q3330000000000000000"} {
		b.CommandID = admission
		bb, err := model.Encode(b)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := model.DecodeBundle(bb)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.CommandID != admission || decoded.Packets[0].CommandID != wirePacketID || decoded.Packets[0].Digest != model.HashBytes(encoded) || decoded.Predecessor != "" {
			t.Fatalf("admission identity changed packet binding or genesis: %+v", decoded)
		}
		if !reflect.DeepEqual(decoded.Events, got.Events) {
			t.Fatalf("bundle changed the packet's ordered events: %+v", decoded.Events)
		}
		roundTrip, err := model.Encode(decoded)
		if err != nil || !bytes.Equal(bb, roundTrip) {
			t.Fatalf("valid bundle round trip changed exact bytes: %v", err)
		}
	}
}

func TestWireDefectFixturesHaveAcceptedSingleRepairControls(t *testing.T) {
	cases := []struct{ name, defect, repair, code string }{
		{"duplicate-key", `"k": 1, "\u006b": 2`, `"k": 1`, "invalid-json"},
		{"unknown-field", `, "surprise": true`, "", "invalid-field"},
		{"unknown-version", `"version": 2`, `"version": 1`, "unknown-version"},
		{"unknown-actor", `, "unknown_reason": "not supplied"`, "", "invalid-field"},
		{"git-content-confusion", `"kind": "git"`, `"kind": "content"`, "invalid-field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad, err := os.ReadFile(filepath.Join("testdata", "wire", tc.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(bad), tc.defect) != 1 {
				t.Fatalf("single-defect repair anchor is not unique: %q", tc.defect)
			}
			good := bytes.Replace(bad, []byte(tc.defect), []byte(tc.repair), 1)
			check := func(b []byte) error { return wireDecode(b, false) }
			if tc.name == "git-content-confusion" {
				// Data is opaque at U01: explicitly exercise the exported ref validator.
				check = func(b []byte) error {
					ref, err := wireSource(b)
					if err != nil {
						return err
					}
					return model.ValidateArtifactRef(ref, "events[0].data.source")
				}
			}
			if err := check(good); err != nil {
				t.Fatalf("single-repair good control refused; negative result is meaningless: %v", err)
			}
			err = check(bad)
			var fault *model.Fault
			if !errors.As(err, &fault) || fault.Code != tc.code {
				t.Fatalf("defective fixture must yield %s Fault; got %v", tc.code, err)
			}
		})
	}
}

func TestWireStrictDecodersRejectSingleDefectsAtEveryDepth(t *testing.T) {
	cases := []struct{ name, old, new string }{
		{"deep_duplicate_through_array_and_escaped_key", `"data":{}`, `"data":{"a":[{"b":{"k":1,"\u006b":2}}]}`},
		{"deep_duplicate_empty_key", `"data":{}`, `"data":{"a":[{"b":{"":1,"":2}}]}`},
		{"unknown_envelope_field", `"version":1`, `"version":1,"surprise":true`},
		{"unknown_case_variant_field", `"version":1`, `"VERSION":1`},
		{"case_alias_overwrites_same_envelope_field", `"project":"datum/acceptance"`, `"project":"datum/acceptance","PROJECT":"impostor"`},
		{"case_alias_overwrites_event_type", `"type":"task.create"`, `"type":"task.create","TYPE":"source.intake"`},
		{"unknown_event_envelope_field", `"type":"task.create"`, `"type":"task.create","surprise":true`},
		{"unknown_version", `"version":1`, `"version":2`},
		{"empty_event_list", `[{"type":"task.create","data":{}}]`, `[]`},
		{"empty_event_type", `"type":"task.create"`, `"type":""`},
		{"data_is_array", `"data":{}`, `"data":[]`},
		{"data_is_null", `"data":{}`, `"data":null`},
		{"data_is_string", `"data":{}`, `"data":"{}"`},
		{"data_is_number", `"data":{}`, `"data":1`},
		{"data_is_boolean", `"data":{}`, `"data":true`},
		{"lowercase_ulid", `01K5V8Q`, `01k5v8q`},
		{"uppercase_digest", wireDigest, strings.ToUpper(wireDigest)},
		{"nonhex_digest", wireDigest, strings.Repeat("g", 64)},
		{"short_digest", wireDigest, wireDigest[1:]},
	}
	for _, bundle := range []bool{false, true} {
		kind, good := "packet", wirePacket
		if bundle {
			kind, good = "bundle", wireBundle
		}
		t.Run(kind, func(t *testing.T) {
			if err := wireDecode([]byte(good), bundle); err != nil {
				t.Fatalf("good control refused: %v", err)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					bad := strings.Replace(good, tc.old, tc.new, 1)
					if bad == good {
						t.Fatal("attack did not change the good control")
					}
					if err := wireDecode([]byte(bad), bundle); err == nil {
						t.Fatalf("strict decoder accepted %s: %s", tc.name, bad)
					}
				})
			}
			for _, suffix := range []string{`}`, `]`, `} {}`, `{} `, `null`, `garbage`} {
				t.Run("trailing_"+suffix, func(t *testing.T) {
					if err := wireDecode([]byte(good+suffix), bundle); err == nil {
						t.Fatalf("strict decoder accepted trailing bytes %q after a valid %s", suffix, kind)
					}
				})
			}
			if err := wireDecode([]byte(good+" \t\r\n"), bundle); err != nil {
				t.Fatalf("legal trailing whitespace refused: %v", err)
			}
		})
	}
}

func TestWireActorUnionAndKnownUnknownComparisonCannotInventIdentity(t *testing.T) {
	known := model.Actor{ID: "lane-e"}
	unknown := model.Actor{UnknownReason: "not supplied"}
	for _, tc := range []struct {
		name string
		a, b model.Actor
		want bool
	}{
		{"known_same", known, known, true},
		{"known_different", known, model.Actor{ID: "coordinator"}, false},
		{"unknown_known", unknown, known, false},
		{"known_unknown", known, unknown, false},
		{"unknown_reason_equal_to_known_id", model.Actor{UnknownReason: known.ID}, known, false},
		{"unknown_unknown", unknown, unknown, false},
		{"both_branches_cannot_be_known", model.Actor{ID: known.ID, UnknownReason: "not supplied"}, known, false},
		{"blank_ids_cannot_be_known", model.Actor{ID: " \t"}, model.Actor{ID: " \t"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.SameActor(tc.a, tc.b); got != tc.want {
				t.Errorf("SameActor(%+v, %+v) = %v; want %v", tc.a, tc.b, got, tc.want)
			}
			if got := model.SameActor(tc.b, tc.a); got != tc.want {
				t.Errorf("reversed SameActor = %v; want %v", got, tc.want)
			}
		})
	}
	for _, bundle := range []bool{false, true} {
		kind, input, old := "packet", wirePacket, `{"id":"lane-e"}`
		if bundle {
			kind, input, old = "bundle", wireBundle, `{"id":"coordinator"}`
		}
		for _, actor := range []string{`{"id":"named"}`, `{"unknown_reason":"not supplied"}`} {
			if err := wireDecode([]byte(strings.Replace(input, old, actor, 1)), bundle); err != nil {
				t.Fatalf("valid %s actor %s refused: %v", kind, actor, err)
			}
		}
		for _, actor := range []string{`{}`, `{"id":"named","unknown_reason":"missing"}`, `{"id":" \t"}`, `{"unknown_reason":"\u2003"}`} {
			t.Run(kind+"_refuses_"+actor, func(t *testing.T) {
				if err := wireDecode([]byte(strings.Replace(input, old, actor, 1)), bundle); err == nil {
					t.Errorf("%s accepted actor without exactly one nonblank branch: %s", kind, actor)
				}
			})
		}
	}
}

func TestWireEncodeSortsUnicodeEscapesAndEmptyKeysRecursively(t *testing.T) {
	a := json.RawMessage(`{"雪":{"z":0,"":1},"é":2,"e\u0301":3,"a\"":4,"":5,"array":[{"z":1,"a":2},3]}`)
	b := json.RawMessage(`{"array":[{"a":2,"z":1},3],"":5,"a\u0022":4,"é":3,"\u00e9":2,"\u96ea":{"":1,"z":0}}`)
	want := "{\n  \"\": 5,\n  \"a\\\"\": 4,\n  \"array\": [\n    {\n      \"a\": 2,\n      \"z\": 1\n    },\n    3\n  ],\n  \"é\": 3,\n  \"é\": 2,\n  \"雪\": {\n    \"\": 1,\n    \"z\": 0\n  }\n}\n"
	for _, input := range []json.RawMessage{a, b} {
		out, err := model.Encode(input)
		if err != nil || string(out) != want || !utf8.Valid(out) || !json.Valid(out) {
			t.Fatalf("recursive sort/JSON/indent/newline mismatch: %v\nwant %s\ngot %s", err, want, out)
		}
	}
}

func TestWireEncodeControlCharactersRemainValidJSONAndRoundTrip(t *testing.T) {
	for r := rune(0); r <= 0x7f; r++ {
		if r > 0x1f && r != 0x7f {
			continue
		}
		t.Run(fmt.Sprintf("U+%04X_in_key_and_value", r), func(t *testing.T) {
			value := map[string]string{string(r): "value" + string(r)}
			out, err := model.Encode(value)
			if err != nil {
				t.Fatalf("valid Unicode string refused: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("Encode emitted non-JSON escapes for U+%04X: %q (%v)", r, out, err)
			}
			if !reflect.DeepEqual(got, value) {
				t.Fatalf("Encode changed string/key bytes: got %#v; want %#v", got, value)
			}
		})
	}
}

func TestWireEncodeSameInstantUsesUTCAndOneDigest(t *testing.T) {
	instant := time.Date(2026, 9, 21, 12, 34, 56, 123456789, time.UTC)
	for _, bundle := range []bool{false, true} {
		var baseline []byte
		for _, offset := range []int{0, -5 * 3600, 5*3600 + 1800} {
			kind := "packet"
			var value any
			if bundle {
				b, err := model.DecodeBundle([]byte(wireBundle))
				if err != nil {
					t.Fatal(err)
				}
				kind, b.RecordedAt = "bundle", instant.In(time.FixedZone("capture", offset))
				value = b
			} else {
				p, err := model.DecodePacket([]byte(wirePacket))
				if err != nil {
					t.Fatal(err)
				}
				p.CapturedAt = instant.In(time.FixedZone("capture", offset))
				value = p
			}
			out, err := model.Encode(value)
			if err != nil {
				t.Fatal(err)
			}
			if offset == 0 {
				baseline = out
			}
			if !bytes.Equal(out, baseline) || !bytes.Contains(out, []byte(instant.Format(time.RFC3339Nano))) {
				t.Errorf("same %s instant at offset %d encoded differently instead of UTC: UTC digest=%s offset digest=%s\n%s", kind, offset, model.HashBytes(baseline), model.HashBytes(out), out)
			}
		}
	}
}

func TestWireNumericTokensAndArrayOrderSurviveWithoutDigestCollisions(t *testing.T) {
	seen := make(map[model.Digest]string)
	for _, token := range []string{"1.10", "1.1", "1e3", "1E+3", "1000", "9007199254740992", "9007199254740993", "1234567890123456789012345678901234567890", "-0", "0", "1e400"} {
		t.Run(token, func(t *testing.T) {
			input := strings.Replace(wirePacket, `"data":{}`, `"data":{"n":`+token+`}`, 1)
			p, err := model.DecodePacket([]byte(input))
			if err != nil {
				t.Fatalf("valid number token refused: %v", err)
			}
			out, err := model.Encode(p)
			if err != nil || !bytes.Contains(out, []byte(`"n": `+token+"\n")) {
				t.Fatalf("number token %q changed: %v\n%s", token, err, out)
			}
			digest := model.HashBytes(out)
			if prior, ok := seen[digest]; ok {
				t.Errorf("distinct numeric spellings %s and %s produced one digest %s", prior, token, digest)
			}
			seen[digest] = token
			direct, err := model.Encode(map[string]json.Number{"n": json.Number(token)})
			if err != nil || string(direct) != "{\n  \"n\": "+token+"\n}\n" {
				t.Fatalf("json.Number(%q) changed token: %v; %s", token, err, direct)
			}
			round, err := model.DecodePacket(out)
			if err != nil {
				t.Fatal(err)
			}
			again, err := model.Encode(round)
			if err != nil || !bytes.Equal(out, again) {
				t.Fatalf("number token round trip unstable: %v", err)
			}
		})
	}
	for _, pair := range [][2]string{
		{`[{"x":1},{"x":2}]`, `[{"x":2},{"x":1}]`},
		{`[1.10,1e3,-0]`, `[-0,1e3,1.10]`},
	} {
		a, errA := model.Encode(json.RawMessage(pair[0]))
		b, errB := model.Encode(json.RawMessage(pair[1]))
		if errA != nil || errB != nil || bytes.Equal(a, b) || model.HashBytes(a) == model.HashBytes(b) {
			t.Fatalf("array order was refused or erased: %s vs %s (%v, %v)", a, b, errA, errB)
		}
	}
}

func TestWireInvalidUTF8CannotSilentlyBecomeReplacementCharacterAndOneDigest(t *testing.T) {
	good := strings.Replace(wirePacket, `"data":{}`, `"data":{"text":"�"}`, 1)
	if _, err := model.DecodePacket([]byte(good)); err != nil {
		t.Fatalf("real replacement character is valid Unicode: %v", err)
	}
	for _, invalid := range []string{string([]byte{0xff}), string([]byte{0xc0, 0xaf})} {
		bad := strings.Replace(good, "�", invalid, 1)
		if _, err := model.DecodePacket([]byte(bad)); err == nil {
			t.Errorf("strict decoder accepted invalid UTF-8 bytes %x", []byte(invalid))
		}
		out, err := model.Encode(json.RawMessage(bad))
		if err == nil {
			t.Errorf("Encode silently rewrote invalid UTF-8 %x instead of refusing, digest=%s: %s", []byte(invalid), model.HashBytes(out), out)
		}
	}
	first, firstErr := model.Encode(json.RawMessage(strings.Replace(good, "�", string([]byte{0xff}), 1)))
	second, secondErr := model.Encode(json.RawMessage(good))
	if firstErr == nil && secondErr == nil && bytes.Equal(first, second) {
		t.Error("invalid byte FF and valid U+FFFD collapsed to the same encoded bytes and digest")
	}
}

func TestWireULIDUsesAll128BitsAndRejects130BitSpellings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ms      int64
		entropy byte
		want    model.ID
	}{
		{"minimum", 0, 0, "00000000000000000000000000"},
		{"one_millisecond", 1, 0, "00000000010000000000000000"},
		{"maximum", (1 << 48) - 1, 0xff, "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := model.NewID(time.UnixMilli(tc.ms), bytes.NewReader(bytes.Repeat([]byte{tc.entropy}, 10)))
			if err != nil || got != tc.want || !model.ValidID(got) {
				t.Fatalf("ULID vector: got %q (%v); want %q", got, err, tc.want)
			}
		})
	}
	for _, ms := range []int64{-1, 1 << 48} {
		if id, err := model.NewID(time.UnixMilli(ms), bytes.NewReader(make([]byte, 10))); err == nil {
			t.Errorf("out-of-range milliseconds %d minted %q", ms, id)
		}
	}
	if id, err := model.NewID(time.UnixMilli(0), bytes.NewReader(make([]byte, 9))); err == nil {
		t.Errorf("short entropy minted %q", id)
	}
	for _, id := range []model.ID{"80000000000000000000000000", "ZZZZZZZZZZZZZZZZZZZZZZZZZZ", "01K5V8Q111000000000000000I", "01k5v8q1110000000000000000"} {
		t.Run("refuse_"+string(id), func(t *testing.T) {
			if model.ValidID(id) {
				t.Errorf("ValidID accepted malformed/overflow ULID %q (128-bit maximum starts with 7)", id)
			}
			if _, err := model.BundleName(1, id); err == nil {
				t.Errorf("BundleName accepted malformed/overflow ULID %q", id)
			}
			for _, bundle := range []bool{false, true} {
				input, old := wirePacket, wirePacketID
				if bundle {
					input, old = wireBundle, wireAdmissionID
				}
				if err := wireDecode([]byte(strings.Replace(input, old, string(id), 1)), bundle); err == nil {
					t.Errorf("decoder (bundle=%v) accepted malformed/overflow ULID %q", bundle, id)
				}
			}
		})
	}
}

func TestWireBundlePredecessorShapeAndEightDigitFilenameBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, seq, predecessor string
		valid                  bool
	}{
		{"genesis_without_parent", "1", "", true},
		{"second_with_parent", "2", wireAdmissionID, true},
		{"genesis_with_well_formed_but_wrong_parent", "1", wirePacketID, false},
		{"later_without_parent", "2", "", false},
		{"sequence_zero", "0", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(wireBundle, `"sequence":1`, `"sequence":`+tc.seq+`,"predecessor":"`+tc.predecessor+`"`, 1)
			input = strings.Replace(input, wireAdmissionID, "01K5V8Q3330000000000000000", 1)
			if err := wireDecode([]byte(input), true); (err == nil) != tc.valid {
				t.Fatalf("sequence %s predecessor %q: valid=%v; got %v", tc.seq, tc.predecessor, tc.valid, err)
			}
		})
	}
	for _, seq := range []uint64{1, 99999999} {
		name, err := model.BundleName(seq, wireAdmissionID)
		if want := fmt.Sprintf("%08d-%s.json", seq, wireAdmissionID); err != nil || name != want {
			t.Errorf("filename got %q (%v); want %q", name, err, want)
		}
	}
	for _, seq := range []uint64{0, 100000000, ^uint64(0)} {
		if name, err := model.BundleName(seq, wireAdmissionID); err == nil {
			t.Errorf("sequence %d escaped eight-digit range as %q", seq, name)
		}
	}
}

func TestWireArtifactKindRequiresItsOwnPinAndValidObjectID(t *testing.T) {
	git := &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "evidence.json"}
	content := &model.ContentPin{SHA256: wireDigest, Length: 3, MediaType: "application/json", Locators: []model.Locator{{Path: "evidence.json"}}}
	for _, tc := range []struct {
		name  string
		ref   model.ArtifactRef
		valid bool
	}{
		{"git_control", model.ArtifactRef{Kind: "git", Git: git, Selector: model.Selector{Kind: "whole"}}, true},
		{"content_control", model.ArtifactRef{Kind: "content", Content: content, Selector: model.Selector{Kind: "whole"}}, true},
		{"empty_pointer_is_root", model.ArtifactRef{Kind: "git", Git: git, Selector: model.Selector{Kind: "json-pointer"}}, true},
		{"git_cannot_borrow_content_pin", model.ArtifactRef{Kind: "git", Content: content, Selector: model.Selector{Kind: "whole"}}, false},
		{"content_cannot_borrow_git_pin", model.ArtifactRef{Kind: "content", Git: git, Selector: model.Selector{Kind: "whole"}}, false},
		{"whole_cannot_select_pointer", model.ArtifactRef{Kind: "git", Git: git, Selector: model.Selector{Kind: "whole", Pointer: "/x"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := model.ValidateArtifactRef(tc.ref, "source"); (err == nil) != tc.valid {
				t.Fatalf("artifact valid=%v, got %v: %+v", tc.valid, err, tc.ref)
			}
		})
	}
	for _, format := range []string{"sha1", "sha256"} {
		length := 40
		if format == "sha256" {
			length = 64
		}
		for _, char := range []string{"a", "g"} {
			t.Run(format+"_object_id_"+char, func(t *testing.T) {
				pin := &model.GitPin{ObjectFormat: format, Commit: strings.Repeat(char, length), Path: git.Path}
				ref := model.ArtifactRef{Kind: "git", Git: pin, Selector: model.Selector{Kind: "whole"}}
				if err := model.ValidateArtifactRef(ref, "source"); (err == nil) != (char == "a") {
					t.Errorf("%s full-length %q object id: expected hex validation, got %v", format, char, err)
				}
			})
		}
	}
}

func TestWireDisagreeingCorroborationRemainsVisibleForU07(t *testing.T) {
	// Construct an actual git blob/tree/commit identity in memory: the git pin
	// binds "git copy", while the content pin binds different bytes at that path.
	// U01 has no resolver; comparing commit and raw-content hashes is incorrect.
	gitBytes, contentBytes := []byte("git copy\n"), []byte("different content copy\n")
	gitObjectID := func(kind string, data []byte) []byte {
		h := sha1.New() // Git's declared sha1 object format, not a security choice.
		fmt.Fprintf(h, "%s %d%c", kind, len(data), 0)
		h.Write(data)
		return h.Sum(nil)
	}
	blob := gitObjectID("blob", gitBytes)
	tree := gitObjectID("tree", append([]byte("100644 copy.txt\x00"), blob...))
	commit := gitObjectID("commit", []byte(fmt.Sprintf("tree %x\nauthor Lane E <lane-e@example.invalid> 0 +0000\ncommitter Lane E <lane-e@example.invalid> 0 +0000\n\nWire attack\n", tree)))
	gitSum, contentSum := sha256.Sum256(gitBytes), sha256.Sum256(contentBytes)
	if gitSum == contentSum {
		t.Fatal("attack control must describe different bytes")
	}
	ref := model.ArtifactRef{
		Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: hex.EncodeToString(commit), Path: "copy.txt"},
		Content:  &model.ContentPin{SHA256: model.Digest(hex.EncodeToString(contentSum[:])), Length: uint64(len(contentBytes)), MediaType: "text/plain", Locators: []model.Locator{{Path: "copy.txt"}}},
		Selector: model.Selector{Kind: "whole"},
	}
	if err := model.ValidateArtifactRef(ref, "source"); err != nil {
		t.Fatalf("both well-formed pins are legal pending U07 resolution: %v", err)
	}
	out, err := model.Encode(ref)
	if err != nil {
		t.Fatal(err)
	}
	var got model.ArtifactRef
	if err := json.Unmarshal(out, &got); err != nil || !reflect.DeepEqual(got, ref) {
		t.Fatalf("wire encoding hid/rewrote corroboration before U07 could compare bytes: %v\n%s", err, out)
	}
	t.Log("U07 must resolve git copy and content copy and reject their disagreement; U01 proves preservation only")
}

func TestWireHashBytesBindsExactBytesWithoutNormalization(t *testing.T) {
	seen := make(map[model.Digest]string)
	for _, input := range []string{"", "abc", "abc\n", "{\"a\":1,\"b\":2}", "{\"b\":2,\"a\":1}", "1.10", "1.1", "é", "é"} {
		want := sha256.Sum256([]byte(input))
		got := model.HashBytes([]byte(input))
		if got != model.Digest(hex.EncodeToString(want[:])) || !model.ValidDigest(got) {
			t.Errorf("HashBytes(%q) = %q; not exact-byte lowercase SHA-256", input, got)
		}
		if previous, ok := seen[got]; ok {
			t.Errorf("different byte strings %q and %q collapsed to digest %s", previous, input, got)
		}
		seen[got] = input
	}
}
