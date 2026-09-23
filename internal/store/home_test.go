package store

// The registry's rules: DATUM_HOME moves every per-machine store together, an
// unbound project refuses, a missing home refuses without fallback or
// recreation, relocation checks continuity, symlink aliases name one home, and
// rebinding is serialized with admission under the ledger lock.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"datum/internal/model"
)

// homeWorld is one isolated Datum home and a project root declaring id.
func homeWorld(t *testing.T) (root string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(HomeEnv, filepath.Join(t.TempDir(), "datum-home"))
	return homeCheckout(t, "team/project")
}

// homeCheckout is a new directory holding a datum.toml that declares id.
func homeCheckout(t *testing.T, id string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(root, "datum.toml"), []byte("id = '"+id+"'\nledger = '.datum/events'\n"))
	return root
}

func mustBind(t *testing.T, cwd, target string) BindResult {
	t.Helper()
	r, err := Bind(context.Background(), cwd, target)
	if err != nil {
		t.Fatalf("control: binding %s: %v", target, err)
	}
	return r
}

func mustOpen(t *testing.T, cwd string) Project {
	t.Helper()
	p, err := Open(cwd)
	if err != nil {
		t.Fatalf("control: opening %s: %v", cwd, err)
	}
	return p
}

// copyLedger copies from's ledger files into to's ledger, as a clone would.
func copyLedger(t *testing.T, from, to string) {
	t.Helper()
	src, dst := filepath.Join(from, ".datum", "events"), filepath.Join(to, ".datum", "events")
	mustMkdirAll(t, dst)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(dst, e.Name()), data)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHomeEnvIsolatesAllFourStores(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	home := filepath.Join(t.TempDir(), "datum-home")
	t.Setenv(HomeEnv, home)
	root := homeCheckout(t, "team/project")
	p := mustBind(t, root, root).Project
	capturedControl(t, p, commandID(3), "owner words")
	staging, err := MakeRunStaging(p, commandID(7))
	if err != nil {
		t.Fatalf("control: staging: %v", err)
	}
	if _, err := MachineID(); err != nil {
		t.Fatalf("control: machine id: %v", err)
	}
	encoded, _ := encodeProjectID(p.ID)
	for _, want := range []string{
		filepath.Join(home, "projects", encoded),
		filepath.Join(home, "intake", encoded),
		filepath.Join(home, "staging", encoded),
		filepath.Join(home, MachineIDFile),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("%s must be under DATUM_HOME: %v", want, err)
		}
	}
	if !strings.HasPrefix(staging, home+string(filepath.Separator)) {
		t.Errorf("staging %s is outside DATUM_HOME %s", staging, home)
	}
	if _, err := os.Lstat(filepath.Join(userHome, ".datum")); !os.IsNotExist(err) {
		t.Fatalf("with DATUM_HOME set nothing may reach ~/.datum, found it: %v", err)
	}
	t.Setenv(HomeEnv, "relative/home")
	if _, err := Home(); err == nil {
		t.Fatal("a relative DATUM_HOME must be refused, not resolved against the cwd")
	}
}

func TestUnboundProjectRefusesAndIsNeverBoundSilently(t *testing.T) {
	root := homeWorld(t)
	for i := 0; i < 2; i++ {
		_, err := Open(root)
		requireFault(t, err, "home-unbound")
		if !strings.Contains(err.Error(), "datum home PATH") {
			t.Fatalf("the refusal must say how to bind: %v", err)
		}
	}
	if _, bound, err := Binding("team/project"); bound || err != nil {
		t.Fatalf("opening must never bind: bound=%v, %v", bound, err)
	}
	// Control: after an explicit bind the same checkout opens.
	r := mustBind(t, root, root)
	if r.Continuity != ContinuityNew || r.Project.Root != root {
		t.Fatalf("first bind: %+v", r)
	}
	if p := mustOpen(t, root); p.Root != root || p.Checkout != root {
		t.Fatalf("home and checkout are one place here: %+v", p)
	}
}

func TestSecondCheckoutReadsAndAdmitsThroughTheHome(t *testing.T) {
	home := homeWorld(t)
	clone := homeCheckout(t, "team/project")
	mustBind(t, home, home)
	p := mustOpen(t, clone)
	if p.Root != home || p.Checkout != clone || p.Ledger != filepath.Join(home, ".datum", "events") || p.ExecRoot() != clone {
		t.Fatalf("a second checkout must read and admit through the home and execute here: %+v", p)
	}
	admitControl(t, p, 1)
	readControl(t, Project{ID: p.ID, Root: home, Ledger: filepath.Join(home, ".datum", "events")}, 1)
	if _, err := os.Stat(filepath.Join(clone, ".datum")); !os.IsNotExist(err) {
		t.Fatalf("admission from a second checkout wrote its own ledger: %v", err)
	}
}

func TestMissingHomeRefusesWithoutFallbackOrRecreation(t *testing.T) {
	home := homeWorld(t)
	clone := homeCheckout(t, "team/project")
	mustBind(t, home, home)
	opened := mustOpen(t, clone)
	admitControl(t, Project{ID: "team/project", Root: clone, Ledger: filepath.Join(clone, ".datum", "events")}, 1)
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	_, err := Open(clone)
	requireFault(t, err, "home-unavailable")
	// A project opened before the home vanished must not recreate it.
	_, err = Transact(context.Background(), opened, admissionID(2), digestFor(2), proposeFor(2))
	requireFault(t, err, "home-unavailable")
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("a deleted home was recreated: %v", err)
	}
	// The clone's own ledger is untouched and was never read as the home.
	readControl(t, Project{ID: "team/project", Root: clone, Ledger: filepath.Join(clone, ".datum", "events")}, 1)
	// A home that now declares another project is unavailable too.
	mustMkdirAll(t, home)
	putFile(t, filepath.Join(home, "datum.toml"), []byte("id = 'team/other'\nledger = '.datum/events'\n"))
	_, err = Open(clone)
	requireFault(t, err, "home-unavailable")
}

func TestRelocationChecksContinuity(t *testing.T) {
	old := homeWorld(t)
	mustBind(t, old, old)
	admitSeries(t, mustOpen(t, old), 2)

	diverged := homeCheckout(t, "team/project")
	copyLedger(t, old, diverged)
	admitControl(t, Project{ID: "team/project", Root: diverged, Ledger: filepath.Join(diverged, ".datum", "events")}, 3)
	short := homeCheckout(t, "team/project")
	behind := homeCheckout(t, "team/project")
	copyLedger(t, old, behind)
	admitControl(t, mustOpen(t, old), 4) // the old home moves on; behind lacks bundle 3

	for name, dest := range map[string]string{"empty": short, "behind": behind} {
		_, err := Bind(context.Background(), old, dest)
		requireFault(t, err, "home-continuity")
		if root, _, _ := Binding("team/project"); root != old {
			t.Fatalf("%s: a refused relocation changed the binding to %s", name, root)
		}
	}
	// diverged has old's first two bundles but a different third.
	_, err := Bind(context.Background(), old, diverged)
	requireFault(t, err, "home-continuity")

	// Control: a destination that continues the old history, plus more.
	next := homeCheckout(t, "team/project")
	copyLedger(t, old, next)
	admitControl(t, Project{ID: "team/project", Root: next, Ledger: filepath.Join(next, ".datum", "events")}, 5)
	r := mustBind(t, old, next)
	if r.Continuity != ContinuityContinued || r.Previous != old || r.Bundles != 4 {
		t.Fatalf("relocation with continuity: %+v", r)
	}
	if p := mustOpen(t, old); p.Root != next {
		t.Fatalf("after relocation the old checkout reads through the new home: %+v", p)
	}
}

func TestRelocationWithTheOldHomeGone(t *testing.T) {
	old := homeWorld(t)
	mustBind(t, old, old)
	admitSeries(t, mustOpen(t, old), 2)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	r := mustBind(t, moved, moved)
	if r.Continuity != ContinuityNotCompared || r.Previous != old || r.Bundles != 2 {
		t.Fatalf("old home gone: continuity must be not-compared, got %+v", r)
	}
	// Still validated: a gone old home does not excuse a broken destination.
	broken := homeCheckout(t, "team/project")
	mustMkdirAll(t, filepath.Join(broken, ".datum", "events"))
	putFile(t, filepath.Join(broken, ".datum", "events", "junk"), []byte("x"))
	if err := os.RemoveAll(moved); err != nil {
		t.Fatal(err)
	}
	_, err = Bind(context.Background(), broken, broken)
	requireFault(t, err, "home-invalid")
}

func TestRelocationToAnotherProjectIsRefused(t *testing.T) {
	root := homeWorld(t)
	mustBind(t, root, root)
	other := homeCheckout(t, "team/other")
	_, err := Bind(context.Background(), root, other)
	requireFault(t, err, "home-invalid")
	if !strings.Contains(err.Error(), "team/other") {
		t.Fatalf("the refusal must name the declared project: %v", err)
	}
	escaping := homeCheckout(t, "team/project")
	putFile(t, filepath.Join(escaping, "datum.toml"), []byte("id = 'team/project'\nledger = '../elsewhere'\n"))
	_, err = Bind(context.Background(), root, escaping)
	requireFault(t, err, "home-invalid")
	if got, _, _ := Binding("team/project"); got != root {
		t.Fatalf("refused binds changed the binding to %s", got)
	}
}

func TestSymlinkAliasesNameOneHome(t *testing.T) {
	root := homeWorld(t)
	alias := filepath.Join(t.TempDir(), "alias")
	mustSymlink(t, root, alias)
	r := mustBind(t, alias, alias)
	if r.Project.Root != root {
		t.Fatalf("the binding must hold the canonical root %s, got %s", root, r.Project.Root)
	}
	raw, err := os.ReadFile(func() string { p, _ := bindingPath("team/project"); return p }())
	if err != nil || string(raw) != root+"\n" {
		t.Fatalf("binding file: %q, %v", raw, err)
	}
	if again := mustBind(t, root, root); again.Continuity != ContinuitySame {
		t.Fatalf("binding the real path after its alias is the same home, got %+v", again)
	}
	// Opening through the alias is the home itself, in the alias's spelling.
	p := mustOpen(t, alias)
	if p.Root != alias || p.Checkout != alias {
		t.Fatalf("alias open: %+v", p)
	}
	admitControl(t, p, 1)
	readControl(t, mustOpen(t, root), 1)
}

// TestRebindInsideTheLockWindowIsCaught rebinds after admission's first check
// and before it takes the lock: only the check under the lock can see it.
func TestRebindInsideTheLockWindowIsCaught(t *testing.T) {
	old := homeWorld(t)
	mustBind(t, old, old)
	p := mustOpen(t, old)
	admitControl(t, p, 1)
	next := homeCheckout(t, "team/project")
	copyLedger(t, old, next)
	disk := systemPublishIO()
	disk.beforeLock = func() { mustBind(t, old, next) }
	_, err := transact(context.Background(), p, admissionID(2), digestFor(2), proposeFor(2), disk)
	requireFault(t, err, "home-moved")
	readControl(t, p, 1)
	// Control: the same admission opened afterwards lands in the new home.
	admitControl(t, mustOpen(t, old), 2)
	readControl(t, Project{ID: p.ID, Root: next, Ledger: filepath.Join(next, ".datum", "events")}, 2)
}

// TestConcurrentAdmissionAndRebindLoseNothing races admissions against a
// relocation. Whatever the interleaving, every admission that succeeded is in
// the home bound at the end: nothing lands in an old home after the switch.
func TestConcurrentAdmissionAndRebindLoseNothing(t *testing.T) {
	for round := 0; round < 5; round++ {
		old := homeWorld(t)
		mustBind(t, old, old)
		admitControl(t, mustOpen(t, old), 1)
		next := homeCheckout(t, "team/project")
		copyLedger(t, old, next)
		p := mustOpen(t, old)
		var wg sync.WaitGroup
		var mu sync.Mutex
		admitted := []model.ID{admissionID(1)}
		for n := 2; n <= 6; n++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				if _, err := Transact(context.Background(), p, admissionID(n), digestFor(n), proposeFor(n)); err == nil {
					mu.Lock()
					admitted = append(admitted, admissionID(n))
					mu.Unlock()
				}
			}(n)
		}
		_, bindErr := Bind(context.Background(), old, next)
		wg.Wait()
		bound, _, _ := Binding(p.ID)
		if bindErr != nil {
			requireFault(t, bindErr, "home-continuity")
		}
		final, err := ReadPrefix(Project{ID: p.ID, Root: bound, Ledger: filepath.Join(bound, ".datum", "events")})
		if err != nil {
			t.Fatal(err)
		}
		in := map[model.ID]bool{}
		for _, b := range final {
			in[b.CommandID] = true
		}
		for _, id := range admitted {
			if !in[id] {
				t.Fatalf("round %d: admission %s succeeded but is not in the bound home %s (bind error %v)", round, id, bound, bindErr)
			}
		}
	}
}
