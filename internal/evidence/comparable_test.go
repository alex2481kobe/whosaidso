package evidence

// Comparability beyond configuration: execution identity, instrument revision,
// declared source pins and the visual trust envelope. Configuration and
// observed-condition comparability stay in observations_test.go.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"whosaidso/internal/model"
)

func knownOf[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}

func unknownOf[T any](why string) model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: why}
}

// wantBothOrders checks that the refusal does not depend on member order.
func wantBothOrders(t *testing.T, c model.CriterionFix, a, b Observation, verdict Verdict, contains string) {
	t.Helper()
	forward := evaluate(t, c, a, b)
	wantVerdict(t, forward, verdict, contains)
	if backward := evaluate(t, c, b, a); !reflect.DeepEqual(forward.Verdict, backward.Verdict) {
		t.Fatalf("order changed the verdict: %s vs %s", forward.Verdict, backward.Verdict)
	}
}

func TestExecutionIdentityDecidesComparability(t *testing.T) {
	c := testCriterion(t)
	wantBothOrders(t, c, passing(invocationA), passing(invocationB), True, "")

	for _, tc := range []struct {
		name     string
		alter    func(a, b *Observation)
		contains string
	}{
		{"different machines", func(a, b *Observation) { b.Execution.MachineID = knownOf[model.ID]("01ARZ3NDEKTSV4RRFFQ69G5FZY") }, "different machines"},
		{"one machine unknown", func(a, b *Observation) { b.Execution.MachineID = unknownOf[model.ID]("not captured") }, "one run recorded its machine"},
		{"both machines unknown, same reason", func(a, b *Observation) {
			a.Execution.MachineID, b.Execution.MachineID = unknownOf[model.ID]("not captured"), unknownOf[model.ID]("not captured")
		}, "neither run recorded its machine"},
		{"different instrument revisions", func(a, b *Observation) { b.Instrument.Revision = 2 }, "instrument revisions"},
		{"different instruments", func(a, b *Observation) { b.Instrument.RecordID = invocationB }, "instrument revisions"},
		{"different projects", func(a, b *Observation) { b.Execution.Project = "other/project" }, "different projects"},
		{"different source pins", func(a, b *Observation) {
			b.Execution.SourceRefs = []model.ArtifactRef{contentRef("source", "text/plain", []string{"src/a.go"}, "whole", "")}
		}, "source pins"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := passing(invocationA), passing(invocationB)
			tc.alter(&a, &b)
			wantBothOrders(t, c, a, b, Unknown, tc.contains)
		})
	}

	t.Run("control: the same source pinned from each run's own copy compares", func(t *testing.T) {
		a, b := passing(invocationA), passing(invocationB)
		a.Execution.SourceRefs = []model.ArtifactRef{contentRef("source", "text/plain", []string{"copies/" + string(invocationA) + "/src.go"}, "whole", "")}
		b.Execution.SourceRefs = []model.ArtifactRef{contentRef("source", "text/plain", []string{"copies/" + string(invocationB) + "/src.go"}, "whole", "")}
		wantBothOrders(t, c, a, b, True, "")
	})

}

// Source must be ESTABLISHED equal (coordinator decision 2026-09-23): equal
// source pins, or a known equal HEAD with both checkouts known clean.
func TestExecutedSourceMustBeEstablishedEqual(t *testing.T) {
	c := testCriterion(t)
	otherHead := model.GitHead{ObjectFormat: "sha1", Commit: "2222222222222222222222222222222222222222"}
	pin := func(id model.ID) []model.ArtifactRef {
		return []model.ArtifactRef{contentRef("source", "text/plain", []string{"copies/" + string(id) + "/src.go"}, "whole", "")}
	}
	t.Run("control: equal known HEADs, both clean, compare", func(t *testing.T) {
		wantBothOrders(t, c, passing(invocationA), passing(invocationB), True, "")
	})
	t.Run("control: equal pins with unknown HEAD and dirty state compare", func(t *testing.T) {
		a, b := passing(invocationA), passing(invocationB)
		for _, o := range []*Observation{&a, &b} {
			o.Execution.Head, o.Execution.Dirty = unknownOf[model.GitHead]("no git"), unknownOf[bool]("no git")
		}
		a.Execution.SourceRefs, b.Execution.SourceRefs = pin(invocationA), pin(invocationB)
		wantBothOrders(t, c, a, b, True, "")
	})
	t.Run("control: equal pins establish a dirty checkout", func(t *testing.T) {
		a, b := passing(invocationA), passing(invocationB)
		a.Execution.Dirty, b.Execution.Dirty = knownOf(true), knownOf(true)
		a.Execution.SourceRefs, b.Execution.SourceRefs = pin(invocationA), pin(invocationB)
		wantBothOrders(t, c, a, b, True, "")
	})

	for _, tc := range []struct {
		name     string
		alter    func(a, b *Observation)
		contains string
	}{
		{"different HEADs", func(a, b *Observation) { b.Execution.Head = knownOf(otherHead) }, "different source"},
		{"one HEAD unknown", func(a, b *Observation) { b.Execution.Head = unknownOf[model.GitHead]("no git") }, "HEAD was not recorded"},
		{"both HEADs unknown, same reason", func(a, b *Observation) {
			a.Execution.Head, b.Execution.Head = unknownOf[model.GitHead]("no git"), unknownOf[model.GitHead]("no git")
			a.Execution.Dirty, b.Execution.Dirty = unknownOf[bool]("no git"), unknownOf[bool]("no git")
		}, "HEAD was not recorded"},
		{"one checkout dirty", func(a, b *Observation) { b.Execution.Dirty = knownOf(true) }, "dirty"},
		{"both checkouts dirty", func(a, b *Observation) { a.Execution.Dirty, b.Execution.Dirty = knownOf(true), knownOf(true) }, "dirty"},
		{"one dirty state unknown", func(a, b *Observation) { b.Execution.Dirty = unknownOf[bool]("status failed") }, "dirty"},
		{"both dirty states unknown", func(a, b *Observation) {
			a.Execution.Dirty, b.Execution.Dirty = unknownOf[bool]("status failed"), unknownOf[bool]("status failed")
		}, "dirty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := passing(invocationA), passing(invocationB)
			tc.alter(&a, &b)
			wantBothOrders(t, c, a, b, Unknown, tc.contains)
		})
	}
}

func testVisual() model.VisualObservation {
	n := func(s string) json.Number { return json.Number(s) }
	transforms := contentRef(`{"joints":[]}`, "application/json", []string{"copies/" + string(invocationA) + "/transforms.json"}, "whole", "")
	return model.VisualObservation{
		Framing: knownOf(model.VisualFraming{
			Subject: knownOf("left knee"), ProjectedBounds: unknownOf[model.ArtifactRef]("not measured"),
			CameraPosition: knownOf(model.Vector3{X: n("1"), Y: n("2"), Z: n("3")}), CameraTarget: knownOf(model.Vector3{X: n("0"), Y: n("1"), Z: n("0")}),
			Projection: knownOf("perspective"), Viewport: knownOf(model.Viewport{Width: 800, Height: 600}), DPR: knownOf(n("2"))}),
		Transforms: knownOf(transforms),
		Clip:       knownOf("walk"), Time: knownOf(n("0.5")), Seed: knownOf("7"), Backend: knownOf("webgpu"),
		Appearance: knownOf(model.VisualAppearance{Lights: unknownOf[model.ArtifactRef]("x"), Exposure: knownOf(n("1")), ColourSpace: knownOf("srgb"),
			Materials: unknownOf[model.ArtifactRef]("x"), DiagnosticOverrides: unknownOf[model.ArtifactRef]("x")}),
		Limits: knownOf(model.VisualLimits{Occlusion: knownOf("none"), UnviewedSurfaces: knownOf([]string{}), UntestedBackends: knownOf([]string{}), StillLimitations: "a still"}),
	}
}

func TestVisualConditionsDecideComparability(t *testing.T) {
	c := testCriterion(t)
	pictured := func(id model.ID, alter func(*model.VisualObservation)) Observation {
		o := passing(id)
		v := testVisual()
		// Unknown sub-fields other than the one under test are set KNOWN here, so
		// only the alteration decides.
		f := *v.Framing.Value
		f.ProjectedBounds = knownOf(contentRef("bounds", "text/plain", []string{"copies/" + string(id) + "/bounds.txt"}, "whole", ""))
		v.Framing = knownOf(f)
		a := *v.Appearance.Value
		a.Lights, a.Materials, a.DiagnosticOverrides = knownOf(contentRef("l", "text/plain", []string{"copies/" + string(id) + "/l"}, "whole", "")),
			knownOf(contentRef("m", "text/plain", []string{"copies/" + string(id) + "/m"}, "whole", "")), knownOf(contentRef("d", "text/plain", []string{"copies/" + string(id) + "/d"}, "whole", ""))
		v.Appearance = knownOf(a)
		if alter != nil {
			alter(&v)
		}
		o.Visual = knownOf(v)
		return o
	}
	wantBothOrders(t, c, pictured(invocationA, nil), pictured(invocationB, nil), True, "")

	t.Run("control: numeric runs with no picture have no visual condition", func(t *testing.T) {
		wantBothOrders(t, c, passing(invocationA), passing(invocationB), True, "")
	})
	t.Run("control: visual limits are not compared", func(t *testing.T) {
		b := pictured(invocationB, func(v *model.VisualObservation) { v.Limits = unknownOf[model.VisualLimits]("not reported") })
		wantBothOrders(t, c, pictured(invocationA, nil), b, True, "")
	})

	for _, tc := range []struct {
		name     string
		a, b     Observation
		contains string
	}{
		{"one run observed a frame, the other did not", pictured(invocationA, nil), passing(invocationB), "one run observed its visual state"},
		{"a picture with no visual observation in either run", func() Observation { o := passing(invocationA); o.ImageOutput = true; return o }(), passing(invocationB), "neither run observed its visual state"},
		{"camera moved", pictured(invocationA, nil), pictured(invocationB, func(v *model.VisualObservation) {
			f := *v.Framing.Value
			f.CameraPosition = knownOf(model.Vector3{X: "1", Y: "2", Z: "4"})
			v.Framing = knownOf(f)
		}), "camera_position"},
		{"camera position unknown in both", pictured(invocationA, func(v *model.VisualObservation) {
			f := *v.Framing.Value
			f.CameraPosition = unknownOf[model.Vector3]("not reported")
			v.Framing = knownOf(f)
		}), pictured(invocationB, func(v *model.VisualObservation) {
			f := *v.Framing.Value
			f.CameraPosition = unknownOf[model.Vector3]("not reported")
			v.Framing = knownOf(f)
		}), "not observed in either run"},
		{"different transforms bytes", pictured(invocationA, nil), pictured(invocationB, func(v *model.VisualObservation) {
			v.Transforms = knownOf(contentRef(`{"joints":[25.03]}`, "application/json", []string{"copies/" + string(invocationB) + "/transforms.json"}, "whole", ""))
		}), "transforms"},
		{"different backend", pictured(invocationA, nil), pictured(invocationB, func(v *model.VisualObservation) { v.Backend = knownOf("webgl2") }), "backend"},
		{"different viewport", pictured(invocationA, nil), pictured(invocationB, func(v *model.VisualObservation) {
			f := *v.Framing.Value
			f.Viewport = knownOf(model.Viewport{Width: 800, Height: 601})
			v.Framing = knownOf(f)
		}), "viewport"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantBothOrders(t, c, tc.a, tc.b, Unknown, tc.contains)
		})
	}

	t.Run("control: equal numbers spelled differently compare", func(t *testing.T) {
		b := pictured(invocationB, func(v *model.VisualObservation) { v.Time = knownOf(json.Number("5e-1")) })
		wantBothOrders(t, c, pictured(invocationA, nil), b, True, "")
	})
}

// Observe records whether the run made a picture from its declared outputs, so
// a pictured run with no visual observation cannot compare as a numeric one.
func TestObserveMarksARunThatMadeAPicture(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, storeCopy(resultArtifact), resultArtifact)
	result := runOutput("out/result.json", resultArtifact, "application/json")
	frame := runOutput("frame.png", "\x89PNG\r\n\x1a\nframe", "image/png")
	for _, tc := range []struct {
		name    string
		outputs []model.RunOutput
		want    bool
	}{
		{"control: numeric outputs only", []model.RunOutput{result}, false},
		{"an image output", []model.RunOutput{result, frame}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnvelope(t, invocationA)
			env.Outputs = knownOutputs(tc.outputs...)
			o, err := NewResolver(root).Observe(context.Background(), testCriterion(t), env)
			if err != nil || o.Unavailable != "" || o.ImageOutput != tc.want {
				t.Fatalf("want ImageOutput=%v, got %v (%s, %v)", tc.want, o.ImageOutput, o.Unavailable, err)
			}
		})
	}
}
