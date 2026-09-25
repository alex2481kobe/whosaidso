package write

// DOGFOOD item 64: a dry run gives the verdict admission gives for the same
// packet and blobs, whether the packet is uncaptured (events plus the blobs
// capture would store) or already captured, and writes nothing either way.

import (
	"context"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
)

// admissionParity dry-runs the events with blobs as an uncaptured packet, then
// captures exactly them and dry-runs the captured packet, then admits it. Each
// dry run must write nothing and agree with admission: clean when it admits,
// and led by admission's own refusal when it refuses. It returns admission's error.
func admissionParity(t *testing.T, f *admissionFixture, blobs [][]byte, events ...model.TypedEvent) error {
	t.Helper()
	raw := make([]model.Event, len(events))
	for i, event := range events {
		raw[i] = admissionTestEvent(t, event)
	}
	uncaptured, err := UncapturedPacket(f.project, f.author, raw, blobs)
	if err != nil {
		t.Fatal(err)
	}
	before := checkTree(t, f)
	early, err := CheckAdmission(context.Background(), f.project, nil, []Uncaptured{uncaptured}, f.author)
	if err != nil {
		t.Fatal(err)
	}
	if checkTree(t, f) != before {
		t.Fatal("the dry run of an uncaptured packet wrote to the project, intake or artifact store")
	}
	packet := f.capture(blobs, events...)
	before = checkTree(t, f)
	captured, err := CheckAdmission(context.Background(), f.project, []model.ID{packet.CommandID}, nil, model.Actor{ID: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if checkTree(t, f) != before {
		t.Fatal("the dry run of a captured packet wrote to the project, intake or artifact store")
	}
	_, admitErr := Admit(context.Background(), f.project, f.request(packet))
	for name, check := range map[string]AdmissionCheck{"uncaptured": early, "captured": captured} {
		first := ""
		if len(check.Refusals) > 0 {
			// A refusal may name the packet's intake path, and the two packets differ only in id.
			first = strings.ReplaceAll(check.Refusals[0].Err.Error(), string(uncaptured.Packet.CommandID), string(packet.CommandID))
		}
		switch {
		case admitErr == nil && len(check.Refusals) != 0:
			t.Errorf("%s dry run refuses what admission admits: %+v", name, check.Refusals)
		case admitErr != nil && len(check.Refusals) == 0:
			t.Errorf("%s dry run admits what admission refuses: %v", name, admitErr)
		case admitErr != nil && first != admitErr.Error():
			t.Errorf("%s dry run's first refusal %q is not admission's %q", name, first, admitErr)
		}
	}
	return admitErr
}

// A criterion.fix whose example exists nowhere on disk: only in the blob.
func TestCheckAdmissionGivesAdmissionsVerdictForACriterionExampleBlob(t *testing.T) {
	example := strings.Replace(proofPass, "0.0200", "0.0400", 1)
	for _, tc := range []struct {
		name  string
		blobs [][]byte
		code  string
	}{
		{"the example's bytes admit", [][]byte{[]byte(example)}, ""},
		{"no blob refuses", nil, "unavailable"},
		{"bytes not matching the pin refuse", [][]byte{[]byte(proofFail)}, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newProofWorld(t, true)
			fix := w.fixEvent(w.claim)
			fix.Expression.ResultSelector = proofPin(example, "example/stdout.json")
			fix.Expression.ResultSelector.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}
			err := admissionParity(t, w.f, tc.blobs, fix)
			if admissionErrorCode(err) != tc.code || tc.code == "" && err != nil {
				t.Fatalf("admission: want %q, got %v", tc.code, err)
			}
		})
	}
}

// A run's seal checked before capture: its output comes from the blobs handed
// with it, exactly as admission reads the packet's own captured blob.
func TestCheckAdmissionGivesAdmissionsVerdictForARunOutputBlob(t *testing.T) {
	for _, tc := range []struct {
		name  string
		blobs [][]byte
		code  string
	}{
		{"its own output admits", [][]byte{[]byte(proofPass)}, ""},
		{"other bytes refuse", [][]byte{[]byte(proofFail)}, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newProofWorld(t, true)
			env := w.envelope(w.criterion)
			w.f.accept(w.f.capture(nil, &model.InvocationStart{Envelope: env}))
			err := admissionParity(t, w.f, tc.blobs, proofSealed(env, proofPass))
			if admissionErrorCode(err) != tc.code || tc.code == "" && err != nil {
				t.Fatalf("admission: want %q, got %v", tc.code, err)
			}
		})
	}
}

// A ruling whose words exist only in the blob: the quote is compared with the
// bytes admission would have preserved, not refused for want of a source.
func TestCheckAdmissionGivesAdmissionsVerdictForAnAuthorityBlob(t *testing.T) {
	const ruling = `{"ruling":"ship the blob-only revision"}`
	for _, tc := range []struct {
		quote, code string
	}{
		{"ship the blob-only revision", ""},
		{"ship revision one", "quote-not-verbatim"},
	} {
		t.Run(tc.quote, func(t *testing.T) {
			w := newDisposeWorld(t)
			e := w.dispose(1, "approved")
			e.Authority.SourceRef = proofPin(ruling, "rulings/blob-only.json")
			e.Quote = tc.quote
			err := admissionParity(t, w.f, [][]byte{[]byte(ruling)}, e)
			if admissionErrorCode(err) != tc.code || tc.code == "" && err != nil {
				t.Fatalf("admission: want %q, got %v", tc.code, err)
			}
		})
	}
}

// A proof over a run still in intake: the family gate evaluates the run over
// the output admission would have preserved, so a proof admission accepts
// checks clean.
func TestCheckAdmissionOfAProofOverAPendingRunIsClean(t *testing.T) {
	w := newProofWorld(t, true)
	fresh := strings.Replace(proofPass, "0.0200", "0.0300", 1)
	pass, start, seal := w.run(w.criterion, fresh)
	proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"}))
	before := checkTree(t, w.f)
	check, err := CheckAdmission(context.Background(), w.f.project, []model.ID{start.CommandID, seal.CommandID, proof.CommandID}, nil, model.Actor{ID: "coordinator"})
	if err != nil || len(check.Refusals) != 0 {
		t.Fatalf("a proof admission accepts must check clean: %+v, %v", check.Refusals, err)
	}
	if checkTree(t, w.f) != before {
		t.Fatal("the dry run wrote")
	}
	w.f.accept(start, seal, proof)
}

// An uncaptured blob is held to intake's byte limit, as admission holds a
// captured one.
func TestUncapturedBlobHeldToTheIntakeLimit(t *testing.T) {
	big := make([]byte, evidence.DefaultMaxBytes+1)
	digest := model.HashBytes(big)
	dry := &dryRun{blobs: map[model.ID]map[model.Digest][]byte{"P": {digest: big}}}
	if _, err := dry.packetBlob(t.TempDir(), "P", digest); admissionErrorCode(err) != "unavailable" {
		t.Fatalf("a blob over the limit must be refused, got %v", err)
	}
	small := []byte("small")
	dry.blobs["P"][model.HashBytes(small)] = small
	if got, err := dry.packetBlob(t.TempDir(), "P", model.HashBytes(small)); err != nil || string(got) != "small" {
		t.Fatalf("control: a blob under the limit reads back: %q, %v", got, err)
	}
}
