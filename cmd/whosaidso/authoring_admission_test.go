package main

// Admission controls exercise dry runs, provenance and amendment preservation.
// Fixtures are isolated projects; directory snapshots detect hidden writes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func newTaskArgs() []string {
	return []string{"task", "new", "--subject", "Export report", "--intent", "Export results", "--for", "worker", "--criterion", "CSV opens", "--non-goal", "PDF export", "--applies-when", "Report export", "--limitations", "CSV only"}
}

func authoringTree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				files[path] = "directory"
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestAuthoringDryRunUsesAdmissionWithoutWriting(t *testing.T) {
	root, input := cliFixture(t)
	cliControl(t, root, input)
	proofWrite(t, root, "request.txt", "Export requested\n")
	home := t.TempDir()
	t.Setenv("WHOSAIDSO_HOME", home)
	cases := [][]string{newTaskArgs(), {"task", "amend", string(cliID(1)), "--for", "reviewer"}, {"source", "add", "request.txt", "--speaker", "requester", "--order", "0", "--about", string(cliID(1))},
		{"task", "close", string(cliID(1)), "--outcome", "success", "--witness", "request.txt", "--reason", "Reviewed"}}
	for _, args := range cases {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			before := authoringTree(t, root, home)
			out, errs, code := cliRun(t, root, nil, "worker", append(args, "--dry-run", "--json")...)
			var a admissionAnswer
			if code != 0 || json.Unmarshal([]byte(out), &a) != nil || a.Result != "would-admit" {
				t.Fatalf("admission control: %d %s %s", code, out, errs)
			}
			if after := authoringTree(t, root, home); !reflect.DeepEqual(before, after) {
				t.Fatal("dry run changed project or home")
			}
		})
	}
	// The gate must refuse a schema-valid but ineffective successful closure.
	before := authoringTree(t, root, home)
	out, errs, code := cliRun(t, root, nil, "worker", "task", "close", string(cliID(1)), "--outcome", "success", "--reason", "No witness", "--dry-run", "--json")
	if code != 1 || !strings.Contains(out, "closure-ineffective") || !strings.Contains(out, "would-refuse") {
		t.Fatalf("gate refusal: %d %s %s", code, out, errs)
	}
	if !reflect.DeepEqual(before, authoringTree(t, root, home)) {
		t.Fatal("refused dry run wrote files")
	}
}

func TestAuthoringAdmitKeepsSelfAdmissionPolicy(t *testing.T) {
	for _, mode := range []string{"allow", "refuse"} {
		t.Run(mode, func(t *testing.T) {
			root, _ := cliFixture(t)
			if mode == "refuse" {
				proofWrite(t, root, "whosaidso.toml", "id = 'test/cli'\nledger = '.whosaidso/events'\nself_admission = 'refuse'\n")
			}
			out, errs, code := cliRun(t, root, nil, "worker", append(newTaskArgs(), "--admit", "--reason", "Reviewed the task", "--json")...)
			want := 0
			if mode == "refuse" {
				want = 4
			}
			if code != want {
				t.Fatalf("self admission %s: %d %s %s", mode, code, out, errs)
			}
			var answer struct {
				Capture struct {
					CommandID model.ID `json:"command_id"`
				} `json:"capture"`
			}
			if json.Unmarshal([]byte(out), &answer) != nil || answer.Capture.CommandID == "" {
				t.Fatalf("capture must stand: %s", out)
			}
			p, err := store.Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			packets, err := store.ReadVerifiedIntake(p, []model.ID{answer.Capture.CommandID})
			if err != nil || len(packets) != 1 {
				t.Fatalf("captured packet lost: %v", err)
			}
			s, err := store.Load(p)
			if err != nil {
				t.Fatal(err)
			}
			exists := len(s.Snapshot().Tasks()) > 0
			if exists != (mode == "allow") {
				t.Fatalf("admission policy ignored: tasks present %v", exists)
			}
		})
	}
}

func TestAuthoringProvenanceAndAuthorityUseAdmittedSources(t *testing.T) {
	root, input := cliFixture(t)
	cliControl(t, root, input)
	proofWrite(t, root, "request.txt", "Export requested\n")
	out, errs, code := cliRun(t, root, nil, "worker", "source", "add", "request.txt", "--speaker", "requester", "--order", "0", "--about", string(cliID(1)), "--admit", "--reason", "Original request", "--json")
	var answer struct {
		Capture struct {
			Created []createdRecord `json:"created"`
		} `json:"capture"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &answer) != nil || len(answer.Capture.Created) != 1 {
		t.Fatalf("source admission: %d %s %s", code, out, errs)
	}
	source := answer.Capture.Created[0].ID
	proofWrite(t, root, "notes.txt", "Scope confirmed\n")
	// The template control combines an admitted source and a separately pinned file.
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	intake := state.Snapshot().Sources()[0].Intake
	authoringParity(t, root, []string{"task.amend", "--from", string(cliID(1)), "--set", jsonSet("provenance.source_refs[0]", intake.SourceRef), "--pin", "provenance.source_refs[1]=notes.txt", "--set", jsonSet("replacement.subject", "null text")},
		[]string{"task", "amend", string(cliID(1)), "--subject", "null text", "--source", source, "--pin", "notes.txt"})
	ref := model.RecordRef{Project: p.ID, RecordID: cliID(1), Revision: 1}
	authority := model.Authority{Actor: intake.Speaker, SourceRef: intake.SourceRef, Selector: model.Selector{Kind: "whole"}, Scope: model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{ref}, AppliesWhen: "This closure", Limitations: "This task"}}
	authoringParity(t, root, []string{"task.close", "--task", string(cliID(1)), "--set", "outcome=cancelled", "--set", "acceptance_witness_refs=[]", "--set", "delivery_witness_refs=[]", "--set", jsonSet("authority", authority)},
		[]string{"task", "close", string(cliID(1)), "--outcome", "cancelled", "--authority", source, "--applies-when", "This closure", "--limitations", "This task", "--reason", "Request cancelled"})
}

func TestAuthoringAmendChangesOnlyGivenSpecFields(t *testing.T) {
	root, input := cliFixture(t)
	cliControl(t, root, input)
	control := []string{"task.amend", "--from", string(cliID(1)), "--set", "provenance.source_refs=[]", "--set", "replacement.subject=Updated report", "--set", "replacement.intent=null", "--set", "replacement.scope.applies_when=This export", "--set", "replacement.scope.limitations=Text only", "--set", `replacement.non_goals=["Charts","PDF"]`, "--set", "replacement.acceptance_criteria=[]", "--set", "replacement.acceptance_criteria[0].criterion=Export exists", "--set", "replacement.acceptance_criteria[1].criterion=Rows match"}
	// A literal "null" is authored text, so quote it for template's --set.
	control[8] = `replacement.intent="null"`
	authoringParity(t, root, control, []string{"task", "amend", string(cliID(1)), "--subject", "Updated report", "--intent", "null", "--applies-when", "This export", "--limitations", "Text only", "--non-goal", "Charts", "--non-goal", "PDF", "--criterion", "Export exists", "--criterion", "Rows match"})
}

func TestAuthoringAssignmentReplacesAnUnknownActor(t *testing.T) {
	root, input := cliFixture(t)
	events, err := model.DecodeEvents(input)
	if err != nil {
		t.Fatal(err)
	}
	event, err := model.DecodeEvent(events[0])
	if err != nil {
		t.Fatal(err)
	}
	task := event.(*model.TaskCreate)
	task.Spec.NextActor = model.Actor{UnknownReason: "assignment pending"}
	boundAdmit(t, root, task)
	authoringParity(t, root, []string{"task.amend", "--from", string(cliID(1)), "--set", "provenance.source_refs=[]", "--set", `replacement.next_actor={"id":"reviewer"}`}, []string{"task", "amend", string(cliID(1)), "--for", "reviewer"})
}
