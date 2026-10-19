//go:build windows

package store

// The restore command an insecure-permissions refusal names must work when a
// user pastes it into PowerShell, whatever made the path widen and whatever
// the path holds. icacls exits 0 even when it changed nothing, so each case
// reads the list back through PowerShell afterwards instead of trusting the
// exit status.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/testaccess"
)

// unmapped is a SID no account resolves to on any machine, as the capability
// and sandbox SIDs some tools grant on a parent folder do not resolve here.
const unmapped = "S-1-5-21-1-2-3-4"

// setList replaces path's access list with sddl, through .NET rather than
// icacls, which refuses to name an unmapped SID.
func setList(t *testing.T, path, sddl string) {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$i = Get-Item -LiteralPath $env:WHOSAIDSO_ACL_PATH; $a = $i.GetAccessControl('Access'); "+
			"$a.SetSecurityDescriptorSddlForm($env:WHOSAIDSO_ACL_SDDL, 'Access'); $i.SetAccessControl($a)")
	cmd.Env = append(os.Environ(), "WHOSAIDSO_ACL_PATH="+path, "WHOSAIDSO_ACL_SDDL="+sddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("set %s on %s: %v: %s", sddl, path, err, out)
	}
}

type widening struct {
	name      string
	directory bool
	widen     func(t *testing.T, path string)
}

func widenings(user string) []widening {
	return []widening{
		{"explicit unmapped grant", true, func(t *testing.T, path string) {
			setList(t, path, "D:P(A;OICI;FA;;;"+user+")(A;OICI;FA;;;"+unmapped+")")
		}},
		{"unmapped grant inherited from the parent", true, func(t *testing.T, path string) {
			parent := filepath.Dir(path)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			setList(t, parent, "D:P(A;OICI;FA;;;"+user+")(A;OICI;FA;;;"+unmapped+")")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory whose own entry is not inherited", true, func(t *testing.T, path string) {
			setList(t, path, "D:P(A;;FA;;;"+user+")")
		}},
		{"NULL list", true, func(t *testing.T, path string) {
			setList(t, path, "D:NO_ACCESS_CONTROL")
		}},
		{"file with an unmapped grant", false, func(t *testing.T, path string) {
			setList(t, path, "D:P(A;;FA;;;"+user+")(A;;FA;;;"+unmapped+")")
		}},
	}
}

func TestRestoreCommandMakesThePathOwnerOnly(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range widenings(user) {
		t.Run(w.name, func(t *testing.T) {
			requireRestoreWorks(t, user, filepath.Join(t.TempDir(), "it's private"), w)
		})
	}
}

// Each name holds what PowerShell would expand or end a string at inside
// double quotes, or cmd would expand: the path must reach icacls as typed.
func TestRestoreCommandQuotesThePathForPowerShell(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	w := widenings(user)[0]
	for _, name := range []string{"$(Write-Output x)", "back`tick", "%USERPROFILE%", "it's", "it’s", "a $b `$c '%d%' (e)"} {
		t.Run(name, func(t *testing.T) {
			requireRestoreWorks(t, user, filepath.Join(t.TempDir(), name), w)
		})
	}
}

func requireRestoreWorks(t *testing.T, user, path string, w widening) {
	t.Helper()
	if w.directory {
		if err := mkdirPrivate(path); err != nil {
			t.Fatal(err)
		}
	} else {
		putFile(t, path, []byte("x"))
	}
	w.widen(t, path)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	refusal := privateAccessFault(path, info, w.directory)
	requireFault(t, refusal, "insecure-permissions")
	var fault *model.Fault
	errors.As(refusal, &fault)
	restore := between(t, fault.Detail, "restore permissions in PowerShell with ", "; then retry")
	if out, err := testaccess.RunRestore(restore); err != nil {
		t.Fatalf("%s: %v: %s", restore, err, out)
	}
	// The user's own entry, as sddl displays it; the list must be protected
	// (P; icacls also sets AI) and hold exactly that entry.
	own := "D:P(A;;FA;;;" + user + ")"
	if w.directory {
		own = "D:P(A;OICI;FA;;;" + user + ")"
	}
	_, want, _ := strings.Cut(rendered(t, own), "D:P")
	t.Setenv("WHOSAIDSO_ACL_PATH", path)
	got := sddl(t, path)
	_, list, _ := strings.Cut(got, "D:")
	flags, entries, _ := strings.Cut(list, "(")
	if !strings.Contains(flags, "P") || "("+entries != want {
		t.Fatalf("after %s the list is %s, want protected and exactly %s", restore, got, want)
	}
	if err := privateAccessFault(path, info, w.directory); err != nil {
		t.Fatalf("after the restore: %v", err)
	}
}

func between(t *testing.T, s, start, end string) string {
	t.Helper()
	i := strings.Index(s, start)
	j := strings.LastIndex(s, end)
	if i < 0 || j < i+len(start) {
		t.Fatalf("no %q ... %q in %q", start, end, s)
	}
	return s[i+len(start) : j]
}
