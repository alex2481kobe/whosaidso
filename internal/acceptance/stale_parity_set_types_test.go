// Final stale-proposal parity and schema-typed --set probes belong here.
// Production repairs and unrelated adoption checks do not; fixtures use TempDir.
package acceptance_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// Each replay preserves packet boundaries and authored event order. The
// expected ordering follows providers first, then capture IDs; reversing a
// start and amendment deliberately makes the start stale, not reorderable.
func TestStaleStartAmendParity(t *testing.T) {
	for _, shape := range []string{"split", "merged", "already-admitted"} {
		for _, order := range []string{"01", "10"} {
			t.Run(shape+"/"+order, func(t *testing.T) {
				w := holdIdentityNew(t)
				start := &model.TaskStart{Task: w.ref, Actor: w.actor, AttemptID: w.id()}
				groups := [][]model.TypedEvent{{start}, {w.amend(1)}}
				groups, capture := revisionOrderAsAdmitted(groups, order, order)
				want := ""
				if order == "10" {
					want = "revision-conflict"
				}
				if shape == "merged" {
					groups, capture = [][]model.TypedEvent{append(groups[0], groups[1]...)}, "0"
				}
				if shape == "already-admitted" {
					w.add(groups[0]...)
					groups, capture = groups[1:], "0"
				}
				revisionOrderProbe(w.gateVerifyFixture, w.actor, groups, capture, want, want)
				if want != "" {
					return
				}
				loaded, err := store.Load(w.p)
				if err != nil {
					t.Fatal(err)
				}
				tasks := loaded.Snapshot().Tasks()
				if len(tasks) != 1 || tasks[0].Task.Revision != 2 || len(tasks[0].Attempts) != 1 || tasks[0].Attempts[0].TaskRevision != 1 {
					t.Errorf("amendment changed the attempt's starting contract: %+v", tasks)
				}
			})
		}
	}
}

func TestStaleDependencyParity(t *testing.T) {
	orders := map[string]string{"012": "012", "021": "021", "102": "012", "120": "012", "201": "021", "210": "021"}
	for capture, admitted := range orders {
		for _, historical := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/historical-%t", capture, historical), func(t *testing.T) {
				f := revisionOrderNew(t)
				a := model.Actor{ID: "set-types"}
				create := revisionOrderTask(f, []model.RecordRef{}...)
				r1 := model.RecordRef{Project: f.p.ID, RecordID: create.ID, Revision: 1}
				var use model.TypedEvent = &model.TaskStart{Task: r1, Actor: a, AttemptID: f.id()}
				if historical {
					use = revisionOrderTask(f, r1)
				}
				amend := &model.TaskAmend{Target: r1, Provenance: create.Provenance, Replacement: create.Spec}
				groups, renumbered := revisionOrderAsAdmitted([][]model.TypedEvent{{create}, {use}, {amend}}, capture, admitted)
				want := ""
				if !historical && admitted == "021" {
					want = "revision-conflict"
				}
				revisionOrderProbe(f, a, groups, renumbered, want, want)
			})
		}
	}
}

// This is an end-to-end text control: draft, capture, admission and ledger
// decoding must all preserve the exact authored bytes in string fields.
func TestSetCapturedText(t *testing.T) {
	bin := writeVerifyCLI(t)
	for _, value := range []string{"79461799e564", "1e9999", "0012", "true", "false", `{"meaning":"text"}`, "[1,2]", `"null"`} {
		t.Run(value, func(t *testing.T) {
			f := cliHoldNew(t)
			a := model.Actor{ID: "holder"}
			task := revisionOrderTask(f, []model.RecordRef{}...)
			if _, err := f.admit(a, a, task); err != nil {
				t.Fatal(err)
			}
			_, errs, err := cliHoldRun(t, f, bin, nil, "template", "task.amend", "--from", string(task.ID),
				"--set", "provenance.source_refs=[]", "--set", "replacement.intent="+value,
				"--capture", "--admit", "--reason", "confirm typed text")
			if err != nil {
				t.Fatalf("text amendment: %v %s", err, errs)
			}
			loaded, err := store.Load(f.p)
			if err != nil {
				t.Fatal(err)
			}
			r, ok := loaded.Snapshot().Record(model.RecordRef{Project: f.p.ID, RecordID: task.ID, Revision: 2})
			want := value
			if value == `"null"` {
				want = "null"
			}
			if !ok || r.Task.Intent != want {
				t.Errorf("want exact text %q in admitted revision, got %+v", want, r)
			}
		})
	}
}

// Malformed typed values remain malformed; the strict model decoder, rather
// than an extra template schema, must refuse them before capture writes intake.
func TestSetTypedRefusals(t *testing.T) {
	bin := writeVerifyCLI(t)
	f := cliHoldNew(t)
	a := model.Actor{ID: "holder"}
	task := revisionOrderTask(f, []model.RecordRef{}...)
	if _, err := f.admit(a, a, task); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1", `"1"`, "true", "1.5", "{}", "[]"} {
		t.Run(value, func(t *testing.T) {
			_, errs, err := cliHoldRun(t, f, bin, nil, "template", "task.amend", "--from", string(task.ID),
				"--set", "provenance.source_refs=[]", "--set", "target.revision="+value, "--capture")
			if value == "1" {
				if err != nil {
					t.Fatalf("valid integer control: %v %s", err, errs)
				}
			} else if err == nil || !strings.Contains(errs, "revision") {
				t.Errorf("malformed revision %s must be refused by the model: %v %s", value, err, errs)
			}
		})
	}
}

func TestSetDraftTypes(t *testing.T) {
	bin := writeVerifyCLI(t)
	f := cliHoldNew(t)
	cases := []struct {
		name, event string
		sets        []string
		path        []any
		want        any
	}{
		{"appended-text", "task.create", []string{"spec.non_goals=[]", "spec.non_goals[0]=false", "spec.non_goals[1]=123"}, []any{"spec", "non_goals", 1}, "123"},
		{"grown-text", "task.create", []string{"spec.progress=null", "spec.progress.summary={\"x\":1}"}, []any{"spec", "progress", "summary"}, `{"x":1}`},
		{"quoted-text", "task.create", []string{`spec.intent="line\nnext"`}, []any{"spec", "intent"}, "line\nnext"},
		{"revision", "task.start", []string{"task.revision=2"}, []any{"task", "revision"}, float64(2)},
		{"boolean", "attempt.terminal", []string{"commits_denied=true", "commits_denied=false"}, []any{"commits_denied"}, false},
		{"number", "criterion.fix", []string{"expression.target.number=-5e-2"}, []any{"expression", "target", "number"}, -0.05},
		{"integer", "invocation.seal", []string{"envelope.outcome.value.exit_code=17"}, []any{"envelope", "outcome", "value", "exit_code"}, float64(17)},
		{"array", "task.create", []string{`spec.non_goals=["1","false"]`}, []any{"spec", "non_goals"}, []any{"1", "false"}},
		{"object", "task.create", []string{`spec.progress={"summary":"123","witness_refs":[]}`}, []any{"spec", "progress", "summary"}, "123"},
		{"null-text", "task.create", []string{"spec.progress.summary=null"}, []any{"spec", "progress", "summary"}, nil},
		{"null-object", "task.create", []string{"spec.progress=null"}, []any{"spec", "progress"}, nil},
		{"null-bool", "criterion.fix", []string{"expression.empty_result=null"}, []any{"expression", "empty_result"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{tc.event}
			for _, set := range tc.sets {
				args = append(args, "--set", set)
			}
			data := cliHoldDraft(t, f, bin, args...)
			if got := flowGet(data, tc.path...); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%v: got %#v (%T), want %#v (%T)", tc.path, got, got, tc.want, tc.want)
			}
			if tc.want == nil {
				parent := flowGet(data, tc.path[:len(tc.path)-1]...).(map[string]any)
				if _, exists := parent[tc.path[len(tc.path)-1].(string)]; exists {
					t.Error("null retained a key instead of omitting it")
				}
			}
		})
	}
	for _, set := range []string{"spec.intent=null", "spec.non_goals=null", "spec.unknown=1", "spec.non_goals[99]=x"} {
		if _, errs, err := cliHoldRun(t, f, bin, nil, "template", "task.create", "--set", set); err == nil {
			t.Errorf("invalid path or required null %q was accepted: %s", set, errs)
		}
	}
}

func TestSetMapTypes(t *testing.T) {
	bin := writeVerifyCLI(t)
	f := cliHoldNew(t)
	for _, container := range []string{"config_requested", "conditions_declared"} {
		for _, value := range []string{"8e1", "true", `{"a":1}`, "[1]", `"quoted"`} {
			t.Run(container+"/"+value, func(t *testing.T) {
				path := "envelope." + container
				data := cliHoldDraft(t, f, bin, "invocation.start", "--set",
					path+`={"label":{"type":"string","string":"x"},"n":{"type":"number","number":1},"b":{"type":"bool","bool":false}}`,
					"--set", path+".label.string="+value, "--set", path+".n.number=8e1", "--set", path+".b.bool=true")
				want := strings.Trim(value, `"`)
				for key, expected := range map[string]any{"label": want, "n": float64(80), "b": true} {
					leaf := map[string]string{"label": "string", "n": "number", "b": "bool"}[key]
					if got := flowGet(data, "envelope", container, key, leaf); !reflect.DeepEqual(got, expected) {
						t.Errorf("%s.%s.%s: got %#v (%T), want %#v", path, key, leaf, got, got, expected)
					}
				}
			})
		}
	}
}
