package reduce

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"datum/internal/model"
)

func proofKnown[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}

func proofUnknown[T any]() model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: "not observed"}
}

func proofInstrument() model.InstrumentSpec {
	return model.InstrumentSpec{
		QuestionAnswered: "does replay preserve the fixture answer", BlindTo: "production logs",
		NotAnswered: "filesystem durability", ConfigSurface: []string{}, DangerousDefaults: []string{},
		ValidRange: "these fixture inputs", ImplementationRef: blobRef("instrument"),
		Validation: proofKnown(model.InstrumentValidation{Ref: blobRef("validation"), Version: "1"}),
	}
}

func proofCriterion(claim model.RecordRef) model.CriterionRef {
	return model.CriterionRef{Claim: claim, CriterionID: newID("CRTA"), Revision: 1}
}

func fixProofCriterion(claim model.RecordRef) *model.CriterionFix {
	n := json.Number("0")
	return &model.CriterionFix{
		Claim: claim, CriterionID: newID("CRTA"), Revision: 1,
		Expression: model.CriterionExpression{
			ResultSelector: blobRef("result"), Unit: "failures",
			Population: model.Population{Identity: "all cases", Selector: blobRef("result"), Denominator: "all cases"},
			Operator:   model.Equal, Target: model.Scalar{Type: "number", Number: &n}, Reducer: model.All,
		},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author: model.Actor{ID: "lane-a"}, SourceRefs: []model.ArtifactRef{blobRef("criterion")},
	}
}

func proofEnvelope(claim model.RecordRef, id model.ID) model.InvocationEnvelope {
	return model.InvocationEnvelope{
		InvocationID: id, AttemptID: newID("ATTA"), InstrumentRef: ref(newID("HNSS"), 1),
		CriterionRef: proofKnown(proofCriterion(claim)),
		ExecutionSourceIdentity: model.ExecutionIdentity{
			Project: testProject, MachineID: proofUnknown[model.ID](), SourceRefs: []model.ArtifactRef{blobRef("source")},
			Head: proofUnknown[model.GitHead](), Dirty: proofUnknown[bool](),
		},
		Argv: []string{"fixture-runner"}, InputRefs: []model.ArtifactRef{},
		ConfigRequested: map[string]model.Scalar{}, ConfigEffective: proofUnknown[map[string]model.Availability[model.Scalar]](),
		ConditionsDeclared: map[string]model.Scalar{}, ConditionsObserved: proofUnknown[map[string]model.Availability[model.Scalar]](),
		Isolation: proofUnknown[model.Isolation](), StartedAt: baseTime.Add(time.Hour),
		ObservedAt: proofUnknown[time.Time](), Outcome: proofUnknown[model.ProcessOutcome](),
		OutputRefs: proofUnknown[[]model.ArtifactRef](), Visual: proofUnknown[model.VisualObservation](),
	}
}

func sealProof(env model.InvocationEnvelope, exit int) *model.InvocationSeal {
	env.ObservedAt = proofKnown(env.StartedAt.Add(time.Second))
	env.Outcome = proofKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	env.OutputRefs = proofKnown([]model.ArtifactRef{blobRef("result")})
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: testProject, InvocationID: env.InvocationID}, Envelope: env}
}

func admitProof(claim model.RecordRef, invocation model.ID) *model.ProofAdmit {
	return &model.ProofAdmit{
		Claim: claim, CriterionRef: proofCriterion(claim),
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: testProject, InvocationID: invocation}, Disposition: "supports", Reason: "all cases satisfy the criterion"}},
		Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "reviewer"}, Reason: "the scoped criterion holds"},
		Verdict:  model.VerdictSupports,
	}
}

func proofLedger(t *testing.T, proven bool) *ledgerBuilder {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	env := proofEnvelope(claim, newID("RNA"))
	l.add(t, &model.InvocationStart{Envelope: env})
	l.add(t, sealProof(env, 0))
	if proven {
		l.add(t, admitProof(claim, env.InvocationID))
	}
	return l
}

func wantClaim(t *testing.T, s Snapshot, target model.RecordRef, status ClaimStatus) ClaimProjection {
	t.Helper()
	p, ok := s.ClaimAt(target)
	if !ok || p.Status != status {
		t.Fatalf("claim %v = %+v, exists %v, want %s", target, p, ok, status)
	}
	return p
}

func supportNow(t *testing.T, s Snapshot, target model.RecordRef) SupportFacts {
	t.Helper()
	p, ok := s.Support(target, SupportContext{EvidenceAvailable: TruthTrue, ScopeApplicable: TruthTrue})
	if !ok {
		t.Fatalf("missing support projection for %v", target)
	}
	return p
}

func TestU06ClaimAchievementAndRevision(t *testing.T) {
	l := proofLedger(t, true)
	claim := ref(newID("CMA1"), 1)
	wantClaim(t, mustReplay(t, l.out[:3]), claim, StatusUnmeasured)
	wantClaim(t, mustReplay(t, l.out[:4]), claim, StatusUnmeasured)
	wantClaim(t, mustReplay(t, l.out[:5]), claim, StatusMeasured)
	s := mustReplay(t, l.out)
	p := wantClaim(t, s, claim, StatusProven)
	if len(p.Proofs) != 1 || p.Support.EvidenceAvailable != TruthUnknown || p.Support.Current() != TruthUnknown {
		t.Fatalf("proof history or unchecked evidence was lost: %+v", p)
	}
	if got := supportNow(t, s, claim).Current(); got != TruthTrue {
		t.Fatalf("good current support = %s", got)
	}
	replacement := claimSpec()
	replacement.Assertion = "a different assertion needs its own observations"
	l.add(t, &model.ClaimRevise{Target: claim, ExpectedRevision: 1, Provenance: provenance("lane-a"), Replacement: replacement})
	revised := mustReplay(t, l.out)
	current, ok := revised.Claim(ident(claim))
	if !ok || current.Claim.Revision != 2 || current.Status != StatusUnmeasured || len(current.Proofs) != 0 {
		t.Fatalf("revision inherited proof: %+v", current)
	}
	wantClaim(t, revised, claim, StatusProven)
	if supportNow(t, revised, claim).ApplicableScope != TruthFalse {
		t.Fatal("old proof is applicable to current assertion")
	}
	wantClaim(t, s, claim, StatusProven)
}

func TestU06FailedAndUnavailableObservations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*model.InvocationSeal)
		want   ClaimStatus
	}{
		{"failed observation", func(s *model.InvocationSeal) {
			n := 7
			s.Envelope.Outcome = proofKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &n})
		}, StatusMeasured},
		{"observer died", func(s *model.InvocationSeal) { s.Envelope.Outcome = proofUnknown[model.ProcessOutcome]() }, StatusUnmeasured},
		{"no output known", func(s *model.InvocationSeal) { s.Envelope.OutputRefs = proofUnknown[[]model.ArtifactRef]() }, StatusUnmeasured},
		{"spawn failed", func(s *model.InvocationSeal) {
			d := "no executable"
			s.Envelope.Outcome = proofKnown(model.ProcessOutcome{Kind: "spawn-failed", Diagnostic: &d})
		}, StatusUnmeasured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := proofLedger(t, false)
			claim := ref(newID("CMA1"), 1)
			wantClaim(t, mustReplay(t, l.out), claim, StatusMeasured)
			seal := sealProof(proofEnvelope(claim, newID("RNA")), 0)
			tc.change(seal)
			raw, err := model.EncodeEvent(seal)
			if err != nil {
				t.Fatal(err)
			}
			l.out[4].Events[0] = raw
			wantClaim(t, mustReplay(t, l.out), claim, tc.want)
		})
	}
}

func TestU06ImportedConfidence(t *testing.T) {
	l := proofLedger(t, true)
	wantClaim(t, mustReplay(t, l.out), ref(newID("CMA1"), 1), StatusProven)
	for i, tag := range []string{"VERIFIED", "VENDOR CLAIM", "REPORTED MEASUREMENT"} {
		spec := claimSpec()
		source := model.RecordRef{Project: "elsewhere", RecordID: newID("CMA1"), Revision: 99}
		spec.ExternalRefs = []model.ExternalReference{{Tag: tag, Citation: "reported PROVEN elsewhere", RecordRef: &source}}
		id := []model.ID{newID("CMA2"), newID("CMA3"), newID("CMA4")}[i]
		l.add(t, &model.ClaimAssert{ID: id, Provenance: provenance("importer"), Spec: spec})
		p := wantClaim(t, mustReplay(t, l.out), ref(id, 1), StatusUnmeasured)
		if p.Spec.ExternalRefs[0].Tag != tag {
			t.Fatal("external tag changed")
		}
	}
}

func withdrawal() *model.TrustWithdraw {
	return &model.TrustWithdraw{Instrument: ref(newID("HNSS"), 1), Scope: testScope(), RevalidationCondition: "repeat validation with the repaired instrument"}
}

func TestU06WithdrawnInstrument(t *testing.T) {
	l := proofLedger(t, true)
	before := mustReplay(t, l.out)
	claim := ref(newID("CMA1"), 1)
	if supportNow(t, before, claim).Current() != TruthTrue {
		t.Fatal("good proof lacks support")
	}
	b := l.add(t, withdrawal())
	after, err := Apply(before, b)
	if err != nil {
		t.Fatal(err)
	}
	wantClaim(t, after, claim, StatusProven)
	facts := supportNow(t, after, claim)
	if facts.Current() != TruthFalse || facts.ActiveTrust != TruthFalse || facts.EvidenceAvailable != TruthTrue || facts.CorrectionFree != TruthTrue {
		t.Fatalf("withdrawal collapsed distinct facts: %+v", facts)
	}
	instrument, ok := after.Instrument(ident(ref(newID("HNSS"), 1)))
	if !ok || len(instrument.Withdrawals) != 1 || instrument.Support.ActiveTrust != TruthFalse {
		t.Fatalf("trust history missing: %+v", instrument)
	}
	if supportNow(t, before, claim).Current() != TruthTrue {
		t.Fatal("Apply mutated prior snapshot")
	}
	l.add(t, &model.InstrumentRevise{Target: ref(newID("HNSS"), 1), ExpectedRevision: 1, Provenance: provenance("lane-a"), Replacement: proofInstrument()})
	revised := mustReplay(t, l.out)
	instrument, _ = revised.Instrument(ident(ref(newID("HNSS"), 1)))
	if instrument.Instrument.Revision != 2 || instrument.Support.ActiveTrust != TruthTrue {
		t.Fatalf("new instrument inherited withdrawal: %+v", instrument)
	}
	if supportNow(t, revised, claim).ActiveTrust != TruthFalse {
		t.Fatal("new validation repaired old proof")
	}
}

func TestU06DisposedSupport(t *testing.T) {
	l := proofLedger(t, true)
	claim := ref(newID("CMA1"), 1)
	if supportNow(t, mustReplay(t, l.out), claim).Current() != TruthTrue {
		t.Fatal("good proof lacks support")
	}
	artifact := blobRef("result")
	artifact.Content.Locators = []model.Locator{{Path: "moved/result"}}
	l.add(t, &model.ArtifactDispose{Artifact: artifact, Digest: artifact.Content.SHA256, PreviousLocation: "moved/result", SupportLoss: []model.SupportLoss{}, Authority: rulingAuthority("owner")})
	after := mustReplay(t, l.out)
	wantClaim(t, after, claim, StatusProven)
	facts := supportNow(t, after, claim)
	if facts.Current() != TruthFalse || facts.EvidenceAvailable != TruthFalse || facts.ActiveTrust != TruthTrue || facts.CorrectionFree != TruthTrue {
		t.Fatalf("disposal was lost or conflated with correction: %+v", facts)
	}
	if len(after.ArtifactDisposals()) != 1 || len(after.Deferred()) != 0 {
		t.Fatal("disposal history is missing or still deferred")
	}
}

func TestU06TransitiveCorrectionCycle(t *testing.T) {
	l := proofLedger(t, true)
	a := ref(newID("CMA1"), 1)
	b := ref(newID("CMA2"), 1)
	c := ref(newID("CMA3"), 1)
	for _, pair := range [][2]model.RecordRef{{b, a}, {c, b}} {
		spec := claimSpec()
		spec.Scope.ContextRefs = []model.RecordRef{pair[1]}
		l.add(t, &model.ClaimAssert{ID: pair[0].RecordID, Provenance: provenance("lane-a"), Spec: spec}, fixProofCriterion(pair[0]))
		env := proofEnvelope(pair[0], pair[0].RecordID)
		l.add(t, &model.InvocationStart{Envelope: env}, sealProof(env, 0), admitProof(pair[0], env.InvocationID))
	}
	before := mustReplay(t, l.out)
	if supportNow(t, before, c).Current() != TruthTrue {
		t.Fatal("good chain lacks support")
	}
	// A proof points back to its criterion and the criterion points to its claim.
	// Traversal must terminate even before considering user-authored link cycles.
	correction := &model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: &a}, AffectedRevisions: []model.RecordRef{a}, Reason: "counterexample refutes the assertion", CorrectiveRef: blobRef("counterexample")}
	l.add(t, correction)
	after := mustReplay(t, l.out)
	for _, target := range []model.RecordRef{a, b, c} {
		wantClaim(t, after, target, StatusProven)
		facts := supportNow(t, after, target)
		if facts.CorrectionFree != TruthFalse || facts.Current() != TruthFalse || len(facts.Losses) != 1 {
			t.Fatalf("correction did not terminate and propagate once: %+v", facts)
		}
	}
	if len(after.Corrections()) != 1 {
		t.Fatal("correction history missing")
	}
	if !reflect.DeepEqual(after.Claims(), mustReplay(t, l.out).Claims()) {
		t.Fatal("replay is nondeterministic")
	}
	if supportNow(t, before, c).Current() != TruthTrue {
		t.Fatal("prior support mutated")
	}
}

func TestU06DecisionDispositionsAndSupersession(t *testing.T) {
	l := newLedger()
	d := ref(newID("DCSA"), 1)
	l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
	s := mustReplay(t, l.out)
	p, ok := s.Decision(ident(d))
	if !ok || p.Status != StatusOpen || p.Spec.WaitingActor.ID != "owner" {
		t.Fatalf("open decision = %+v", p)
	}
	for _, disposition := range []string{"approved", "rejected", "withdrawn"} {
		l.add(t, &model.DecisionDispose{Decision: d, Disposition: disposition, Quote: "  exact ruling  ", Scope: testScope(), Authority: rulingAuthority("owner")})
		p, ok = mustReplay(t, l.out).Decision(ident(d))
		if !ok || p.Status != StatusDecided || p.Dispositions[len(p.Dispositions)-1].Disposition.Disposition != disposition || p.Dispositions[0].Disposition.Quote != "  exact ruling  " {
			t.Fatalf("decision = %+v", p)
		}
	}
	l.add(t, &model.DecisionRevise{Target: d, ExpectedRevision: 1, Replacement: decisionSpec(), Provenance: provenance("author")})
	s = mustReplay(t, l.out)
	p, _ = s.Decision(ident(d))
	if p.Status != StatusOpen || len(p.Dispositions) != 0 {
		t.Fatalf("decision revision inherited disposition: %+v", p)
	}
	l.add(t, &model.Supersede{Prior: d, Replacement: ref(d.RecordID, 2), Reason: "question changed", Authority: ptrProof(rulingAuthority("owner"))})
	s = mustReplay(t, l.out)
	p, _ = s.DecisionAt(d)
	if p.Status != StatusDecided || len(p.Dispositions) != 3 || len(s.Supersessions()) != 1 || len(s.Deferred()) != 0 {
		t.Fatalf("supersession erased history: %+v", p)
	}
	if supportNow(t, s, d).ApplicableScope != TruthFalse {
		t.Fatal("superseded decision retains current applicability")
	}
}

func ptrProof[T any](v T) *T { return &v }

func TestU06RefusesUnrelatedProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*model.ProofAdmit)
	}{
		{"foreign evidence", func(p *model.ProofAdmit) { p.Evidence[0].InvocationRef.Project = "elsewhere" }},
		{"no supporting member", func(p *model.ProofAdmit) { p.Evidence[0].Disposition = "inconclusive" }},
		{"contradicting member", func(p *model.ProofAdmit) { p.Evidence[0].Disposition = "contradicts" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := proofLedger(t, true)
			mustReplay(t, l.out)
			p := admitProof(ref(newID("CMA1"), 1), newID("RNA"))
			tc.change(p)
			raw, err := model.EncodeEvent(p)
			if err != nil {
				t.Fatal(err)
			}
			l.out[5].Events[0] = raw
			_, err = Replay(l.out)
			wantFault(t, err, CodeInvalidTransition)
		})
	}
}

func TestU06ChangedSealCannotEstablishObservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*model.InvocationSeal)
	}{
		{"argv", func(s *model.InvocationSeal) { s.Envelope.Argv = []string{"different-command"} }},
		{"criterion", func(s *model.InvocationSeal) { s.Envelope.CriterionRef = proofUnknown[model.CriterionRef]() }},
		{"source", func(s *model.InvocationSeal) {
			s.Envelope.ExecutionSourceIdentity.SourceRefs = []model.ArtifactRef{blobRef("other-source")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := proofLedger(t, false)
			wantClaim(t, mustReplay(t, l.out), ref(newID("CMA1"), 1), StatusMeasured)
			seal := sealProof(proofEnvelope(ref(newID("CMA1"), 1), newID("RNA")), 0)
			tc.change(seal)
			raw, err := model.EncodeEvent(seal)
			if err != nil {
				t.Fatal(err)
			}
			l.out[4].Events[0] = raw
			got, err := Replay(l.out)
			wantFault(t, err, CodeInvalidField)
			if !reflect.DeepEqual(got, Snapshot{}) {
				t.Fatal("contradictory seal published a snapshot")
			}
			l.add(t, admitProof(ref(newID("CMA1"), 1), newID("RNA")))
			_, err = Replay(l.out)
			wantFault(t, err, CodeInvalidField)
		})
	}
}

func TestU06RefusesUnscopedDecisionAndWrongKindWithdrawal(t *testing.T) {
	l := proofLedger(t, true)
	d := ref(newID("DCSA"), 1)
	l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
	good := &model.DecisionDispose{Decision: d, Disposition: "approved", Quote: "approved", Scope: testScope(), Authority: rulingAuthority("owner")}
	l.add(t, good)
	before := mustReplay(t, l.out)
	bad := *good
	bad.Scope.AppliesWhen = "an unrelated question"
	_, err := Apply(before, l.add(t, &bad))
	wantFault(t, err, CodeInvalidTransition)
	l = proofLedger(t, true)
	l.add(t, withdrawal())
	mustReplay(t, l.out)
	wrong := withdrawal()
	wrong.Instrument = ref(newID("CMA1"), 1)
	_, err = Apply(mustReplay(t, l.out), l.add(t, wrong))
	wantFault(t, err, CodeInvalidTransition)
}

func TestU06GoldenNineMeanings(t *testing.T) {
	l := proofLedger(t, true)
	l.add(t,
		&model.TaskCreate{ID: newID("TSKB"), Provenance: provenance("lane-a"), Spec: taskSpec()},
		&model.TaskCreate{ID: newID("TSKC"), Provenance: provenance("lane-a"), Spec: taskSpec(withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil))},
		&model.TaskCreate{ID: newID("TSKD"), Provenance: provenance("lane-a"), Spec: taskSpec()},
		closeSuccess(newID("TSKD"), 1),
		&model.ClaimAssert{ID: newID("CMA2"), Provenance: provenance("lane-a"), Spec: claimSpec()},
		&model.ClaimAssert{ID: newID("CMA3"), Provenance: provenance("lane-a"), Spec: claimSpec()},
		fixProofCriterion(ref(newID("CMA3"), 1)),
		&model.DecisionOpen{ID: newID("DCSA"), Provenance: provenance("lane-a"), Spec: decisionSpec()},
		&model.DecisionOpen{ID: newID("DCSB"), Provenance: provenance("lane-a"), Spec: decisionSpec()},
		&model.DecisionDispose{Decision: ref(newID("DCSB"), 1), Disposition: "rejected", Quote: "rejected", Scope: testScope(), Authority: rulingAuthority("owner")},
	)
	env := proofEnvelope(ref(newID("CMA3"), 1), newID("RNB"))
	l.add(t, &model.InvocationStart{Envelope: env}, sealProof(env, 2))
	for i, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		id := []model.ID{newID("PKTA"), newID("PKTB"), newID("PKTC")}[i]
		l.add(t, review(outcome, model.Actor{ID: "reviewer"}, "explicit review result", []model.PacketRef{{CommandID: id, Digest: newDigest(outcome)}}, model.Actor{ID: "lane-a"}))
	}
	s := mustReplay(t, l.out)
	got := []string{}
	for _, p := range s.Claims() {
		got = append(got, "CLAIM "+string(p.Status))
	}
	for _, p := range s.Decisions() {
		got = append(got, "DECISION "+string(p.Status))
	}
	for _, p := range s.Tasks() {
		got = append(got, "TASK "+string(p.Status))
	}
	want := []string{"CLAIM PROVEN", "CLAIM UNMEASURED", "CLAIM MEASURED", "DECISION OPEN", "DECISION DECIDED", "TASK IN FLIGHT", "TASK READY", "TASK BLOCKED", "TASK CLOSED"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("golden meanings = %v, want %v", got, want)
	}
	if _, invented := reflect.TypeOf(InstrumentProjection{}).FieldByName("Status"); invented {
		t.Fatal("instrument acquired a tenth status")
	}
	if s.Instruments()[0].Spec.Validation.State != model.Known {
		t.Fatal("instrument validation vanished")
	}
	for i, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		id := []model.ID{newID("PKTA"), newID("PKTB"), newID("PKTC")}[i]
		if review, ok := s.Review(ReviewKey{Project: testProject, CommandID: id}); !ok || review.Outcome != outcome {
			t.Fatalf("review %s vanished", outcome)
		}
	}
	var incremental Snapshot
	for _, bundle := range l.out {
		var err error
		incremental, err = Apply(incremental, bundle)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(s, incremental) {
		t.Fatal("Replay and Apply disagree")
	}
}

func TestU06TypedCorrectionsAndLateDependents(t *testing.T) {
	for _, kind := range []string{"criterion", "support"} {
		t.Run(kind, func(t *testing.T) {
			l := proofLedger(t, true)
			claim := ref(newID("CMA1"), 1)
			if supportNow(t, mustReplay(t, l.out), claim).Current() != TruthTrue {
				t.Fatal("good proof lacks support")
			}
			target := model.CorrectionTarget{Kind: kind}
			if kind == "criterion" {
				target.Criterion = ptrProof(proofCriterion(claim))
			} else {
				target.Support = &model.SupportLink{Dependent: claim, Evidence: blobRef("result")}
			}
			l.add(t, &model.Correction{Target: target, AffectedRevisions: []model.RecordRef{claim}, Reason: "the cited premise is false", CorrectiveRef: blobRef("corrective")})
			spec := claimSpec()
			spec.Scope.ContextRefs = []model.RecordRef{claim}
			l.add(t, &model.ClaimAssert{ID: newID("CMA2"), Provenance: provenance("lane-a"), Spec: spec})
			s := mustReplay(t, l.out)
			for _, ref := range []model.RecordRef{claim, ref(newID("CMA2"), 1)} {
				if supportNow(t, s, ref).CorrectionFree != TruthFalse {
					t.Fatal("typed correction missed dependent")
				}
			}
			l.add(t, &model.ClaimRevise{Target: claim, ExpectedRevision: 1, Provenance: provenance("lane-a"), Replacement: claimSpec()})
			p := wantClaim(t, mustReplay(t, l.out), ref(claim.RecordID, 2), StatusUnmeasured)
			if p.Support.CorrectionFree != TruthTrue {
				t.Fatal("replacement inherited prior correction through its expected revision")
			}
		})
	}
}

func TestU06LossReachesClosedHistoryWithoutReopeningIt(t *testing.T) {
	l := proofLedger(t, true)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.TaskCreate{ID: newID("TSKB"), Provenance: provenance("lane-a"), Spec: taskSpec(withPrerequisite("claim-proof", claim, "forbid", nil))}, closeSuccess(newID("TSKB"), 1))
	before := mustReplay(t, l.out)
	p, _ := before.Task(ident(ref(newID("TSKB"), 1)))
	if p.Status != StatusClosed || p.Outcome != model.ClosureSuccess {
		t.Fatal("good task is not closed successfully")
	}
	l.add(t, withdrawal())
	after := mustReplay(t, l.out)
	p, _ = after.Task(ident(ref(newID("TSKB"), 1)))
	if p.Status != StatusClosed || p.Outcome != model.ClosureSuccess {
		t.Fatal("support loss erased task achievement")
	}
	if supportNow(t, after, ref(newID("TSKB"), 1)).ActiveTrust != TruthFalse {
		t.Fatal("loss missed the transitive task dependency")
	}
}

func TestU06SupportFactsRemainIndependent(t *testing.T) {
	s := mustReplay(t, proofLedger(t, true).out)
	claim := ref(newID("CMA1"), 1)
	for _, tc := range []struct{ evidence, scope, want Truth }{
		{TruthTrue, TruthTrue, TruthTrue}, {TruthUnknown, TruthTrue, TruthUnknown},
		{TruthFalse, TruthTrue, TruthFalse}, {TruthTrue, TruthUnknown, TruthUnknown}, {TruthTrue, TruthFalse, TruthFalse},
	} {
		facts, _ := s.Support(claim, SupportContext{EvidenceAvailable: tc.evidence, ScopeApplicable: tc.scope})
		if facts.EvidenceAvailable != tc.evidence || facts.ApplicableScope != tc.scope || facts.Current() != tc.want || facts.ActiveTrust != TruthTrue || facts.CorrectionFree != TruthTrue {
			t.Fatalf("independent support facts changed: %+v", facts)
		}
		wantClaim(t, s, claim, StatusProven)
	}
}

func TestU06ProofRefusalIsAtomic(t *testing.T) {
	l := proofLedger(t, true)
	before := mustReplay(t, l.out)
	claim := ref(newID("CMA1"), 1)
	if supportNow(t, before, claim).Current() != TruthTrue {
		t.Fatal("good proof lacks support")
	}
	bad := &model.DecisionDispose{Decision: claim, Disposition: "approved", Quote: "wrong kind", Scope: testScope(), Authority: rulingAuthority("owner")}
	after, err := Apply(before, l.add(t, withdrawal(), bad))
	wantFault(t, err, CodeInvalidTransition)
	if after.Watermark().Sequence != 0 {
		t.Fatal("failed Apply returned partial state")
	}
	if len(before.TrustWithdrawals()) != 0 || supportNow(t, before, claim).Current() != TruthTrue {
		t.Fatal("failed Apply mutated input")
	}
}

func TestU06UnvalidatedAndForeignInstrumentsCannotProve(t *testing.T) {
	for _, name := range []string{"unvalidated", "foreign", "wrong kind"} {
		t.Run(name, func(t *testing.T) {
			l := proofLedger(t, true)
			claim := ref(newID("CMA1"), 1)
			wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
			want := StatusUnmeasured
			if name == "unvalidated" {
				instrument := proofInstrument()
				instrument.Validation = proofUnknown[model.InstrumentValidation]()
				raw, err := model.EncodeEvent(&model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: instrument})
				if err != nil {
					t.Fatal(err)
				}
				l.out[2].Events[0] = raw
				want = StatusMeasured
			} else {
				env := proofEnvelope(claim, newID("RNA"))
				if name == "foreign" {
					env.InstrumentRef.Project = "elsewhere"
				} else {
					env.InstrumentRef = ref(newID("TSKA"), 1)
				}
				start, err := model.EncodeEvent(&model.InvocationStart{Envelope: env})
				if err != nil {
					t.Fatal(err)
				}
				seal, err := model.EncodeEvent(sealProof(env, 0))
				if err != nil {
					t.Fatal(err)
				}
				l.out[3].Events[0], l.out[4].Events[0] = start, seal
			}
			wantClaim(t, mustReplay(t, l.out[:5]), claim, want)
			_, err := Replay(l.out)
			wantFault(t, err, CodeInvalidTransition)
		})
	}
}
