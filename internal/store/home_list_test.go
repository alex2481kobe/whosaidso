package store

// Listing the registry: every bound project opens as Open would open it, an
// unavailable home is listed with its reason rather than dropped, and the
// list is the one under WHOSAIDSO_HOME, never another home's.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredListsEveryBindingAndWhyAHomeIsUnavailable(t *testing.T) {
	root := homeWorld(t)
	mustBind(t, root, root)
	gone := homeCheckout(t, "team/gone")
	mustBind(t, gone, gone)
	moved := homeCheckout(t, "team/moved")
	mustBind(t, moved, moved)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(moved, "whosaidso.toml"), []byte("id = 'team/other'\nledger = '.whosaidso/events'\n"))
	home, _ := Home()
	putFile(t, filepath.Join(home, "projects", "not-hex"), []byte(root+"\n"))
	putFile(t, filepath.Join(home, "projects", ".binding-1.tmp"), []byte(root+"\n"))

	list, err := Registered()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Registration{}
	for _, r := range list {
		got[string(r.ID)] = r
	}
	if len(list) != 4 {
		t.Fatalf("want 4 entries (a temp binding is not one), got %+v", list)
	}
	ok := got["team/project"]
	if ok.Reason != "" || ok.Home != root || ok.Project.Root != root || ok.Project.ID != "team/project" || ok.Project.Ledger == "" {
		t.Fatalf("the available project must open at its home: %+v", ok)
	}
	if want := mustOpen(t, root); ok.Project.Ledger != want.Ledger {
		t.Fatalf("listing must open the ledger Open opens: %s vs %s", ok.Project.Ledger, want.Ledger)
	}
	if r := got["team/gone"]; r.Home != gone || !strings.Contains(r.Reason, "unavailable") || r.Project.Root != "" {
		t.Fatalf("a deleted home is listed unavailable with its reason: %+v", r)
	}
	if r := got["team/moved"]; !strings.Contains(r.Reason, "declares project team/other") {
		t.Fatalf("a home declaring another project is unavailable: %+v", r)
	}
	if r := got["not-hex"]; !strings.Contains(r.Reason, "not the lowercase hex") {
		t.Fatalf("an undecodable entry is listed with its reason: %+v", r)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ID > list[i].ID {
			t.Fatalf("the list is sorted by project id: %v", list)
		}
	}
}

func TestRegisteredReadsOnlyTheHomeWhoSaidSoHomeNames(t *testing.T) {
	root := homeWorld(t)
	mustBind(t, root, root)
	if list, err := Registered(); err != nil || len(list) != 1 {
		t.Fatalf("control: the bound project is listed: %v %v", list, err)
	}
	t.Setenv(HomeEnv, filepath.Join(t.TempDir(), "elsewhere"))
	if list, err := Registered(); err != nil || len(list) != 0 {
		t.Fatalf("another WHOSAIDSO_HOME lists nothing, got %v %v", list, err)
	}
	t.Setenv(HomeEnv, "relative/home")
	if _, err := Registered(); err == nil {
		t.Fatal("a relative WHOSAIDSO_HOME is refused, as Home refuses it")
	}
}
