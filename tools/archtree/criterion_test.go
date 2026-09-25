// Criterion tests: archtree's readings put through WhoSaidSo's own observation and
// evaluation path (internal/evidence Observe then Evaluate), exactly as a run's
// stdout would be. A reading that only looks selectable proves nothing; these
// assert the evaluator reaches TRUE or FALSE, never UNKNOWN. Scanner known
// answers live in known_answer_test.go.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
)

const (
	critProject    = model.ProjectID("example/example")
	critClaim      = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	critID         = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	critAttempt    = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FBV")
	critInstrument = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FBW")
	critInvocation = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAX")
)

func stdoutPin(body []byte, pointer string) model.ArtifactRef {
	return model.ArtifactRef{
		Kind: "content",
		Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)),
			MediaType: "application/json", Locators: []model.Locator{{Path: "stdout"}}},
		Selector: model.Selector{Kind: "json-pointer", Pointer: pointer},
	}
}

// fileLimit is "every production Go file under <result> is at most <max>
// lines", the criterion the dogfood run could not express module-wide.
func fileLimit(t *testing.T, example []byte, result, identity string, max int) model.CriterionFix {
	t.Helper()
	target := json.Number(itoa(max))
	c := model.CriterionFix{
		Claim:       model.RecordRef{Project: critProject, RecordID: critClaim, Revision: 1},
		CriterionID: critID,
		Revision:    1,
		Expression: model.CriterionExpression{
			ResultSelector: stdoutPin(example, result),
			Unit:           "lines",
			Population: model.Population{
				Identity:    identity,
				Selector:    stdoutPin(example, result+"/values"),
				Denominator: "production Go files",
			},
			Operator: model.LessEqual,
			Target:   model.Scalar{Type: "number", Number: &target},
			Reducer:  model.All,
		},
		Policy:     model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author:     model.Actor{ID: "archtree-agent"},
		SourceRefs: []model.ArtifactRef{},
	}
	if err := model.ValidateSchema(c); err != nil {
		t.Fatalf("fixture criterion is not valid: %v", err)
	}
	return c
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// evaluate stores stdout as admission stores a run's stdout and lets WhoSaidSo's
// resolver find, read and judge it.
func evaluate(t *testing.T, c model.CriterionFix, stdout []byte) evidence.Evaluation {
	t.Helper()
	root := t.TempDir()
	// write.Run's stdout is the output named stdout; admission publishes its
	// bytes to the store by digest, where Observe reads them.
	full := filepath.Join(root, filepath.FromSlash(evidence.DefaultArtifactDir), string(model.HashBytes(stdout)))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, stdout, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := model.CriterionRef{Claim: c.Claim, CriterionID: c.CriterionID, Revision: c.Revision}
	exit := 0
	outcome := model.ProcessOutcome{Kind: "exit", ExitCode: &exit}
	pin := stdoutPin(stdout, "").Content
	out := model.RunOutput{Name: "stdout", SHA256: pin.SHA256, Length: pin.Length, MediaType: pin.MediaType}
	env := model.InvocationEnvelope{
		InvocationID:            critInvocation,
		AttemptID:               critAttempt,
		InstrumentRef:           model.RecordRef{Project: critProject, RecordID: critInstrument, Revision: 1},
		CriterionRef:            model.Availability[model.CriterionRef]{State: model.Known, Value: &ref},
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: critProject, SourceRefs: []model.ArtifactRef{}},
		Argv:                    []string{"go", "run", "./tools/archtree"},
		StartedAt:               time.Unix(1_700_000_000, 0).UTC(),
		Outcome:                 model.Availability[model.ProcessOutcome]{State: model.Known, Value: &outcome},
		Outputs:                 model.Availability[[]model.RunOutput]{State: model.Known, Value: &[]model.RunOutput{out}},
	}
	o, err := evidence.NewResolver(root).Observe(context.Background(), c, env)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := evidence.Evaluate(c, []evidence.Observation{o})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func archtreeJSON(t *testing.T, root string) []byte {
	t.Helper()
	r, err := measure(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func lines(n int) string { return "package a\n" + strings.Repeat("\n", n-1) }

func TestModuleWideFileLimitDecides(t *testing.T) {
	const ptr, pop = "/readings/file_lines", "production Go files in module m"
	under := archtreeJSON(t, writeModule(t, map[string]string{"go.mod": "module m\n", "a/x.go": lines(300), "b/y.go": lines(12)}))
	over := archtreeJSON(t, writeModule(t, map[string]string{"go.mod": "module m\n", "a/x.go": lines(300), "b/y.go": lines(301)}))

	// Control: the criterion is frozen against one example and judges another
	// run's bytes, as it would in the ledger.
	if ev := evaluate(t, fileLimit(t, under, ptr, pop, 300), under); ev.Verdict != evidence.True {
		t.Fatalf("all files at or under 300: %s", ev.Describe())
	}
	if ev := evaluate(t, fileLimit(t, under, ptr, pop, 300), over); ev.Verdict != evidence.False {
		t.Fatalf("one file at 301: %s", ev.Describe())
	}
	// A population the artifact does not state is refused, not waved through.
	if ev := evaluate(t, fileLimit(t, under, ptr, "production Go files in module other", 300), under); ev.Verdict != evidence.Unknown ||
		!strings.Contains(ev.Reason, "population mismatch") {
		t.Fatalf("wrong population must be UNKNOWN with a reason: %s", ev.Describe())
	}
}

// By name, not position: a package that sorts in ahead of b must not move the
// pointer that means b.
func TestPackagePointerSurvivesANewPackage(t *testing.T) {
	const ptr, pop = "/readings/by_package/b/file_lines", "production Go files in package m/b"
	before := archtreeJSON(t, writeModule(t, map[string]string{"go.mod": "module m\n", "b/y.go": lines(301)}))
	after := archtreeJSON(t, writeModule(t, map[string]string{"go.mod": "module m\n", "a/x.go": lines(5), "b/y.go": lines(301)}))
	c := fileLimit(t, before, ptr, pop, 300)
	for name, out := range map[string][]byte{"before": before, "after a new package": after} {
		if ev := evaluate(t, c, out); ev.Verdict != evidence.False {
			t.Fatalf("%s: %s", name, ev.Describe())
		}
	}
}

// The real module, as `go run ./tools/archtree` prints it now. The verdict
// depends on what other agents have written, so it is not pinned; that it is a
// verdict at all is.
func TestThisModuleFileLimitIsDecidable(t *testing.T) {
	out := archtreeJSON(t, "../..")
	c := fileLimit(t, out, "/readings/file_lines", "production Go files in module github.com/alex2481kobe/whosaidso", 300)
	ev := evaluate(t, c, out)
	if ev.Verdict == evidence.Unknown {
		t.Fatalf("module-wide file limit is UNKNOWN: %s", ev.Describe())
	}
	t.Logf("module-wide file_lines <= 300: %s", ev.Describe())
	if testing.Verbose() {
		b, _ := json.MarshalIndent(model.Event{Type: "criterion.fix", Data: mustJSON(t, c)}, "", "  ")
		t.Logf("%s", b)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The report used to carry "unit":"lines" at its root, and the evaluator lets
// a root's direct children inherit it, so /cycles read as a count of lines.
func TestCyclesDoNotInheritLines(t *testing.T) {
	out := archtreeJSON(t, writeModule(t, map[string]string{"go.mod": "module m\n", "a/x.go": lines(3)}))
	c := fileLimit(t, out, "/cycles", "production Go files in module m", 300)
	ev := evaluate(t, c, out)
	if ev.Verdict != evidence.Unknown || !strings.Contains(ev.Reason, "unit") {
		t.Fatalf("/cycles must state no unit of lines: %s", ev.Describe())
	}
}
