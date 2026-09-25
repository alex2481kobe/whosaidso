// Receipt, retry and schema counterexamples belong here: admission, replay,
// schema-fixture and revision-diff counterexamples. Production fixes do not.
package acceptance_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

func receiptReplay(t *testing.T, f *gateVerifyFixture, mutate func(model.TypedEvent)) (reduce.Snapshot, error) {
	t.Helper()
	prefix, err := store.ReadPrefix(f.p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reduce.Replay(prefix); err != nil {
		t.Fatalf("honest replay: %v", err)
	}
	if mutate != nil {
		last := &prefix[len(prefix)-1]
		for i, raw := range last.Events {
			e, err := model.DecodeEvent(raw)
			if err != nil {
				t.Fatal(err)
			}
			mutate(e)
			last.Events[i] = recEncode(t, e)
		}
	}
	return reduce.Replay(prefix)
}

func TestReceiptAttribution(t *testing.T) {
	for _, unknownHolder := range []bool{false, true} {
		t.Run(map[bool]string{false: "known-holder", true: "unknown-holder"}[unknownHolder], func(t *testing.T) {
			f := cliParityNew(t)
			holder := model.Actor{ID: "holder"}
			if unknownHolder {
				holder = model.Actor{UnknownReason: "not identified"}
			}
			ref, req := hbVerifyStart(t, f, holder)
			terminal := &model.AttemptTerminal{Task: ref, AttemptID: req.AttemptID, Outcome: model.AttemptStopped, Reason: "stop", NextAction: "resume", DeliveryRefs: []model.ArtifactRef{}}
			unknown := model.Actor{UnknownReason: "not identified"}
			if _, err := f.admit(unknown, model.Actor{ID: "reviewer"}, terminal); recCode(err) != reduce.CodeAttributionMismatch {
				t.Fatalf("unknown author must refuse: %v", err)
			}
			if _, err := f.admit(model.Actor{ID: "holder"}, model.Actor{ID: "reviewer"}, terminal); err != nil {
				t.Fatal(err)
			}
			_, err := receiptReplay(t, f, func(e model.TypedEvent) {
				if r, ok := e.(*model.ReviewAdmit); ok {
					for p := range r.Authors {
						r.Authors[p] = unknown
					}
				}
			})
			if recCode(err) != reduce.CodeAttributionMismatch {
				t.Fatalf("replay unknown author: %v", err)
			}
		})
	}
}

func TestInvalidCacheSettingOnAdmissionRetry(t *testing.T) {
	f := cliParityNew(t)
	a := model.Actor{ID: "holder"}
	packet := f.capture(a, recEncode(t, f.claim(a)))
	r := write.AdmitRequest{CommandID: f.id(), PacketIDs: []model.ID{packet.CommandID}, Admitter: a, Outcome: "accepted", Reason: "retry control"}
	if _, err := write.Admit(context.Background(), f.p, r); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.NoCacheEnv, "1")
	if _, err := write.Admit(context.Background(), f.p, r); err != nil {
		t.Fatalf("valid cache-off retry control: %v", err)
	}
	for _, value := range []string{"0", "true", "yes", " 1", "1 ", "2"} {
		t.Setenv(store.NoCacheEnv, value)
		if _, err := store.Load(f.p); recCode(err) != "invalid-field" {
			t.Fatalf("invalid setting control: %v", err)
		}
		if _, err := write.Admit(context.Background(), f.p, r); err == nil {
			t.Errorf("admission retry accepted WHOSAIDSO_NO_CACHE=%q after the same setting was refused by Load; invalid cache settings must not be silently ignored on one admission path", value)
		}
	}
}

func TestSchemaFixturesFailForTheirNamedReason(t *testing.T) {
	root := t.TempDir()
	read := func(name string) model.Event {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", "schema", "replacement-not-patch", name))
		if err != nil {
			t.Fatal(err)
		}
		pvPut(t, root, name, raw)
		var e model.Event
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	good := read("good.json")
	if _, err := model.DecodeEvent(good); err != nil {
		t.Fatalf("good fixture: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(good.Data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad-empty-replacement.json", "bad-partial-replacement.json", "bad-replacement-without-non-goals.json", "bad-target-without-revision.json"} {
		t.Run(name, func(t *testing.T) {
			bad := read(name)
			if _, err := model.DecodeEvent(bad); err == nil {
				t.Fatal("negative fixture must fail")
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(bad.Data, &data); err != nil {
				t.Fatal(err)
			}
			key := "replacement"
			if strings.Contains(name, "target") {
				key = "target"
			}
			data[key] = fields[key]
			raw, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			bad.Data = raw
			if _, err := model.DecodeEvent(bad); err != nil {
				t.Errorf("repairing the named defect must make this refusal fixture valid; it still fails: %v. The obsolete expected_revision field masks the check this fixture claims to exercise", err)
			}
		})
	}
}

func TestAmendmentDiffIncludesSourceCitations(t *testing.T) {
	f := cliParityNew(t)
	a := model.Actor{ID: "holder"}
	c := f.claim(a)
	if _, err := f.admit(a, a, c); err != nil {
		t.Fatal(err)
	}
	// Positive control: an ordinary specification edit is actually diffed.
	changed := c.Spec
	changed.Falsifier = "one reproducible counterexample"
	control := &model.ClaimRevise{Target: model.RecordRef{Project: f.p.ID, RecordID: c.ID, Revision: 1}, Replacement: changed, Provenance: c.Provenance}
	if _, err := f.admit(a, a, control); err != nil {
		t.Fatal(err)
	}
	answer, err := query.ReadView(f.p, query.ViewRequest{View: "continue", ID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len((*answer.(*query.ContinueAnswer).Amendments)[0].Changes) != 1 {
		t.Fatal("specification edit control must show a field change")
	}
	body := []byte(`{"source":"new basis for unchanged assertion"}`)
	pvPut(t, f.p.Root, "source.json", body)
	ref := model.RecordRef{Project: f.p.ID, RecordID: c.ID, Revision: 2}
	revise := &model.ClaimRevise{Target: ref, Replacement: changed, Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{gateVerifyContent(body, "source.json")}}}
	if _, err := f.admit(a, a, revise); err != nil {
		t.Fatal(err)
	}
	if _, err := receiptReplay(t, f, nil); err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"continue", "history"} {
		answer, err := query.ReadView(f.p, query.ViewRequest{View: view, ID: c.ID})
		if err != nil {
			t.Fatal(err)
		}
		var changes []query.FieldChange
		switch v := answer.(type) {
		case *query.ContinueAnswer:
			changes = (*v.Amendments)[1].Changes
		case *query.HistoryAnswer:
			for _, e := range v.Events {
				if e.Amendment != nil {
					changes = e.Amendment.Changes
				}
			}
		}
		if len(changes) == 0 {
			t.Errorf("%s says no fields changed after an admitted source_refs addition; the revision diff must disclose the changed evidence provenance", view)
		}
	}
}
