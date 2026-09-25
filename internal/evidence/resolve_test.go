package evidence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// ---- observations --------------------------------------------------------

const resultArtifact = `{
  "results": {
    "unit": "mm",
    "population": "pose sweep",
    "denominator": "poses",
    "values": [{"pose": "p1", "value": 0.01}, {"pose": "p2", "value": 0.02}]
  }
}`

// observeFixture builds a resolver whose root holds one output artifact, plus
// the criterion and envelope that point at it.
func observeFixture(t *testing.T) (*Resolver, model.CriterionFix, model.InvocationEnvelope) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, storeCopy(resultArtifact), resultArtifact)

	c := testCriterion(t)
	env := testEnvelope(t, invocationA)
	env.Outputs = knownOutputs(runOutput("out/result.json", resultArtifact, "application/json"))
	return NewResolver(root), c, env
}

func TestObserveReadsTheRunsOwnArtifact(t *testing.T) {
	r, c, env := observeFixture(t)
	ctx := context.Background()

	// Control: the criterion's pointers read out of this run's output.
	o, err := r.Observe(ctx, c, env)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if o.Unavailable != "" {
		t.Fatalf("control: %s", o.Unavailable)
	}
	if o.Result.Kind != ReadingSet || len(o.Result.Values) != 2 {
		t.Fatalf("control: %+v", o.Result)
	}
	if *o.Result.Unit.Value != "mm" {
		t.Fatalf("control: the unit must come from the artifact, got %+v", o.Result.Unit)
	}
	if size, ok := o.Population.Size(); !ok || size != 2 {
		t.Fatalf("control: population %d %v", size, ok)
	}

	t.Run("a run with no observed outputs has no reading", func(t *testing.T) {
		env := env
		env.Outputs = model.Availability[[]model.RunOutput]{State: model.Unknown, Reason: "the observer was killed"}
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(o.Unavailable, "the observer was killed") {
			t.Fatalf("expected the recorded reason to survive, got %q", o.Unavailable)
		}
	})

	t.Run("outputs that do not include the selected artifact", func(t *testing.T) {
		env := env
		env.Outputs = knownOutputs(runOutput("out/log.txt", "other", "text/plain"))
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(o.Unavailable, "out/result.json") {
			t.Fatalf("expected the missing path to be named, got %q", o.Unavailable)
		}
	})

	t.Run("an output whose bytes are gone", func(t *testing.T) {
		empty := NewResolver(t.TempDir())
		o, err := empty.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if o.Unavailable == "" {
			t.Fatal("expected missing bytes to be reported, not read as zero")
		}
	})

	t.Run("a malformed criterion is an error, not an unknown reading", func(t *testing.T) {
		bad := c
		bad.Expression.Unit = "   "
		_, err := r.Observe(ctx, bad, env)
		wantFault(t, err, "invalid-field")
	})
}

// A reading is derived from the artifact. Retyping the number into the record
// creates a second copy that can drift, and this test states the shape: the
// observation has nowhere to put an authored value.
func TestObservationCarriesNoTranscribedNumber(t *testing.T) {
	r, c, env := observeFixture(t)
	o, err := r.Observe(context.Background(), c, env)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(o.Result)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if _, found := back["Artifact"]; !found {
		t.Fatal("a reading must carry the digest of the bytes it came from")
	}
	if o.Result.Artifact != model.HashBytes([]byte(resultArtifact)) {
		t.Fatalf("the reading must name the exact bytes it was read from, got %s", o.Result.Artifact)
	}
}

func TestResolverRefusesAnOversizedArtifact(t *testing.T) {
	root := t.TempDir()
	const body = `{"depth":0.30}`
	writeFile(t, root, "out/result.json", body)

	// Control: the default limit resolves it.
	r := NewResolver(root)
	if _, err := r.Resolve(context.Background(), contentRef(body, "application/json", []string{"out/result.json"}, "whole", "")); err != nil {
		t.Fatalf("control: %v", err)
	}

	r.MaxBytes = 4
	_, err := r.Resolve(context.Background(), contentRef(body, "application/json", []string{"out/result.json"}, "whole", ""))
	wantFault(t, err, "unavailable")
}

// The headline property, end to end: what the pinned bytes say decides the
// comparison. Change the bytes and the verdict changes. Change what the
// selector asks for and the inference is refused rather than answered from
// something else.
func TestPinnedBytesDecideTheVerdict(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root)
	c := testCriterion(t)
	ctx := context.Background()

	observe := func(t *testing.T, body string, sel model.Selector) Observation {
		t.Helper()
		writeFile(t, root, storeCopy(body), body)
		env := testEnvelope(t, invocationA)
		env.Outputs = knownOutputs(runOutput("out/result.json", body, "application/json"))
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}

	pointer := model.Selector{Kind: "json-pointer", Pointer: "/results"}

	// Control: the clean sweep satisfies the criterion.
	wantVerdict(t, evaluate(t, c, observe(t, resultArtifact, pointer)), True, "")

	const failing = `{
  "results": {
    "unit": "mm",
    "population": "pose sweep",
    "denominator": "poses",
    "values": [{"pose": "p1", "value": 0.2}, {"pose": "p2", "value": 0.0}]
  }
}`
	wantVerdict(t, evaluate(t, c, observe(t, failing, pointer)), False, "")

	t.Run("a whole-artifact selector answers a different question", func(t *testing.T) {
		whole := c
		expr := c.Expression
		expr.ResultSelector = contentRef(criterionExample, "application/json", []string{"out/result.json"}, "whole", "")
		whole.Expression = expr
		writeFile(t, root, storeCopy(resultArtifact), resultArtifact)
		env := testEnvelope(t, invocationA)
		env.Outputs = knownOutputs(runOutput("out/result.json", resultArtifact, "application/json"))
		o, err := r.Observe(ctx, whole, env)
		if err != nil {
			t.Fatal(err)
		}
		// The bytes are the same bytes that passed above. Asked for as a whole
		// artifact they read as an identity, which is not millimetres.
		wantVerdict(t, evaluate(t, whole, o), Unknown, "unit mismatch")
	})

	t.Run("bytes that no longer match the pin", func(t *testing.T) {
		env := testEnvelope(t, invocationA)
		env.Outputs = knownOutputs(runOutput("out/result.json", resultArtifact, "application/json"))
		writeFile(t, root, storeCopy(resultArtifact), failing)
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if o.Unavailable == "" {
			t.Fatal("edited bytes must not be read as the pinned artifact")
		}
		wantVerdict(t, evaluate(t, c, o), Unknown, "no locator holds the pinned bytes")
	})
}

func TestResolverNeedsAnAbsoluteRoot(t *testing.T) {
	root := t.TempDir()
	const body = `{"depth":0.30}`
	writeFile(t, root, "out/result.json", body)
	ref := contentRef(body, "application/json", []string{"out/result.json"}, "whole", "")

	// Control: the absolute root resolves.
	if _, err := NewResolver(root).Resolve(context.Background(), ref); err != nil {
		t.Fatalf("control: %v", err)
	}
	_, err := NewResolver("relative/root").Resolve(context.Background(), ref)
	wantFault(t, err, "invalid-field")
}
