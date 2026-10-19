//go:build windows

// Package testaccess gives tests outside the store one way, per platform, to
// widen a path's access beyond its owner, to undo that, and to run the restore
// command an insecure-permissions refusal names. On POSIX that is mode bits;
// on Windows it is the access-control list, changed with icacls and read
// through PowerShell, neither of which shares code with the store. Only tests
// import it.
package testaccess

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"testing"
)

// everyone is the well-known SID of the Everyone group; SDDL writes it WD.
const everyone = "S-1-1-0"

func icacls(t testing.TB, args ...string) {
	t.Helper()
	if out, err := exec.Command("icacls", args...).CombinedOutput(); err != nil {
		t.Fatalf("icacls %v: %v: %s", args, err, out)
	}
}

// Widen lets Everyone read path.
func Widen(t testing.TB, path string) {
	t.Helper()
	icacls(t, path, "/grant", "*"+everyone+":R")
}

// Restore removes the grant Widen added.
func Restore(t testing.TB, path string) {
	t.Helper()
	icacls(t, path, "/remove:g", "*"+everyone)
}

// Widened reports whether Everyone still has an entry in path's list.
func Widened(t testing.TB, path string) bool {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-Item -LiteralPath $env:TESTACCESS_PATH).GetAccessControl().Sddl")
	cmd.Env = append(os.Environ(), "TESTACCESS_PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read the access list of %s: %v: %s", path, err, out)
	}
	sddl := string(out)
	return strings.Contains(sddl, ";WD)") || strings.Contains(sddl, ";"+everyone+")")
}

// OwnerOnly reports whether path's list holds at least one entry and every
// entry, explicit or inherited, allows the current user and no one else.
func OwnerOnly(t testing.TB, path string) bool {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$a = (Get-Item -LiteralPath $env:TESTACCESS_PATH).GetAccessControl('Access'); "+
			"$a.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]) | "+
			"ForEach-Object { $_.AccessControlType.ToString() + ' ' + $_.IdentityReference.Value }")
	cmd.Env = append(os.Environ(), "TESTACCESS_PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read the access list of %s: %v: %s", path, err, out)
	}
	rules := strings.Fields(string(out))
	if len(rules) == 0 || len(rules)%2 != 0 {
		return false
	}
	for i := 0; i < len(rules); i += 2 {
		if rules[i] != "Allow" || rules[i+1] != u.Uid {
			return false
		}
	}
	return true
}

// RunRestore executes a displayed restore command, each of its lines joined
// by " followed by ", through cmd.exe exactly as a user would paste it.
func RunRestore(command string) ([]byte, error) {
	var all []byte
	for _, line := range strings.Split(command, " followed by ") {
		cmd := exec.Command(os.Getenv("ComSpec"))
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + line + `"`}
		out, err := cmd.CombinedOutput()
		all = append(all, out...)
		if err != nil {
			return all, fmt.Errorf("%s: %w", line, err)
		}
	}
	return all, nil
}

// MakePrivate makes an existing directory owner-only, as a home the store
// created itself would be: explicit entries reset (some machines give a new
// directory explicit copies of its parent's entries), inherited entries
// dropped, and one entry for the current user, inherited by everything later
// created below it, so fixtures written there by hand pass the store's
// owner-only checks.
func MakePrivate(dir string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	for _, args := range [][]string{{"/reset"}, {"/inheritance:r", "/grant:r", "*" + u.Uid + ":(OI)(CI)F"}} {
		if out, err := exec.Command("icacls", append([]string{dir}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("icacls %s %v: %v: %s", dir, args, err, out)
		}
	}
	return nil
}
