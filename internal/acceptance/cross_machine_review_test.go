package acceptance_test

// A packet captured on one machine and reviewed on another, in a project
// that refuses self-admission: the author cannot admit their own packet; the
// packet's intake directory, copied with tar into the reviewer's intake (the
// recipe in whosaidso help admit), is listed by the reviewer's todo with its
// author and events and admits under the reviewer; a copy whose bytes were
// changed on the way is refused.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func (w *flowWorld) intakeDir() string {
	w.t.Helper()
	out, err := w.cli(nil, "home", "--json")
	var a struct {
		IntakeDir string `json:"intake_dir"`
	}
	if err != nil || json.Unmarshal(out, &a) != nil || a.IntakeDir == "" {
		w.t.Fatalf("home --json names the intake directory: %v %s", err, out)
	}
	return a.IntakeDir
}

// carry copies one packet's intake directory from one machine to another
// with the recipe help prints.
func carry(t *testing.T, from, to *flowWorld, packet model.ID) {
	t.Helper()
	src, dst := from.intakeDir(), to.intakeDir()
	recipe := `umask 077 && mkdir -p "$DST" && tar -C "$SRC" -cf - "$ID" | tar -C "$DST" -xpf -`
	cmd := exec.Command("sh", "-c", recipe)
	cmd.Env = append(os.Environ(), "SRC="+src, "DST="+dst, "ID="+string(packet))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("carry %s: %v %s", packet, err, out)
	}
}

func TestCrossMachineReviewUnderRefusedSelfAdmission(t *testing.T) {
	t.Parallel()
	author := flowNew(t)
	author.put("whosaidso.toml", []byte("id = \"flow/review\"\nledger = \".whosaidso/events\"\nself_admission = 'refuse'\n"))
	reviewer := machine(t, author.root, 500, author.scope)
	spec := model.TaskSpec{Intent: "review across machines", Subject: "the review fixture", Scope: author.scope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: author.id(), Revision: 1, Criterion: "reviewed"}},
		ContextRefs:        []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: flowAgent}}
	create := &model.TaskCreate{ID: author.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: spec}
	packet := author.capture("biz1/m2/implementer", create)
	admit := func(w *flowWorld, actor string, packet model.ID) error {
		_, err := w.cli(nil, "admit", "--json", "--command-id", string(w.id()), "--actor", actor, "--outcome", "accepted", "--reason", "reviewed", string(packet))
		return err
	}
	if err := admit(author, "biz1/m2/implementer", packet); err == nil || !strings.Contains(err.Error(), "self-admission-refused") {
		t.Fatalf("the author cannot admit their own packet here: %v", err)
	}
	if out, _ := reviewer.cli(nil, "todo", "--json"); strings.Contains(string(out), string(packet)) {
		t.Fatal("control: the reviewer's home does not hold the packet before it is carried")
	}
	carry(t, author, reviewer, packet)
	out, err := reviewer.cli(nil, "todo", "--json")
	if err != nil || !strings.Contains(string(out), string(packet)) || !strings.Contains(string(out), "biz1/m2/implementer") || !strings.Contains(string(out), "task.create") {
		t.Fatalf("the reviewer's todo lists the carried packet with its author and events: %v %.400s", err, out)
	}
	if err := admit(reviewer, "biz2/m3/reviewer", packet); err != nil {
		t.Fatalf("another known actor admits it: %v", err)
	}
	if r := reviewer.record(create.ID); flowStr(r, "task", "status") == "" {
		t.Fatalf("the task is admitted: %+v", r)
	}

	second := author.capture("biz1/m2/implementer", &model.TaskCreate{ID: author.id(), Provenance: create.Provenance, Spec: spec})
	carry(t, author, reviewer, second)
	body := filepath.Join(reviewer.intakeDir(), string(second), "packet.json")
	data, err := os.ReadFile(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(body, []byte(strings.Replace(string(data), "review across machines", "review across machinez", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := admit(reviewer, "biz2/m3/reviewer", second); err == nil || !strings.Contains(err.Error(), "intake-corrupt") {
		t.Fatalf("a packet whose bytes changed on the way is refused as corrupt: %v", err)
	}
}
