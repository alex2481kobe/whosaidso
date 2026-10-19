package store

// The home's rules: WHOSAIDSO_HOME moves every per-machine store together;
// reads and admission use the invoking checkout whatever the registry names;
// a registry entry names only a checkout of the same project whose ledger
// reads, and symlink aliases name one checkout.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homeWorld is one isolated WhoSaidSo home and a project root declaring id.
func homeWorld(t *testing.T) (root string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(HomeEnv, filepath.Join(t.TempDir(), "whosaidso-home"))
	return homeCheckout(t, "team/project")
}

// homeCheckout is a new directory holding a whosaidso.toml that declares id.
func homeCheckout(t *testing.T, id string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(root, "whosaidso.toml"), []byte("id = '"+id+"'\nledger = '.whosaidso/events'\n"))
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
	p, err := Discover(cwd)
	if err != nil {
		t.Fatalf("control: opening %s: %v", cwd, err)
	}
	return p
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
	home := filepath.Join(t.TempDir(), "whosaidso-home")
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
			t.Errorf("%s must be under WHOSAIDSO_HOME: %v", want, err)
		}
	}
	if !strings.HasPrefix(staging, home+string(filepath.Separator)) {
		t.Errorf("staging %s is outside WHOSAIDSO_HOME %s", staging, home)
	}
	if _, err := os.Lstat(filepath.Join(userHome, ".whosaidso")); !os.IsNotExist(err) {
		t.Fatalf("with WHOSAIDSO_HOME set nothing may reach ~/.whosaidso, found it: %v", err)
	}
	t.Setenv(HomeEnv, "relative/home")
	if _, err := Home(); err == nil {
		t.Fatal("a relative WHOSAIDSO_HOME must be refused, not resolved against the cwd")
	}
}

// TestEveryCheckoutReadsAndAdmitsItsOwnLedger: the registry routes nothing.
// A second checkout of the project, with the registry naming the first,
// admits into and reads from its own ledger; the first is untouched.
func TestEveryCheckoutReadsAndAdmitsItsOwnLedger(t *testing.T) {
	first := homeWorld(t)
	second := homeCheckout(t, "team/project")
	mustBind(t, first, first)
	p := mustOpen(t, second)
	if p.Root != second || p.Ledger != filepath.Join(second, ".whosaidso", "events") {
		t.Fatalf("a checkout must open its own ledger whatever the registry names: %+v", p)
	}
	admitControl(t, p, 1)
	readControl(t, p, 1)
	if _, err := os.Stat(filepath.Join(first, ".whosaidso")); !os.IsNotExist(err) {
		t.Fatalf("admission from the second checkout reached the registered one: %v", err)
	}
	// An unregistered project and a registered checkout that is gone change
	// nothing either.
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	admitControl(t, mustOpen(t, second), 2)
	readControl(t, p, 2)
	t.Setenv(HomeEnv, filepath.Join(t.TempDir(), "empty-home"))
	readControl(t, mustOpen(t, second), 2)
}

func TestRegistrationRefusesAnotherProjectOrAnUnreadableLedger(t *testing.T) {
	root := homeWorld(t)
	mustBind(t, root, root)
	other := homeCheckout(t, "team/other")
	_, err := Bind(context.Background(), root, other)
	requireFault(t, err, "home-invalid")
	if !strings.Contains(err.Error(), "team/other") {
		t.Fatalf("the refusal must name the declared project: %v", err)
	}
	escaping := homeCheckout(t, "team/project")
	putFile(t, filepath.Join(escaping, "whosaidso.toml"), []byte("id = 'team/project'\nledger = '../elsewhere'\n"))
	_, err = Bind(context.Background(), root, escaping)
	requireFault(t, err, "home-invalid")
	broken := homeCheckout(t, "team/project")
	mustMkdirAll(t, filepath.Join(broken, ".whosaidso", "events"))
	putFile(t, filepath.Join(broken, ".whosaidso", "events", "junk"), []byte("x"))
	_, err = Bind(context.Background(), root, broken)
	requireFault(t, err, "home-invalid")
	if got, _, _ := Binding("team/project"); got != root {
		t.Fatalf("refused registrations changed the entry to %s", got)
	}
	// Control: another readable checkout of the project replaces the entry
	// and names the one it replaced; no history is compared.
	next := homeCheckout(t, "team/project")
	admitControl(t, mustOpen(t, next), 1)
	if r := mustBind(t, root, next); r.Previous != root || r.Bundles != 1 || r.Project.Root != next {
		t.Fatalf("re-registration: %+v", r)
	}
}

func TestSymlinkAliasesNameOneCheckout(t *testing.T) {
	root := homeWorld(t)
	alias := filepath.Join(t.TempDir(), "alias")
	mustSymlink(t, root, alias)
	r := mustBind(t, alias, alias)
	if r.Project.Root != root {
		t.Fatalf("the entry must hold the canonical root %s, got %s", root, r.Project.Root)
	}
	raw, err := os.ReadFile(func() string { p, _ := bindingPath("team/project"); return p }())
	if err != nil || string(raw) != root+"\n" {
		t.Fatalf("registry entry: %q, %v", raw, err)
	}
	if again := mustBind(t, root, root); again.Previous != root {
		t.Fatalf("registering the real path after its alias is the same checkout, got %+v", again)
	}
	// Opening through the alias keeps the alias's spelling.
	p := mustOpen(t, alias)
	if p.Root != alias || p.Checkout != alias {
		t.Fatalf("alias open: %+v", p)
	}
	admitControl(t, p, 1)
	readControl(t, mustOpen(t, root), 1)
}
