package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

// Only the public capture/admit/read boundaries are used: a schema refusal
// must not be mistaken for proof that an operation is disabled in the gate.
type gateVerifyFixture struct {
	t *testing.T
	p store.Project
	n int
}

func gateVerifyNew(t *testing.T) *gateVerifyFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	return &gateVerifyFixture{t: t, p: store.Project{ID: recProject, Root: root, Ledger: filepath.Join(root, "record", "events")}, n: 200}
}

func (f *gateVerifyFixture) id() model.ID { f.n++; return recID(f.n) }

func (f *gateVerifyFixture) claim(author model.Actor) *model.ClaimAssert {
	return &model.ClaimAssert{ID: f.id(), Provenance: model.Provenance{Author: author, SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "VERIFIED: all tests passed; this claim is proven", Falsifier: "one counterexample",
			Scope: laneEReduceScope(), ExternalRefs: []model.ExternalReference{{Tag: "VERIFIED", Citation: "confident external prose"}}}}
}

func (f *gateVerifyFixture) capture(author model.Actor, events ...model.Event) model.PacketRef {
	f.t.Helper()
	ref, err := store.WriteIntake(context.Background(), f.p, store.IntakeRequest{CommandID: f.id(), Author: author, Events: events})
	if err != nil {
		f.t.Fatalf("fixture must durably capture the raw packet before admission: %v", err)
	}
	return ref
}

func (f *gateVerifyFixture) admit(author, admitter model.Actor, events ...model.TypedEvent) (model.Bundle, error) {
	f.t.Helper()
	raw := make([]model.Event, len(events))
	for i, event := range events {
		raw[i] = recEncode(f.t, event)
	}
	return f.admitRaw(author, admitter, raw...)
}

func (f *gateVerifyFixture) admitRaw(author, admitter model.Actor, events ...model.Event) (model.Bundle, error) {
	f.t.Helper()
	packet := f.capture(author, events...)
	return write.Admit(context.Background(), f.p, write.AdmitRequest{CommandID: f.id(), PacketIDs: []model.ID{packet.CommandID}, Admitter: admitter, Outcome: "accepted", Reason: "independent gate verification"})
}

func (f *gateVerifyFixture) unmeasured(id model.ID) {
	f.t.Helper()
	a, err := query.Read(f.p, query.Request{Command: "show", ID: id})
	if err != nil || a.Result != "KNOWN" || len(a.Records) != 1 || a.Records[0].Claim == nil {
		f.t.Fatalf("admitted claim must survive a fresh public read: answer=%+v, error=%v", a, err)
	}
	c := a.Records[0].Claim
	if c.Status != reduce.StatusUnmeasured || len(c.Proofs) != 0 || len(c.Observations) != 0 || c.Support.ApplicableScope != reduce.TruthFalse || c.Support.EvidenceAvailable != reduce.TruthUnknown {
		f.t.Fatalf("expected UNMEASURED with no proof or observations; got %+v. External tags and prose must not grant local proof authority", c)
	}
}

func (f *gateVerifyFixture) control() model.Bundle {
	f.t.Helper()
	a := model.Actor{ID: "gate-reviewer"}
	c := f.claim(a)
	b, err := f.admit(a, a, c)
	if err != nil {
		f.t.Fatalf("control: an honestly attributed claim must be admitted: %v", err)
	}
	f.unmeasured(c.ID)
	return b
}

func gateVerifyContent(body []byte, paths ...string) model.ArtifactRef {
	locators := make([]model.Locator, 0, len(paths))
	for _, path := range paths {
		locators = append(locators, model.Locator{Path: path})
	}
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "application/json", Locators: locators}, Selector: model.Selector{Kind: "whole"}}
}

func gateVerifyPut(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGateVerifyClaimAttributionAndUnmeasuredStatus(t *testing.T) {
	f := gateVerifyNew(t)
	f.control()
	for _, author := range []model.Actor{{ID: "owner"}, {UnknownReason: "original author was not recorded"}} {
		c := f.claim(author)
		b, err := f.admit(author, author, c)
		if err != nil {
			t.Fatalf("self-admission, including explicitly unknown attribution, must remain permitted: %v", err)
		}
		f.unmeasured(c.ID)
		// A downstream consumer must not interpret the external VERIFIED tag
		// as satisfaction of its local claim-proof prerequisite.
		spec := laneEReduceSpec(1)
		spec.Prerequisites = []model.Prerequisite{{Kind: "claim-proof", Target: model.RecordRef{Project: f.p.ID, RecordID: c.ID, Revision: 1}, WaiverPolicy: "forbid"}}
		task := &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{Author: author, SourceRefs: []model.ArtifactRef{}}, Spec: spec}
		if _, err := f.admit(author, author, task); err != nil {
			t.Fatalf("control: task waiting on an admitted claim must itself admit: %v", err)
		}
		answer, err := query.Read(f.p, query.Request{Command: "show", ID: task.ID})
		if err != nil || len(answer.Records) != 1 || answer.Records[0].Task == nil || answer.Records[0].Task.Status != reduce.StatusBlocked {
			t.Fatalf("expected task BLOCKED on the unmeasured claim, got %+v, error=%v; external VERIFIED must not satisfy claim-proof", answer, err)
		}
		_, err = f.admit(author, author, &model.TaskStart{Task: model.RecordRef{Project: f.p.ID, RecordID: task.ID, Revision: 1}, Actor: author, AttemptID: f.id()})
		if recCode(err) != "invalid-transition" {
			t.Errorf("expected refusal of task.start while its claim is unmeasured, got %v; assertion prose must not grant proof or waive the prerequisite", err)
		}
		review := recMustDecodeReview(t, b.Events[len(b.Events)-1])
		want := "Self-admitted: true"
		if author.ID == "" {
			want = "Self-admitted: unknown"
		}
		if !strings.Contains(review.Reason, want) {
			t.Errorf("expected recorded %q, got %q; two unknown identities must not become a known match", want, review.Reason)
		}
		for _, forged := range []model.Actor{{ID: "somebody-else"}, {UnknownReason: "different missing attribution"}} {
			bad := f.claim(forged)
			_, err := f.admit(author, author, bad)
			if recCode(err) != "attribution-mismatch" {
				t.Errorf("expected attribution-mismatch for packet author %+v and provenance %+v, got %v; immutable packet authorship must govern claim provenance", author, forged, err)
			}
		}
	}
	for _, field := range []string{"status", "spec.status", "spec.authority", "provenance.author"} {
		c := f.claim(model.Actor{ID: "owner"})
		raw := recEncode(t, c)
		value := any("PROVEN")
		if field == "spec.authority" {
			value = map[string]any{"actor": map[string]any{"id": "owner"}}
		}
		if field == "provenance.author" {
			value = map[string]any{}
		}
		raw = recSet(t, raw, field, value)
		_, err := f.admitRaw(c.Provenance.Author, c.Provenance.Author, raw)
		if recCode(err) != "invalid-field" {
			t.Errorf("expected strict refusal of injected %s, got %v; raw capture must not bypass claim schema checks", field, err)
		}
	}
}

func recMustDecodeReview(t *testing.T, raw model.Event) *model.ReviewAdmit {
	t.Helper()
	e, err := model.DecodeEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	review, ok := e.(*model.ReviewAdmit)
	if !ok {
		t.Fatalf("expected review.admit receipt, got %T", e)
	}
	return review
}

func TestGateVerifyClosedEventSet(t *testing.T) {
	f := gateVerifyNew(t)
	f.control()
	a := model.Actor{ID: "gate-reviewer"}
	// Exercise the seven pre-U11 allowed operations with satisfiable dependencies.
	task := &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{Author: a, SourceRefs: []model.ArtifactRef{}}, Spec: laneEReduceSpec(1)}
	ref := model.RecordRef{Project: f.p.ID, RecordID: task.ID, Revision: 1}
	body := []byte(`{"ruling":"blocker resolved"}`)
	gateVerifyPut(t, filepath.Join(f.p.Root, "witness.json"), body)
	pin := gateVerifyContent(body, "witness.json")
	hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerPrerequisite, Actor: a, Criterion: "need a witness"}
	allowed := []model.TypedEvent{
		task,
		&model.SourceIntake{SourceID: f.id(), SourceRef: pin, OriginalDigest: pin.Content.SHA256, Length: pin.Content.Length, Speaker: a, Referents: []model.RecordRef{ref}},
		hold,
		&model.BlockerClear{Task: ref, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: ref, BlockerID: hold.BlockerID}, ResolvingWitness: pin},
		&model.TaskAmend{Provenance: task.Provenance, Target: ref, ExpectedRevision: 1, Replacement: task.Spec},
		&model.TaskStart{Task: model.RecordRef{Project: ref.Project, RecordID: ref.RecordID, Revision: 2}, Actor: a, AttemptID: f.id()},
		f.claim(a),
	}
	seen := map[model.EventType]bool{}
	for _, event := range allowed {
		if _, err := f.admit(a, a, event); err != nil {
			t.Fatalf("control: allowed %s must admit with satisfied dependencies: %v", event.EventType(), err)
		}
		seen[event.EventType()] = true
	}
	// Exception: U11 explicitly enabled task.takeover and attempt.terminal.
	// Their API and admission integration landed; this is the authorised unit
	// boundary moving, not permission to weaken tests to match arbitrary code.
	// Require successful admission AND their own rule refusals before exempting
	// just these two types from the allowlist expectation. The other 23 stay as-is.
	gateVerifyU11(t, f, a, allowed[5].(*model.TaskStart))
	seen["task.takeover"], seen["attempt.terminal"] = true, true
	refused := 0
	for _, event := range evAll() {
		if seen[event.EventType()] {
			continue
		}
		refused++
		t.Run(string(event.EventType()), func(t *testing.T) {
			g := gateVerifyNew(t)
			control := g.control()
			// evAll supplies fully schema-valid payloads, not empty structs.
			raw := recEncode(t, event)
			if _, err := model.DecodeEvent(raw); err != nil {
				t.Fatalf("control payload must decode: %v", err)
			}
			claim := g.claim(a)
			_, err := g.admitRaw(a, a, recEncode(t, claim), raw)
			if recCode(err) != "unavailable-until-integrated" {
				t.Errorf("expected gate allowlist refusal for schema-valid %s, got %v; downstream reference failure would not prove this operation stayed disabled", event.EventType(), err)
			}
			prefix, err := store.ReadPrefix(g.p)
			if err != nil || len(prefix) != 1 || prefix[0].CommandID != control.CommandID {
				t.Errorf("expected unchanged ledger after rejected %s, got %d bundles and %v; a bundled claim must not be partially admitted", event.EventType(), len(prefix), err)
			}
		})
		seen[event.EventType()] = true
	}
	for _, kind := range evClosedSet {
		if !seen[kind] {
			t.Errorf("closed event type %s was not tested", kind)
		}
	}
	if len(seen) != 25 {
		t.Fatalf("expected all 25 closed event types, exercised %d", len(seen))
	}
	if refused != 16 {
		t.Fatalf("expected 16 allowlist refusals (25 total minus 7 pre-U11 and 2 U11 operations), exercised %d", refused)
	}
}

func gateVerifyU11(t *testing.T, f *gateVerifyFixture, a model.Actor, start *model.TaskStart) {
	t.Helper()
	// Each refusal uses the same otherwise valid operation that subsequently
	// admits. Unrelated schema/reference errors cannot stand in for its rule.
	refuse := func(event model.TypedEvent, code, path string) {
		t.Helper()
		before, err := store.ReadPrefix(f.p)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.admit(a, a, f.claim(a), event)
		var fault *model.Fault
		if !errors.As(err, &fault) || fault.Code != code || fault.Path != path {
			t.Fatalf("expected %s's own rule refusal %s at %s, got %v", event.EventType(), code, path, err)
		}
		after, err := store.ReadPrefix(f.p)
		beforeBytes, beforeErr := model.Encode(before)
		afterBytes, afterErr := model.Encode(after)
		if err != nil || beforeErr != nil || afterErr != nil || !bytes.Equal(beforeBytes, afterBytes) {
			t.Fatalf("refused %s partially published its bundled claim or operation: %v", event.EventType(), err)
		}
	}
	takeover := &model.TaskTakeover{Task: start.Task, Actor: a, AttemptID: f.id(), PriorAttemptID: start.AttemptID,
		StoppedConfirmationRef: gateVerifyContent([]byte(`{"writer":"stopped"}`), "stopped.json")}
	refuse(takeover, "unavailable", "artifact.content")
	gateVerifyPut(t, filepath.Join(f.p.Root, "stopped.json"), []byte(`{"writer":"stopped"}`))
	if _, err := f.admit(a, a, takeover); err != nil {
		t.Fatalf("U11: takeover with resolvable prior-writer confirmation must admit: %v", err)
	}
	terminal := &model.AttemptTerminal{Task: start.Task, AttemptID: takeover.AttemptID, Outcome: model.AttemptBlockedMidTask,
		Reason: "lane reached a boundary", NextAction: "owner supplies a resolution", DeliveryRefs: []model.ArtifactRef{}}
	refuse(terminal, "missing-hold", "attempt.terminal")
	hold := &model.BlockerHold{Task: start.Task, BlockerID: f.id(), Reason: model.BlockerResume, Actor: a, Criterion: "owner resolves the boundary"}
	bundle, err := f.admit(a, a, terminal, hold)
	if err != nil {
		t.Fatalf("U11: blocked-mid-task receipt with its bundled hold must admit: %v", err)
	}
	prefix, err := store.ReadPrefix(f.p)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		t.Fatal(err)
	}
	projection, ok := snapshot.Task(reduce.Ident{Project: f.p.ID, ID: start.Task.RecordID})
	if !ok || len(projection.Attempts) != 2 || len(projection.LiveAttempts) != 1 || len(projection.Blockers) != 2 {
		t.Fatalf("U11 operations did not survive fresh replay: %+v", projection)
	}
	for _, attempt := range projection.Attempts {
		if attempt.Key.Attempt == start.AttemptID && attempt.Terminal != nil {
			t.Fatal("takeover fabricated the prior writer's terminal receipt")
		}
		if attempt.Key.Attempt == takeover.AttemptID && (!attempt.Takeover || attempt.PriorAttempt != start.AttemptID || attempt.Terminal == nil || attempt.Terminal.Outcome != terminal.Outcome || attempt.Terminal.Origin.Sequence != bundle.Sequence) {
			t.Fatalf("takeover or terminal receipt lost: %+v", attempt)
		}
	}
	for _, blocker := range projection.Blockers {
		if blocker.Key.Blocker == hold.BlockerID && (!blocker.Open() || blocker.Held.Sequence != bundle.Sequence) {
			t.Fatal("blocked-mid-task receipt escaped its atomic hold")
		}
	}
}

func TestGateVerifyClaimArtifactCannotEscapeRootThroughSymlink(t *testing.T) {
	for _, field := range []string{"provenance", "external"} {
		for _, route := range []string{"locator-file", "locator-parent", "empty-locators-store"} {
			t.Run(field+"/"+route, func(t *testing.T) {
				f := gateVerifyNew(t)
				a := model.Actor{ID: "gate-reviewer"}
				attach := func(c *model.ClaimAssert, pin model.ArtifactRef) {
					if field == "provenance" {
						c.Provenance.SourceRefs = []model.ArtifactRef{pin}
					} else {
						c.Spec.ExternalRefs[0].SourceRef = &pin
					}
				}
				controlBody := []byte(`{"source":"inside the declared root"}`)
				gateVerifyPut(t, filepath.Join(f.p.Root, "inside.json"), controlBody)
				control := f.claim(a)
				attach(control, gateVerifyContent(controlBody, "inside.json"))
				if _, err := f.admit(a, a, control); err != nil {
					t.Fatalf("control: claim artifact inside root must admit: %v", err)
				}
				f.unmeasured(control.ID)
				// Different bytes ensure the control's materialized blob cannot
				// satisfy the attack without following the escaping symlink.
				body := []byte(`{"source":"outside the declared root"}`)
				outside := t.TempDir()
				gateVerifyPut(t, filepath.Join(outside, "source.json"), body)
				pin := gateVerifyContent(body, "escape.json")
				link, target := filepath.Join(f.p.Root, "escape.json"), filepath.Join(outside, "source.json")
				switch route {
				case "locator-parent":
					link, target = filepath.Join(f.p.Root, "escape"), outside
					pin.Content.Locators[0].Path = "escape/source.json"
				case "empty-locators-store":
					pin.Content.Locators = []model.Locator{}
					link = filepath.Join(f.p.Root, evidence.DefaultArtifactDir, string(pin.Content.SHA256))
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				claim := f.claim(a)
				attach(claim, pin)
				bundle, err := f.admit(a, a, claim)
				if err == nil {
					f.unmeasured(claim.ID)
					stored, readErr := os.ReadFile(filepath.Join(f.p.Root, evidence.DefaultArtifactDir, string(pin.Content.SHA256)))
					if readErr != nil || !bytes.Equal(stored, body) {
						t.Fatalf("expected outside bytes to be reachable through the admitted artifact store, got %q, error=%v", stored, readErr)
					}
					t.Errorf("expected refusal of %s claim artifact through %s, but admission published sequence %d and a fresh read finds the claim. Relative locator %q follows %q -> %q outside project root %q; hash/length agreement does not establish path containment", field, route, bundle.Sequence, pin.Content.Locators, link, target, f.p.Root)
				}
			})
		}
	}
}

func TestGateVerifyClaimArtifactRefusals(t *testing.T) {
	f := gateVerifyNew(t)
	f.control()
	a := model.Actor{ID: "gate-reviewer"}
	body := []byte(`{"source":"valid bytes"}`)
	gateVerifyPut(t, filepath.Join(f.p.Root, "source.json"), body)
	for _, field := range []string{"provenance", "external"} {
		for _, mutation := range []string{"traversal", "absolute", "backslash", "corroborating-git", "missing", "wrong-hash", "missing-selector", "invalid-selector"} {
			c := f.claim(a)
			pin := gateVerifyContent(body, "source.json")
			switch mutation {
			case "traversal":
				pin.Content.Locators[0].Path = "../source.json"
			case "absolute":
				pin.Content.Locators[0].Path = filepath.Join(f.p.Root, "source.json")
			case "backslash":
				pin.Content.Locators[0].Path = `..\source.json`
			case "corroborating-git":
				pin.Git = &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "../source.json"}
			case "missing":
				pin = gateVerifyContent([]byte(`{"not":"present"}`))
			case "wrong-hash":
				pin.Content.SHA256 = model.HashBytes([]byte("different"))
			case "missing-selector":
				pin.Selector = model.Selector{Kind: "json-pointer", Pointer: "/absent"}
			case "invalid-selector":
				pin.Selector = model.Selector{Kind: "json-pointer", Pointer: "/bad~2"}
			}
			if field == "provenance" {
				c.Provenance.SourceRefs = []model.ArtifactRef{pin}
			} else {
				c.Spec.ExternalRefs[0].SourceRef = &pin
			}
			// Deliberately bypass EncodeEvent so the public admission decoder
			// has to reject malformed paths in a durably captured packet.
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.admitRaw(a, a, model.Event{Type: c.EventType(), Data: data})
			if err == nil {
				t.Errorf("expected refusal of %s/%s, got admission; every claim artifact must be checked", field, mutation)
			}
		}
	}
}

func TestGateVerifyCheapReferenceValidatorMustRejectInvalidSelectors(t *testing.T) {
	body := []byte(`{"value":1}`)
	root := t.TempDir()
	gateVerifyPut(t, filepath.Join(root, "source.json"), body)
	resolver := evidence.NewResolver(root)
	good := gateVerifyContent(body, "source.json")
	good.Selector = model.Selector{Kind: "json-pointer", Pointer: "/value"}
	if err := model.ValidateArtifactRef(good, "ref"); err != nil {
		t.Fatalf("control: valid pointer must validate: %v", err)
	}
	resolved, err := resolver.Resolve(context.Background(), good)
	if err != nil {
		t.Fatalf("control: valid reference must resolve: %v", err)
	}
	if _, err := evidence.Select(resolved, good.Selector); err != nil {
		t.Fatalf("control: valid pointer must select: %v", err)
	}
	for _, pointer := range []string{"value", "/value~", "/value~2"} {
		bad := gateVerifyContent(body, "source.json")
		bad.Selector = model.Selector{Kind: "json-pointer", Pointer: pointer}
		if err := model.ValidateSchema(bad); err == nil {
			t.Fatalf("fixture must be invalid under the full schema: %q", pointer)
		}
		cheapErr := model.ValidateArtifactRef(bad, "ref")
		got, resolveErr := resolver.Resolve(context.Background(), bad)
		if cheapErr == nil || resolveErr == nil {
			t.Errorf("expected malformed JSON pointer %q to be refused before returning a resolved reference; cheap validation=%v, resolution=%v, returned pointer=%q. The cheap validator checks only selector kind and lets schema-invalid selectors reach its resolver caller", pointer, cheapErr, resolveErr, got.Ref.Selector.Pointer)
		}
	}
}

func TestGateVerifyCheapReferencePathAndEmptyLocatorBoundaries(t *testing.T) {
	body := []byte(`{"value":1}`)
	root := t.TempDir()
	good := gateVerifyContent(body)
	gateVerifyPut(t, filepath.Join(root, evidence.DefaultArtifactDir, string(good.Content.SHA256)), body)
	if err := model.ValidateSchema(good); err != nil {
		t.Fatalf("control: explicit empty locators are legal with stored content: %v", err)
	}
	if _, err := evidence.NewResolver(root).Resolve(context.Background(), good); err != nil {
		t.Fatalf("control: empty locator list must resolve the in-root stored blob: %v", err)
	}
	for _, path := range []string{"../outside", "/outside", `C:\outside`, "safe/../../outside", "\x00", "\u200b"} {
		for _, kind := range []string{"primary-content", "corroborating-content", "corroborating-git"} {
			bad := gateVerifyContent(body, path)
			if kind == "corroborating-content" {
				bad.Kind = "git"
				bad.Git = &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "safe.json"}
			}
			if kind == "corroborating-git" {
				bad.Content.Locators = []model.Locator{}
				bad.Git = &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: path}
			}
			if err := model.ValidateArtifactRef(bad, "ref"); err == nil {
				t.Errorf("expected unsafe %s path %q to be refused even when another pin or the content store is available", kind, path)
			}
		}
	}
}

func TestGateVerifyBundleSequenceBounds(t *testing.T) {
	f := gateVerifyNew(t)
	control := f.control()
	for _, seq := range []uint64{1, 99999999, 0, 100000000} {
		b := control
		b.Sequence = seq
		b.Predecessor = ""
		if seq != 1 {
			b.Predecessor = recID(100)
		}
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		_, decodeErr := model.DecodeBundle(data)
		_, nameErr := model.BundleName(seq, b.CommandID)
		wantOK := seq == 1 || seq == 99999999
		if (decodeErr == nil) != wantOK || (nameErr == nil) != wantOK {
			t.Errorf("sequence %d: expected decode and writable-name acceptance=%v, got decode=%v, name=%v; ledger decoding must not accept an unwritable sequence", seq, wantOK, decodeErr, nameErr)
		}
	}
}

func TestGateVerifyDecimalBoundsAndSignificandCost(t *testing.T) {
	if ok, err := model.CompareScalars(recNumber("0.1"), model.Equal, recNumber("1e-1")); err != nil || !ok {
		t.Fatalf("control: exact decimal equivalence must hold: %v, %v", ok, err)
	}
	for _, exponent := range []int{-4096, 4096, -4097, 4097} {
		_, err := model.DecimalRat(json.Number(fmt.Sprintf("1e%d", exponent)))
		wantOK := exponent >= -4096 && exponent <= 4096
		if (err == nil) != wantOK {
			t.Errorf("exponent %d: expected acceptance=%v, got %v", exponent, wantOK, err)
		}
	}
	// No machine-dependent timeout assertion: significand length is explicitly
	// unbounded. Measure the remaining cost without inventing a size contract.
	for _, shape := range []string{"fractional-zero-prefix", "all-nines-integer"} {
		for _, digits := range []int{1000, 10000, 100000, 1000000} {
			text := "0." + strings.Repeat("0", digits-1) + "1"
			if shape == "all-nines-integer" {
				text = strings.Repeat("9", digits)
			}
			n := recNumber(text)
			started := time.Now()
			ok, err := model.CompareScalars(n, model.Greater, recNumber("0"))
			elapsed := time.Since(started)
			if err != nil || !ok {
				t.Fatalf("a positive decimal within the current significand contract compared incorrectly: shape=%s, digits=%d, result=%v, error=%v", shape, digits, ok, err)
			}
			t.Logf("accepted %s with %d digits without an explicit exponent; one exact comparison took %s", shape, digits, elapsed)
		}
	}
}

func TestGateVerifyPermissionRecoveryCommandQuotesExactPath(t *testing.T) {
	f := gateVerifyNew(t)
	f.control()
	for _, name := range []string{"plain", "with space", "with'quote", "with\nnewline", "space ' quote\n$(false); tail"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			g := gateVerifyNew(t)
			home := filepath.Join(t.TempDir(), name)
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			g.control()
			inbox, err := store.IntakeDir(g.p)
			if err != nil {
				t.Fatal(err)
			}
			packets, err := store.ReadIntake(g.p, nil)
			if err != nil || len(packets) != 1 {
				t.Fatalf("control packet must be readable: %v", err)
			}
			packetPath := filepath.Join(inbox, string(packets[0].CommandID), "packet.json")
			before, err := os.ReadFile(packetPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{packetPath, inbox} {
				mode, insecure := os.FileMode(0600), os.FileMode(0644)
				if path == inbox {
					mode, insecure = 0700, 0755
				}
				if err := os.Chmod(path, insecure); err != nil {
					t.Fatal(err)
				}
				_, refusal := store.ReadIntake(g.p, nil)
				var fault *model.Fault
				if !errors.As(refusal, &fault) || fault.Code != "insecure-permissions" {
					t.Fatalf("expected owner-only permission refusal before recovery, got %v", refusal)
				}
				const prefix = "intake requires owner-only access; restore permissions with "
				const suffix = "; then retry the identical read or write; do not delete or recapture the packet"
				if !strings.HasPrefix(fault.Detail, prefix) || !strings.HasSuffix(fault.Detail, suffix) {
					t.Fatalf("expected extractable recovery command, got %q", fault.Detail)
				}
				command := strings.TrimSuffix(strings.TrimPrefix(fault.Detail, prefix), suffix)
				// Execute exactly the displayed POSIX command, only against our
				// temporary fixture. Newlines inside quotes must remain literal.
				if out, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
					t.Fatalf("recovery command must quote the exact path %q, command=%q: %v, %s", path, command, err, out)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("expected recovery to restore mode %o on %q, stat=%v error=%v", mode, path, info, err)
				}
				if _, err := store.ReadIntake(g.p, nil); err != nil {
					t.Fatalf("recovery must make the identical packet readable: %v", err)
				}
				after, err := os.ReadFile(packetPath)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("permission recovery must preserve immutable packet bytes: %v", err)
				}
			}
		})
	}
}
