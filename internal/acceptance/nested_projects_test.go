package acceptance_test

// Nested projects across git worktrees, end to end: the owner's two workflows
// (an app project born in a worktree below a repo-wide project and merged back;
// two app projects born in two worktrees, merged, then a root project added)
// and the rules told to the owner about them (every checkout and worktree
// records in its own ledger, and a late record merges like any branch; two
// histories born apart under one id refuse once merged; cross-project
// references read UNKNOWN; registering a checkout for the viewer refuses one
// that lacks the evidence its records cite). Every step is the built binary in
// a fresh process against real git repositories, worktrees and merges under
// t.TempDir(), each test with its own WHOSAIDSO_HOME. It does not reuse
// flowWorld.cli on purpose: that helper registers the invoking checkout before
// every call, which would hide that no read or write needs a registration.
// What does not belong here: the single-project read, write and gate rules the
// other acceptance files own.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// nestMachine is one machine: its own WhoSaidSo home and user home. ids is
// shared by machines in one test so no two histories mint the same id.
type nestMachine struct {
	t    *testing.T
	home string
	ids  *int
}

func nestNew(t *testing.T) *nestMachine {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixtures; Windows is out of scope")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	n := 100
	return &nestMachine{t: t, home: t.TempDir(), ids: &n}
}

// another is a second machine sharing this test's id counter.
func (m *nestMachine) another() *nestMachine {
	return &nestMachine{t: m.t, home: m.t.TempDir(), ids: m.ids}
}

func (m *nestMachine) id() model.ID { *m.ids++; return recID(*m.ids) }

// cli runs the binary in dir; it never binds anything on its own.
func (m *nestMachine) cli(dir string, env []string, stdin []byte, args ...string) (string, string, error) {
	m.t.Helper()
	cmd := exec.Command(pvWhoSaidSo(m.t), args...)
	cmd.Dir, cmd.Stdin = dir, bytes.NewReader(stdin)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "WHOSAIDSO_") && !strings.HasPrefix(e, "HOME=") && !strings.HasPrefix(e, "USERPROFILE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(append(cmd.Env, "HOME="+m.home, "USERPROFILE="+m.home, "WHOSAIDSO_HOME="+filepath.Join(m.home, "whosaidso")), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("whosaidso %s in %s: %v: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), err
}

func (m *nestMachine) must(dir string, args ...string) string {
	m.t.Helper()
	out, _, err := m.cli(dir, nil, nil, args...)
	if err != nil {
		m.t.Fatalf("control: %v", err)
	}
	return out
}

// register is `whosaidso home .` in dir: the viewer's entry, which routes nothing.
func (m *nestMachine) register(dir string) error {
	m.t.Helper()
	_, _, err := m.cli(dir, nil, nil, "home", ".")
	return err
}

// admit captures events as agent in dir, then admits the packet as coordinator.
func (m *nestMachine) admit(dir string, events ...model.TypedEvent) error {
	m.t.Helper()
	raw := make([]model.Event, len(events))
	for i, e := range events {
		raw[i] = recEncode(m.t, e)
	}
	body, err := model.Encode(raw)
	if err != nil {
		m.t.Fatal(err)
	}
	out, _, err := m.cli(dir, nil, body, "capture", "--json", "--actor", flowAgent, "--command-id", string(m.id()), "--events", "-")
	if err != nil {
		m.t.Fatalf("control: capture writes intake: %v", err)
	}
	return m.review(dir, flowPacket(m.t, []byte(out)))
}

func (m *nestMachine) review(dir string, packet model.ID) error {
	_, _, err := m.cli(dir, nil, nil, "admit", "--json", "--command-id", string(m.id()), "--actor", "coordinator",
		"--outcome", "accepted", "--reason", "nested review", string(packet))
	return err
}

func (m *nestMachine) mustAdmit(dir string, events ...model.TypedEvent) {
	m.t.Helper()
	if err := m.admit(dir, events...); err != nil {
		m.t.Fatalf("control: fixture step must admit: %v", err)
	}
}

// ---- git -----------------------------------------------------------------------

func nestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=nest", "-c", "user.email=nest@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// nestIgnore keeps each project's disposable working files out of git at any depth.
const nestIgnore = "**/.whosaidso/cache/\n**/.whosaidso/events/.lock\n**/.whosaidso/events/*.json.tmp\n"

// nestRepo is a fresh repository on main with one commit.
func nestRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	pvPut(t, repo, ".gitignore", []byte(nestIgnore))
	nestGit(t, repo, "init", "-q")
	nestGit(t, repo, "add", ".")
	nestGit(t, repo, "commit", "-q", "-m", "repo")
	return repo
}

// nestWorktree adds a worktree on a new branch beside the repository.
func nestWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	wt := filepath.Join(filepath.Dir(repo), "wt-"+branch)
	nestGit(t, repo, "worktree", "add", "-q", "-b", branch, wt)
	return wt
}

func nestCommitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	nestGit(t, dir, "add", "-A")
	nestGit(t, dir, "commit", "-q", "-m", msg)
	return nestGit(t, dir, "rev-parse", "HEAD")
}

// nestProject writes a project's config and one committed file for its pins.
func nestProject(t *testing.T, gitDir, root string, id model.ProjectID, file string) (commit string) {
	t.Helper()
	pvPut(t, root, "whosaidso.toml", []byte(fmt.Sprintf("id = '%s'\nledger = '.whosaidso/events'\n", id)))
	pvPut(t, root, file, []byte(fmt.Sprintf(`{"project":%q,"file":%q}`, id, file)))
	return nestCommitAll(t, gitDir, "project "+string(id))
}

func nestPin(commit, path string) model.ArtifactRef {
	return model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: commit, Path: path}, Selector: model.Selector{Kind: "whole"}}
}

// ---- records -------------------------------------------------------------------

var nestScope = model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "the nested fixture", Limitations: "a temp repository"}

func nestTaskEvent(m *nestMachine, project model.ProjectID, intent string, prereqs ...model.Prerequisite) (*model.TaskCreate, model.RecordRef) {
	if prereqs == nil {
		prereqs = []model.Prerequisite{}
	}
	spec := model.TaskSpec{Intent: intent, Subject: "the nested fixture", Scope: nestScope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: m.id(), Revision: 1, Criterion: "the fixture file is delivered"}},
		ContextRefs:        []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: prereqs, NextActor: model.Actor{ID: flowAgent}}
	create := &model.TaskCreate{ID: m.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: spec}
	return create, model.RecordRef{Project: project, RecordID: create.ID, Revision: 1}
}

func (m *nestMachine) task(dir string, project model.ProjectID, intent string) model.ID {
	m.t.Helper()
	create, _ := nestTaskEvent(m, project, intent)
	m.mustAdmit(dir, create)
	return create.ID
}

// claim is a claim whose provenance is a git pin, so admission resolves it.
func (m *nestMachine) claim(dir string, pin model.ArtifactRef) model.ID {
	m.t.Helper()
	c := &model.ClaimAssert{ID: m.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{pin}},
		Spec: model.ClaimSpec{Assertion: "the pinned file names its project", Falsifier: "it names another", Scope: nestScope, ExternalRefs: []model.ExternalReference{}}}
	m.mustAdmit(dir, c)
	return c.ID
}

// closedTask runs a task to a success close whose witnesses are the git pin.
func (m *nestMachine) closedTask(dir string, project model.ProjectID, witness model.ArtifactRef) model.ID {
	m.t.Helper()
	create, ref := nestTaskEvent(m, project, "deliver the pinned file")
	m.mustAdmit(dir, create)
	attempt := m.id()
	m.mustAdmit(dir, &model.TaskStart{Task: ref, Actor: model.Actor{ID: flowAgent}, AttemptID: attempt})
	out := m.must(dir, "handback", "--json", "--command-id", string(m.id()), "--actor", flowAgent, "--attempt-id", string(attempt),
		"--outcome", "success", "--reason", "the file is committed", "--next-action", "accept it")
	if err := m.review(dir, flowPacket(m.t, []byte(out))); err != nil {
		m.t.Fatalf("control: the success receipt admits: %v", err)
	}
	m.mustAdmit(dir, &model.TaskClose{Task: ref, Outcome: model.ClosureSuccess,
		AcceptanceWitnessRefs: []model.AcceptanceWitness{{CriterionID: create.Spec.AcceptanceCriteria[0].ID, CriterionRevision: 1, WitnessRef: witness}},
		DeliveryWitnessRefs:   []model.ArtifactRef{witness}})
	return create.ID
}

// nestHistory is what one project admitted in the fixture.
type nestHistory struct {
	project model.ProjectID
	ids     []model.ID
	pins    []model.ArtifactRef
}

// populate admits a git-pinned claim, a closed task witnessed by the same pin
// and an open task into the project at dir, whose file sits at rel.
func (m *nestMachine) populate(dir, commit, rel string, project model.ProjectID) nestHistory {
	m.t.Helper()
	pin := nestPin(commit, rel)
	return nestHistory{project: project, pins: []model.ArtifactRef{pin},
		ids: []model.ID{m.claim(dir, pin), m.closedTask(dir, project, pin), m.task(dir, project, "open work in "+string(project))}}
}

// ---- reads ---------------------------------------------------------------------

// answers is every read of the project at dir: todo, show, history, and show,
// continue and history of each id, as --json. The fresh look at this checkout
// (continue's observed HEAD, dirty state and time) is dropped, and every
// spelling of the given roots is replaced, so two checkouts compare.
func (m *nestMachine) answers(dir string, noCache bool, ids []model.ID, roots ...string) map[string]any {
	m.t.Helper()
	var env []string
	if noCache {
		env = []string{"WHOSAIDSO_NO_CACHE=1"}
	}
	reads := [][]string{{"todo"}, {"show"}, {"history"}}
	for _, id := range ids {
		reads = append(reads, []string{"show", string(id)}, []string{"continue", string(id)}, []string{"history", string(id)})
	}
	all := map[string]any{}
	for _, r := range reads {
		out, _, err := m.cli(dir, env, nil, append([]string{r[0], "--json"}, r[1:]...)...)
		if err != nil {
			m.t.Fatalf("read %v: %v", r, err)
		}
		for _, root := range roots {
			real, _ := filepath.EvalSymlinks(root)
			for _, spelling := range []string{real, root} {
				if spelling != "" {
					out = strings.ReplaceAll(out, spelling, "<root>")
				}
			}
		}
		all[strings.Join(r, " ")] = nestDropObserved(flowDecode(m.t, []byte(out)))
	}
	return all
}

func nestDropObserved(v any) any {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "observed")
		for k := range x {
			x[k] = nestDropObserved(x[k])
		}
	case []any:
		for i := range x {
			x[i] = nestDropObserved(x[i])
		}
	}
	return v
}

// nestSame reports every read whose answer differs between two snapshots.
func nestSame(t *testing.T, what string, want, got map[string]any) {
	t.Helper()
	for k := range want {
		if !reflect.DeepEqual(want[k], got[k]) {
			a, _ := json.Marshal(want[k])
			b, _ := json.Marshal(got[k])
			t.Errorf("%s: `whosaidso %s` answers differently:\nbefore %s\nafter  %s", what, k, a, b)
		}
	}
}

// shownIDs is every record id bare `show` lists, and the raw answer.
func (m *nestMachine) shownIDs(dir string) (map[string]bool, string) {
	m.t.Helper()
	out := m.must(dir, "show", "--json")
	ids := map[string]bool{}
	for _, rec := range flowList(flowDecode(m.t, []byte(out)), "records") {
		ids[flowStr(rec, "fact", "key", "id")] = true
	}
	return ids, out
}

// owns asserts the project at dir lists exactly its own ids among those given.
func (m *nestMachine) owns(dir string, mine []model.ID, others ...model.ID) {
	m.t.Helper()
	ids, raw := m.shownIDs(dir)
	for _, id := range mine {
		if !ids[string(id)] {
			m.t.Errorf("%s: its own record %s is missing from show", dir, id)
		}
	}
	for _, id := range others {
		if strings.Contains(raw, string(id)) {
			m.t.Errorf("%s: another project's record %s appears in this project's show", dir, id)
		}
	}
}

// resolves dry-runs a fresh claim citing each pin: admission's own resolver
// must still find the committed bytes from dir.
func (m *nestMachine) resolves(dir string, pins []model.ArtifactRef) {
	m.t.Helper()
	for _, pin := range pins {
		c := &model.ClaimAssert{ID: m.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{pin}},
			Spec: model.ClaimSpec{Assertion: "the pin still resolves", Falsifier: "it does not", Scope: nestScope, ExternalRefs: []model.ExternalReference{}}}
		body, err := model.Encode([]model.Event{recEncode(m.t, c)})
		if err != nil {
			m.t.Fatal(err)
		}
		if out, _, err := m.cli(dir, nil, body, "check", "admission", "--actor", flowAgent, "--events", "-"); err != nil {
			described, _ := json.Marshal(pin)
			m.t.Errorf("pin %s no longer resolves from %s: %v\n%s", described, dir, err, out)
		}
	}
}

func nestLedger(t *testing.T, root string) map[string][]byte {
	t.Helper()
	w := &flowWorld{t: t, root: root}
	bundles := w.ledger()
	for name := range bundles {
		if strings.HasPrefix(name, ".") {
			delete(bundles, name) // the admission lock, never a record
		}
	}
	return bundles
}

// ---- scenario A: an app project born in a worktree, merged under a repo project ----

// nestA is scenario A's world: the repository and its worktree, the root
// project on main, repo/foo in the worktree and (after the merge) on main.
type nestA struct {
	m             *nestMachine
	repo, wt      string
	foo, wtFoo    string // main's apps/foo and the worktree's
	root, fooHist nestHistory
	rootLate      model.ID
	before        map[string]any // foo's answers from the worktree, just before the merge
}

// nestScenarioA builds scenario A: repo/root on main, repo/foo born in a
// worktree's apps/foo, root records added on main meanwhile, then (if merge)
// the feature branch merged into main. Nothing is registered: reads and
// admission need no registry entry.
func nestScenarioA(t *testing.T, merge bool) *nestA {
	t.Helper()
	m := nestNew(t)
	a := &nestA{m: m, repo: nestRepo(t)}
	a.foo = filepath.Join(a.repo, "apps", "foo")
	commit := nestProject(t, a.repo, a.repo, "repo/root", "docs/root.json")
	a.root = m.populate(a.repo, commit, "docs/root.json", "repo/root")
	nestCommitAll(t, a.repo, "root ledger")

	a.wt = nestWorktree(t, a.repo, "feature")
	a.wtFoo = filepath.Join(a.wt, "apps", "foo")
	commit = nestProject(t, a.wt, a.wtFoo, "repo/foo", "src/out.json")
	// src/out.json is foo-relative; git holds it at apps/foo/src/out.json.
	a.fooHist = m.populate(a.wtFoo, commit, "src/out.json", "repo/foo")
	nestCommitAll(t, a.wt, "foo ledger")
	a.before = m.answers(a.wtFoo, false, a.fooHist.ids, a.wt)
	nestSame(t, "control: in the worktree, a cached read equals WHOSAIDSO_NO_CACHE=1", a.before, m.answers(a.wtFoo, true, a.fooHist.ids, a.wt))
	nestClosedControl(t, a.before, a.fooHist)

	a.rootLate = m.task(a.repo, "repo/root", "root work after the branch")
	nestCommitAll(t, a.repo, "root ledger after branch")
	if merge {
		nestGit(t, a.repo, "merge", "-q", "--no-ff", "-m", "merge feature", "feature")
	}
	return a
}

func TestNestedAppProjectMergesUnderRepoProject(t *testing.T) {
	t.Parallel()
	a := nestScenarioA(t, true)
	m := a.m
	if !reflect.DeepEqual(nestLedger(t, a.wtFoo), nestLedger(t, a.foo)) {
		t.Fatalf("control: the merge must carry foo's ledger to main byte for byte: %v vs %v", nestNames(nestLedger(t, a.wtFoo)), nestNames(nestLedger(t, a.foo)))
	}
	after := m.answers(a.foo, false, a.fooHist.ids, a.repo)
	nestSame(t, "repo/foo read from main after the merge vs the worktree before it", a.before, after)
	nestSame(t, "repo/foo read from main with WHOSAIDSO_NO_CACHE=1", a.before, m.answers(a.foo, true, a.fooHist.ids, a.repo))

	rootIDs := append(append([]model.ID{}, a.root.ids...), a.rootLate)
	m.owns(a.repo, rootIDs, a.fooHist.ids...)
	m.owns(a.foo, a.fooHist.ids, rootIDs...)
	nestSame(t, "repo/root cached vs WHOSAIDSO_NO_CACHE=1", m.answers(a.repo, true, rootIDs), m.answers(a.repo, false, rootIDs))
	m.resolves(a.repo, a.root.pins)
	m.resolves(a.foo, a.fooHist.pins)

	rootBefore, fooBefore := nestLedger(t, a.repo), nestLedger(t, a.foo)
	fooNew := m.claim(a.foo, a.fooHist.pins[0])
	if got := nestLedger(t, a.foo); len(got) != len(fooBefore)+1 || !reflect.DeepEqual(rootBefore, nestLedger(t, a.repo)) {
		t.Errorf("an admission in main's apps/foo must land in foo's ledger only: foo %d -> %d bundles, root changed=%v",
			len(fooBefore), len(got), !reflect.DeepEqual(rootBefore, nestLedger(t, a.repo)))
	}
	fooBefore = nestLedger(t, a.foo)
	rootNew := m.claim(a.repo, a.root.pins[0])
	if got := nestLedger(t, a.repo); len(got) != len(rootBefore)+1 || !reflect.DeepEqual(fooBefore, nestLedger(t, a.foo)) {
		t.Errorf("an admission at the repo root must land in root's ledger only: root %d -> %d bundles", len(rootBefore), len(got))
	}
	m.owns(a.repo, append(rootIDs, rootNew), append(a.fooHist.ids, fooNew)...)
	m.owns(a.foo, append(a.fooHist.ids, fooNew), append(rootIDs, rootNew)...)
}

func nestNames(l map[string][]byte) []string {
	names := []string{}
	for k := range l {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// nestClosedControl proves a snapshot holds real answers: the closed task
// reads CLOSED success and the open one is not closed.
func nestClosedControl(t *testing.T, answers map[string]any, h nestHistory) {
	t.Helper()
	closed := flowGet(answers["show "+string(h.ids[1])], "records", 0, "task")
	open := flowGet(answers["show "+string(h.ids[2])], "records", 0, "task")
	if flowStr(closed, "status") != "CLOSED" || flowStr(closed, "outcome") != "success" || flowStr(open, "status") == "CLOSED" {
		t.Fatalf("control: %s must read closed task CLOSED success and open task open; got %v / %v", h.project, closed, open)
	}
}

// ---- scenario 2: two app projects from two worktrees, then a root project -----

func TestNestedTwoWorktreeProjectsThenRootProject(t *testing.T) {
	t.Parallel()
	for _, order := range [][]string{{"a", "b"}, {"b", "a"}} {
		order := order
		t.Run(strings.Join(order, "-then-"), func(t *testing.T) {
			t.Parallel()
			m := nestNew(t)
			repo := nestRepo(t)
			hist, before, wtDir := map[string]nestHistory{}, map[string]map[string]any{}, map[string]string{}
			for _, app := range []string{"a", "b"} {
				wt := nestWorktree(t, repo, "app-"+app)
				dir, project := filepath.Join(wt, "apps", app), model.ProjectID("repo/"+app)
				commit := nestProject(t, wt, dir, project, "src/"+app+".json")
				hist[app] = m.populate(dir, commit, "src/"+app+".json", project)
				nestCommitAll(t, wt, string(project)+" ledger")
				before[app] = m.answers(dir, false, hist[app].ids, wt)
				nestClosedControl(t, before[app], hist[app])
				wtDir[app] = wt
			}
			for _, app := range order {
				nestGit(t, repo, "merge", "-q", "--no-ff", "-m", "merge "+app, "app-"+app)
			}
			commit := nestProject(t, repo, repo, "repo/root", "docs/root.json")
			root := m.populate(repo, commit, "docs/root.json", "repo/root")
			nestCommitAll(t, repo, "root ledger")

			other := map[string]string{"a": "b", "b": "a"}
			for _, app := range []string{"a", "b"} {
				dir := filepath.Join(repo, "apps", app)
				nestSame(t, "repo/"+app+" from main vs its worktree before the merge", before[app], m.answers(dir, false, hist[app].ids, repo, wtDir[app]))
				nestSame(t, "repo/"+app+" from main, WHOSAIDSO_NO_CACHE=1", before[app], m.answers(dir, true, hist[app].ids, repo, wtDir[app]))
				m.owns(dir, hist[app].ids, append(append([]model.ID{}, root.ids...), hist[other[app]].ids...)...)
				m.resolves(dir, hist[app].pins)
			}
			m.owns(repo, root.ids, append(append([]model.ID{}, hist["a"].ids...), hist["b"].ids...)...)
			nestSame(t, "repo/root cached vs WHOSAIDSO_NO_CACHE=1", m.answers(repo, false, root.ids), m.answers(repo, true, root.ids))
			m.resolves(repo, root.pins)
		})
	}
}

// ---- the rules told to the owner -------------------------------------------------

// Cross-project: a root task whose prerequisite is foo's closed-success task
// reads that prerequisite UNKNOWN, never TRUE and never as a refusal.
func TestNestedCrossProjectPrerequisiteReadsUnknown(t *testing.T) {
	t.Parallel()
	a := nestScenarioA(t, true)
	m := a.m
	// Control: the same prerequisite on root's own closed-success task is TRUE.
	control, _ := nestTaskEvent(m, "repo/root", "wait on root's delivery", model.Prerequisite{Kind: "task-success",
		Target: model.RecordRef{Project: "repo/root", RecordID: a.root.ids[1], Revision: 1}, WaiverPolicy: "forbid"})
	cross, _ := nestTaskEvent(m, "repo/root", "wait on foo's delivery", model.Prerequisite{Kind: "task-success",
		Target: model.RecordRef{Project: "repo/foo", RecordID: a.fooHist.ids[1], Revision: 1}, WaiverPolicy: "forbid"})
	m.mustAdmit(a.repo, control)
	if err := m.admit(a.repo, cross); err != nil {
		t.Fatalf("a cross-project prerequisite is exempt from the reference check by design, so it must admit: %v", err)
	}
	read := func(view string, id model.ID) map[string]any {
		out, _, err := m.cli(a.repo, nil, nil, view, "--json", string(id))
		if err != nil {
			t.Fatalf("%s of a task with a prerequisite must answer, not refuse: %v", view, err)
		}
		return flowDecode(t, []byte(out))
	}
	ctl := flowGet(read("show", control.ID), "records", 0, "task")
	if flowStr(ctl, "prerequisites", 0, "truth") != "TRUE" || flowStr(ctl, "status") != "READY" {
		t.Fatalf("control: a same-project prerequisite on a closed-success task reads TRUE and READY, got %s %s",
			flowStr(ctl, "prerequisites", 0, "truth"), flowStr(ctl, "status"))
	}
	task := flowGet(read("show", cross.ID), "records", 0, "task")
	if got := flowStr(task, "prerequisites", 0, "truth"); got != "UNKNOWN" || flowStr(task, "status") == "READY" {
		t.Errorf("show: foo's task is CLOSED success in foo's own ledger, but from repo/root it must read UNKNOWN (never TRUE, never READY); got truth %s, status %s", got, flowStr(task, "status"))
	}
	owed := flowGet(read("continue", cross.ID), "owed")
	// The reason must say why: another project's ledger, not an absent record here.
	if got := flowStr(owed, "items", 0, "satisfied"); got != "UNKNOWN" || flowStr(owed, "items", 0, "status", "state") != "UNKNOWN" ||
		!strings.Contains(flowStr(owed, "items", 0, "status", "reason"), "repo/foo") {
		t.Errorf("continue: the cross-project item must read satisfied UNKNOWN with its reason, got %s %v", got, flowGet(owed, "items", 0, "status"))
	}

}

// After scenario A's merge, a record admitted from the old worktree lands in
// the worktree's ledger only, and reaches main the way any branch does: the
// worktree commits, main merges it, and main's reads show it.
func TestNestedLateWorktreeRecordMergesLikeABranch(t *testing.T) {
	t.Parallel()
	a := nestScenarioA(t, true)
	m := a.m
	wtBefore, mainBefore := nestLedger(t, a.wtFoo), nestLedger(t, a.foo)
	late := m.task(a.wtFoo, "repo/foo", "admitted in the worktree after the merge")
	if len(nestLedger(t, a.wtFoo)) != len(wtBefore)+1 || !reflect.DeepEqual(mainBefore, nestLedger(t, a.foo)) {
		t.Errorf("an admission in the worktree must land in the worktree's ledger only: worktree %d -> %d, main changed=%v",
			len(wtBefore), len(nestLedger(t, a.wtFoo)), !reflect.DeepEqual(mainBefore, nestLedger(t, a.foo)))
	}
	if ids, _ := m.shownIDs(a.foo); ids[string(late)] {
		t.Error("main must not show a record its checkout does not hold")
	}
	nestCommitAll(t, a.wt, "late foo record")
	nestGit(t, a.repo, "merge", "-q", "--no-ff", "-m", "merge late", "feature")
	if ids, _ := m.shownIDs(a.foo); !ids[string(late)] {
		t.Error("after the merge, main must show the late record")
	}
	nestSame(t, "repo/foo from main vs the worktree after the late merge",
		m.answers(a.wtFoo, true, append(append([]model.ID{}, a.fooHist.ids...), late), a.wt),
		m.answers(a.foo, true, append(append([]model.ID{}, a.fooHist.ids...), late), a.repo))
}

// Same id in two worktrees: two histories of one project id born apart, on
// one machine, committed on two branches. Each worktree records in its own
// ledger; once git puts both ledgers in one folder no read may answer from it,
// no admission lands in it, and it cannot be registered.
func TestNestedSameProjectIDInTwoWorktrees(t *testing.T) {
	t.Parallel()
	m1 := nestNew(t)
	repo := nestRepo(t)
	wtX, wtY := nestWorktree(t, repo, "x-one"), nestWorktree(t, repo, "x-two")
	dX, dY, main := filepath.Join(wtX, "apps", "x"), filepath.Join(wtY, "apps", "x"), filepath.Join(repo, "apps", "x")
	cX := nestProject(t, wtX, dX, "repo/x", "src/one.json")
	cY := nestProject(t, wtY, dY, "repo/x", "src/two.json")
	hX := m1.populate(dX, cX, "src/one.json", "repo/x")
	nestCommitAll(t, wtX, "x history one")
	hY := m1.populate(dY, cY, "src/two.json", "repo/x")
	nestCommitAll(t, wtY, "x history two")
	m1.owns(dX, hX.ids, hY.ids...)
	m1.owns(dY, hY.ids, hX.ids...)

	// Merge x-one, read main (so a cache exists), then merge x-two.
	nestGit(t, repo, "merge", "-q", "--no-ff", "-m", "merge x-one", "x-one")
	m1.owns(main, hX.ids, hY.ids...)
	nestGit(t, repo, "merge", "-q", "--no-ff", "-m", "merge x-two", "x-two")
	if got := len(nestLedger(t, main)); got != len(nestLedger(t, dX))+len(nestLedger(t, dY)) {
		t.Fatalf("control: git must have merged both ledgers into one folder, found %d bundles", got)
	}
	// Every read of the merged folder refuses.
	for _, noCache := range []bool{false, true} {
		var env []string
		if noCache {
			env = []string{"WHOSAIDSO_NO_CACHE=1"}
		}
		reads := [][]string{{"todo"}, {"show"}, {"history"}}
		for _, id := range append(append([]model.ID{}, hX.ids...), hY.ids...) {
			reads = append(reads, []string{"show", string(id)}, []string{"continue", string(id)})
		}
		for _, r := range reads {
			out, _, err := m1.cli(main, env, nil, append([]string{r[0], "--json"}, r[1:]...)...)
			if err == nil {
				t.Errorf("no-cache=%v `whosaidso %s` answered from a ledger holding two histories of repo/x instead of refusing: %.300s", noCache, strings.Join(r, " "), out)
			} else if !strings.Contains(err.Error(), "ledger-fork") {
				t.Errorf("no-cache=%v `whosaidso %s` refused, but not as a fork: %v", noCache, strings.Join(r, " "), err)
			}
		}
	}
	if err := m1.admit(main, &model.ClaimAssert{ID: m1.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "a", Falsifier: "b", Scope: nestScope, ExternalRefs: []model.ExternalReference{}}}); err == nil {
		t.Error("admission into a ledger holding two histories must refuse")
	}
	if err := m1.another().register(main); err == nil {
		t.Error("a forked ledger must not be registered for the viewer")
	}
}

// The admitted records cite bytes held only in the checkout's artifact store:
// an owner's message captured from outside the project travels as a blob and
// nowhere else. An owner who commits the ledger (events) but not the store
// and merges leaves main citing bytes it lacks; registering main for the
// viewer must then refuse, and with the store committed main resolves them.
func TestNestedMoveKeepsCitedArtifacts(t *testing.T) {
	t.Parallel()
	for _, commitStore := range []bool{true, false} {
		commitStore := commitStore
		t.Run(map[bool]string{true: "store committed (control)", false: "ledger committed, store not"}[commitStore], func(t *testing.T) {
			t.Parallel()
			a := nestScenarioA(t, false)
			m := a.m
			body := []byte(`{"owner":"ship foo when the tests pass"}`)
			outside := filepath.Join(t.TempDir(), "owner.json")
			pvPut(t, filepath.Dir(outside), "owner.json", body)
			pin := pvPin(body) // no locator: the blob is the only copy
			intake := &model.SourceIntake{SourceID: m.id(), OriginalDigest: pin.Content.SHA256, Length: pin.Content.Length, SourceRef: pin,
				Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{{Project: "repo/foo", RecordID: a.fooHist.ids[2], Revision: 1}}}
			raw, err := model.Encode([]model.Event{recEncode(t, intake)})
			if err != nil {
				t.Fatal(err)
			}
			out, _, err := m.cli(a.wtFoo, nil, raw, "capture", "--json", "--actor", flowAgent, "--command-id", string(m.id()), "--blob", outside, "--events", "-")
			if err != nil {
				t.Fatalf("control: %v", err)
			}
			if err := m.review(a.wtFoo, flowPacket(t, []byte(out))); err != nil {
				t.Fatalf("control: the owner's source admits: %v", err)
			}
			m.resolves(a.wtFoo, []model.ArtifactRef{pin}) // control: the old home resolves it
			if commitStore {
				nestGit(t, a.wt, "add", "-A")
			} else {
				nestGit(t, a.wt, "add", "apps/foo/.whosaidso/events")
			}
			nestGit(t, a.wt, "commit", "-q", "-m", "owner source")
			nestGit(t, a.repo, "merge", "-q", "--no-ff", "-m", "merge feature", "feature")
			err = m.register(a.foo)
			if !commitStore {
				if err == nil || !strings.Contains(err.Error(), "home-evidence-missing") {
					t.Fatalf("registering a checkout that lacks admitted evidence must refuse: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("control: with the store committed, main registers: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(a.wtFoo, ".whosaidso", "artifacts", string(pin.Content.SHA256))); statErr != nil {
				t.Fatalf("control: the old home must still hold the blob: %v", statErr)
			}
			m.resolves(a.foo, []model.ArtifactRef{pin})
		})
	}
}
