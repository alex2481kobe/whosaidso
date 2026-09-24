// Lane E's independent U02 attacks on the closed event set: the exhaustive
// decode switch, the envelope carried by invocation.start and invocation.seal,
// and the typed reference walker every later unit is told to reuse.
//
// The payload constructors live in records_test.go. Every fixture slot gets a
// distinct record id so a link that a walker drops cannot hide behind a link
// that happens to look the same.
package acceptance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
)

// evClosedSet is the closed event set as the plan's event table names it,
// transcribed here rather than read back out of the implementation.
var evClosedSet = []model.EventType{
	"task.create", "task.amend",
	"task.start", "task.takeover",
	"attempt.terminal",
	"task.close",
	"blocker.hold", "blocker.clear",
	"invocation.start", "invocation.seal",
	"source.intake",
	"claim.assert", "claim.revise",
	"criterion.fix",
	"proof.admit",
	"decision.open", "decision.revise", "decision.dispose",
	"supersede",
	"correction",
	"instrument.declare", "instrument.revise",
	"trust.withdraw",
	"review.admit",
	"artifact.dispose",
}

func recStart() *model.TaskStart {
	return &model.TaskStart{Task: recRef(recID(1), 2), Actor: recActor("lane-e"), AttemptID: recID(70)}
}

func recPointerRef(d model.Digest, pointer string) model.ArtifactRef {
	return model.ArtifactRef{
		Kind: "content",
		Content: &model.ContentPin{
			SHA256: d, Length: 4096, MediaType: "application/json",
			Locators: []model.Locator{{Path: ".whosaidso/artifacts/" + string(d)}},
		},
		Selector: model.Selector{Kind: "json-pointer", Pointer: pointer},
	}
}

func recCriterionFix() *model.CriterionFix {
	return &model.CriterionFix{
		Claim:       recRef(recID(2), 3),
		CriterionID: recID(50),
		Revision:    1,
		Expression: model.CriterionExpression{
			ResultSelector: recPointerRef(recDigest('c'), "/results/frame_time_p95"),
			Unit:           "milliseconds",
			Population: model.Population{
				Identity:    "every frame recorded in the sealed run",
				Selector:    recPointerRef(recDigest('c'), "/results/frames"),
				Denominator: "frames recorded, not frames requested",
			},
			Operator: model.LessEqual,
			Target:   recNumber("16.7"),
			Reducer:  model.All,
		},
		Policy:     model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author:     recActor("lane-e"),
		SourceRefs: []model.ArtifactRef{recGit("documentation/design/research/DATUM-CONTRACT.md")},
	}
}

func recProof() *model.ProofAdmit {
	return &model.ProofAdmit{
		Claim:        recRef(recID(2), 3),
		CriterionRef: model.CriterionRef{Claim: recRef(recID(2), 3), CriterionID: recID(50), Revision: 1},
		Evidence: []model.ObservationDisposition{
			{InvocationRef: model.InvocationRef{Project: recProject, InvocationID: recID(51)},
				Disposition: "supports", Reason: "the sealed run met the fixed criterion"},
			{InvocationRef: model.InvocationRef{Project: recProject, InvocationID: recID(52)},
				Disposition: "inapplicable", Reason: "that run used a different backend"},
		},
		Judgment: model.ResponsibleJudgment{
			Actor:  recActor("owner"),
			Reason: "the applicable family is complete and nothing contradicts it",
		},
		Verdict: model.VerdictSupports, // R18.2: every proof states its verdict.
	}
}

func recSupersede() *model.Supersede {
	return &model.Supersede{
		Prior:       recRef(recID(60), 1),
		Replacement: recRef(recID(61), 1),
		Reason:      "the earlier ruling named the wrong revision",
		Authority:   ptr(recAuthority(recID(62))),
	}
}

func recCorrection() *model.Correction {
	return &model.Correction{
		Target:            model.CorrectionTarget{Kind: "record", Record: ptr(recRef(recID(63), 2))},
		AffectedRevisions: []model.RecordRef{recRef(recID(64), 1)},
		Reason:            "the measured population excluded the failing retries",
		CorrectiveRef:     recGit("internal/evidence/criterion.go"),
	}
}

func recTrustWithdraw() *model.TrustWithdraw {
	return &model.TrustWithdraw{
		Instrument:            recRef(recID(4), 2),
		Scope:                 recScope(recID(65)),
		RevalidationCondition: "a validation run on the current backend passes again",
	}
}

// recSealedEnvelope is the same invocation AFTER it ran, so every field a
// running process supplies is now known.
func recSealedEnvelope() model.InvocationEnvelope {
	e := recEnvelope()
	e.CriterionRef = recKnown(model.CriterionRef{Claim: recRef(recID(2), 3), CriterionID: recID(50), Revision: 1})
	e.ConfigEffective = recKnown(map[string]model.Availability[model.Scalar]{
		"strict":    recKnown(recBool(true)),
		"max_depth": recUnknown[model.Scalar]("the adapter did not report this knob"),
	})
	e.ConditionsObserved = recKnown(map[string]model.Availability[model.Scalar]{
		"max_depth": recKnown(recNumber("64")),
	})
	e.Isolation = recKnown(model.IsolationClean)
	e.ObservedAt = recKnown(recWhen.Add(90 * time.Second))
	e.Outcome = recKnown(model.ProcessOutcome{Kind: "exit", ExitCode: ptr(0)})
	e.Outputs = recKnown([]model.RunOutput{{Name: "out/result.json", SHA256: recDigest('d'), Length: 128, MediaType: "application/json"}})
	return e
}

func recSeal() *model.InvocationSeal {
	envelope := recSealedEnvelope()
	return &model.InvocationSeal{
		StartRef: model.InvocationRef{Project: recProject, InvocationID: envelope.InvocationID},
		Envelope: envelope,
	}
}

// evAll builds one valid payload per member of the closed set.
func evAll() []model.TypedEvent {
	return []model.TypedEvent{
		&model.TaskCreate{Provenance: recProvenance(), ID: recID(1), Spec: recTaskSpec()},
		&model.TaskAmend{Provenance: recProvenance(), Target: recRef(recID(1), 2), Replacement: recTaskSpec()},
		recStart(),
		&model.TaskTakeover{
			Task: recRef(recID(1), 2), Actor: recActor("lane-b"), AttemptID: recID(71),
			PriorAttemptID: recID(70), StoppedConfirmationRef: recGit(".whosaidso/handback/lane-e.json"),
		},
		&model.AttemptTerminal{
			Task: recRef(recID(1), 2), AttemptID: recID(70), Outcome: model.AttemptNoReading,
			Reason:        "the harness produced no frame timings at all",
			NextAction:    "repair the adapter before another run is scheduled",
			DeliveryRefs:  []model.ArtifactRef{recContent(recDigest('e'), 64)},
			CommitsDenied: true, ReconciliationOwed: false,
		},
		&model.TaskClose{ // R15.1: a closure's authority is optional, so it is a pointer.
			Task: recRef(recID(1), 3), Outcome: model.ClosureCancelled, Authority: ptr(recAuthority(recID(80))),
			AcceptanceWitnessRefs: []model.AcceptanceWitness{{
				CriterionID: recID(21), CriterionRevision: 1, WitnessRef: recContent(recDigest('f'), 32),
			}},
			DeliveryWitnessRefs: []model.ArtifactRef{recGit("internal/acceptance/events_test.go")},
		},
		&model.BlockerHold{
			Task: recRef(recID(1), 2), BlockerID: recID(81), Reason: model.BlockerAwaitingAcceptance,
			Actor: recActor("coordinator"), Criterion: "the owner accepts the delivered fixture",
		},
		&model.BlockerClear{
			Task: recRef(recID(1), 2), BlockerID: recID(81),
			HoldRef:          model.BlockerRef{Task: recRef(recID(1), 2), BlockerID: recID(81)},
			ResolvingWitness: recContent(recDigest('a'), 16),
		},
		&model.InvocationStart{Envelope: recEnvelope()},
		recSeal(),
		&model.SourceIntake{
			SourceID: recID(90), OriginalDigest: recDigest('b'), Length: 2048,
			SourceRef: recContent(recDigest('b'), 2048),
			Speaker:   recActor("owner"), Order: 0,
			Referents: []model.RecordRef{recRef(recID(91), 1)},
		},
		&model.ClaimAssert{Provenance: recProvenance(), ID: recID(2), Spec: recClaimSpec()},
		&model.ClaimRevise{Provenance: recProvenance(), Target: recRef(recID(2), 2), Replacement: recClaimSpec()},
		recCriterionFix(),
		recProof(),
		&model.DecisionOpen{Provenance: recProvenance(), ID: recID(3), Spec: recDecisionSpec()},
		&model.DecisionRevise{Provenance: recProvenance(), Target: recRef(recID(3), 2), Replacement: recDecisionSpec()},
		&model.DecisionDispose{
			Decision: recRef(recID(3), 2), Disposition: "approved",
			Quote:     "refuse observed fields in a start",
			Scope:     recScope(recID(92)),
			Authority: recAuthority(recID(93)),
		},
		recSupersede(),
		recCorrection(),
		&model.InstrumentDeclare{Provenance: recProvenance(), ID: recID(4), Spec: recInstrumentSpec()},
		&model.InstrumentRevise{Provenance: recProvenance(), Target: recRef(recID(4), 2), Replacement: recInstrumentSpec()},
		recTrustWithdraw(),
		&model.ReviewAdmit{
			Packets: []model.PacketRef{{CommandID: recID(94), Digest: recDigest('a')}},
			Outcome: "correction-requested", Actor: recActor("coordinator"),
			Reason: "the packet cites a revision that was never admitted",
			// R18.2: every review carries authors, captured_at and event_packets.
			Authors:    map[model.ID]model.Actor{recID(94): recActor("lane")},
			CapturedAt: map[model.ID]model.Availability[time.Time]{recID(94): {State: model.Unknown, Reason: "not recorded"}}, EventPackets: []model.ID{},
		},
		&model.ArtifactDispose{
			Artifact:         recContent(recDigest('b'), 2048),
			Digest:           recDigest('b'),
			PreviousLocation: ".whosaidso/artifacts/" + string(recDigest('b')),
			SupportLoss: []model.SupportLoss{{
				Target: recRef(recID(95), 1), Reason: "the only observation behind this claim is gone",
			}},
			Authority: recAuthority(recID(96)),
		},
	}
}

// ---- attacks --------------------------------------------------------------

func TestEventsEveryMemberOfTheClosedSetRoundTripsThroughItsOwnTypedPayload(t *testing.T) {
	seen := map[model.EventType]bool{}
	for _, payload := range evAll() {
		payload := payload
		t.Run(string(payload.EventType()), func(t *testing.T) {
			if seen[payload.EventType()] {
				t.Fatalf("the fixture set declares %s twice", payload.EventType())
			}
			e, err := model.EncodeEvent(payload)
			if err != nil {
				t.Fatalf("a valid %s payload was refused by EncodeEvent: %v", payload.EventType(), err)
			}
			if e.Type != payload.EventType() {
				t.Fatalf("encoded as %s, authored as %s", e.Type, payload.EventType())
			}
			decoded, err := model.DecodeEvent(e)
			if err != nil {
				t.Fatalf("a valid %s payload was refused by DecodeEvent: %v", payload.EventType(), err)
			}
			if reflect.TypeOf(decoded) != reflect.TypeOf(payload) {
				t.Fatalf("%s decoded into %T, authored as %T", e.Type, decoded, payload)
			}
			again, err := model.EncodeEvent(decoded)
			if err != nil {
				t.Fatalf("the decoded %s no longer encodes: %v", e.Type, err)
			}
			if !bytes.Equal(again.Data, e.Data) {
				t.Fatalf("%s did not survive a round trip:\n before %s\n after  %s", e.Type, e.Data, again.Data)
			}
			if _, err := model.EventReferences(decoded); err != nil {
				t.Fatalf("%s has no typed reference walker: %v", e.Type, err)
			}
		})
		seen[payload.EventType()] = true
	}
	for _, want := range evClosedSet {
		if !seen[want] {
			t.Errorf("the closed set names %s and no fixture covers it", want)
		}
	}
	for got := range seen {
		found := false
		for _, want := range evClosedSet {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is decodable but is not a member of the closed set", got)
		}
	}
}

func TestEventsRefuseTypeNamesOutsideTheClosedSetIncludingCaseAndSpacingVariants(t *testing.T) {
	good, err := model.EncodeEvent(recStart())
	if err != nil {
		t.Fatalf("control: a valid task.start must encode: %v", err)
	}
	if _, err := model.DecodeEvent(good); err != nil {
		t.Fatalf("control: a valid task.start must decode: %v", err)
	}
	for _, name := range []model.EventType{
		"", " ", "task.Create", "TASK.START", "Task.Start", "task.start ", " task.start",
		model.EventType("task.start" + string(rune(0x200b))), "record.update", "task.accept", "reconciliation",
		"task_start", "task.starts", "task.start\n",
	} {
		t.Run(fmt.Sprintf("%q", string(name)), func(t *testing.T) {
			decoded, err := model.DecodeEvent(model.Event{Type: name, Data: good.Data})
			if err == nil {
				t.Fatalf("the event type %q was accepted and decoded as %T", string(name), decoded)
			}
			if code := recCode(err); code != "unknown-event" {
				t.Errorf("the event type %q was refused with code %q, want unknown-event: %v", string(name), code, err)
			}
		})
	}
}

func TestEventsDataMustBeOneAccountableObjectWithNoUnknownOrRepeatedKeys(t *testing.T) {
	good, err := model.EncodeEvent(recStart())
	if err != nil {
		t.Fatalf("control: a valid task.start must encode: %v", err)
	}
	recMustAccept(t, "the unmodified task.start control", good)

	body := string(good.Data)
	cases := []struct {
		name string
		data string
	}{
		{"an unknown field beside the known ones", `{"status":"IN FLIGHT",` + body[1:]},
		{"a repeated key that a last-wins decoder would resolve silently", `{"actor":{"id":"someone-else"},` + body[1:]},
		{"a repeated attempt id", `{"attempt_id":"` + string(recID(99)) + `",` + body[1:]},
		{"null instead of an object", `null`},
		{"an array instead of an object", `[]`},
		{"a bare string", `"task.start"`},
		{"trailing content after the payload", body + `{}`},
		{"a second payload concatenated", body + body},
		{"nothing at all", ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recMustRefuse(t, "task.start data with "+c.name, model.Event{Type: "task.start", Data: []byte(c.data)})
		})
	}

	t.Run("a case variant of a known field name", func(t *testing.T) {
		aliased := recDrop(t, recSet(t, good, "Actor", map[string]any{"id": "lane-e"}), "actor")
		recMustRefuse(t, "task.start whose actor key is spelled Actor", aliased)
	})
	t.Run("an unknown nested field", func(t *testing.T) {
		recMustRefuse(t, "task.start whose task reference carries an extra key",
			recSet(t, good, "task.branch", "main"))
	})
}

func TestEventsInvocationStartCannotCarryObservationsOnlyASealCanHave(t *testing.T) {
	// Control: the honest pre-launch envelope encodes, and the same fields are
	// legitimate once the run is sealed.
	if _, err := model.EncodeEvent(&model.InvocationStart{Envelope: recEnvelope()}); err != nil {
		t.Fatalf("control: an honest pre-launch invocation.start must encode: %v", err)
	}
	if _, err := model.EncodeEvent(recSeal()); err != nil {
		t.Fatalf("control: a sealed invocation must encode: %v", err)
	}

	observed := []struct {
		name  string
		apply func(*model.InvocationEnvelope)
	}{
		{"an observed terminal outcome", func(e *model.InvocationEnvelope) {
			e.Outcome = recKnown(model.ProcessOutcome{Kind: "exit", ExitCode: ptr(0)})
		}},
		{"an observation time", func(e *model.InvocationEnvelope) {
			e.ObservedAt = recKnown(recWhen.Add(time.Minute))
		}},
		{"sealed outputs", func(e *model.InvocationEnvelope) {
			e.Outputs = recKnown([]model.RunOutput{{Name: "out/result.json", SHA256: recDigest('d'), Length: 8, MediaType: "application/json"}})
		}},
		{"observed conditions", func(e *model.InvocationEnvelope) {
			e.ConditionsObserved = recKnown(map[string]model.Availability[model.Scalar]{
				"max_depth": recKnown(recNumber("64")),
			})
		}},
		{"an effective configuration the process never reported", func(e *model.InvocationEnvelope) {
			e.ConfigEffective = recKnown(map[string]model.Availability[model.Scalar]{
				"strict": recKnown(recBool(true)),
			})
		}},
		{"an observed clean isolation", func(e *model.InvocationEnvelope) {
			e.Isolation = recKnown(model.IsolationClean)
		}},
	}
	for _, c := range observed {
		t.Run(c.name, func(t *testing.T) {
			envelope := recEnvelope()
			c.apply(&envelope)
			if _, err := model.EncodeEvent(&model.InvocationStart{Envelope: envelope}); err == nil {
				t.Errorf("invocation.start declared %s before the process was launched. "+
					"pre-launch intent cannot carry a fact only the run can supply", c.name)
			}
			sealed := recSealedEnvelope()
			c.apply(&sealed)
			if _, err := model.EncodeEvent(&model.InvocationSeal{
				StartRef: model.InvocationRef{Project: recProject, InvocationID: sealed.InvocationID},
				Envelope: sealed,
			}); err != nil {
				t.Errorf("the same fact must remain legal in a seal: %v", err)
			}
		})
	}
}

func TestEventsSealAndBlockerClearMustLinkTheirOwnSubject(t *testing.T) {
	seal := recEncode(t, recSeal())
	clear := recEncode(t, &model.BlockerClear{
		Task: recRef(recID(1), 2), BlockerID: recID(81),
		HoldRef:          model.BlockerRef{Task: recRef(recID(1), 2), BlockerID: recID(81)},
		ResolvingWitness: recContent(recDigest('a'), 16),
	})

	t.Run("seal naming another invocation", func(t *testing.T) {
		recMustRefuse(t, "invocation.seal whose start_ref names a different invocation",
			recSet(t, seal, "start_ref.invocation_id", string(recID(97))))
	})
	t.Run("seal naming another project", func(t *testing.T) {
		recMustRefuse(t, "invocation.seal whose start_ref names a different project",
			recSet(t, seal, "start_ref.project", "datum/elsewhere"))
	})
	t.Run("clear naming another blocker", func(t *testing.T) {
		recMustRefuse(t, "blocker.clear whose hold_ref names a different blocker",
			recSet(t, clear, "hold_ref.blocker_id", string(recID(98))))
	})
	t.Run("clear naming another task revision", func(t *testing.T) {
		recMustRefuse(t, "blocker.clear whose hold_ref names another task revision",
			recSet(t, clear, "hold_ref.task.revision", json.Number("3")))
	})
	t.Run("takeover reusing the prior attempt id", func(t *testing.T) {
		takeover := recEncode(t, &model.TaskTakeover{
			Task: recRef(recID(1), 2), Actor: recActor("lane-b"), AttemptID: recID(71),
			PriorAttemptID: recID(70), StoppedConfirmationRef: recGit(".whosaidso/handback/lane-e.json"),
		})
		recMustRefuse(t, "task.takeover whose new attempt reuses the prior attempt id",
			recSet(t, takeover, "attempt_id", string(recID(70))))
	})
}

func TestEventsProofFamilyIsCompleteDistinctAndAboutExactlyOneAssertion(t *testing.T) {
	proof := recEncode(t, recProof())
	recMustAccept(t, "a proof naming one assertion and two distinct observations", proof)

	t.Run("empty evidence family", func(t *testing.T) {
		recMustRefuse(t, "proof.admit with no evidence at all", recSet(t, proof, "evidence", []any{}))
	})
	t.Run("the same observation counted twice", func(t *testing.T) {
		var evidence []any
		if err := json.Unmarshal(mustField(t, proof.Data, "evidence"), &evidence); err != nil {
			t.Fatalf("fixture evidence is not an array: %v", err)
		}
		recMustRefuse(t, "proof.admit counting one observation twice",
			recSet(t, proof, "evidence", []any{evidence[0], evidence[0]}))
	})
	t.Run("a criterion belonging to another assertion", func(t *testing.T) {
		recMustRefuse(t, "proof.admit whose criterion names a different claim",
			recSet(t, proof, "criterion_ref.claim.record_id", string(recID(99))))
	})
	t.Run("a criterion at another revision of the same assertion", func(t *testing.T) {
		recMustRefuse(t, "proof.admit whose criterion names another revision of the claim",
			recSet(t, proof, "criterion_ref.claim.revision", json.Number("2")))
	})
	t.Run("an anonymous judgment", func(t *testing.T) {
		recMustRefuse(t, "proof.admit whose judgment names no responsible actor",
			recSet(t, proof, "judgment.actor", map[string]any{"unknown_reason": "no one wanted to sign this"}))
	})
	t.Run("a disposition outside the union", func(t *testing.T) {
		recMustRefuse(t, "proof.admit with a disposition of probably",
			recSet(t, proof, "evidence.[0].disposition", "probably"))
	})
}

func TestEventsReferenceWalkerReturnsEveryAuthoredLinkInEachPayload(t *testing.T) {
	for _, payload := range evAll() {
		payload := payload
		t.Run(string(payload.EventType()), func(t *testing.T) {
			walked, err := model.EventReferences(payload)
			if err != nil {
				t.Fatalf("EventReferences refused a valid payload: %v", err)
			}
			got := evFingerprints(walked)
			want := evReachable(reflect.ValueOf(payload))
			sort.Strings(got)
			sort.Strings(want)
			if strings.Join(got, "\n") == strings.Join(want, "\n") {
				return
			}
			missing, extra := evDiff(want, got)
			if len(missing) > 0 {
				t.Errorf("%s carries links the typed walker never returns, so admission and query cannot see them:\n  %s",
					payload.EventType(), strings.Join(missing, "\n  "))
			}
			if len(extra) > 0 {
				t.Errorf("%s: the walker returned links that are not in the payload:\n  %s",
					payload.EventType(), strings.Join(extra, "\n  "))
			}
		})
	}
}

// ---- helpers for the walker comparison ------------------------------------

func evFingerprints(refs []model.Reference) []string {
	out := []string{}
	for _, r := range refs {
		switch {
		case r.Record != nil:
			out = append(out, evRecordKey(*r.Record))
		case r.Criterion != nil:
			out = append(out, evCriterionKey(*r.Criterion))
		case r.Invocation != nil:
			out = append(out, fmt.Sprintf("invocation %s/%s", r.Invocation.Project, r.Invocation.InvocationID))
		case r.Blocker != nil:
			out = append(out, fmt.Sprintf("blocker %s in %s", r.Blocker.BlockerID, evRecordKey(r.Blocker.Task)))
		default:
			out = append(out, "reference with no branch set at "+r.Path)
		}
	}
	return out
}

func evRecordKey(r model.RecordRef) string {
	return fmt.Sprintf("record %s/%s@%d", r.Project, r.RecordID, r.Revision)
}

func evCriterionKey(c model.CriterionRef) string {
	return fmt.Sprintf("criterion %s@%d of %s", c.CriterionID, c.Revision, evRecordKey(c.Claim))
}

var (
	evRecordType     = reflect.TypeOf(model.RecordRef{})
	evCriterionType  = reflect.TypeOf(model.CriterionRef{})
	evInvocationType = reflect.TypeOf(model.InvocationRef{})
	evBlockerType    = reflect.TypeOf(model.BlockerRef{})
)

// evReachable finds every reference a payload actually contains, independently
// of the walker under test. A composite reference is recorded whole and not
// descended into, because that is how the walker reports it.
func evReachable(v reflect.Value) []string {
	out := []string{}
	if !v.IsValid() {
		return out
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return out
		}
		return evReachable(v.Elem())
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			out = append(out, evReachable(v.Index(i))...)
		}
		return out
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			out = append(out, evReachable(iter.Value())...)
		}
		return out
	case reflect.Struct:
		switch v.Type() {
		case evRecordType:
			return []string{evRecordKey(v.Interface().(model.RecordRef))}
		case evCriterionType:
			return []string{evCriterionKey(v.Interface().(model.CriterionRef))}
		case evInvocationType:
			r := v.Interface().(model.InvocationRef)
			return []string{fmt.Sprintf("invocation %s/%s", r.Project, r.InvocationID)}
		case evBlockerType:
			r := v.Interface().(model.BlockerRef)
			return []string{fmt.Sprintf("blocker %s in %s", r.BlockerID, evRecordKey(r.Task))}
		}
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			out = append(out, evReachable(v.Field(i))...)
		}
		return out
	}
	return out
}

func evDiff(want, got []string) (missing, extra []string) {
	count := map[string]int{}
	for _, w := range want {
		count[w]++
	}
	for _, g := range got {
		count[g]--
	}
	for key, n := range count {
		for ; n > 0; n-- {
			missing = append(missing, key)
		}
		for ; n < 0; n++ {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func mustField(t *testing.T, data []byte, key string) []byte {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("fixture is not an object: %v", err)
	}
	v, ok := obj[key]
	if !ok {
		t.Fatalf("fixture has no %q", key)
	}
	return v
}
