package write

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func (f *admissionFixture) instrument() *model.InstrumentDeclare {
	return &model.InstrumentDeclare{
		ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.InstrumentSpec{
			QuestionAnswered: "how many bytes does the input contain", BlindTo: "the meaning of those bytes",
			NotAnswered: "whether the input is correct", ConfigSurface: []string{}, DangerousDefaults: []string{},
			ValidRange: "fixture text", ImplementationRef: admissionContent([]byte("instrument implementation")),
			Validation: model.Availability[model.InstrumentValidation]{State: model.Unknown, Reason: "not independently validated"},
		},
	}
}

func TestAdmissionInstrumentDeclaration(t *testing.T) {
	for _, unknownAuthor := range []bool{false, true} {
		t.Run(map[bool]string{false: "identified-author", true: "unknown-author"}[unknownAuthor], func(t *testing.T) {
			f := newAdmissionFixture(t)
			if unknownAuthor {
				f.author = model.Actor{UnknownReason: "original author was not recorded"}
			}
			instrument, consumer := f.instrument(), f.task()
			ref := f.ref(instrument.ID, 1)
			consumer.Spec.ContextRefs = []model.RecordRef{ref}
			first := f.capture(nil, consumer)
			second := f.capture([][]byte{[]byte("instrument implementation")}, instrument)
			bundle := f.accept(first, second)
			if bundle.Events[0].Type != "instrument.declare" || bundle.Events[1].Type != "task.create" {
				t.Fatal("instrument did not provide revision 1 to its forward consumer")
			}
			got, ok := f.snapshot().InstrumentAt(ref)
			if !ok || !reflect.DeepEqual(got.Spec, &instrument.Spec) || got.Support.ActiveTrust != reduce.TruthUnknown {
				t.Fatalf("declaration changed its specification or acquired trust: %+v", got)
			}
			s := f.snapshot()
			record, ok := s.Record(ref)
			if !ok || s.EventAuthor(record.Origin).Author != f.author {
				t.Fatalf("authorship was lost: %+v", record)
			}
			consumer = f.task()
			consumer.Spec.ContextRefs = []model.RecordRef{ref}
			f.accept(f.capture(nil, consumer)) // also resolves from the admitted prefix
		})
	}
}

func TestGateInstrumentMalformedDeclaration(t *testing.T) {
	for _, field := range []string{"blind_to", "not_answered"} {
		for _, value := range []string{"omitted", "", " \t", "\u200b"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				f := newAdmissionFixture(t)
				raw := admissionTestEvent(t, f.instrument())
				var data map[string]any
				if err := json.Unmarshal(raw.Data, &data); err != nil {
					t.Fatal(err)
				}
				spec := data["spec"].(map[string]any)
				if value == "omitted" {
					delete(spec, field)
				} else {
					spec[field] = value
				}
				assertInstrumentDecodeRefusal(t, f, raw, data, "spec."+field)
			})
		}
	}
	for _, mutation := range []string{"missing-author", "status", "spec.status", "trusted-validation"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			raw := admissionTestEvent(t, f.instrument())
			var data map[string]any
			if err := json.Unmarshal(raw.Data, &data); err != nil {
				t.Fatal(err)
			}
			path := mutation
			switch mutation {
			case "missing-author":
				data["provenance"].(map[string]any)["author"] = map[string]any{}
				path = "provenance.author"
			case "status":
				data["status"] = "TRUSTED"
			case "spec.status":
				data["spec"].(map[string]any)["status"] = "TRUSTED"
			case "trusted-validation":
				data["spec"].(map[string]any)["validation"] = map[string]any{"state": "trusted"}
				path = "spec.validation"
			}
			assertInstrumentDecodeRefusal(t, f, raw, data, path)
		})
	}
}

func assertInstrumentDecodeRefusal(t *testing.T, f *admissionFixture, raw model.Event, data map[string]any, path string) {
	t.Helper()
	var err error
	raw.Data, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass capture's encoder to exercise the gate on untrusted event bytes.
	packets, err := gatePackets(f.project.ID, reduce.Snapshot{}, []model.Packet{{Project: f.project.ID, CommandID: f.id(), Author: f.author, Events: []model.Event{raw}}})
	if admissionErrorCode(err) != "invalid-field" || !strings.Contains(err.Error(), "event.data."+path) || len(packets) != 0 {
		t.Fatalf("wanted malformed declaration refused at %s: packets %v, error %v", path, packets, err)
	}
}

func TestAdmissionInstrumentAuthorityAndIdentityRefusals(t *testing.T) {
	// Known validation and instrument.revise are admitted when their
	// artifacts resolve; TestInstrumentKnownValidationMustResolve covers them.
	for _, mutation := range []string{"duplicate-instrument", "duplicate-task"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			instrument := f.instrument()
			events := []model.TypedEvent{instrument}
			code := "conflict"
			switch mutation {
			case "duplicate-instrument":
				other := f.instrument()
				other.ID = instrument.ID
				events = append(events, other)
			case "duplicate-task":
				other := f.task()
				other.ID = instrument.ID
				events = append(events, other)
			}
			f.refuse(f.request(f.capture([][]byte{[]byte("instrument implementation")}, events...)), code)
		})
	}
}

func TestAdmissionInstrumentArtifacts(t *testing.T) {
	for _, location := range []string{"provenance", "implementation"} {
		for _, scenario := range []string{"intake", "locator", "missing", "wrong-length", "missing-selector", "traversal", "absolute-path", "symlink-file", "symlink-parent", "symlink-store"} {
			t.Run(location+"/"+scenario, func(t *testing.T) {
				f := newAdmissionFixture(t)
				instrument := f.instrument()
				body := []byte(`{"tool":"counts bytes"}`)
				ref := admissionContent(body)
				ref.Content.MediaType = "application/json"
				ref.Selector = model.Selector{Kind: "json-pointer", Pointer: "/tool"}
				blobs := [][]byte{[]byte("instrument implementation"), body}
				code := ""
				switch scenario {
				case "missing":
					blobs, code = blobs[:1], "unavailable"
				case "wrong-length":
					ref.Content.Length++
					code = "conflict"
				case "missing-selector":
					ref.Selector.Pointer, code = "/absent", "unavailable"
				case "traversal":
					ref.Content.Locators = []model.Locator{{Path: "../tool.json"}}
				case "absolute-path":
					ref.Content.Locators = []model.Locator{{Path: filepath.Join(f.project.Root, "tool.json")}}
				case "locator", "symlink-file", "symlink-parent", "symlink-store":
					blobs = blobs[:1] // no safe copy of the target pin may hide an escape
					path := filepath.Join(f.project.Root, "tool.json")
					ref.Content.Locators = []model.Locator{{Path: "tool.json"}}
					if scenario != "locator" {
						outside := t.TempDir()
						path = filepath.Join(outside, "tool.json")
						link, target := filepath.Join(f.project.Root, "tool.json"), path
						if scenario == "symlink-parent" {
							link, target = filepath.Join(f.project.Root, "linked"), outside
							ref.Content.Locators[0].Path = "linked/tool.json"
						} else if scenario == "symlink-store" {
							link, target = filepath.Join(f.project.Root, evidence.DefaultArtifactDir, string(ref.Content.SHA256)), path
							ref.Content.Locators = []model.Locator{}
						}
						if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(target, link); err != nil {
							t.Fatal(err)
						}
						code = "unavailable"
					}
					if err := os.WriteFile(path, body, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if location == "provenance" {
					instrument.Provenance.SourceRefs = []model.ArtifactRef{ref}
				} else {
					instrument.Spec.ImplementationRef = ref
				}
				if scenario == "traversal" || scenario == "absolute-path" {
					data, err := json.Marshal(instrument)
					if err != nil {
						t.Fatal(err)
					}
					packets, err := gatePackets(f.project.ID, reduce.Snapshot{}, []model.Packet{{Project: f.project.ID, CommandID: f.id(), Author: f.author, Events: []model.Event{{Type: instrument.EventType(), Data: data}}}})
					if admissionErrorCode(err) != "invalid-field" || len(packets) != 0 {
						t.Fatalf("unsafe artifact path admitted: %v, %v", packets, err)
					}
					return
				}
				packet := f.capture(blobs, instrument)
				if code != "" {
					before := f.snapshot().Watermark()
					_, err := Admit(context.Background(), f.project, f.request(packet))
					if admissionErrorCode(err) != code || f.snapshot().Watermark() != before {
						t.Fatalf("wanted %s without publication, got %v", code, err)
					}
					if strings.HasPrefix(scenario, "symlink-") && !strings.Contains(err.Error(), "resolved outside the root") {
						t.Fatalf("refusal did not reach resolver symlink containment: %v", err)
					}
					return
				}
				bundle := f.accept(packet)
				admitted, err := model.DecodeEvent(bundle.Events[0])
				if err != nil || !reflect.DeepEqual(admitted, instrument) {
					t.Fatalf("admission rewrote the declaration: %+v, %v", admitted, err)
				}
				if scenario == "locator" {
					if err := os.Remove(filepath.Join(f.project.Root, "tool.json")); err != nil {
						t.Fatal(err)
					}
				}
				resolved, err := evidence.NewResolver(f.project.Root).Resolve(context.Background(), ref)
				if err != nil || !bytes.Equal(resolved.Bytes, body) || resolved.Origin != evidence.OriginArtifactStore {
					t.Fatalf("artifact was not preserved: %+v, %v", resolved, err)
				}
			})
		}
	}
}

func TestGateInstrumentArtifactEnumeration(t *testing.T) {
	f := newAdmissionFixture(t)
	instrument := f.instrument()
	source, validation := admissionContent([]byte("source")), admissionContent([]byte("validation"))
	instrument.Provenance.SourceRefs = []model.ArtifactRef{source}
	// Known validation is admitted only when this enumeration hands its artifact
	// to resolution, so the enumeration must cover the complete model.
	instrument.Spec.Validation = model.Availability[model.InstrumentValidation]{State: model.Known, Value: &model.InstrumentValidation{Ref: validation, Version: "v1"}}
	if got, want := admissionArtifacts(instrument), []model.ArtifactRef{source, instrument.Spec.ImplementationRef, validation}; !reflect.DeepEqual(got, want) {
		t.Fatalf("declaration artifacts: got %+v, want %+v", got, want)
	}
}

func TestAdmissionReferenceAndAttributionRefusals(t *testing.T) {
	for _, mutation := range []string{"missing-reference", "cycle", "in-packet-forward", "foreign-subject", "duplicate-provider"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			control := f.goodControl()
			first, second := f.task(), f.task()
			var packets []model.PacketRef
			code := "unknown-reference"
			switch mutation {
			case "missing-reference":
				first.Spec.ContextRefs = []model.RecordRef{f.ref(second.ID, 1)}
				packets = []model.PacketRef{f.capture(nil, first)}
			case "cycle":
				first.Spec.ContextRefs = []model.RecordRef{f.ref(second.ID, 1)}
				second.Spec.ContextRefs = []model.RecordRef{f.ref(first.ID, 1)}
				packets = []model.PacketRef{f.capture(nil, first), f.capture(nil, second)}
				code = "dependency-cycle"
			case "in-packet-forward":
				first.Spec.ContextRefs = []model.RecordRef{f.ref(second.ID, 1)}
				packets = []model.PacketRef{f.capture(nil, first, second)}
			case "foreign-subject":
				target := f.ref(control.ID, 1)
				target.Project = "another/project"
				packets = []model.PacketRef{f.capture(nil, &model.TaskStart{Task: target, Actor: f.author, AttemptID: f.id()})}
				code = "invalid-field"
			case "duplicate-provider":
				second.ID = first.ID
				packets = []model.PacketRef{f.capture(nil, first), f.capture(nil, second)}
				code = "conflict"
			}
			f.refuse(f.request(packets...), code)
		})
	}
}

func TestAdmissionStableTieBreakAndExternalReferences(t *testing.T) {
	f := newAdmissionFixture(t)
	first, second, third := f.task(), f.task(), f.task()
	first.Spec.ContextRefs = []model.RecordRef{{Project: "elsewhere", RecordID: f.id(), Revision: 3}}
	a, b, c := f.capture(nil, first), f.capture(nil, second), f.capture(nil, third)
	bundle := f.accept(c, a, b)
	for i, want := range []model.ID{first.ID, second.ID, third.ID} {
		event, err := model.DecodeEvent(bundle.Events[i])
		if err != nil || event.(*model.TaskCreate).ID != want {
			t.Fatalf("unstable command-id tie-break at %d: %v, %v", i, event, err)
		}
	}
	if got := f.snapshot().Records()[0].Task.ContextRefs; len(got) != 1 || got[0].Project != "elsewhere" {
		t.Fatal("external unresolved reference was lost")
	}
}

func TestAdmissionOnlyFirstGateOperations(t *testing.T) {
	// decision.open is enabled (TestDecisionOpenAndReviseAdmitWithoutDisposition).
	for _, operation := range []string{"review"} {
		t.Run(operation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			control := f.goodControl()
			var event model.TypedEvent
			switch operation {
			case "decision":
				event = &model.DecisionOpen{ID: f.id(), Provenance: control.Provenance, Spec: model.DecisionSpec{Question: "may this be enabled", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: control.Spec.Scope}}
			case "review":
				id := f.id()
				event = &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: id, Digest: model.Digest(strings.Repeat("a", 64))}}, Outcome: "accepted", Actor: model.Actor{ID: "owner"}, Reason: "a packet cannot mint another admission",
					Authors: map[model.ID]model.Actor{id: {ID: "owner"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{id: {State: model.Unknown, Reason: "not recorded"}}, EventPackets: []model.ID{}}
			}
			f.refuse(f.request(f.capture(nil, f.task(), event)), "unavailable-until-integrated")
		})
	}
}

func (f *admissionFixture) claim() *model.ClaimAssert {
	return &model.ClaimAssert{ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "a finding awaiting measurement", Falsifier: "a counterexample", Scope: f.task().Spec.Scope, ExternalRefs: []model.ExternalReference{}}}
}

func TestAdmissionClaimRemainsUnmeasured(t *testing.T) {
	for _, tag := range []string{"", "VERIFIED", "VENDOR CLAIM", "REPORTED MEASUREMENT"} {
		t.Run("external-tag="+tag, func(t *testing.T) {
			f := newAdmissionFixture(t)
			claim := f.claim()
			if tag != "" {
				claim.Spec.ExternalRefs = []model.ExternalReference{{Tag: tag, Citation: "an external report is context, not local proof"}}
			}
			packet := f.capture(nil, claim)
			request := f.request(packet)
			request.Admitter = f.author
			if _, err := Admit(context.Background(), f.project, request); err != nil {
				t.Fatal(err)
			}
			snapshot := f.snapshot()
			got, ok := snapshot.ClaimAt(f.ref(claim.ID, 1))
			if !ok || got.Status != reduce.StatusUnmeasured || len(got.Observations) != 0 || len(got.Proofs) != 0 || got.Support.EvidenceAvailable != reduce.TruthUnknown || got.Support.ApplicableScope != reduce.TruthFalse {
				t.Fatalf("assertion acquired measurement or support: %+v", got)
			}
			review, ok := snapshot.Review(reduce.ReviewKey{Project: f.project.ID, CommandID: packet.CommandID})
			if !ok || review.SelfAdmission != model.SelfAdmissionTrue {
				t.Fatalf("self-admission must be admitted and queryable: %+v", review)
			}
		})
	}
}

func TestGateClaimCannotInjectProofStatusOrOmitAuthor(t *testing.T) {
	for _, mutation := range []string{"status", "spec.status", "missing-author"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			claim := f.claim()
			raw := admissionTestEvent(t, claim)
			var data map[string]any
			if err := json.Unmarshal(raw.Data, &data); err != nil {
				t.Fatal(err)
			}
			wantPath := "event.data." + mutation
			switch mutation {
			case "status":
				data["status"] = "PROVEN"
			case "spec.status":
				data["spec"].(map[string]any)["status"] = "PROVEN"
			case "missing-author":
				data["provenance"].(map[string]any)["author"] = map[string]any{}
				wantPath = "event.data.provenance.author"
			}
			var err error
			raw.Data, err = json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the gate's untrusted-event decode directly: the normal
			// capture encoder already refuses these malformed payloads.
			packets, err := gatePackets(f.project.ID, reduce.Snapshot{}, []model.Packet{{Project: f.project.ID, CommandID: f.id(), Author: f.author, Events: []model.Event{raw}}})
			if admissionErrorCode(err) != "invalid-field" || !strings.Contains(err.Error(), wantPath) || len(packets) != 0 {
				t.Fatalf("malformed claim must be refused at %s: packets %v, error %v", wantPath, packets, err)
			}
		})
	}
}

func TestAdmissionClaimCannotAssertProofWithoutEvidence(t *testing.T) {
	f := newAdmissionFixture(t)
	claim := f.claim()
	ref := f.ref(claim.ID, 1)
	// Syntactically valid names do not establish a frozen criterion or an
	// observation, so a proof bundled with its claim has nothing to stand on.
	proof := &model.ProofAdmit{Claim: ref, CriterionRef: model.CriterionRef{Claim: ref, CriterionID: f.id(), Revision: 1},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: f.project.ID, InvocationID: f.id()}, Disposition: "supports", Reason: "asserted without an observation"}},
		Judgment: model.ResponsibleJudgment{Actor: f.author, Reason: "asserted proof without evidence"}, Verdict: model.VerdictSupports}
	f.refuse(f.request(f.capture(nil, claim, proof)), "unknown-reference")
	if _, exists := f.snapshot().ClaimAt(ref); exists {
		t.Fatal("refused proof packet partially admitted its claim")
	}
	// The finding itself remains admissible after the proof attempt is refused.
	f.accept(f.capture(nil, claim))
}

func TestAdmissionClaimReferencesAndAttribution(t *testing.T) {
	for _, mutation := range []string{"scope-reference", "external-reference", "duplicate-claim", "duplicate-task", "cycle", "in-packet-forward"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			claim, other := f.claim(), f.claim()
			var packets []model.PacketRef
			code := "unknown-reference"
			switch mutation {
			case "scope-reference":
				claim.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
			case "external-reference":
				ref := f.ref(other.ID, 1)
				claim.Spec.ExternalRefs = []model.ExternalReference{{Tag: "VERIFIED", Citation: "missing local record", RecordRef: &ref}}
			case "duplicate-claim":
				other.ID = claim.ID
				packets = append(packets, f.capture(nil, other))
				code = "conflict"
			case "duplicate-task":
				task := f.task()
				task.ID = claim.ID
				packets = append(packets, f.capture(nil, task))
				code = "conflict"
			case "cycle":
				claim.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
				other.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(claim.ID, 1)}
				packets = append(packets, f.capture(nil, other))
				code = "dependency-cycle"
			case "in-packet-forward":
				claim.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
				f.refuse(f.request(f.capture(nil, claim, other)), code)
				return
			}
			packets = append(packets, f.capture(nil, claim))
			f.refuse(f.request(packets...), code)
		})
	}
}

func TestAdmissionClaimForwardProviderAndUnknownAuthor(t *testing.T) {
	f := newAdmissionFixture(t)
	f.author = model.Actor{UnknownReason: "original reviewer was not recorded"}
	claim, consumer := f.claim(), f.task()
	consumer.Spec.ContextRefs = []model.RecordRef{f.ref(claim.ID, 1)}
	first, second := f.capture(nil, consumer), f.capture(nil, claim)
	bundle := f.accept(first, second)
	if bundle.Events[0].Type != "claim.assert" || bundle.Events[1].Type != "task.create" {
		t.Fatal("claim revision did not provide the forward reference")
	}
	s := f.snapshot()
	record, ok := s.Record(f.ref(claim.ID, 1))
	if !ok || s.EventAuthor(record.Origin).Author != f.author {
		t.Fatalf("explicit unknown authorship was lost: %+v", record)
	}
	review, ok := f.snapshot().Review(reduce.ReviewKey{Project: f.project.ID, CommandID: second.CommandID})
	if !ok || review.SelfAdmission != model.SelfAdmissionUnknown {
		t.Fatalf("unknown attribution invented self-admission: %+v", review)
	}
	// The same reference also resolves from an admitted snapshot.
	another := f.claim()
	ref := f.ref(claim.ID, 1)
	another.Spec.ExternalRefs = []model.ExternalReference{{Tag: "VERIFIED", Citation: "prior finding", RecordRef: &ref}}
	f.accept(f.capture(nil, another))
}

func TestAdmissionExactAuthorityCarrier(t *testing.T) {
	for _, mutation := range []string{"actor-only", "wrong-speaker", "wrong-target", "wrong-scope", "wrong-pin", "missing-selector"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			target := f.goodControl()
			targetRef := f.ref(target.ID, 1)
			body := []byte(`{"ruling":"waiver is permitted for this exact prerequisite"}`)
			pin := admissionContent(body)
			pin.Content.MediaType = "application/json"
			source := &model.SourceIntake{SourceID: f.id(), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), SourceRef: pin, Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{targetRef}}
			authority := model.Authority{Actor: source.Speaker, SourceRef: pin, Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: target.Spec.Scope}
			authority.Scope.ContextRefs = []model.RecordRef{targetRef}
			consumer := f.task()
			consumer.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: targetRef, WaiverPolicy: "allow-with-authority", Authority: &authority}}
			// The first good control uses a carrier in the same bundle, even
			// though the consumer packet was authored before the source packet.
			consumerPacket := f.capture(nil, consumer)
			sourcePacket := f.capture([][]byte{body}, source)
			f.accept(consumerPacket, sourcePacket)
			admittedCarrierConsumer := f.task()
			admittedCarrierConsumer.Spec.Prerequisites = consumer.Spec.Prerequisites
			f.accept(f.capture(nil, admittedCarrierConsumer))
			bad := f.task()
			changed := authority
			bad.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: targetRef, WaiverPolicy: "allow-with-authority", Authority: &changed}}
			code := "authority-unavailable"
			switch mutation {
			case "actor-only":
				changed.SourceRef = admissionContent([]byte("an actor string does not grant authority"))
			case "wrong-speaker":
				changed.Actor.ID = "someone else"
			case "wrong-target":
				other := f.goodControl()
				bad.Spec.Prerequisites[0].Target = f.ref(other.ID, 1)
				changed.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
			case "wrong-scope":
				changed.Scope.ContextRefs = []model.RecordRef{}
				code = "authority-scope"
			case "wrong-pin":
				changed.SourceRef = admissionContent([]byte("different words"))
			case "missing-selector":
				changed.Selector.Pointer = "/absent"
				code = "unavailable"
			}
			f.refuse(f.request(f.capture(nil, bad)), code)
		})
	}
}

func TestAdmissionUnknownIdentityIsNotSelfAdmission(t *testing.T) {
	f := newAdmissionFixture(t)
	f.author = model.Actor{UnknownReason: "author was not recorded"}
	packet := f.capture(nil, f.task())
	request := f.request(packet)
	request.Admitter = f.author
	bundle, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	event, err := model.DecodeEvent(bundle.Events[len(bundle.Events)-1])
	if err != nil || event.(*model.ReviewAdmit).Authors[packet.CommandID] != f.author {
		t.Fatalf("unknown author was not recorded: %v, %v", event, err)
	}
	review, ok := f.snapshot().Review(reduce.ReviewKey{Project: bundle.Project, CommandID: packet.CommandID})
	if !ok || review.SelfAdmission != model.SelfAdmissionUnknown {
		t.Fatalf("matching unknown reasons were mistaken for an identity: %+v", review)
	}
}

// attemptPackets captures a takeover, its blocked-mid-task receipt and an open
// hold as three packets in the named ULID order. Event IDs are drawn before any
// capture, so every order carries byte-identical events.
func attemptPackets(t *testing.T, order string) (*admissionFixture, []model.PacketRef) {
	f, task, r := handbackControl(t)
	ref, body := f.ref(task.ID, 1), []byte("prior writer stopped")
	takeover := &model.TaskTakeover{Task: ref, Actor: f.author, AttemptID: f.id(), PriorAttemptID: r.AttemptID, StoppedConfirmationRef: admissionContent(body)}
	terminal := &model.AttemptTerminal{Task: ref, AttemptID: takeover.AttemptID, Outcome: model.AttemptBlockedMidTask, Reason: "new writer reached a boundary", NextAction: "owner resumes", DeliveryRefs: []model.ArtifactRef{}}
	hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerResume, Actor: f.author, Criterion: "owner supplies missing input"}
	var packets []model.PacketRef
	for _, name := range strings.Split(order, ",") {
		switch name {
		case "takeover":
			packets = append(packets, f.capture([][]byte{body}, takeover))
		case "receipt":
			packets = append(packets, f.capture(nil, terminal))
		case "hold":
			packets = append(packets, f.capture(nil, hold))
		}
	}
	return f, packets
}

func TestAdmissionAttemptDependencyOrder(t *testing.T) {
	var dependent []byte
	for _, c := range []struct{ order, want string }{
		{"takeover,receipt,hold", "task.takeover,attempt.terminal,blocker.hold"},
		// The receipt's lower ULID must not strand it before its attempt.
		{"receipt,takeover,hold", "task.takeover,attempt.terminal,blocker.hold"},
		// Independent packets keep authored (ULID) order around the dependency.
		{"hold,receipt,takeover", "blocker.hold,task.takeover,attempt.terminal"},
	} {
		f, packets := attemptPackets(t, c.order)
		request := f.request(packets...)
		bundle, err := Admit(context.Background(), f.project, request)
		if err != nil {
			t.Fatalf("%s: valid takeover, receipt and hold must admit in any ULID order: %v", c.order, err)
		}
		var got []string
		for _, event := range bundle.Events[:3] {
			got = append(got, string(event.Type))
		}
		if strings.Join(got, ",") != c.want {
			t.Fatalf("%s: got event order %v, want %s", c.order, got, c.want)
		}
		// Separate admissions differ in clock fields, never in admitted events.
		events, err := model.Encode(bundle.Events[:3])
		if err != nil {
			t.Fatal(err)
		}
		if c.want == "task.takeover,attempt.terminal,blocker.hold" {
			if dependent != nil && !bytes.Equal(dependent, events) {
				t.Fatalf("%s: admitted events differ from the other packet order", c.order)
			}
			dependent = events
		}
		// The same command with a permuted request returns identical bytes.
		permuted := request
		permuted.PacketIDs = []model.ID{request.PacketIDs[2], request.PacketIDs[0], request.PacketIDs[1]}
		again, err := Admit(context.Background(), f.project, permuted)
		first, firstErr := model.Encode(bundle)
		second, secondErr := model.Encode(again)
		if err != nil || firstErr != nil || secondErr != nil || !bytes.Equal(first, second) {
			t.Fatalf("%s: permuted retry must return identical bundle bytes: %v", c.order, err)
		}
	}
}

func TestAdmissionAttemptDependencyRefusals(t *testing.T) {
	for _, c := range []struct{ name, code, path string }{
		{"cycle", "dependency-cycle", "packets"},
		{"missing-receipt-attempt", "unknown-reference", "attempt_id"},
		{"missing-prior-attempt", "unknown-reference", "prior_attempt_id"},
		{"duplicate-attempt", "conflict", "packets"},
		{"takeover-chain", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, task, r := handbackControl(t)
			ref, body := f.ref(task.ID, 1), []byte("prior writer stopped")
			takeover := &model.TaskTakeover{Task: ref, Actor: f.author, AttemptID: f.id(), PriorAttemptID: r.AttemptID, StoppedConfirmationRef: admissionContent(body)}
			terminal := &model.AttemptTerminal{Task: ref, AttemptID: takeover.AttemptID, Outcome: model.AttemptBlockedMidTask, Reason: "boundary", NextAction: "owner resumes", DeliveryRefs: []model.ArtifactRef{}}
			hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerResume, Actor: f.author, Criterion: "owner supplies missing input"}
			missing := f.id()
			var packets []model.PacketRef
			switch c.name {
			case "cycle":
				// The receipt needs the takeover's attempt; the takeover's
				// packet clears the hold bundled with the receipt.
				clear := &model.BlockerClear{Task: ref, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: ref, BlockerID: hold.BlockerID}, ResolvingWitness: admissionContent(body)}
				packets = []model.PacketRef{f.capture([][]byte{body}, takeover, clear), f.capture(nil, hold, terminal)}
			case "missing-receipt-attempt":
				terminal.AttemptID = missing
				packets = []model.PacketRef{f.capture(nil, terminal, hold)}
			case "missing-prior-attempt":
				takeover.PriorAttemptID = missing
				packets = []model.PacketRef{f.capture([][]byte{body}, takeover)}
			case "duplicate-attempt":
				packets = []model.PacketRef{f.capture([][]byte{body}, takeover), f.capture([][]byte{body}, takeover)}
			case "takeover-chain":
				// The later takeover's lower ULID must wait for its prior attempt.
				chained := *takeover
				chained.AttemptID, chained.PriorAttemptID = f.id(), takeover.AttemptID
				packets = []model.PacketRef{f.capture([][]byte{body}, &chained), f.capture([][]byte{body}, takeover)}
			}
			if c.code == "" {
				bundle := f.accept(packets...)
				if bundle.Events[0].Type != "task.takeover" || bundle.Events[1].Type != "task.takeover" || len(f.snapshot().Attempts(reduce.Ident{Project: f.project.ID, ID: task.ID})) != 3 {
					t.Fatalf("takeover chain did not admit in dependency order: %+v", bundle.Events)
				}
				return
			}
			before := f.snapshot().Watermark()
			_, err := Admit(context.Background(), f.project, f.request(packets...))
			var fault *model.Fault
			if !errors.As(err, &fault) || fault.Code != c.code || fault.Path != c.path {
				t.Fatalf("wanted %s at %s, got %v", c.code, c.path, err)
			}
			if c.code == "unknown-reference" && !strings.Contains(fault.Detail, string(missing)) {
				t.Fatalf("refusal must name the missing attempt %s: %v", missing, err)
			}
			if f.snapshot().Watermark() != before {
				t.Fatal("refusal published")
			}
		})
	}
}
