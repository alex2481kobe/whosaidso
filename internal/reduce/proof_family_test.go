package reduce

// Replay-time closure of a proof's evaluation family lives here: omitted and
// unsealed members of the criterion family. Artifact evaluation, pending intake
// and judgment attribution are gate work and are tested in internal/write.

import (
	"errors"
	"testing"
	"time"

	"whosaidso/internal/model"
)

func familyLedger(t *testing.T, sealSecond bool) (*ledgerBuilder, model.RecordRef, model.InvocationEnvelope, model.InvocationEnvelope) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	failed, passed := proofEnvelope(claim, newID("RNA0")), proofEnvelope(claim, newID("RNA1"))
	l.add(t, &model.InvocationStart{Envelope: failed}, &model.InvocationStart{Envelope: passed})
	l.add(t, sealProof(passed, 0))
	if sealSecond {
		l.add(t, sealProof(failed, 1))
	}
	return l, claim, failed, passed
}

func TestU12ProofMustListTheWholeAdmittedFamily(t *testing.T) {
	l, claim, failed, _ := familyLedger(t, true)
	// Control: listing the failed run too, dispositioned, admits.
	control := admitProof(claim, newID("RNA1"))
	control.Evidence = append([]model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: testProject, InvocationID: failed.InvocationID}, Disposition: "inconclusive", Reason: "the judgment reviewed it"}}, control.Evidence...)
	l.add(t, control)
	wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
	// Cherry-picking the passing run alone must be refused on replay.
	l2, claim2, _, _ := familyLedger(t, true)
	l2.add(t, admitProof(claim2, newID("RNA1")))
	_, err := Replay(l2.out)
	if f := wantFault(t, err, CodeIncompleteFamily); f.Path != "evidence" {
		t.Fatalf("cherry-picked proof refused at %s, want evidence", f.Path)
	}
}

func TestU12ProofWaitsForAnUnsealedFamilyMember(t *testing.T) {
	l, claim, failed, _ := familyLedger(t, false)
	proof := admitProof(claim, newID("RNA1"))
	proof.Evidence = append(proof.Evidence, model.ObservationDisposition{InvocationRef: model.InvocationRef{Project: testProject, InvocationID: failed.InvocationID}, Disposition: "inconclusive", Reason: "still running"})
	l.add(t, proof)
	if _, err := Replay(l.out); err == nil {
		t.Fatal("a proof was admitted while a family member had no seal")
	}
	// Omitting the unsealed member is refused too: absence of a seal is not absence of a member.
	l2, claim2, _, _ := familyLedger(t, false)
	l2.add(t, admitProof(claim2, newID("RNA1")))
	if _, err := Replay(l2.out); err == nil {
		t.Fatal("a proof omitting an unsealed family member was admitted")
	}
}

// wantBoth asserts the last bundle of l is refused with code by Replay of the
// whole ledger and by Apply onto the prefix before it, or, for an empty code,
// admitted by both. Each path is reported on its own, so a rule that holds on
// only one of them is named.
func wantBoth(t *testing.T, l *ledgerBuilder, code string) Snapshot {
	t.Helper()
	last := len(l.out) - 1
	replayed, rerr := Replay(l.out)
	applied, aerr := Apply(mustReplay(t, l.out[:last]), l.out[last])
	for _, path := range []struct {
		name string
		err  error
	}{{"replay", rerr}, {"apply", aerr}} {
		var f *model.Fault
		switch {
		case code == "" && path.err != nil:
			t.Errorf("%s: control refused: %v", path.name, path.err)
		case code != "" && (!errors.As(path.err, &f) || f.Code != code):
			t.Errorf("%s: want fault %q, got %v", path.name, code, path.err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	if code == "" && replayed.Watermark() != applied.Watermark() {
		t.Fatalf("replay and apply disagree: %+v vs %+v", replayed.Watermark(), applied.Watermark())
	}
	return replayed
}

func member(id model.ID, disposition string) model.ObservationDisposition {
	return model.ObservationDisposition{InvocationRef: model.InvocationRef{Project: testProject, InvocationID: id}, Disposition: disposition, Reason: "accounted for under the judgment"}
}

// A family member admitted later in the proof's own bundle is still a member:
// sequential state has not seen it yet, the bundle inventory has.
func TestProofFamilyClosureCoversLaterEventsInTheSameBundle(t *testing.T) {
	setup := func() (*ledgerBuilder, model.RecordRef, model.InvocationEnvelope) {
		l, claim, _ := rejectedLedgerBase(t)
		return l, claim, proofEnvelope(claim, newID("RNA2"))
	}
	// Control: the second run before the proof, listed, admits in one bundle.
	l, claim, late := setup()
	proof := admitProof(claim, newID("RNA1"))
	proof.Evidence = append(proof.Evidence, member(late.InvocationID, "inconclusive"))
	l.add(t, &model.InvocationStart{Envelope: late}, sealProof(late, 1), proof)
	wantClaim(t, wantBoth(t, l, ""), claim, StatusProven)
	// The proof first, the sealed run after it: an omission.
	l, claim, late = setup()
	l.add(t, admitProof(claim, newID("RNA1")), &model.InvocationStart{Envelope: late}, sealProof(late, 1))
	wantBoth(t, l, CodeIncompleteFamily)
	// The proof first, an unsealed run after it: the proof waits.
	l, claim, late = setup()
	l.add(t, admitProof(claim, newID("RNA1")), &model.InvocationStart{Envelope: late})
	wantBoth(t, l, CodeInvalidTransition)
	// A later start under another criterion is not a member.
	l, claim, late = setup()
	other := fixProofCriterion(claim)
	other.CriterionID = newID("CRTB")
	l.add(t, other)
	late.CriterionRef = proofKnown(model.CriterionRef{Claim: claim, CriterionID: other.CriterionID, Revision: 1})
	l.add(t, admitProof(claim, newID("RNA1")), &model.InvocationStart{Envelope: late}, sealProof(late, 1))
	wantBoth(t, l, "")
	// A rejected run recorded later in the same bundle cannot be skipped either.
	l, claim, late = setup()
	packet := model.PacketRef{CommandID: newID("PKR9"), Digest: model.HashBytes([]byte("late rejected packet"))}
	judged := attributeFixture(99, []model.TypedEvent{admitProof(claim, newID("RNA1"))})
	l.add(t, append(judged, &model.ReviewAdmit{Packets: []model.PacketRef{packet}, Outcome: "rejected", Actor: model.Actor{ID: "reviewer"}, Reason: "not canonical",
		Authors: map[model.ID]model.Actor{packet.CommandID: {ID: "lane-a"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet.CommandID: knownAt(baseTime)}, EventPackets: []model.ID{},
		Invocations: []model.ReviewedInvocation{{Packet: packet.CommandID, Event: "invocation.start", InvocationID: late.InvocationID, CriterionRef: late.CriterionRef, EnvelopeDigest: model.HashBytes([]byte("start"))}}})...)
	wantBoth(t, l, CodeRejectedFamilyMember)
}

// rejectedLedgerBase is a ledger with one passing sealed run RNA1 and no
// rejected review yet.
func rejectedLedgerBase(t *testing.T) (*ledgerBuilder, model.RecordRef, model.InvocationEnvelope) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	passed := proofEnvelope(claim, newID("RNA1"))
	l.add(t, &model.InvocationStart{Envelope: passed})
	l.add(t, sealProof(passed, 0))
	return l, claim, passed
}

// rejectRun records, through a rejected review, the exact start and seal of a
// run with the given exit code, by their canonical envelope digests.
func rejectRun(t *testing.T, l *ledgerBuilder, env model.InvocationEnvelope, exit int) *model.InvocationSeal {
	t.Helper()
	seal := sealProof(env, exit)
	startDigest, err := model.EnvelopeDigest(env)
	if err != nil {
		t.Fatal(err)
	}
	sealDigest, err := model.EnvelopeDigest(seal.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	packet := model.PacketRef{CommandID: newID("PKR1"), Digest: model.HashBytes([]byte("rejected run"))}
	l.add(t, &model.ReviewAdmit{Packets: []model.PacketRef{packet}, Outcome: "rejected", Actor: model.Actor{ID: "reviewer"}, Reason: "turned away",
		Authors: map[model.ID]model.Actor{packet.CommandID: {ID: "lane-a"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet.CommandID: knownAt(baseTime)}, EventPackets: []model.ID{},
		Invocations: []model.ReviewedInvocation{
			{Packet: packet.CommandID, Event: "invocation.start", InvocationID: env.InvocationID, CriterionRef: env.CriterionRef, EnvelopeDigest: startDigest},
			{Packet: packet.CommandID, Event: "invocation.seal", InvocationID: env.InvocationID, CriterionRef: env.CriterionRef, EnvelopeDigest: sealDigest},
		}})
	return seal
}

// U12 on replay: a run the ledger recorded as rejected and later admitted must
// be admitted with the very start and seal the review recorded.
func TestProofRejectedDigestMustMatchTheAdmittedRun(t *testing.T) {
	for _, tc := range []struct {
		name        string
		admitted    func(env model.InvocationEnvelope) []model.TypedEvent
		want        string
		disposition string
	}{
		{"identical", func(env model.InvocationEnvelope) []model.TypedEvent {
			return []model.TypedEvent{&model.InvocationStart{Envelope: env}, sealProof(env, 1)}
		}, "", "inconclusive"},
		{"substitute seal", func(env model.InvocationEnvelope) []model.TypedEvent {
			return []model.TypedEvent{&model.InvocationStart{Envelope: env}, sealProof(env, 0)}
		}, CodeRejectedFamilyMember, "supports"},
		{"substitute start", func(env model.InvocationEnvelope) []model.TypedEvent {
			env.Argv = []string{"fixture-runner", "--other"}
			return []model.TypedEvent{&model.InvocationStart{Envelope: env}, sealProof(env, 1)}
		}, CodeRejectedFamilyMember, "inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, claim, _ := rejectedLedgerBase(t)
			env := proofEnvelope(claim, newID("RNR1"))
			rejectRun(t, l, env, 1)
			for _, e := range tc.admitted(env) {
				l.add(t, e)
			}
			proof := admitProof(claim, newID("RNA1"))
			proof.Evidence = append(proof.Evidence, member(env.InvocationID, tc.disposition))
			l.add(t, proof)
			wantBoth(t, l, tc.want)
		})
	}
	// A rejected seal and an admitted start with no admitted seal are not the
	// same run: closure refuses first, since the member has no seal.
	l, claim, _ := rejectedLedgerBase(t)
	env := proofEnvelope(claim, newID("RNR1"))
	rejectRun(t, l, env, 1)
	l.add(t, &model.InvocationStart{Envelope: env})
	proof := admitProof(claim, newID("RNA1"))
	l.add(t, proof)
	wantBoth(t, l, CodeInvalidTransition)
}

// R10.3 on replay: a rejected-only member is listed, set aside, never support.
func TestProofRejectedOnlyMemberRules(t *testing.T) {
	for _, disposition := range []string{"inapplicable", "inconclusive", "supports", "contradicts", "omitted"} {
		t.Run(disposition, func(t *testing.T) {
			l, claim, _ := rejectedLedgerBase(t)
			env := proofEnvelope(claim, newID("RNR1"))
			rejectRun(t, l, env, 1)
			proof := admitProof(claim, newID("RNA1"))
			if disposition != "omitted" {
				proof.Evidence = append(proof.Evidence, member(env.InvocationID, disposition))
			}
			l.add(t, proof)
			want := CodeRejectedFamilyMember
			if disposition == "inapplicable" || disposition == "inconclusive" {
				want = ""
			}
			wantBoth(t, l, want)
		})
	}
	// A passing rejected-only run cannot be the proof's only support.
	l, claim, _ := rejectedLedgerBase(t)
	env := proofEnvelope(claim, newID("RNR1"))
	rejectRun(t, l, env, 0)
	proof := admitProof(claim, newID("RNA1"))
	proof.Evidence = []model.ObservationDisposition{member(newID("RNA1"), "inconclusive"), member(env.InvocationID, "supports")}
	l.add(t, proof)
	wantBoth(t, l, CodeRejectedFamilyMember)
}

// A run under an earlier criterion revision, even one started after the new
// revision was fixed, is set aside and never support.
func TestProofEarlierRevisionNeverSupports(t *testing.T) {
	for _, disposition := range []string{"inconclusive", "supports"} {
		t.Run(disposition, func(t *testing.T) {
			l := goodLedger(t)
			claim := ref(newID("CMA1"), 1)
			second := fixProofCriterion(claim)
			second.Revision = 2
			l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
				&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
			l.add(t, second)
			rev2 := model.CriterionRef{Claim: claim, CriterionID: second.CriterionID, Revision: 2}
			old := proofEnvelope(claim, newID("RNA2")) // revision 1, started after revision 2
			l.add(t, &model.InvocationStart{Envelope: old}, sealProof(old, 0))
			current := proofEnvelope(claim, newID("RNA3"))
			current.CriterionRef = proofKnown(rev2)
			l.add(t, &model.InvocationStart{Envelope: current}, sealProof(current, 0))
			proof := admitProof(claim, newID("RNA3"))
			proof.CriterionRef = rev2
			if disposition == "supports" {
				// The earlier-revision run offered as the only support.
				proof.Evidence[0].Disposition = "inconclusive"
			}
			proof.Evidence = append(proof.Evidence, member(old.InvocationID, disposition))
			l.add(t, proof)
			want := CodeInvalidTransition
			if disposition == "inconclusive" {
				want = ""
			}
			wantBoth(t, l, want)
		})
	}
}

// A later bundle never enlarges an earlier proof's family: new runs and new
// rejections of the same criterion leave the admitted proof admitted.
func TestProofFamilyIsNotEnlargedByALaterBundle(t *testing.T) {
	l, claim, _ := rejectedLedgerBase(t)
	l.add(t, admitProof(claim, newID("RNA1")))
	proven := len(l.out)
	later := proofEnvelope(claim, newID("RNA2"))
	rejectRun(t, l, proofEnvelope(claim, newID("RNR1")), 1)
	l.add(t, &model.InvocationStart{Envelope: later}, sealProof(later, 1))
	// Apply each later bundle onto the proven snapshot, then replay it all.
	s := mustReplay(t, l.out[:proven])
	for _, b := range l.out[proven:] {
		var err error
		if s, err = Apply(s, b); err != nil {
			t.Errorf("apply: a later bundle was refused over an earlier proof: %v", err)
			break
		}
	}
	if _, err := Replay(l.out); err != nil {
		t.Fatalf("replay: a later bundle was refused over an earlier proof: %v", err)
	}
	wantClaim(t, s, claim, StatusProven)
	p := wantClaim(t, wantBoth(t, l, ""), claim, StatusProven)
	if len(p.Proofs) != 1 {
		t.Fatalf("proof history changed: %+v", p.Proofs)
	}
	// A new proof now needs the whole, larger family.
	l.add(t, admitProof(claim, newID("RNA1")))
	wantBoth(t, l, CodeIncompleteFamily)
}
