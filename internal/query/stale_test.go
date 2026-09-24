package query

// The stale-claims read with a git the test supplies: which claims it
// answers, which run is the last, and the HEAD-only comparison's TRUE, FALSE
// and UNKNOWN. That the CLI supplies the invoking checkout's git is tested in
// cmd/whosaidso; the git observer itself in internal/evidence.

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// staleWorld admits task 1 with attempt 70, validated instrument 10, and
// claim 21 (scoped to internal/query/query.go) with its criterion, unobserved.
func staleWorld(t *testing.T) store.Project {
	t.Helper()
	p := testProject(t)
	readyControl(t, p)
	appendEvents(t, p, 101, admitted(101,
		&model.InstrumentDeclare{ID: testID(10), Provenance: prov("agent-a"), Spec: instrumentSpec(true)},
		&model.ClaimAssert{ID: testID(21), Provenance: prov("agent-e"), Spec: claimSpec()}, criterionFix(21))...)
	appendEvents(t, p, 102, &model.TaskStart{Task: testRef(1, 1), AttemptID: testID(70), Actor: model.Actor{ID: "worker"}})
	return p
}

// staleRun admits and seals run id of claim 21, recording head and dirty.
func staleRun(t *testing.T, p store.Project, id int, head model.Availability[model.GitHead], dirty model.Availability[bool]) {
	t.Helper()
	env := envelope(id, 70, 10, 21)
	env.ExecutionSourceIdentity.Head, env.ExecutionSourceIdentity.Dirty = head, dirty
	appendEvents(t, p, id*10, admitted(id*10, &model.InvocationStart{Envelope: env})...)
	appendEvents(t, p, id*10+1, seal(env, 0, time.Second))
}

// fakeGit answers HEAD and records every comparison it is asked for.
type fakeGit struct {
	asked   [][2]model.GitHead
	changed []string
	err     error
}

func (g *fakeGit) at(head model.Availability[model.GitHead]) *StaleGit {
	return &StaleGit{Head: head, Changes: func(from, to model.GitHead, scope []string) ([]string, error) {
		if !reflect.DeepEqual(scope, []string{"internal/query/query.go"}) {
			panic("the comparison must be asked over the claim's scope, got " + strings.Join(scope, ","))
		}
		g.asked = append(g.asked, [2]model.GitHead{from, to})
		return g.changed, g.err
	}}
}

func staleOnly(t *testing.T, p store.Project, git *StaleGit) []StaleClaim {
	t.Helper()
	a := view_(t, p, ViewRequest{View: "show", Stale: git}).(*ShowAnswer)
	if a.Stale == nil || !strings.Contains(a.Stale.BlindSpot, "uncommitted changes are invisible") {
		t.Fatalf("show --stale must carry its section and HEAD-only blind spot: %+v", a.Stale)
	}
	return a.Stale.Claims
}

func TestStaleClaimsComparesTheLastRunsCommitWithTheSuppliedHead(t *testing.T) {
	p := staleWorld(t)
	head := func(c byte) model.GitHead {
		return model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat(string(c), 40)}
	}
	a, b, current := head('a'), head('b'), head('c')
	git := &fakeGit{changed: []string{"internal/query/query.go"}}
	if got := staleOnly(t, p, git.at(known(current))); len(got) != 0 || len(git.asked) != 0 {
		t.Fatalf("control: an unobserved claim has no last run to be stale against: %+v, asked %v", got, git.asked)
	}

	staleRun(t, p, 50, known(a), known(false))
	got := staleOnly(t, p, git.at(known(current)))
	if len(got) != 1 || got[0].Stale != reduce.TruthTrue || !reflect.DeepEqual(got[0].ChangedPaths, git.changed) ||
		*got[0].RunHead != a || *got[0].CurrentHead != current || got[0].LastRun.InvocationID != testID(50) {
		t.Fatalf("scoped files changed since the run at a: want stale TRUE, got %+v", got)
	}
	if want := [][2]model.GitHead{{a, current}}; !reflect.DeepEqual(git.asked, want) {
		t.Fatalf("git must be asked from the run's commit to the supplied HEAD: %v", git.asked)
	}

	staleRun(t, p, 51, known(b), known(false))
	git = &fakeGit{changed: []string{}}
	if got := staleOnly(t, p, git.at(known(current))); len(got) != 1 || got[0].Stale != reduce.TruthFalse || got[0].LastRun.InvocationID != testID(51) ||
		len(got[0].ChangedPaths) != 0 || len(git.asked) != 1 || git.asked[0][0] != b {
		t.Fatalf("the last sealed run is at b and nothing scoped changed: want stale FALSE, got %+v, asked %v", got, git.asked)
	}

	git = &fakeGit{err: errors.New("commit not found in this repository: " + b.Commit)}
	if got := staleOnly(t, p, git.at(known(current))); len(got) != 1 || got[0].Stale != reduce.TruthUnknown || got[0].Reason != git.err.Error() {
		t.Fatalf("a comparison git cannot make is UNKNOWN with git's reason, never FALSE: %+v", got)
	}

	git = &fakeGit{}
	if got := staleOnly(t, p, git.at(notKnown[model.GitHead]("no checkout here"))); len(got) != 1 || got[0].Stale != reduce.TruthUnknown ||
		got[0].Reason != "this checkout's HEAD is unknown: no checkout here" || len(git.asked) != 0 {
		t.Fatalf("an unreadable HEAD is UNKNOWN with its reason and asks git nothing: %+v", got)
	}
}

func TestStaleClaimsCannotCompareARunNotKnownClean(t *testing.T) {
	head := model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40)}
	for name, run := range map[string]struct {
		head  model.Availability[model.GitHead]
		dirty model.Availability[bool]
	}{
		"unknown head":  {notKnown[model.GitHead]("not observed"), known(false)},
		"unknown dirty": {known(head), notKnown[bool]("the checkout differs from HEAD")},
		"dirty":         {known(head), known(true)},
	} {
		t.Run(name, func(t *testing.T) {
			p := staleWorld(t)
			staleRun(t, p, 50, run.head, run.dirty)
			git := &fakeGit{changed: []string{"internal/query/query.go"}}
			got := staleOnly(t, p, git.at(known(model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("c", 40)})))
			if len(got) != 1 || got[0].Stale != reduce.TruthUnknown || !strings.Contains(got[0].Reason, "not known clean") || len(git.asked) != 0 {
				t.Fatalf("a run whose commit is not known clean cannot be compared: %+v, asked %v", got, git.asked)
			}
		})
	}
}
