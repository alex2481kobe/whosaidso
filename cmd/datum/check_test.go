package main

// Fresh-process tests for `datum check criterion` and `datum check admission`
// (R19: formerly criterion check and proof check): each verdict is compared
// with what admission then does with the same inputs, each check opens with
// its scope, exits by its result, and leaves the project and intake untouched.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/store"
)

// checkCriterionEvents writes the admitted criterion.fix back out as capture
// would read it, from the ledger itself.
func checkCriterionEvents(t *testing.T, root string) string {
	t.Helper()
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range prefix {
		for _, event := range bundle.Events {
			if event.Type == "criterion.fix" {
				data, err := model.Encode([]model.Event{event})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "criterion.json")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				return path
			}
		}
	}
	t.Fatal("no criterion admitted")
	return ""
}

// checkSealedRun admits one run whose output in its own run directory is body.
func checkSealedRun(t *testing.T, root string, criterion model.CriterionRef, instrument model.RecordRef, attempt model.ID, body string, n int) model.InvocationRef {
	t.Helper()
	unknown := func(why string) model.Availability[map[string]model.Availability[model.Scalar]] {
		return model.Availability[map[string]model.Availability[model.Scalar]]{State: model.Unknown, Reason: why}
	}
	env := model.InvocationEnvelope{InvocationID: cliID(n), AttemptID: attempt, InstrumentRef: instrument, CriterionRef: e2eKnown(criterion),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: "test/cli", SourceRefs: []model.ArtifactRef{}, MachineID: model.Availability[model.ID]{State: model.Unknown, Reason: "fixture"}, Head: model.Availability[model.GitHead]{State: model.Unknown, Reason: "fixture"}, Dirty: model.Availability[bool]{State: model.Unknown, Reason: "fixture"}},
		Argv:                    []string{"/bin/sh", "tools/measure.sh"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{}, ConditionsDeclared: map[string]model.Scalar{},
		ConfigEffective: unknown("not launched"), ConditionsObserved: unknown("not launched"), Isolation: model.Availability[model.Isolation]{State: model.Unknown, Reason: "not enforced"},
		StartedAt: time.Now().UTC(), ObservedAt: model.Availability[time.Time]{State: model.Unknown, Reason: "not launched"}, Outcome: model.Availability[model.ProcessOutcome]{State: model.Unknown, Reason: "not launched"},
		OutputRefs: model.Availability[[]model.ArtifactRef]{State: model.Unknown, Reason: "not launched"}, Visual: model.Availability[model.VisualObservation]{State: model.Unknown, Reason: "numeric"}}
	seal := env
	exit := 0
	seal.ObservedAt, seal.Outcome = e2eKnown(env.StartedAt.Add(time.Millisecond)), e2eKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	output := ".datum/artifacts/runs/" + string(env.InvocationID) + "/out/result.json"
	proofWrite(t, root, output, body)
	seal.OutputRefs = e2eKnown([]model.ArtifactRef{e2ePin(body, output, "application/json")})
	for i, event := range []model.TypedEvent{&model.InvocationStart{Envelope: env}, &model.InvocationSeal{StartRef: model.InvocationRef{Project: "test/cli", InvocationID: env.InvocationID}, Envelope: seal}} {
		if _, err := e2eInvoke(t, root, []model.TypedEvent{event}, "capture", "--command-id", string(cliID(n+1+i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(n+3)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "run", string(cliID(n+1)), string(cliID(n+2))); err != nil {
		t.Fatal(err)
	}
	return model.InvocationRef{Project: "test/cli", InvocationID: env.InvocationID}
}

func checkProofFile(t *testing.T, criterion model.CriterionRef, member model.InvocationRef, disposition string) (string, *model.ProofAdmit) {
	t.Helper()
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Verdict: model.VerdictSupports, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "lane"}, Reason: "judged"},
		Evidence: []model.ObservationDisposition{{InvocationRef: member, Disposition: disposition, Reason: "reviewed"}}}
	event, err := model.EncodeEvent(proof)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := model.Encode([]model.Event{event})
	path := filepath.Join(t.TempDir(), "proof.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, proof
}

// specCriterionScope is COMMAND-SPEC §4's wording, written out rather than
// read from the constant, so a scope that stops naming what was NOT checked
// fails here.
const specCriterionScope = "criterion preview only: instrument validation, proof-family completeness, comparability and admission were NOT checked"

// The criterion check's verdict is the one admission then acts on: TRUE is
// admitted as support, FALSE is refused as a counterexample, UNKNOWN is refused
// as an unsatisfied family.
func TestCriterionCheckVerdictIsWhatAdmissionDoes(t *testing.T) {
	noUnit := strings.Replace(e2ePass, `"unit":"mm",`, "", 1)
	fail := strings.Replace(e2ePass, "0.0100", "0.2000", 1)
	for _, tc := range []struct {
		name, body, verdict, exit, refusal string
	}{
		{"pass", e2ePass, "result: TRUE", "", ""},
		{"fail", fail, "result: FALSE", "exit status 1", "counterevidence-unresolved"},
		{"no unit", noUnit, "result: UNKNOWN", "exit status 3", "criterion-unsatisfied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, criterion, instrument, attempt := e2eWorld(t)
			events := checkCriterionEvents(t, root)
			candidate := filepath.Join(t.TempDir(), "candidate.json")
			if err := os.WriteFile(candidate, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			before := cliTree(t, root)
			out, err := e2eInvoke(t, root, nil, "check", "criterion", "--events", events, "--output", candidate)
			if (tc.exit == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.exit) || !strings.HasPrefix(string(out), specCriterionScope+"\n"+tc.verdict+"\n") {
				t.Fatalf("check criterion must open with its scope and exit by its verdict (%s): %v\n%s", tc.exit, err, out)
			}
			if after := cliTree(t, root); after != before {
				t.Fatalf("criterion check wrote:\n%s\n%s", before, after)
			}
			member := checkSealedRun(t, root, criterion, instrument, attempt, tc.body, 700)
			_, proof := checkProofFile(t, criterion, member, "supports")
			if _, err := e2eInvoke(t, root, []model.TypedEvent{proof}, "capture", "--command-id", string(cliID(710))); err != nil {
				t.Fatal(err)
			}
			_, err = e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(711)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "proof", string(cliID(710)))
			if tc.refusal == "" && err != nil || tc.refusal != "" && (err == nil || !strings.Contains(err.Error(), tc.refusal)) {
				t.Fatalf("check said %q, admission answered %v", tc.verdict, err)
			}
		})
	}
}

// With no --output the check reads the criterion's pinned example; --blob
// supplies it when the pin does not resolve in the project.
func TestCriterionCheckReadsTheExampleOrTheBlob(t *testing.T) {
	root, _, _, _ := e2eWorld(t)
	events := checkCriterionEvents(t, root)
	if out, err := e2eInvoke(t, root, nil, "check", "criterion", "--events", events); err != nil || !strings.Contains(string(out), "result: TRUE") {
		t.Fatalf("example: %v\n%s", err, out)
	}
	// Remove every copy of the example; only --blob can supply it now.
	os.Remove(filepath.Join(root, "out", "result.json"))
	os.Remove(filepath.Join(root, ".datum", "artifacts", string(model.HashBytes([]byte(e2ePass)))))
	if _, err := e2eInvoke(t, root, nil, "check", "criterion", "--events", events); err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("an unresolvable example must be named, got %v", err)
	}
	blob := filepath.Join(t.TempDir(), "example.json")
	os.WriteFile(blob, []byte(e2ePass), 0600)
	if out, err := e2eInvoke(t, root, nil, "check", "criterion", "--events", events, "--blob", blob); err != nil || !strings.Contains(string(out), "result: TRUE") {
		t.Fatalf("blob: %v\n%s", err, out)
	}
	if out, err := e2eInvoke(t, root, nil, "check", "--help"); err != nil || !strings.Contains(string(out), "--output") {
		t.Fatalf("usage: %v\n%s", err, out)
	}
}

// The proof check's first refusal is admission's refusal, it lists more than
// admission does, and it writes nothing; a proof it passes is admitted.
func TestProofCheckAgreesWithAdmissionAndWritesNothing(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	member := checkSealedRun(t, root, criterion, instrument, attempt, e2ePass, 700)
	path, proof := checkProofFile(t, criterion, member, "inconclusive")
	before := cliTree(t, root)
	out, err := e2eInvoke(t, root, nil, "check", "admission", "--events", path)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") || !strings.HasPrefix(string(out), "admission dry run at watermark ") ||
		!strings.Contains(string(out), ": full gate, evidence and authority checks; result may change if the ledger moves\nresult: would-refuse\n") {
		t.Fatalf("check admission must open with its scope and exit 1 on a refusal: %v\n%s", err, out)
	}
	if after := cliTree(t, root); after != before {
		t.Fatalf("proof check wrote:\n%s\n%s", before, after)
	}
	lines := strings.Split(string(out), "\n")
	first := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "  1. [") {
			first = line[strings.Index(line, "] ")+2:]
		}
	}
	if !strings.Contains(string(out), "criterion TRUE") || !strings.Contains(string(out), "  2. [") {
		t.Fatalf("expected the member's own TRUE and more than one refusal:\n%s", out)
	}
	if _, err := e2eInvoke(t, root, []model.TypedEvent{proof}, "capture", "--command-id", string(cliID(710))); err != nil {
		t.Fatal(err)
	}
	_, err = e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(711)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "proof", string(cliID(710)))
	if err == nil || first == "" || !strings.Contains(err.Error(), first) {
		t.Fatalf("admission refused %v; the check's first refusal was %q", err, first)
	}
	path, proof = checkProofFile(t, criterion, member, "supports")
	if out, err := e2eInvoke(t, root, nil, "check", "admission", "--events", path); err != nil || !strings.Contains(string(out), "\nresult: would-admit\n") {
		t.Fatalf("a passing proof: %v\n%s", err, out)
	}
	if _, err := e2eInvoke(t, root, []model.TypedEvent{proof}, "capture", "--command-id", string(cliID(712))); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(713)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "proof", string(cliID(712))); err != nil {
		t.Fatalf("the check accepted what admission refuses: %v", err)
	}
	var usage []byte
	if usage, err = e2eInvoke(t, root, nil, "check", "--help"); err != nil || !strings.Contains(string(usage), "--packet") {
		t.Fatalf("usage: %v\n%s", err, usage)
	}
}
