//go:build windows

package store

// Intake creates its staging directory with the explicit owner-only list,
// not one inherited from the inbox. Once the inbox passes the owner-only
// check the two grant the same access, so only the list's own form, read
// while the capture runs, tells them apart.

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestIntakeStagingListIsSetAtCreation(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	p := intakeProject(t)
	disk := systemIntakeIO()
	got := ""
	disk.sync = func(f *os.File) error {
		name := f.Name()
		if info, err := os.Stat(name); got == "" && err == nil && info.IsDir() && strings.HasSuffix(name, ".tmp") {
			t.Setenv("WHOSAIDSO_ACL_PATH", name)
			got = sddl(t, name)
		}
		return f.Sync()
	}
	if _, err := writeIntake(context.Background(), p, requestFor(commandID(1), "source"), disk); err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("the staging directory was never flushed")
	}
	dacl := got[strings.Index(got, "D:"):]
	if want := rendered(t, "D:P(A;OICI;FA;;;"+user+")"); dacl != want {
		t.Fatalf("staging list = %s, want the explicit protected list %s", dacl, want)
	}
}
