package store

// Recovery from a registry entry that names no home (home.go, home_bind.go):
// the advice the refusal gives, an explicit bind, must work and say what it
// replaced; a readable entry naming another home is never replaced silently,
// and a ledger path changed since open refuses admission. The registry's
// ordinary binding and relocation cases are in home_test.go.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"whosaidso/internal/model"
)

func TestBindReplacesAnUnreadableBindingAndSaysSo(t *testing.T) {
	root := homeWorld(t)
	mustBind(t, root, root) // control: an ordinary binding
	path, err := bindingPath("team/project")
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []func(){
		func() { putFile(t, path, []byte("broken\n")) },
		func() { must(t, os.Remove(path)); must(t, os.Symlink(root, path)) },
	} {
		damage()
		if _, err := Open(root); !isFault(err, codeBindingCorrupt) {
			t.Fatalf("control: a damaged entry must refuse reads, got %v", err)
		}
		r := mustBind(t, root, root)
		if r.Continuity != ContinuityUnreadable || r.Previous != "" {
			t.Fatalf("replacing an unreadable entry must say so and invent no previous home: %+v", r)
		}
		if got, bound, err := Binding("team/project"); err != nil || !bound || got != root {
			t.Fatalf("the entry must name the bound home again: %q %t %v", got, bound, err)
		}
	}
	// A readable entry naming another home that still exists is relocation:
	// its history is compared (home_test.go refuses a broken one), and the
	// result names the previous home, never "unreadable".
	other := homeCheckout(t, "team/project")
	if r := mustBind(t, other, other); r.Continuity != ContinuityContinued || r.Previous != root {
		t.Fatalf("a readable binding must go through relocation, got %+v", r)
	}
}

func TestAdmissionRefusesALedgerPathChangedSinceOpen(t *testing.T) {
	root := homeWorld(t)
	mustBind(t, root, root)
	p := mustOpen(t, root)
	if err := recheckHome(p); err != nil {
		t.Fatalf("control: an unchanged home rechecks clean: %v", err)
	}
	putFile(t, filepath.Join(root, "whosaidso.toml"), []byte("id = 'team/project'\nledger = '.relocated/events'\n"))
	if err := recheckHome(p); !isFault(err, "home-moved") {
		t.Fatalf("a ledger path changed since open must refuse publication, got %v", err)
	}
}

func isFault(err error, code string) bool {
	var f *model.Fault
	return errors.As(err, &f) && f.Code == code
}
